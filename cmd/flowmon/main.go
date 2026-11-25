// Command flowmon is the Flowmon collector, API and web UI in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/jkop-xyz/flowmon/internal/adguard"
	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/capture"
	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/devices"
	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/enrichment"
	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/geoip"
	"github.com/jkop-xyz/flowmon/internal/healthcheck"
	"github.com/jkop-xyz/flowmon/internal/hosts"
	"github.com/jkop-xyz/flowmon/internal/httpserver"
	"github.com/jkop-xyz/flowmon/internal/ingest"
	"github.com/jkop-xyz/flowmon/internal/interfaces"
	"github.com/jkop-xyz/flowmon/internal/reprocess"
	"github.com/jkop-xyz/flowmon/internal/secret"
	"github.com/jkop-xyz/flowmon/internal/services"
	"github.com/jkop-xyz/flowmon/internal/settings"
	"github.com/jkop-xyz/flowmon/internal/setup"
	"github.com/jkop-xyz/flowmon/internal/snmp"
	"github.com/jkop-xyz/flowmon/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: flowmon <command>

Commands:
  serve                          Run the collector, API and web UI (default)
  healthcheck                    Exit 0 if the local server is healthy, 1 otherwise
  admin reset-password <user>    Set a new password for an account
  version                        Print the version

Configuration is read from FLOWMON_* environment variables.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "serve":
		return serve(stderr)
	case "healthcheck":
		return check(stderr)
	case "admin":
		return admin(args[1:], stdin, stdout, stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func serve(stderr io.Writer) int {
	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	logger := newLogger(cfg, stderr)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var debugCapture *capture.Writer
	if cfg.DebugCapture != "" {
		debugCapture, err = capture.Open(capture.Options{
			Path:         cfg.DebugCapture,
			MaxFileBytes: cfg.DebugCaptureMaxBytes,
			MaxFiles:     cfg.DebugCaptureFiles,
			MaxDuration:  cfg.DebugCaptureMaxDuration,
		}, time.Now())
		if err != nil {
			logger.Error("debug capture", "err", err)
			return 1
		}
		defer debugCapture.Close()
		logger.Warn("debug capture is on: received datagrams are written to disk and contain private traffic metadata",
			"path", cfg.DebugCapture, "max_duration", cfg.DebugCaptureMaxDuration)
	}

	settingsEnv, err := settings.FromConfig(cfg)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		logger.Error("data directory", "err", err)
		return 1
	}
	flowStorePath := filepath.Join(cfg.DataDir, "flows.duckdb")
	store, err := flowstore.Open(ctx, flowStorePath, flowstore.Options{
		MemoryLimit: cfg.FlowStoreMemoryLimit,
		Threads:     cfg.FlowStoreThreads,
	})
	if err != nil {
		logger.Error("flow store", "err", err)
		return 1
	}
	defer store.Close()

	// Local Networks and retention: environment variables win, then what was set in
	// Settings, then the defaults.
	collectionSettings, err := settings.Load(ctx, store, settingsEnv)
	if err != nil {
		logger.Error("settings", "err", err)
		return 1
	}
	classifier := classify.New(collectionSettings.Current().LocalNetworks)

	ifaceRegistry, err := interfaces.Load(ctx, store)
	if err != nil {
		logger.Error("interfaces", "err", err)
		return 1
	}
	resolver := &geoip.Resolver{}
	geo := geoip.NewManager(geoip.Options{
		Dir: cfg.GeoIPDir, DownloadDir: filepath.Join(cfg.DataDir, "geoip-downloads"), Download: cfg.GeoIPDownload,
		MaxMindAccountID: cfg.MaxMindAccountID, MaxMindLicenseKey: cfg.MaxMindLicenseKey,
		UserAgent: "flowmon/" + version, Logger: logger,
	}, resolver)
	geo.Load() // what is already on disk, so the first Flows are enriched

	allowlist, err := classify.ParseLocalNetworks(cfg.ExporterAllowlist)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error: FLOWMON_EXPORTER_ALLOWLIST:", err)
		return 2
	}
	registry, err := exporters.Load(ctx, exporters.Options{Persister: store, AutoAccept: cfg.ExporterAutoAccept, Allowlist: allowlist})
	if err != nil {
		logger.Error("exporters", "err", err)
		return 1
	}

	knownRouters, err := store.RouterMACs(ctx)
	if err != nil {
		logger.Error("router MACs", "err", err)
		return 1
	}
	recent, err := store.DeviceAddresses(ctx)
	if err != nil {
		logger.Error("device addresses", "err", err)
		return 1
	}
	var sightings []devices.Sighting
	for _, d := range recent {
		if time.Since(d.LastSeen) < devices.AddressTTL {
			sightings = append(sightings, devices.Sighting{Address: d.Address, MAC: d.MAC, At: d.LastSeen})
		}
	}
	assigner := devices.New(devices.Options{
		Known:  routerMACsFrom(knownRouters),
		Recent: sightings,
		Learned: func(r devices.Router) {
			if err := store.AddRouterMAC(context.Background(), flowstore.RouterMAC(r)); err != nil {
				logger.Error("saving a router MAC", "mac", r.MAC.String(), "err", err)
			}
		},
		Logger: logger,
	})
	// Domain Attribution: the lookups of the last day, then each one AdGuard Home sources
	// store as they come.
	attributor := domains.NewAttributor()
	if qs, err := store.DNSQueries(ctx, time.Now().Add(-domains.Window), time.Now().Add(time.Minute), 10_000_000); err != nil {
		logger.Error("loading DNS Queries for Domain Attribution", "err", err)
	} else {
		attributor.Add(lookupsFrom(qs))
	}
	attribute := func(f *flow.Flow) {
		if f.Domain == "" && (f.Direction == flow.DirectionInbound || f.Direction == flow.DirectionOutbound) {
			f.Domain = attributor.Name(f.Local, f.Peer, f.Start)
		}
	}
	enrich := func(f *flow.Flow) {
		resolver.Enrich(f)
		assigner.Assign(f)
		attribute(f)
	}
	pipeline := ingest.New(ingest.Options{Writer: store, Classifier: classifier, Enrich: enrich, Logger: logger, WatermarkOverride: cfg.WatermarkOverride,
		SamplingRate:   registry.SamplingRate,
		MultiplePoints: registry.MultiplePoints,
		OnInterfaceName: func(exp netip.Addr, idx uint32, name string, at time.Time) {
			if _, err := ifaceRegistry.SetName(context.Background(), exp, idx, name, interfaces.SourceOptions, at); err != nil {
				logger.Warn("saving an interface name from an Options Template", "exporter", exp.String(), "err", err)
			}
		}})
	pipelineCtx, stopPipeline := context.WithCancel(ctx)
	var pipelineDone sync.WaitGroup
	pipelineDone.Add(1)
	go func() {
		defer pipelineDone.Done()
		pipeline.Run(pipelineCtx)
	}()
	// Runs before store.Close (defers are LIFO), on every exit path: the pipeline stops
	// and writes its last batch before the store closes.
	defer func() {
		stopPipeline()
		pipelineDone.Wait()
	}()

	go maintain(pipelineCtx, logger, store, pipeline, func() flowstore.Retention { return collectionSettings.Current().Retention }, cfg.FlowStoreMaxBytes)
	go geo.Run(pipelineCtx)

	classifier.SetInterfaces(interfaceClassification{registry, ifaceRegistry})
	classifier.SetRoles(registry)
	collectionSettings.OnChange(func(v settings.Values) {
		before := classifier.LocalNetworks()
		classifier.SetLocalNetworks(v.LocalNetworks)
		if slices.Equal(before, v.LocalNetworks) {
			return
		}
		// Flows from now on are classified with the new Local Networks; charts mark when.
		now := time.Now()
		for _, c := range registry.Configs() {
			if c.Status != exporters.StatusApproved {
				continue
			}
			if err := store.AddClassificationChange(context.Background(), c.Address, now, "Local Networks changed"); err != nil {
				logger.Warn("recording a classification change", "exporter", c.Address.String(), "err", err)
			}
		}
		logger.Info("local networks changed", "networks", v.LocalNetworks)
	})
	reprocessor := reprocess.New(store, func(f *flow.Flow) {
		peer := f.Peer
		classifier.Classify(f, f.End)
		// A Flow keeps the enrichment it was stored with, unless its Peer changed; then
		// the new Peer is looked up in today's databases, the best there is.
		if f.Peer != peer {
			resolver.Enrich(f)
		}
		// Devices come from the stored fields, so reprocessing fills them in for Flows
		// stored before Flowmon knew Devices.
		assigner.Assign(f)
		attribute(f)
	}, logger)

	box, err := secret.LoadOrCreate(filepath.Join(cfg.DataDir, "secret.key"))
	if err != nil {
		logger.Error("secret key", "err", err)
		return 1
	}
	sourceEnv, err := datasource.FromEnviron(os.Environ())
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	adguardKind := adguard.New(adguard.Options{
		Store:      store,
		IsExporter: func(a netip.Addr) bool { _, ok := registry.Config(a); return ok },
		Retention:  func() time.Duration { return collectionSettings.Current().Retention.DNS },
		Stored:     func(qs []flowstore.DNSQuery) { attributor.Add(lookupsFrom(qs)) },
		Logger:     logger,
	})
	go fillDomains(pipelineCtx, logger, store, attributor)
	// SNMP Data Sources feed an Exporter's interface names, as its own polling did.
	snmpKind := snmp.NewSourceKind(snmp.SourceOptions{Writer: store, Logger: logger,
		Names: func(src datasource.Source, names map[uint32]string, at time.Time) {
			a, ok := snmp.ExporterOf(src.Settings)
			if !ok || registry.Status(a) != exporters.StatusApproved {
				return
			}
			for idx, name := range names {
				if _, err := ifaceRegistry.SetName(context.Background(), a, idx, name, interfaces.SourceSNMP, at); err != nil {
					logger.Warn("saving an interface name from SNMP", "exporter", a.String(), "err", err)
				}
			}
		}})
	sources, err := datasource.New(datasource.Options{Store: store, Box: box, Kinds: []datasource.Kind{adguardKind, snmpKind}, Env: sourceEnv, Logger: logger})
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	if err := sources.Load(ctx); err != nil {
		// Data Sources are optional; Flow collection starts regardless.
		logger.Error("stored data sources unavailable", "err", err)
	}
	moveExporterSNMP(ctx, logger, store, snmp.NewSettings(store, box), sources, registry)
	sources.Start(pipelineCtx)

	wanInterfaces := func(a netip.Addr) map[string]uint32 {
		out := map[string]uint32{}
		for _, i := range ifaceRegistry.Current(a) {
			if i.Role == interfaces.RoleWAN {
				out[i.Name] = i.Index
			}
		}
		return out
	}
	// The SNMP Data Source that polls an Exporter: an enabled one first.
	snmpSourceFor := func(a netip.Addr) (datasource.Status, bool) {
		var found datasource.Status
		ok := false
		for _, src := range sources.List() {
			if e, is := snmp.ExporterOf(src.Settings); src.Kind == "snmp" && is && e == a && (!ok || src.Enabled && !found.Enabled) {
				found, ok = src, true
			}
		}
		return found, ok
	}

	gate := collector.GateFunc(func(a netip.Addr) collector.Admission {
		switch registry.Status(a) {
		case exporters.StatusApproved:
			return collector.AdmitAccept
		case exporters.StatusIgnored:
			return collector.AdmitIgnore
		}
		return collector.AdmitPending
	})

	coll := collector.New(collector.Options{
		MaxExporters:  cfg.MaxExporters,
		ReceiveBuffer: cfg.UDPReceiveBuffer,
		Capture:       debugCapture,
		Logger:        logger,
		Sink:          pipeline,
		Gate:          gate,
	})
	for _, addr := range cfg.FlowUDPAddrs {
		if err := coll.ListenUDP(ctx, addr); err != nil {
			logger.Error("collector", "err", err)
			return 1
		}
	}

	proxies, err := classify.ParseLocalNetworks(cfg.TrustedProxies)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error: FLOWMON_TRUSTED_PROXIES:", err)
		return 2
	}
	holder := &appstore.Holder{}
	defer func() {
		if s := holder.Get(); s != nil {
			s.Close()
		}
	}()
	authManager := auth.NewManager(holder.Get, auth.Options{
		Proxies:       auth.Proxies{Trusted: proxies},
		SecureCookies: auth.SecureCookies(cfg.SecureCookies),
		Available:     func() bool { return holder.Health().Available },
	})
	// Watch the App Store, so an outage is noticed once, shown in the UI, and skipped
	// by everything that can do without it. Collection never touches it.
	go holder.Monitor(ctx, 15*time.Second, func(h appstore.Health) {
		if h.Available {
			logger.Info("app store available again")
		} else {
			logger.Error("app store unavailable; collection continues, logins and changes to accounts, labels and names wait", "err", h.Error)
		}
	})
	storeCfg, locked, storeErr := appStoreConfig(cfg)
	setupCtl := setup.New(setup.Options{
		Holder: holder, Auth: authManager, BootstrapPath: bootstrapPath(cfg), DefaultSQLitePath: defaultSQLite(cfg),
		Locked: locked, Logger: logger,
		OnConfigured: func(s *appstore.Store) { go expireSessions(ctx, logger, s) },
	})
	announceSetup := func() {
		if st, err := setupCtl.Status(ctx); err == nil && st.Step != setup.StepDone {
			logger.Warn("first-run setup: open the web UI and enter this setup token", "token", setupCtl.Token(), "step", st.Step)
		}
	}
	switch {
	case storeErr == nil:
		openAppStore(ctx, logger, storeCfg, holder, func(s *appstore.Store) {
			if ok, err := setup.SeedAdmin(ctx, s, cfg.AdminUsername, cfg.AdminPassword, time.Now()); err != nil {
				logger.Error("creating the admin account from FLOWMON_ADMIN_USERNAME", "err", err)
			} else if ok {
				logger.Info("admin account created from FLOWMON_ADMIN_USERNAME", "username", cfg.AdminUsername)
			}
			announceSetup()
			go expireSessions(ctx, logger, s)
		})
		if holder.Get() == nil {
			announceSetup()
		}
	case errors.Is(storeErr, errNotConfigured):
		announceSetup()
	default:
		logger.Error("app store configuration", "err", storeErr)
		announceSetup()
	}

	// Reverse DNS and RDAP are cached in the App Store when it is reachable.
	ipCache := func() enrichment.Persister {
		if s := holder.Usable(); s != nil {
			return s
		}
		return nil
	}
	reverseDNS := enrichment.NewReverseDNS(enrichment.ReverseDNSOptions{Store: ipCache, Logger: logger})
	go reverseDNS.Run(pipelineCtx)
	rdap := enrichment.NewRDAP(enrichment.RDAPOptions{Store: ipCache, UserAgent: "flowmon/" + version})
	// Local Hosts are named by the resolver that knows them (the router, a Pi-hole), so
	// their names are cached apart from Remote IPs' and kept for less time: DHCP moves them.
	localDNS := enrichment.NewReverseDNS(enrichment.ReverseDNSOptions{
		Resolver: localResolver(cfg.LocalDNSServer), Store: ipCache, Kind: "local-rdns", TTL: time.Hour,
		Allow: func(a netip.Addr) bool {
			return a.IsValid() && !a.IsUnspecified() && !a.IsLoopback() && !a.IsMulticast()
		},
		Logger: logger,
	})
	go localDNS.Run(pipelineCtx)
	hostDirectory := hosts.NewDirectory(func() hosts.LabelStore {
		if s := holder.Usable(); s != nil {
			return s
		}
		return nil
	}, localDNS)
	go autoMergeDevices(pipelineCtx, logger, store, hostDirectory)

	srv := httpserver.NewServer(cfg.HTTPAddr, httpserver.New(httpserver.Options{
		Version:   version,
		UI:        web.Dist(),
		Logger:    logger,
		Collector: coll,
		Timing:    pipeline,
		Flows:     store,
		Watermark: pipeline,
		Auth:      authManager,
		Setup:     setupCtl,
		Exporters: registry,
		Interfaces: &httpserver.InterfaceOptions{
			Registry: ifaceRegistry, Stats: pipeline, Changes: store, Reprocess: reprocessor, RawRetention: func() time.Duration { return collectionSettings.Current().Retention.Raw },
		},
		SNMP:   &httpserver.SNMPOptions{SourceFor: snmpSourceFor, Store: store, WAN: wanInterfaces},
		GeoIP:  geo,
		Remote: &httpserver.RemoteOptions{Store: store, Lookup: resolver, Names: reverseDNS, RDAP: rdap},
		Hosts:  &httpserver.HostOptions{Store: store, Directory: hostDirectory},
		Domains: &httpserver.DomainOptions{Store: store, DNSServerAddrs: func(ctx context.Context) map[netip.Addr]bool {
			return dnsSourceAddrs(ctx, sources.List())
		}, IsExporter: func(a netip.Addr) bool { _, ok := registry.Config(a); return ok }, DNSSources: func() int {
			n := 0
			for _, src := range sources.List() {
				if src.Kind == "adguard" {
					n++
				}
			}
			return n
		}},
		Explorer:       store,
		Settings:       collectionSettings,
		Sources:        sources,
		Devices:        store,
		NoFlowsWarning: cfg.NoFlowsWarning,
		Metrics:        &httpserver.MetricsOptions{Token: cfg.MetricsToken, Pipeline: pipeline, Sources: sources},
		FlowStore: &httpserver.FlowStoreOptions{Store: store, Path: flowStorePath,
			MaxBytes: cfg.FlowStoreMaxBytes, BackupToken: cfg.BackupToken},
		AppStore: holder,
		Services: services.NewDirectory(func() services.Store {
			if s := holder.Usable(); s != nil {
				return s
			}
			return nil
		}),
	}))

	errCh := make(chan error, 1)
	go func() {
		logger.Info("flowmon starting", "version", version, "http_addr", cfg.HTTPAddr, "data_dir", cfg.DataDir)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		logger.Error("http server stopped", "err", err)
		return 1
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("shutdown", "err", err)
		return 1
	}
	return 0
}

// localResolver asks server (host:port) for Local Hosts' names, or the system resolver
// when it is empty.
func localResolver(server string) *net.Resolver {
	if server == "" {
		return net.DefaultResolver
	}
	return &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, network, server)
	}}
}

// interfaceClassification tells the classifier which Exporters classify by interface
// and what their interfaces' roles were.
type interfaceClassification struct {
	exporters  *exporters.Registry
	interfaces *interfaces.Registry
}

func (i interfaceClassification) ByInterface(a netip.Addr) bool { return i.exporters.ByInterface(a) }

func (i interfaceClassification) Role(a netip.Addr, idx uint32, at time.Time) classify.InterfaceRole {
	return i.interfaces.RoleAt(a, idx, at)
}

// expireSessions deletes expired sessions and cached IP Enrichment every hour.
func expireSessions(ctx context.Context, logger *slog.Logger, s *appstore.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if _, err := s.DeleteExpiredSessions(ctx, time.Now()); err != nil && ctx.Err() == nil {
			logger.Warn("deleting expired sessions", "err", err)
		}
		if _, err := s.DeleteExpiredIPInfo(ctx, time.Now()); err != nil && ctx.Err() == nil {
			logger.Warn("deleting expired reverse DNS and RDAP results", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// maintain compacts Rollups, builds Top Remote IPs, applies retention and keeps the Flow
// Store under maxBytes (0: no cap) every ten minutes.
func maintain(ctx context.Context, logger *slog.Logger, store *flowstore.Store, pipeline *ingest.Pipeline, retention func() flowstore.Retention, maxBytes int64) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := store.Compact(ctx); err != nil && ctx.Err() == nil {
			logger.Error("compacting rollups", "err", err)
		}
		if n, err := store.BuildTopRemote(ctx, time.Now(), pipeline.MaxWatermark()); err != nil && ctx.Err() == nil {
			logger.Error("building Top Remote IPs", "err", err)
		} else if n > 0 {
			logger.Debug("Top Remote IPs built", "periods", n)
		}
		dropped, err := store.ApplyRetention(ctx, retention(), time.Now(), pipeline.MaxWatermark())
		if err != nil && ctx.Err() == nil {
			logger.Error("applying retention", "err", err)
		}
		if len(dropped) > 0 {
			logger.Info("retention dropped old data", "tables", dropped)
		}
		if maxBytes > 0 {
			wasOver := store.CapStatus().Over
			dropped, err := store.EnforceCap(ctx, maxBytes, time.Now(), pipeline.MaxWatermark())
			if err != nil && ctx.Err() == nil {
				logger.Error("applying the Flow Store size cap", "err", err)
			}
			if len(dropped) > 0 {
				logger.Warn("the Flow Store size cap dropped data before its retention ended", "tables", dropped, "max_bytes", maxBytes)
			}
			if over := store.CapStatus().Over; over && !wasOver {
				logger.Warn("the Flow Store is over its size cap and nothing more may be dropped: raise FLOWMON_FLOW_STORE_MAX_GB or shorten minute and hour Rollup retention", "max_bytes", maxBytes)
			} else if !over && wasOver {
				logger.Info("the Flow Store is back under its size cap")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func check(stderr io.Writer) int {
	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 1
	}
	url, err := healthcheck.URL(cfg.HTTPAddr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := healthcheck.Check(context.Background(), url); err != nil {
		fmt.Fprintln(stderr, "unhealthy:", err)
		return 1
	}
	return 0
}

// lookupsFrom turns stored DNS Queries into what Domain Attribution needs. Blocked
// lookups name nothing a Flow could have gone to.
func lookupsFrom(qs []flowstore.DNSQuery) []domains.Lookup {
	out := make([]domains.Lookup, 0, len(qs))
	for _, q := range qs {
		if !q.Blocked && len(q.Answers) > 0 {
			out = append(out, domains.Lookup{Time: q.Time, Client: q.Client, Name: q.Name, Answers: q.Answers})
		}
	}
	return out
}

// lateDNS is how far back the late pass looks for Flows stored before their lookup was
// collected: AdGuard Home is polled every 15 s, and a poll can fail for a while.
const lateDNS = 10 * time.Minute

// fillDomains gives domains to Flows stored before their lookup arrived, forgets
// lookups too old to matter, then counts the Flows received DomainRollupDelay ago in
// Domain Rollups, every minute.
func fillDomains(ctx context.Context, logger *slog.Logger, store *flowstore.Store, a *domains.Attributor) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		now := time.Now()
		if n, err := store.FillDomains(ctx, now.Add(-lateDNS), a.Name); err != nil && ctx.Err() == nil {
			logger.Error("filling in domains", "err", err)
		} else if n > 0 {
			logger.Debug("domains filled in from late DNS", "flows", n)
		}
		a.Prune(now)
		if _, err := store.BuildDomainRollups(ctx, now); err != nil && ctx.Err() == nil {
			logger.Error("building Domain Rollups", "err", err)
		}
	}
}

// autoMergeDevices merges Private Addresses that are an earlier Device under a new
// randomized MAC (devices.AutoMerges), every ten minutes. Names come from Host Labels and
// reverse DNS, which may take a round to resolve.
func autoMergeDevices(ctx context.Context, logger *slog.Logger, store *flowstore.Store, dir *hosts.Directory) {
	t := time.NewTicker(10 * time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		spans, err := store.MACSpans(ctx)
		if err != nil {
			logger.Error("device spans", "err", err)
			continue
		}
		merges, err := store.DeviceMerges(ctx)
		if err != nil {
			logger.Error("device merges", "err", err)
			continue
		}
		merged := map[flow.MAC]flow.MAC{}
		for _, m := range merges {
			merged[m.MAC] = m.Into
		}
		var seen []devices.Seen
		var keys []netip.Prefix
		for _, s := range spans {
			if s.MAC[0]&0x02 == 0 || !s.Latest.IsValid() {
				continue
			}
			seen = append(seen, devices.Seen{MAC: s.MAC, FirstSeen: s.FirstSeen, LastSeen: s.LastSeen, Latest: s.Latest})
			keys = append(keys, netip.PrefixFrom(s.Latest, s.Latest.BitLen()))
		}
		names := map[netip.Addr]string{}
		for k, n := range dir.Names(ctx, keys) {
			names[k.Addr()] = n.Name
		}
		for _, m := range devices.AutoMerges(seen, names, merged) {
			if err := store.MergeDevice(ctx, m.MAC, m.Into, "auto", time.Now()); err != nil {
				logger.Error("merging a Private Address", "mac", m.MAC.String(), "err", err)
				continue
			}
			logger.Info("Private Address merged into the Device it came after, by its name", "mac", m.MAC.String(), "into", m.Into.String())
		}
	}
}

// dnsSourceAddrs resolves the AdGuard Home sources' addresses: where DNS is meant to go.
func dnsSourceAddrs(ctx context.Context, sources []datasource.Status) map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	for _, src := range sources {
		if src.Kind != "adguard" {
			continue
		}
		u, err := url.Parse(src.Settings["url"])
		if err != nil || u.Hostname() == "" {
			continue
		}
		if a, err := netip.ParseAddr(u.Hostname()); err == nil {
			out[a.Unmap()] = true
			continue
		}
		lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		addrs, err := net.DefaultResolver.LookupNetIP(lctx, "ip", u.Hostname())
		cancel()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			out[a.Unmap()] = true
		}
	}
	return out
}

// moveExporterSNMP turns each Exporter's own SNMP settings into an SNMP Data Source,
// once: SNMP moved onto Data Sources (ADR 0005). A setting records that it happened, so a
// source deleted later isn't brought back.
func moveExporterSNMP(ctx context.Context, logger *slog.Logger, store *flowstore.Store, old *snmp.Settings, sources *datasource.Manager, registry *exporters.Registry) {
	const key = "snmp_moved_to_sources"
	if _, done, err := store.Setting(ctx, key); err != nil || done {
		return
	}
	configs, unreadable, err := old.All(ctx)
	if err != nil {
		logger.Error("reading Exporters' SNMP settings to move them", "err", err)
		return
	}
	for _, a := range unreadable {
		logger.Warn("an Exporter's SNMP credentials can't be decrypted, so its settings weren't moved: add it under Settings → Data Sources", "exporter", a.String())
	}
	for a, c := range configs {
		name := "SNMP on " + a.String()
		if cfg, ok := registry.Config(a); ok && cfg.Name != "" {
			name = cfg.Name
		}
		st, err := sources.Create(ctx, datasource.Source{Kind: "snmp", Name: name, Enabled: c.Enabled, Settings: snmp.SettingsOf(c, a)})
		if err != nil {
			logger.Warn("an Exporter's SNMP settings couldn't be moved: add it under Settings → Data Sources", "exporter", a.String(), "err", err)
			continue
		}
		logger.Info("an Exporter's SNMP settings moved to a Data Source", "exporter", a.String(), "source", st.ID, "enabled", st.Enabled)
	}
	if err := store.SetSettings(ctx, map[string]string{key: "1"}); err != nil {
		logger.Error("recording that SNMP settings moved", "err", err)
	}
}

func routerMACsFrom(rs []flowstore.RouterMAC) []devices.Router {
	out := make([]devices.Router, len(rs))
	for i, r := range rs {
		out[i] = devices.Router(r)
	}
	return out
}
