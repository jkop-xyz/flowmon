package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/geoip"
)

// RemoteOptions wires the Remote IPs screens.
type RemoteOptions struct {
	Store interface {
		TopRemoteIPs(ctx context.Context, q flowstore.RemoteQuery) (flowstore.RemoteResult, error)
		RemoteIP(ctx context.Context, ip netip.Addr, q flowstore.RemoteQuery) (flowstore.RemoteDetail, error)
		RemoteFlows(ctx context.Context, ip netip.Addr, from, to time.Time, exporter netip.Addr, limit int) (flowstore.FlowList, error)
	}
	// Lookup gives today's GeoIP data for an address.
	Lookup interface{ Lookup(netip.Addr) geoip.Info }
}

// remoteDetail adds today's GeoIP lookup to what the Flow Store knows.
type remoteDetail struct {
	flowstore.RemoteDetail
	// Current is what today's GeoIP databases say, which may differ from what the Flows
	// were stored with.
	Current *geoInfo `json:"current,omitempty"`
}

type geoInfo struct {
	Country string `json:"country,omitempty"`
	ASN     uint32 `json:"asn,omitempty"`
	ASNOrg  string `json:"asnOrg,omitempty"`
}

func registerRemote(mux *http.ServeMux, opts Options) {
	ro := opts.Remote
	unavailable := func(w http.ResponseWriter) bool {
		if ro == nil || ro.Store == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "flow store not available"})
			return true
		}
		return false
	}
	query := func(w http.ResponseWriter, r *http.Request) (flowstore.RemoteQuery, bool) {
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return flowstore.RemoteQuery{}, false
		}
		q := flowstore.RemoteQuery{From: bq.From, To: bq.To, Exporter: bq.Exporter, Now: opts.Now(), Limit: 50,
			ByFlows: r.URL.Query().Get("sort") == "flows"}
		if s := r.URL.Query().Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > flowstore.MaxTopLimit {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 300"})
				return q, false
			}
			q.Limit = n
		}
		return q, true
	}
	remoteIP := func(w http.ResponseWriter, r *http.Request) (netip.Addr, bool) {
		ip, err := netip.ParseAddr(r.PathValue("ip"))
		if err != nil || ip.Zone() != "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "not an IP address"})
			return ip, false
		}
		return ip.Unmap(), true
	}

	mux.HandleFunc("GET /api/remote-ips", func(w http.ResponseWriter, r *http.Request) {
		if unavailable(w) {
			return
		}
		q, ok := query(w, r)
		if !ok {
			return
		}
		res, err := ro.Store.TopRemoteIPs(r.Context(), q)
		if queryError(w, opts.Logger, "remote IPs", err) {
			return
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: res, IncompleteFrom: incompleteFrom(opts)})
	})
	mux.HandleFunc("GET /api/remote-ips/{ip}", func(w http.ResponseWriter, r *http.Request) {
		if unavailable(w) {
			return
		}
		ip, ok := remoteIP(w, r)
		if !ok {
			return
		}
		q, ok := query(w, r)
		if !ok {
			return
		}
		d, err := ro.Store.RemoteIP(r.Context(), ip, q)
		if queryError(w, opts.Logger, "remote IP", err) {
			return
		}
		out := remoteDetail{RemoteDetail: d}
		if ro.Lookup != nil {
			info := ro.Lookup.Lookup(ip)
			out.Current = &geoInfo{Country: info.Country, ASN: info.ASN, ASNOrg: info.ASNOrg}
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: out, IncompleteFrom: incompleteFrom(opts)})
	})
	mux.HandleFunc("GET /api/remote-ips/{ip}/flows", func(w http.ResponseWriter, r *http.Request) {
		if unavailable(w) {
			return
		}
		ip, ok := remoteIP(w, r)
		if !ok {
			return
		}
		q, ok := query(w, r)
		if !ok {
			return
		}
		limit := 200
		if s := r.URL.Query().Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > flowstore.MaxFlowList {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be between 1 and 1000"})
				return
			}
			limit = n
		}
		list, err := ro.Store.RemoteFlows(r.Context(), ip, q.From, q.To, q.Exporter, limit)
		if queryError(w, opts.Logger, "remote flows", err) {
			return
		}
		writeJSON(w, http.StatusOK, list)
	})
}
