package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/hosts"
)

type fakeHosts struct {
	fakeRemote
	hq flowstore.HostQuery
}

func (f *fakeHosts) HostDetail(_ context.Context, q flowstore.HostQuery) (flowstore.HostSummary, error) {
	f.hq = q
	return flowstore.HostSummary{ReceivedBytes: 7}, nil
}

func (f *fakeHosts) HostTraffic(_ context.Context, q flowstore.HostQuery) (flowstore.HostTrafficResult, error) {
	f.hq = q
	return flowstore.HostTrafficResult{Points: []flowstore.HostPoint{}}, nil
}

func (f *fakeHosts) HostAddresses(_ context.Context, q flowstore.HostQuery) ([]string, error) {
	f.hq = q
	return []string{"2001:db8::a", "2001:db8::b"}, nil
}

func TestHostEndpoints(t *testing.T) {
	ctx := context.Background()
	store, err := appstore.Open(ctx, appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var current hosts.LabelStore
	dir := hosts.NewDirectory(func() hosts.LabelStore { return current }, fakeNames{})
	f := &fakeHosts{}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	h := New(Options{UI: builtUI, Hosts: &HostOptions{Store: f, Directory: dir}, Now: func() time.Time { return now }})
	c := &client{t: t, h: h}

	if rec := c.do("POST", "/api/host-labels", `{"prefix":"10.0.0.1","name":"Desk"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without an App Store: %d %s", rec.Code, rec.Body.String())
	}
	current = store
	rec := c.do("POST", "/api/host-labels", `{"prefix":"2001:db8::17/64","name":"Phone"}`)
	var phone struct{ ID, Prefix, Name string }
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &phone) != nil || phone.Prefix != "2001:db8::/64" {
		t.Fatalf("create: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do("POST", "/api/host-labels", `{"prefix":"10.0.0.1","name":"Desk"}`); rec.Code != http.StatusCreated {
		t.Fatalf("create desk: %d %s", rec.Code, rec.Body.String())
	}
	for body, code := range map[string]int{
		`{"prefix":"2001:db8::/64","name":"Again"}`: http.StatusConflict,
		`{"prefix":"10.0.0.2","name":""}`:           http.StatusBadRequest,
		`{"prefix":"nope","name":"x"}`:              http.StatusBadRequest,
		`{"prefix":"10.0.0.2"}x`:                    http.StatusBadRequest,
	} {
		if rec := c.do("POST", "/api/host-labels", body); rec.Code != code {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
	if rec := c.do("PUT", "/api/host-labels/"+phone.ID, `{"prefix":"2001:db8::/64","name":"Pixel"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Pixel"`) {
		t.Errorf("update: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do("PUT", "/api/host-labels/missing", `{"prefix":"10.0.0.9","name":"x"}`); rec.Code != http.StatusNotFound {
		t.Errorf("update missing: %d", rec.Code)
	}
	rec = get(t, h, "/api/host-labels")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"prefix":"10.0.0.1","name":"Desk"`) {
		t.Errorf("list: %d %s", rec.Code, rec.Body.String())
	}

	rec = get(t, h, "/api/host-names?host=10.0.0.1&host=2001:db8::99&host=2001:db8::/64&host=10.0.0.7")
	var names struct{ Names map[string]hosts.Name }
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &names) != nil {
		t.Fatalf("names: %d %s", rec.Code, rec.Body.String())
	}
	if n := names.Names["10.0.0.1"]; n.Name != "Desk" || n.Source != "label" {
		t.Errorf("desk: %+v", n)
	}
	if n := names.Names["2001:db8::99"]; n.Name != "Pixel" || n.Label == nil || n.Label.Prefix != "2001:db8::/64" {
		t.Errorf("address in a labelled prefix: %+v", n)
	}
	if n := names.Names["10.0.0.7"]; n.Name != "host.10.0.0.7" || n.Source != "dns" {
		t.Errorf("reverse DNS: %+v", n)
	}
	if rec := get(t, h, "/api/host-names?host=example.com"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad host: %d", rec.Code)
	}

	rec = get(t, h, "/api/local-hosts/summary?host=2001:db8::/64&from=2026-11-18T10:00:00Z")
	if rec.Code != http.StatusOK || f.hq.Host != netip.MustParsePrefix("2001:db8::/64") || f.hq.From.Hour() != 10 ||
		len(f.hq.HostGroups) != 2 || !strings.Contains(rec.Body.String(), `"receivedBytes":7`) {
		t.Errorf("summary: %d %s %+v", rec.Code, rec.Body.String(), f.hq)
	}
	if rec := get(t, h, "/api/local-hosts/traffic?host=10.0.0.1"); rec.Code != http.StatusOK || f.hq.Host != netip.MustParsePrefix("10.0.0.1/32") {
		t.Errorf("traffic: %d %s", rec.Code, rec.Body.String())
	}
	rec = get(t, h, "/api/local-hosts/remote-ips?host=2001:db8::/64&limit=5&sort=flows")
	if rec.Code != http.StatusOK || len(f.q.LocalHosts) != 2 || f.q.Limit != 5 || f.q.Sort != flowstore.RemoteSortFlows {
		t.Errorf("remote IPs: %d %s %+v", rec.Code, rec.Body.String(), f.q)
	}
	for _, path := range []string{"/api/local-hosts/summary", "/api/local-hosts/summary?host=x", "/api/local-hosts/remote-ips?host=10.0.0.1&limit=0"} {
		if rec := get(t, h, path); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}

	if rec := c.do("DELETE", "/api/host-labels/"+phone.ID, ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := c.do("DELETE", "/api/host-labels/"+phone.ID, ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", rec.Code)
	}
	if rec := get(t, New(Options{UI: builtUI}), "/api/host-labels"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without hosts: %d", rec.Code)
	}
}
