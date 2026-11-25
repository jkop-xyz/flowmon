package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/hosts"
)

// FlowStore answers the Flow Explorer.
type FlowStore interface {
	Flows(ctx context.Context, q flowstore.FlowQuery) (flowstore.FlowPage, error)
	Flow(ctx context.Context, id string) (flowstore.FlowDetail, error)
	HostAddresses(ctx context.Context, q flowstore.HostQuery) ([]string, error)
}

var directionNames = map[string]flow.Direction{
	"inbound": flow.DirectionInbound, "outbound": flow.DirectionOutbound,
	"internal": flow.DirectionInternal, "transit": flow.DirectionTransit, "uncounted": flow.DirectionUncounted,
}

// badRequest is an error in the request itself.
type badRequest struct{ error }

// hostMatch says what a Host Name in the Local Host filter matched.
type hostMatch struct {
	Name string `json:"name"`
	// Hosts are the Local Hosts it matched, with their Host Names.
	Hosts []hostMatchItem `json:"hosts"`
}

type hostMatchItem struct {
	Host string `json:"host"`
	Name string `json:"name"`
}

// flowQuery reads the Flow Explorer's filters, sort and page from the URL. A Local Host
// filter that isn't an address or prefix is a Host Name; match says what it matched.
func flowQuery(ctx context.Context, r *http.Request, opts Options) (q flowstore.FlowQuery, match *hostMatch, err error) {
	q, err = parseFlowQuery(r, opts)
	if err != nil {
		return q, nil, badRequest{err}
	}
	s := strings.TrimSpace(r.URL.Query().Get("host"))
	if s == "" {
		return q, nil, nil
	}
	if key, err := hosts.ParseKey(s); err == nil {
		// A Host Label's prefix stands for the addresses that count towards it.
		q.LocalHosts, err = opts.Explorer.HostAddresses(ctx, flowstore.HostQuery{Host: key, From: q.From, To: q.To, HostGroups: hostGroups(ctx, opts)})
		return q, nil, err
	}
	if opts.Hosts == nil || opts.Hosts.Directory == nil {
		return q, nil, badRequest{errors.New("host must be an IP address or prefix")}
	}
	match, err = hostsNamed(ctx, opts, s, q)
	if err != nil {
		return q, nil, err
	}
	q.LocalHosts = []string{} // matching nothing finds nothing
	for _, h := range match.Hosts {
		q.LocalHosts = append(q.LocalHosts, h.Host)
	}
	return q, match, nil
}

// hostsNamed finds the Local Hosts a Host Name stands for (#60): a Host Label's exact
// name selects the addresses that count towards it; otherwise the name matches, case
// aside, part of the Host Name (label or reverse DNS) of the Local Hosts in the range.
func hostsNamed(ctx context.Context, opts Options, name string, q flowstore.FlowQuery) (*hostMatch, error) {
	dir := opts.Hosts.Directory
	groups := hostGroups(ctx, opts)
	m := &hostMatch{Name: name, Hosts: []hostMatchItem{}}
	seen := map[string]bool{}
	add := func(host, hostName string) {
		if !seen[host] {
			seen[host] = true
			m.Hosts = append(m.Hosts, hostMatchItem{Host: host, Name: hostName})
		}
	}
	for _, l := range dir.Labels(ctx) {
		if !strings.EqualFold(l.Name, name) {
			continue
		}
		addrs, err := opts.Explorer.HostAddresses(ctx, flowstore.HostQuery{Host: l.Prefix, From: q.From, To: q.To, HostGroups: groups})
		if err != nil {
			return nil, err
		}
		for _, a := range addrs {
			add(a, l.Name)
		}
	}
	if len(seen) > 0 {
		return m, nil
	}
	var keys []netip.Prefix
	for _, all := range []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0"), netip.MustParsePrefix("::/0")} {
		addrs, err := opts.Explorer.HostAddresses(ctx, flowstore.HostQuery{Host: all, From: q.From, To: q.To})
		if err != nil {
			return nil, err
		}
		for _, s := range addrs {
			if a, err := netip.ParseAddr(s); err == nil {
				keys = append(keys, netip.PrefixFrom(a, a.BitLen()))
			}
		}
	}
	want := strings.ToLower(name)
	names := dir.Names(ctx, keys)
	for _, k := range keys {
		if n := names[k].Name; n != "" && strings.Contains(strings.ToLower(n), want) {
			add(k.Addr().String(), n)
		}
	}
	return m, nil
}

func parseFlowQuery(r *http.Request, opts Options) (flowstore.FlowQuery, error) {
	bq, err := bandwidthQuery(r, opts.Now())
	if err != nil {
		return flowstore.FlowQuery{}, err
	}
	v := r.URL.Query()
	q := flowstore.FlowQuery{From: bq.From, To: bq.To, Exporter: bq.Exporter, Sort: flowstore.FlowSort(v.Get("sort")),
		Asc: v.Get("order") == "asc"}
	number := func(name string, bits int) (uint64, bool, error) {
		s := strings.TrimSpace(v.Get(name))
		if s == "" {
			return 0, false, nil
		}
		n, err := strconv.ParseUint(s, 10, bits)
		if err != nil {
			return 0, false, fmt.Errorf("%s must be a number", name)
		}
		return n, true, nil
	}
	if s := strings.TrimSpace(v.Get("remote")); s != "" {
		if q.Remote, err = netip.ParseAddr(s); err != nil || q.Remote.Zone() != "" {
			return q, errors.New("remote must be an IP address")
		}
	}
	asn, hasASN, err := number("asn", 32)
	if err != nil {
		return q, err
	}
	q.ASN, q.HasASN = uint32(asn), hasASN
	proto, hasProto, err := number("protocol", 8)
	if err != nil {
		return q, err
	}
	q.Protocol, q.HasProtocol = uint8(proto), hasProto
	port, _, err := number("port", 16)
	if err != nil {
		return q, err
	}
	q.Port = uint16(port)
	if s := strings.TrimSpace(v.Get("domain")); s != "" {
		if s == "-" {
			q.NoDomain = true
		} else {
			q.Domain = domains.Normalize(s)
		}
	}
	if s := strings.TrimSpace(v.Get("device")); s != "" {
		m, err := flow.ParseMAC(strings.ToLower(s))
		if err != nil || m.IsZero() {
			return q, errors.New("device must be a MAC address such as aa:bb:cc:dd:ee:ff")
		}
		q.Device = m.String()
	}
	if s := strings.TrimSpace(v.Get("country")); s != "" {
		if len(s) != 2 {
			return q, errors.New("country must be a two-letter code")
		}
		q.Country = strings.ToUpper(s)
	}
	if s := v.Get("direction"); s != "" {
		for _, name := range strings.Split(s, ",") {
			d, ok := directionNames[name]
			if !ok {
				return q, errors.New("direction must be inbound, outbound, internal or transit")
			}
			q.Directions = append(q.Directions, d)
		}
	}
	limit, _, err := number("limit", 16)
	if err != nil || limit > flowstore.MaxFlowList {
		return q, fmt.Errorf("limit must be at most %d", flowstore.MaxFlowList)
	}
	offset, _, err := number("offset", 32)
	if err != nil || offset > flowstore.MaxFlowOffset {
		return q, fmt.Errorf("offset must be at most %d", flowstore.MaxFlowOffset)
	}
	q.Limit, q.Offset = int(limit), int(offset)
	return q, nil
}

func registerFlows(mux *http.ServeMux, opts Options) {
	available := func(w http.ResponseWriter) bool {
		if opts.Explorer == nil {
			errorJSON(w, http.StatusServiceUnavailable, "flow store not available")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/flows", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		q, match, err := flowQuery(r.Context(), r, opts)
		var bad badRequest
		if errors.As(err, &bad) {
			errorJSON(w, http.StatusBadRequest, bad.Error())
			return
		}
		if queryError(w, opts.Logger, "flows", err) {
			return
		}
		page, err := opts.Explorer.Flows(r.Context(), q)
		if errors.Is(err, flowstore.ErrUnknownSort) {
			errorJSON(w, http.StatusBadRequest, "sort must be end, start, bytes, packets or duration")
			return
		}
		if queryError(w, opts.Logger, "flows", err) {
			return
		}
		writeJSON(w, http.StatusOK, struct {
			flowstore.FlowPage
			Host *hostMatch `json:"hostMatch,omitempty"`
		}{page, match})
	})
	mux.HandleFunc("GET /api/flows/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		d, err := opts.Explorer.Flow(r.Context(), r.PathValue("id"))
		if errors.Is(err, flowstore.ErrNotFound) {
			errorJSON(w, http.StatusNotFound, "that Flow isn't stored any more")
			return
		}
		if queryError(w, opts.Logger, "flow", err) {
			return
		}
		writeJSON(w, http.StatusOK, d)
	})
}
