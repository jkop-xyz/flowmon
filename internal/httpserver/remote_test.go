package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/enrichment"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/geoip"
)

type fakeRemote struct {
	q    flowstore.RemoteQuery
	ip   netip.Addr
	lim  int
	from time.Time
}

func (f *fakeRemote) TopRemoteIPs(_ context.Context, q flowstore.RemoteQuery) (flowstore.RemoteResult, error) {
	f.q = q
	return flowstore.RemoteResult{Items: []flowstore.RemoteItem{{RemoteIP: "8.8.8.8", TotalBytes: 9}}}, nil
}

func (f *fakeRemote) RemoteIP(_ context.Context, ip netip.Addr, q flowstore.RemoteQuery) (flowstore.RemoteDetail, error) {
	f.ip, f.q = ip, q
	return flowstore.RemoteDetail{RemoteIP: ip.String(), Country: "US", LocalHosts: []flowstore.RemoteHost{}}, nil
}

type fakeLookup struct{}

func (fakeLookup) Lookup(netip.Addr) geoip.Info {
	return geoip.Info{Country: "CA", ASN: 15169, ASNOrg: "Google LLC"}
}

func TestRemoteEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	f := &fakeRemote{}
	h := New(Options{UI: builtUI, Remote: &RemoteOptions{Store: f, Lookup: fakeLookup{}}, Now: func() time.Time { return now }})

	rec := get(t, h, "/api/remote-ips?sort=flows&limit=100")
	if rec.Code != http.StatusOK || !f.q.ByFlows || f.q.Limit != 100 || !f.q.Now.Equal(now) || !strings.Contains(rec.Body.String(), `"remoteIp":"8.8.8.8"`) {
		t.Errorf("list: %d %s %+v", rec.Code, rec.Body.String(), f.q)
	}
	rec = get(t, h, "/api/remote-ips/::ffff:8.8.8.8")
	if rec.Code != http.StatusOK || f.ip != netip.MustParseAddr("8.8.8.8") || !strings.Contains(rec.Body.String(), `"current":{"country":"CA","asn":15169`) {
		t.Errorf("detail: %d %s", rec.Code, rec.Body.String())
	}
	for _, path := range []string{"/api/remote-ips/example.com", "/api/remote-ips?limit=0", "/api/remote-ips?from=yesterday"} {
		if rec := get(t, h, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	if rec := get(t, New(Options{UI: builtUI}), "/api/remote-ips"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without a store: %d", rec.Code)
	}
}

type fakeNames struct{}

func (fakeNames) Names(ips []netip.Addr) map[netip.Addr]enrichment.Name {
	out := map[netip.Addr]enrichment.Name{}
	for _, ip := range ips {
		out[ip.Unmap()] = enrichment.Name{Name: "host." + ip.Unmap().String(), Status: enrichment.NameResolved}
	}
	return out
}

type fakeRDAP struct{ err error }

func (f fakeRDAP) Lookup(context.Context, netip.Addr) (enrichment.RDAPResult, error) {
	if f.err != nil {
		return enrichment.RDAPResult{}, f.err
	}
	return enrichment.RDAPResult{Status: enrichment.RDAPFound, Registration: &enrichment.Registration{Name: "GOGL", CIDRs: []string{}}}, nil
}

func TestReverseDNSAndRDAPEndpoints(t *testing.T) {
	h := New(Options{UI: builtUI, Remote: &RemoteOptions{Names: fakeNames{}, RDAP: fakeRDAP{}}})
	rec := get(t, h, "/api/reverse-dns?ip=8.8.8.8&ip=::ffff:1.1.1.1")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"::ffff:1.1.1.1":{"name":"host.1.1.1.1","status":"resolved"}`) {
		t.Errorf("names: %d %s", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/api/reverse-dns?ip=nope"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad address: %d", rec.Code)
	}
	if rec := get(t, h, "/api/reverse-dns?"+strings.Repeat("ip=1.1.1.1&", 201)); rec.Code != http.StatusBadRequest {
		t.Errorf("too many: %d", rec.Code)
	}
	rec = get(t, h, "/api/remote-ips/8.8.8.8/rdap")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"GOGL"`) {
		t.Errorf("rdap: %d %s", rec.Code, rec.Body.String())
	}
	for err, want := range map[error]int{enrichment.ErrNotPublic: http.StatusBadRequest, errors.New("registry down"): http.StatusBadGateway} {
		h := New(Options{UI: builtUI, Remote: &RemoteOptions{RDAP: fakeRDAP{err: err}}})
		if rec := get(t, h, "/api/remote-ips/8.8.8.8/rdap"); rec.Code != want {
			t.Errorf("%v: %d, want %d", err, rec.Code, want)
		}
	}
}
