package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// DomainOptions wires the domain views (ADR 0005).
type DomainOptions struct {
	Store interface {
		TopDomains(ctx context.Context, q flowstore.DomainQuery) (flowstore.DomainResult, error)
		HostAddresses(ctx context.Context, q flowstore.HostQuery) ([]string, error)
		RemoteNames(ctx context.Context, ip netip.Addr, from, to time.Time, limit int) ([]flowstore.RemoteName, error)
		DNSSummary(ctx context.Context, f flowstore.DNSFilter) (flowstore.DNSSummary, error)
		DNSServers(ctx context.Context, addrs []string, from, to time.Time) ([]flowstore.DNSServer, error)
	}
	// DNSServerAddrs are the addresses of the DNS Data Sources (AdGuard Home), which a
	// Local Host's DNS is expected to go to.
	DNSServerAddrs func(ctx context.Context) map[netip.Addr]bool
	// IsExporter says whether an address is an Exporter's, which may relay DNS.
	IsExporter func(netip.Addr) bool
	// DNSSources counts the configured DNS Data Sources, enabled or not.
	DNSSources func() int
}

// remoteNamesWindow is how far back a Remote IP's names are looked for: they explain
// the address, whatever range the page shows.
const remoteNamesWindow = 7 * 24 * time.Hour

func registerDomains(mux *http.ServeMux, opts Options) {
	do := opts.Domains
	available := func(w http.ResponseWriter) bool {
		if do == nil || do.Store == nil {
			errorJSON(w, http.StatusServiceUnavailable, "flow store not available")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/domains", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		q := flowstore.DomainQuery{From: bq.From, To: bq.To, Exporter: bq.Exporter, Limit: 10}
		if s := r.URL.Query().Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > flowstore.MaxTopLimit {
				errorJSON(w, http.StatusBadRequest, "limit must be between 1 and 300")
				return
			}
			q.Limit = n
		}
		if s := r.URL.Query().Get("host"); s != "" {
			key, mac, err := parseSubject(s)
			if err != nil {
				errorJSON(w, http.StatusBadRequest, err.Error())
				return
			}
			addrs, err := do.Store.HostAddresses(r.Context(), flowstore.HostQuery{Host: key, MAC: mac, From: bq.From, To: bq.To,
				Exporter: bq.Exporter, HostGroups: hostGroups(r.Context(), opts)})
			if queryError(w, opts.Logger, "domains", err) {
				return
			}
			q.LocalHosts = append([]string{}, addrs...)
		}
		res, err := do.Store.TopDomains(r.Context(), q)
		if queryError(w, opts.Logger, "domains", err) {
			return
		}
		sources := 0
		if do.DNSSources != nil {
			sources = do.DNSSources()
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": res, "dnsSources": sources})
	})
	// The DNS views: the whole network, one Local Host (host=) or one domain (domain=).
	mux.HandleFunc("GET /api/dns", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		f := flowstore.DNSFilter{From: bq.From, To: bq.To, Domain: domains.Normalize(r.URL.Query().Get("domain"))}
		if s := r.URL.Query().Get("host"); s != "" {
			key, mac, err := parseSubject(s)
			if err != nil {
				errorJSON(w, http.StatusBadRequest, err.Error())
				return
			}
			if key.IsSingleIP() {
				f.Clients = []string{key.Addr().String()}
			} else {
				addrs, err := do.Store.HostAddresses(r.Context(), flowstore.HostQuery{Host: key, MAC: mac, From: bq.From, To: bq.To,
					Exporter: bq.Exporter, HostGroups: hostGroups(r.Context(), opts)})
				if queryError(w, opts.Logger, "dns", err) {
					return
				}
				f.Clients = append([]string{}, addrs...)
			}
		}
		res, err := do.Store.DNSSummary(r.Context(), f)
		if queryError(w, opts.Logger, "dns", err) {
			return
		}
		sources := 0
		if do.DNSSources != nil {
			sources = do.DNSSources()
		}
		writeJSON(w, http.StatusOK, map[string]any{"result": res, "dnsSources": sources})
	})
	// The DNS servers a Local Host or Device (host=) sent to, each marked as one of the DNS
	// Data Sources, an Exporter, or other: lookups to "other" ones bypass AdGuard Home.
	mux.HandleFunc("GET /api/dns-servers", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		key, mac, err := parseSubject(r.URL.Query().Get("host"))
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		addrs, err := do.Store.HostAddresses(r.Context(), flowstore.HostQuery{Host: key, MAC: mac, From: bq.From, To: bq.To,
			Exporter: bq.Exporter, HostGroups: hostGroups(r.Context(), opts)})
		if queryError(w, opts.Logger, "dns servers", err) {
			return
		}
		servers, err := do.Store.DNSServers(r.Context(), addrs, bq.From, bq.To)
		if queryError(w, opts.Logger, "dns servers", err) {
			return
		}
		known := map[netip.Addr]bool{}
		if do.DNSServerAddrs != nil {
			known = do.DNSServerAddrs(r.Context())
		}
		type server struct {
			flowstore.DNSServer
			// Kind is "source" (a DNS Data Source), "exporter" or "other".
			Kind string `json:"kind"`
		}
		out := []server{}
		for _, s := range servers {
			a, _ := netip.ParseAddr(s.Server)
			kind := "other"
			switch {
			case known[a.Unmap()]:
				kind = "source"
			case do.IsExporter != nil && do.IsExporter(a.Unmap()):
				kind = "exporter"
			}
			out = append(out, server{s, kind})
		}
		writeJSON(w, http.StatusOK, map[string]any{"servers": out, "dnsSources": len(known)})
	})
	mux.HandleFunc("GET /api/remote-ips/{ip}/names", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		ip, err := netip.ParseAddr(r.PathValue("ip"))
		if err != nil || ip.Zone() != "" {
			errorJSON(w, http.StatusBadRequest, "not an IP address")
			return
		}
		now := opts.Now()
		names, err := do.Store.RemoteNames(r.Context(), ip, now.Add(-remoteNamesWindow), now.Add(time.Minute), 20)
		if queryError(w, opts.Logger, "remote IP names", err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"names": names, "days": int(remoteNamesWindow.Hours() / 24)})
	})
}
