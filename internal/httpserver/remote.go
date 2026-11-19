package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"

	"github.com/jkop-xyz/flowmon/internal/enrichment"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/geoip"
)

// RemoteOptions wires the Remote IPs screens.
type RemoteOptions struct {
	Store interface {
		TopRemoteIPs(ctx context.Context, q flowstore.RemoteQuery) (flowstore.RemoteResult, error)
		RemoteIP(ctx context.Context, ip netip.Addr, q flowstore.RemoteQuery) (flowstore.RemoteDetail, error)
	}
	// Lookup gives today's GeoIP data for an address.
	Lookup interface{ Lookup(netip.Addr) geoip.Info }
	// Names answers reverse DNS from cache and queues the rest.
	Names interface {
		Names(ips []netip.Addr) map[netip.Addr]enrichment.Name
	}
	// RDAP looks up registration details.
	RDAP interface {
		Lookup(ctx context.Context, ip netip.Addr) (enrichment.RDAPResult, error)
	}
}

// maxNames bounds one reverse DNS request: about what fits on a screen.
const maxNames = 200

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
		q := flowstore.RemoteQuery{From: bq.From, To: bq.To, Exporter: bq.Exporter, Now: opts.Now(), Limit: 50}
		if !remoteSort(w, r, &q) {
			return q, false
		}
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

	mux.HandleFunc("GET /api/reverse-dns", func(w http.ResponseWriter, r *http.Request) {
		if ro == nil || ro.Names == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "reverse DNS not available"})
			return
		}
		raw := r.URL.Query()["ip"]
		if len(raw) > maxNames {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "at most 200 addresses at a time"})
			return
		}
		ips := make([]netip.Addr, 0, len(raw))
		for _, s := range raw {
			ip, err := netip.ParseAddr(s)
			if err != nil || ip.Zone() != "" {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": s + " is not an IP address"})
				return
			}
			ips = append(ips, ip)
		}
		names := ro.Names.Names(ips)
		// Keyed by the address as the client sent it, so it can match its own strings.
		out := make(map[string]enrichment.Name, len(raw))
		for i, s := range raw {
			out[s] = names[ips[i].Unmap()]
		}
		writeJSON(w, http.StatusOK, map[string]any{"names": out})
	})
	mux.HandleFunc("GET /api/remote-ips/{ip}/rdap", func(w http.ResponseWriter, r *http.Request) {
		if ro == nil || ro.RDAP == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "RDAP not available"})
			return
		}
		ip, ok := remoteIP(w, r)
		if !ok {
			return
		}
		res, err := ro.RDAP.Lookup(r.Context(), ip)
		switch {
		case errors.Is(err, enrichment.ErrNotPublic):
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "registries only describe public addresses"})
		case err != nil:
			opts.Logger.Warn("RDAP lookup", "ip", ip.String(), "err", err)
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		default:
			writeJSON(w, http.StatusOK, res)
		}
	})
}

// remoteSort reads ?sort= (total, received, sent, flows or hosts) and ?order=asc.
func remoteSort(w http.ResponseWriter, r *http.Request, q *flowstore.RemoteQuery) bool {
	switch s := flowstore.RemoteSort(r.URL.Query().Get("sort")); s {
	case "", flowstore.RemoteSortTotal, flowstore.RemoteSortReceived, flowstore.RemoteSortSent, flowstore.RemoteSortFlows, flowstore.RemoteSortLocalHosts:
		q.Sort = s
	case "bytes": // what the Remote IPs page sent before it had sortable columns
		q.Sort = flowstore.RemoteSortTotal
	default:
		errorJSON(w, http.StatusBadRequest, "sort must be total, received, sent, flows or hosts")
		return false
	}
	q.Asc = r.URL.Query().Get("order") == "asc"
	return true
}
