// Command flowmon is the Flowmon collector, API and web UI in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/capture"
	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/healthcheck"
	"github.com/jkop-xyz/flowmon/internal/httpserver"
	"github.com/jkop-xyz/flowmon/internal/ingest"
	"github.com/jkop-xyz/flowmon/internal/setup"
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

	extra, err := classify.ParseLocalNetworks(cfg.ExtraLocalNetworks)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error: FLOWMON_LOCAL_NETWORKS:", err)
		return 2
	}
	classifier := classify.New(append(classify.DefaultLocalNetworks(), extra...))

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		logger.Error("data directory", "err", err)
		return 1
	}
	store, err := flowstore.Open(ctx, filepath.Join(cfg.DataDir, "flows.duckdb"), flowstore.Options{
		MemoryLimit: cfg.FlowStoreMemoryLimit,
		Threads:     cfg.FlowStoreThreads,
	})
	if err != nil {
		logger.Error("flow store", "err", err)
		return 1
	}
	defer store.Close()

	pipeline := ingest.New(ingest.Options{Writer: store, Classifier: classifier, Logger: logger, WatermarkOverride: cfg.WatermarkOverride})
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

	retention := flowstore.Retention{Raw: cfg.RetentionRaw, Minute: cfg.RetentionMinute, Hour: cfg.RetentionHour}
	go maintain(pipelineCtx, logger, store, pipeline, retention)

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
		MaxExporters: cfg.MaxExporters,
		Capture:      debugCapture,
		Logger:       logger,
		Sink:         pipeline,
		Gate:         gate,
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

// expireSessions deletes expired sessions every hour.
func expireSessions(ctx context.Context, logger *slog.Logger, s *appstore.Store) {
	t := time.NewTicker(time.Hour)
	defer t.Stop()
	for {
		if _, err := s.DeleteExpiredSessions(ctx, time.Now()); err != nil && ctx.Err() == nil {
			logger.Warn("deleting expired sessions", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// maintain compacts Rollups and applies retention every ten minutes.
func maintain(ctx context.Context, logger *slog.Logger, store *flowstore.Store, pipeline *ingest.Pipeline, r flowstore.Retention) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := store.Compact(ctx); err != nil && ctx.Err() == nil {
			logger.Error("compacting rollups", "err", err)
		}
		dropped, err := store.ApplyRetention(ctx, r, time.Now(), pipeline.MaxWatermark())
		if err != nil && ctx.Err() == nil {
			logger.Error("applying retention", "err", err)
		}
		if len(dropped) > 0 {
			logger.Info("retention dropped old data", "tables", dropped)
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
