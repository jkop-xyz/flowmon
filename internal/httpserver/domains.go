package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/hosts"
)

// DomainOptions wires the domain views (ADR 0005).
type DomainOptions struct {
	Store interface {
		TopDomains(ctx context.Context, q flowstore.DomainQuery) (flowstore.DomainResult, error)
		HostAddresses(ctx context.Context, q flowstore.HostQuery) ([]string, error)
		RemoteNames(ctx context.Context, ip netip.Addr, from, to time.Time, limit int) ([]flowstore.RemoteName, error)
	}
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
			key, err := hosts.ParseKey(s)
			if err != nil {
				errorJSON(w, http.StatusBadRequest, "host must be an IP address or prefix")
				return
			}
			addrs, err := do.Store.HostAddresses(r.Context(), flowstore.HostQuery{Host: key, From: bq.From, To: bq.To,
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
