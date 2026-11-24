package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type fakeDomains struct{ got flowstore.DomainQuery }

func (f *fakeDomains) TopDomains(_ context.Context, q flowstore.DomainQuery) (flowstore.DomainResult, error) {
	f.got = q
	return flowstore.DomainResult{Items: []flowstore.DomainItem{{Registrable: "bbc.co.uk", TotalBytes: 10}}}, nil
}

func (f *fakeDomains) HostAddresses(context.Context, flowstore.HostQuery) ([]string, error) {
	return []string{"192.168.10.44", "192.168.10.77"}, nil
}

func (f *fakeDomains) RemoteNames(_ context.Context, ip netip.Addr, _, _ time.Time, _ int) ([]flowstore.RemoteName, error) {
	return []flowstore.RemoteName{{Name: "www.bbc.co.uk", Lookups: 3}}, nil
}

func TestDomainEndpoints(t *testing.T) {
	fd := &fakeDomains{}
	h := New(Options{UI: builtUI, Domains: &DomainOptions{Store: fd, DNSSources: func() int { return 2 }}})
	rec := get(t, h, "/api/domains?preset=1h&host=192.168.10.44&limit=5")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"registrable":"bbc.co.uk"`) || !strings.Contains(rec.Body.String(), `"dnsSources":2`) {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if fd.got.Limit != 5 || len(fd.got.LocalHosts) != 2 {
		t.Errorf("query %+v; want the host's addresses and the limit", fd.got)
	}
	if rec := get(t, h, "/api/domains?preset=1h"); rec.Code != http.StatusOK || fd.got.LocalHosts != nil {
		t.Errorf("without a host: %d, hosts %v", rec.Code, fd.got.LocalHosts)
	}
	for _, bad := range []string{"/api/domains?preset=1h&limit=0", "/api/domains?preset=1h&host=nope", "/api/remote-ips/nope/names"} {
		if rec := get(t, h, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if rec := get(t, h, "/api/remote-ips/151.101.0.81/names"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"www.bbc.co.uk"`) {
		t.Errorf("names: %d %s", rec.Code, rec.Body)
	}
}
