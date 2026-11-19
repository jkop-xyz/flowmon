package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/enrichment"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/hosts"
)

type fakeExplorer struct {
	q     flowstore.FlowQuery
	hq    flowstore.HostQuery
	id    string
	addrs []string
}

func (f *fakeExplorer) Flows(_ context.Context, q flowstore.FlowQuery) (flowstore.FlowPage, error) {
	f.q = q
	if q.Sort == "colour" {
		return flowstore.FlowPage{}, flowstore.ErrUnknownSort
	}
	return flowstore.FlowPage{Items: []flowstore.FlowItem{{ID: "abc", Bytes: 9}}, Total: 1}, nil
}

func (f *fakeExplorer) Flow(_ context.Context, id string) (flowstore.FlowDetail, error) {
	f.id = id
	if id != "abc" {
		return flowstore.FlowDetail{}, flowstore.ErrNotFound
	}
	return flowstore.FlowDetail{FlowItem: flowstore.FlowItem{ID: id}, Fields: []flow.FieldView{{ID: 8, Name: "sourceIPv4Address"}}}, nil
}

func (f *fakeExplorer) HostAddresses(_ context.Context, q flowstore.HostQuery) ([]string, error) {
	f.hq = q
	return f.addrs, nil
}

func TestFlowEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := &fakeExplorer{addrs: []string{"192.168.1.5", "192.168.1.6"}}
	h := New(Options{UI: builtUI, Explorer: f, Now: func() time.Time { return now }})

	rec := get(t, h, "/api/flows?host=192.168.1.0/29&remote=::ffff:8.8.8.8&asn=15169&country=us&protocol=6&port=443&direction=inbound,internal&sort=bytes&order=asc&limit=50&offset=100")
	q := f.q
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"total":1`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body.String())
	}
	if len(q.LocalHosts) != 2 || f.hq.Host != netip.MustParsePrefix("192.168.1.0/29") || q.Remote != netip.MustParseAddr("::ffff:8.8.8.8") ||
		!q.HasASN || q.ASN != 15169 || q.Country != "US" || !q.HasProtocol || q.Protocol != 6 || q.Port != 443 ||
		len(q.Directions) != 2 || q.Directions[1] != flow.DirectionInternal || q.Sort != flowstore.SortBytes || !q.Asc ||
		q.Limit != 50 || q.Offset != 100 || !q.To.Equal(now) {
		t.Errorf("query %+v", q)
	}
	if rec := get(t, h, "/api/flows"); rec.Code != http.StatusOK || f.q.HasASN || f.q.LocalHosts != nil || f.q.Asc {
		t.Errorf("no filters: %d %+v", rec.Code, f.q)
	}
	for _, path := range []string{"/api/flows?host=x", "/api/flows?remote=1.1.1.0/24", "/api/flows?asn=AS15169", "/api/flows?country=USA",
		"/api/flows?direction=sideways", "/api/flows?limit=5000", "/api/flows?offset=999999", "/api/flows?port=70000", "/api/flows?sort=colour"} {
		if rec := get(t, h, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	if rec := get(t, h, "/api/flows/abc"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"sourceIPv4Address"`) {
		t.Errorf("detail: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/api/flows/gone"); rec.Code != http.StatusNotFound {
		t.Errorf("missing: %d", rec.Code)
	}
	if rec := get(t, New(Options{UI: builtUI}), "/api/flows"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without a store: %d", rec.Code)
	}
}

// lanNames names Local Hosts like a home resolver would.
type lanNames map[string]string

func (n lanNames) Names(ips []netip.Addr) map[netip.Addr]enrichment.Name {
	out := map[netip.Addr]enrichment.Name{}
	for _, ip := range ips {
		if name, ok := n[ip.String()]; ok {
			out[ip] = enrichment.Name{Name: name, Status: enrichment.NameResolved}
		} else {
			out[ip] = enrichment.Name{Status: enrichment.NameNone}
		}
	}
	return out
}

// hostsExplorer answers HostAddresses from a fixed set of active Local Hosts.
type hostsExplorer struct {
	fakeExplorer
	active []string
}

func (f *hostsExplorer) HostAddresses(_ context.Context, q flowstore.HostQuery) ([]string, error) {
	var out []string
	for _, s := range f.active {
		if q.Host.Contains(netip.MustParseAddr(s)) {
			out = append(out, s)
		}
	}
	return out, nil
}

func TestFlowExplorerFiltersByHostName(t *testing.T) {
	ctx := context.Background()
	store, err := appstore.Open(ctx, appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.CreateHostLabel(ctx, netip.MustParsePrefix("192.168.10.0/30"), "Lab", time.Now()); err != nil {
		t.Fatal(err)
	}
	dir := hosts.NewDirectory(func() hosts.LabelStore { return store }, lanNames{
		"192.168.10.44": "jakob-desktop.lan", "192.168.10.50": "nas.lan", "fd00::44": "Jakob-Laptop.lan",
	})
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := &hostsExplorer{active: []string{"192.168.10.1", "192.168.10.2", "192.168.10.44", "192.168.10.50", "fd00::44"}}
	h := New(Options{UI: builtUI, Explorer: f, Hosts: &HostOptions{Directory: dir}, Now: func() time.Time { return now }})

	for _, c := range []struct {
		host string
		want []string
	}{
		{"lab", []string{"192.168.10.1", "192.168.10.2"}}, // a Host Label's name, case aside
		{"jakob", []string{"192.168.10.44", "fd00::44"}},  // part of reverse DNS names
		{"nas.lan", []string{"192.168.10.50"}},            // a whole one
		{"printer", []string{}},                           // nothing: an empty page, not an error
		{"192.168.10.44", []string{"192.168.10.44"}},      // addresses work as before
	} {
		rec := get(t, h, "/api/flows?host="+c.host)
		if rec.Code != http.StatusOK {
			t.Errorf("%s: %d %s", c.host, rec.Code, rec.Body.String())
			continue
		}
		got := f.q.LocalHosts
		slices.Sort(got)
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: Local Hosts %v, want %v", c.host, got, c.want)
		}
		named := strings.Contains(rec.Body.String(), `"hostMatch"`)
		if isAddr := c.host == "192.168.10.44"; named == isAddr {
			t.Errorf("%s: hostMatch in the response %v: %s", c.host, named, rec.Body.String())
		}
	}
}
