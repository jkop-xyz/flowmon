package enrichment

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// memStore is an in-memory App Store cache.
type memStore struct {
	mu sync.Mutex
	m  map[string]struct {
		v       string
		fetched time.Time
		expires time.Time
	}
}

func newMemStore() *memStore {
	return &memStore{m: map[string]struct {
		v       string
		fetched time.Time
		expires time.Time
	}{}}
}

func (s *memStore) CachedIPInfo(_ context.Context, kind, ip string, now time.Time) (string, time.Time, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[kind+"/"+ip]
	if !ok || !now.Before(e.expires) {
		return "", time.Time{}, errors.New("not found")
	}
	return e.v, e.fetched, nil
}

func (s *memStore) CacheIPInfo(_ context.Context, kind, ip, value string, now time.Time, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[kind+"/"+ip] = struct {
		v       string
		fetched time.Time
		expires time.Time
	}{value, now, now.Add(ttl)}
	return nil
}

func (s *memStore) has(kind, ip string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.m[kind+"/"+ip]
	return ok
}

type fakeResolver struct {
	calls atomic.Int32
	names map[string][]string
	fail  map[string]error
}

func (f *fakeResolver) LookupAddr(_ context.Context, addr string) ([]string, error) {
	f.calls.Add(1)
	if err := f.fail[addr]; err != nil {
		return nil, err
	}
	if n, ok := f.names[addr]; ok {
		return n, nil
	}
	return nil, &net.DNSError{Err: "no such host", Name: addr, IsNotFound: true}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

var (
	one    = netip.MustParseAddr("1.1.1.1")
	google = netip.MustParseAddr("8.8.8.8")
	quad9  = netip.MustParseAddr("9.9.9.9")
	lan    = netip.MustParseAddr("192.168.1.10")
)

func TestReverseDNSQueuesAndCaches(t *testing.T) {
	res := &fakeResolver{
		names: map[string][]string{"1.1.1.1": {"one.one.one.one."}},
		fail:  map[string]error{"9.9.9.9": &net.DNSError{Err: "i/o timeout", IsTimeout: true}},
	}
	store := newMemStore()
	r := NewReverseDNS(ReverseDNSOptions{Resolver: res, Store: func() Persister { return store }, Interval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	got := r.Names([]netip.Addr{one, google, quad9, lan, one})
	if got[one].Status != NamePending || got[lan].Status != NameNone {
		t.Fatalf("first answer %+v", got)
	}
	waitFor(t, func() bool {
		n := r.Names([]netip.Addr{one, google, quad9})
		return n[one].Status != NamePending && n[google].Status != NamePending && n[quad9].Status != NamePending
	})
	got = r.Names([]netip.Addr{one, google, quad9})
	if got[one] != (Name{Name: "one.one.one.one", Status: NameResolved}) || got[google].Status != NameNone || got[quad9].Status != NameFailed {
		t.Errorf("resolved %+v", got)
	}
	// Each address was asked about once, the private one never.
	if c := res.calls.Load(); c != 3 {
		t.Errorf("%d DNS queries, want 3", c)
	}
	// Names and "no name" are kept in the App Store; failures aren't.
	if !store.has("rdns", "1.1.1.1") || !store.has("rdns", "8.8.8.8") || store.has("rdns", "9.9.9.9") {
		t.Errorf("stored %v", store.m)
	}

	// A second instance (after a restart) answers from the App Store without DNS.
	res2 := &fakeResolver{}
	r2 := NewReverseDNS(ReverseDNSOptions{Resolver: res2, Store: func() Persister { return store }, Interval: time.Millisecond})
	go r2.Run(ctx)
	r2.Names([]netip.Addr{one})
	waitFor(t, func() bool { return r2.Names([]netip.Addr{one})[one].Status == NameResolved })
	if res2.calls.Load() != 0 {
		t.Errorf("asked DNS %d times despite the App Store", res2.calls.Load())
	}
}

func TestReverseDNSWithoutAppStore(t *testing.T) {
	res := &fakeResolver{names: map[string][]string{"1.1.1.1": {"one.one.one.one."}}}
	r := NewReverseDNS(ReverseDNSOptions{Resolver: res, Store: func() Persister { return nil }, Interval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)
	r.Names([]netip.Addr{one})
	waitFor(t, func() bool { return r.Names([]netip.Addr{one})[one].Status == NameResolved })
}

const ipv4Bootstrap = `{"version":"1.0","services":[
	[["1.0.0.0/8"],["%[1]s/apnic/","rdap.apnic.example/"]],
	[["8.0.0.0/8"],["%[1]s/arin/"]],
	[["9.0.0.0/8"],["%[1]s/arin/"]]
]}`

const apnicOne = `{
  "objectClassName": "ip network",
  "handle": "1.1.1.0 - 1.1.1.255",
  "startAddress": "1.1.1.0",
  "endAddress": "1.1.1.255",
  "name": "APNIC-LABS",
  "type": "ASSIGNED PORTABLE",
  "country": "au",
  "cidr0_cidrs": [{"v4prefix": "1.1.1.0", "length": 24}],
  "events": [{"eventAction": "registration", "eventDate": "2011-08-10T23:12:35Z"}, {"eventAction": "last changed", "eventDate": "2023-04-26T22:57:58Z"}],
  "entities": [
    {"handle": "ORG-ARAD1-AP", "roles": ["registrant"], "vcardArray": ["vcard", [["version", {}, "text", "4.0"], ["fn", {}, "text", "APNIC Research and Development"], ["kind", {}, "text", "org"]]],
     "entities": [{"handle": "IRT-APNICRANDNET-AU", "roles": ["abuse"], "vcardArray": ["vcard", [["version", {}, "text", "4.0"], ["fn", {}, "text", "IRT-APNICRANDNET-AU"], ["email", {}, "text", "helpdesk@apnic.net"]]]}]}
  ]
}`

const arinGoogle = `{
  "objectClassName": "ip network",
  "handle": "NET-8-8-8-0-2",
  "startAddress": "8.8.8.0",
  "endAddress": "8.8.9.255",
  "name": "GOGL",
  "entities": [{"roles": ["registrant"], "vcardArray": ["vcard", [["fn", {}, "text", "Google LLC"]]]},
    {"roles": ["abuse"], "vcardArray": ["vcard", [["fn", {}, "text", "Abuse"], ["email", {}, "text", "network-abuse@google.com"]]]}]
}`

func TestRDAP(t *testing.T) {
	var requests atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/ipv4.json":
			w.Write([]byte(strings.ReplaceAll(ipv4Bootstrap, "%[1]s", srv.URL)))
		case "/ipv6.json":
			w.Write([]byte(`{"services":[[["2001:4860::/32"],["` + srv.URL + `/arin/"]]]}`))
		case "/apnic/ip/1.1.1.1":
			if r.Header.Get("Accept") != "application/rdap+json" {
				http.Error(w, "bad accept", http.StatusNotAcceptable)
				return
			}
			w.Write([]byte(apnicOne))
		case "/arin/ip/8.8.8.8":
			// ARIN sends some queries on to the registry that holds them.
			http.Redirect(w, r, srv.URL+"/other/ip/8.8.8.8", http.StatusMovedPermanently)
		case "/other/ip/8.8.8.8":
			w.Write([]byte(arinGoogle))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	store := newMemStore()
	r := NewRDAP(RDAPOptions{BootstrapURL: srv.URL, Interval: time.Millisecond, Store: func() Persister { return store }})
	ctx := context.Background()

	res, err := r.Lookup(ctx, one)
	if err != nil {
		t.Fatal(err)
	}
	reg := res.Registration
	if res.Status != RDAPFound || reg.Name != "APNIC-LABS" || reg.Country != "AU" || reg.Organisation != "APNIC Research and Development" ||
		reg.AbuseEmail != "helpdesk@apnic.net" || len(reg.CIDRs) != 1 || reg.CIDRs[0] != "1.1.1.0/24" || reg.Registered == nil ||
		reg.Registered.Year() != 2011 || reg.LastChanged == nil || !strings.HasPrefix(reg.Server, "127.0.0.1") {
		t.Errorf("1.1.1.1: %+v", reg)
	}

	res, err = r.Lookup(ctx, google)
	if err != nil {
		t.Fatal(err)
	}
	// No cidr0_cidrs: computed from the range.
	if res.Registration.Organisation != "Google LLC" || res.Registration.AbuseEmail != "network-abuse@google.com" ||
		strings.Join(res.Registration.CIDRs, ",") != "8.8.8.0/23" {
		t.Errorf("8.8.8.8: %+v", res.Registration)
	}

	if res, err := r.Lookup(ctx, quad9); err != nil || res.Status != RDAPNotFound {
		t.Errorf("9.9.9.9: %+v %v", res, err)
	}
	if _, err := r.Lookup(ctx, netip.MustParseAddr("100.0.0.1")); err == nil {
		t.Error("expected an error for an address no registry is listed for")
	}
	if _, err := r.Lookup(ctx, lan); !errors.Is(err, ErrNotPublic) {
		t.Errorf("private: %v", err)
	}

	// Cached: no more requests, also from a new instance with the same App Store.
	before := requests.Load()
	r.Lookup(ctx, one)
	r2 := NewRDAP(RDAPOptions{BootstrapURL: srv.URL, Store: func() Persister { return store }})
	if res, err := r2.Lookup(ctx, google); err != nil || res.Registration.Organisation != "Google LLC" {
		t.Errorf("from the App Store: %+v %v", res, err)
	}
	if requests.Load() != before {
		t.Errorf("%d requests despite the cache", requests.Load()-before)
	}
}

func TestRDAPRateLimitedRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, ".json") {
			w.Write([]byte(`{"services":[[["1.0.0.0/8"],["http://` + r.Host + `/"]]]}`))
			return
		}
		http.Error(w, "slow down", http.StatusTooManyRequests)
	}))
	defer srv.Close()
	store := newMemStore()
	r := NewRDAP(RDAPOptions{BootstrapURL: srv.URL, Interval: time.Millisecond, Store: func() Persister { return store }})
	if _, err := r.Lookup(context.Background(), one); err == nil || !strings.Contains(err.Error(), "limiting") {
		t.Errorf("err = %v", err)
	}
	if store.has("rdap", "1.1.1.1") {
		t.Error("a refusal must not be cached")
	}
}

func TestRangePrefixes(t *testing.T) {
	for _, c := range []struct{ start, end, want string }{
		{"8.8.8.0", "8.8.9.255", "8.8.8.0/23"},
		{"10.0.0.0", "10.0.0.0", "10.0.0.0/32"},
		{"192.0.2.0", "192.0.2.130", "192.0.2.0/25,192.0.2.128/31,192.0.2.130/32"},
		{"2001:db8::", "2001:db8:ffff:ffff:ffff:ffff:ffff:ffff", "2001:db8::/32"},
	} {
		var got []string
		for _, p := range rangePrefixes(netip.MustParseAddr(c.start), netip.MustParseAddr(c.end)) {
			got = append(got, p.String())
		}
		if strings.Join(got, ",") != c.want {
			t.Errorf("%s-%s: %v, want %s", c.start, c.end, got, c.want)
		}
	}
}

func TestPickURLPrefersHTTPS(t *testing.T) {
	if got := pickURL([]string{"http://rdap.example/", "https://rdap.example/"}); got != "https://rdap.example/" {
		t.Errorf("got %q", got)
	}
}
