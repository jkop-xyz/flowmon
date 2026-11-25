package adguard

import (
	"context"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/adguard/adguardtest"
	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

var (
	ctx    = context.Background()
	t0     = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	router = netip.MustParseAddr("192.168.10.1")
)

type reports struct {
	mu        sync.Mutex
	successes []string
	failures  []error
	warnings  []string
}

func (r *reports) Succeeded(d string) {
	r.mu.Lock()
	r.successes = append(r.successes, d)
	r.mu.Unlock()
}
func (r *reports) Failed(err error) { r.mu.Lock(); r.failures = append(r.failures, err); r.mu.Unlock() }
func (r *reports) Warn(w []string)  { r.mu.Lock(); r.warnings = w; r.mu.Unlock() }

type rig struct {
	t     *testing.T
	store *flowstore.Store
	fake  *adguardtest.Server
	url   string
	now   time.Time
	kind  *Kind
}

func newRig(t *testing.T, pageSize int) *rig {
	t.Helper()
	store, err := flowstore.Open(ctx, filepath.Join(t.TempDir(), "flows.duckdb"), flowstore.Options{MemoryLimit: "256MB", Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	fake := adguardtest.New("admin", "pw")
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	r := &rig{t: t, store: store, fake: fake, url: srv.URL, now: t0}
	r.kind = New(Options{Store: store, Now: func() time.Time { return r.now }, PageSize: pageSize,
		IsExporter: func(a netip.Addr) bool { return a == router },
		Retention:  func() time.Duration { return 7 * 24 * time.Hour }})
	return r
}

func (r *rig) settings() datasource.Settings {
	return datasource.Settings{"url": r.url, "username": "admin", "password": "pw"}
}

// poller returns a fresh poller for source id: a restart.
func (r *rig) poller(id string) (*poller, *reports) {
	rep := &reports{}
	return &poller{k: r.kind, src: id, c: clientFor(r.settings()), r: rep}, rep
}

func (r *rig) stored() []flowstore.DNSQuery {
	r.t.Helper()
	qs, err := r.store.DNSQueries(ctx, t0.Add(-30*24*time.Hour), t0.Add(24*time.Hour), 100000)
	if err != nil {
		r.t.Fatal(err)
	}
	return qs
}

func names(qs []flowstore.DNSQuery) []string {
	var out []string
	for _, q := range qs {
		out = append(out, q.Name)
	}
	return out
}

func TestCollectsEachQueryOnceAcrossPollsAndRestarts(t *testing.T) {
	r := newRig(t, 500)
	r.fake.Add(
		adguardtest.Query(t0.Add(-3*time.Minute), "192.168.10.44", "www.example.com", "A", "CNAME edge.example.net", "A 93.184.215.14", "A 93.184.215.15"),
		adguardtest.Blocked(adguardtest.Query(t0.Add(-2*time.Minute), "192.168.10.44", "ads.tracker.example", "A"), "||tracker.example^"),
		adguardtest.Query(t0.Add(-time.Minute), "fd00::44", "WWW.Example.ORG.", "AAAA", "AAAA 2606:2800:21f:cb07:6820:80da:af6b:8b2c"),
	)
	p, rep := r.poller("a1")
	if err := p.poll(ctx); err != nil {
		t.Fatal(err)
	}
	got := r.stored()
	if !slices.Equal(names(got), []string{"www.example.com", "ads.tracker.example", "www.example.org"}) {
		t.Fatalf("stored %v", names(got))
	}
	first := got[0]
	if first.Registrable != "example.com" || len(first.Answers) != 2 || first.Answers[1] != netip.MustParseAddr("93.184.215.15") ||
		!slices.Equal(first.CNAMEs, []string{"edge.example.net"}) || first.Elapsed != 12500*time.Microsecond || first.Source != "a1" {
		t.Errorf("first query %+v", first)
	}
	if b := got[1]; !b.Blocked || b.Rule != "||tracker.example^" || b.Reason != "FilteredBlackList" {
		t.Errorf("blocked query %+v", b)
	}
	if rep.successes[0] != "3 new queries" {
		t.Errorf("reported %v", rep.successes)
	}

	// A later poll stores only what is new, including a query at the same instant as
	// the newest stored one.
	r.fake.Add(
		adguardtest.Query(t0.Add(-time.Minute), "192.168.10.45", "same.instant.example", "A"),
		adguardtest.Query(t0, "192.168.10.44", "new.example", "A"),
	)
	r.now = t0.Add(15 * time.Second)
	if err := p.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(r.stored()); n != 5 {
		t.Errorf("%d stored after the second poll, want 5: %v", n, names(r.stored()))
	}

	// After a restart nothing is stored twice.
	p2, rep2 := r.poller("a1")
	if err := p2.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(r.stored()); n != 5 || rep2.successes[0] != "0 new queries" {
		t.Errorf("%d stored after a restart (%v), want 5", n, rep2.successes)
	}
}

func TestPagesBackToWhatWasStored(t *testing.T) {
	r := newRig(t, 3)
	for i := 0; i < 10; i++ {
		r.fake.Add(adguardtest.Query(t0.Add(-time.Duration(10-i)*time.Minute), "192.168.10.44", "n"+string(rune('a'+i))+".example", "A"))
	}
	p, _ := r.poller("a1")
	if err := p.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n := len(r.stored()); n != 10 {
		t.Fatalf("stored %d of 10", n)
	}
	before := r.fake.Requests
	r.fake.Add(adguardtest.Query(t0.Add(time.Second), "192.168.10.44", "late.example", "A"))
	if err := p.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if n, reqs := len(r.stored()), r.fake.Requests-before; n != 11 || reqs != 1 {
		t.Errorf("stored %d with %d requests; want 11 with one page", n, reqs)
	}
}

func TestFirstPollReachesBackADay(t *testing.T) {
	r := newRig(t, 500)
	r.fake.Add(
		adguardtest.Query(t0.Add(-30*time.Hour), "192.168.10.44", "too.old.example", "A"),
		adguardtest.Query(t0.Add(-23*time.Hour), "192.168.10.44", "recent.example", "A"),
	)
	p, _ := r.poller("a1")
	if err := p.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if got := names(r.stored()); !slices.Equal(got, []string{"recent.example"}) {
		t.Errorf("stored %v", got)
	}
}

func TestGapInTheLogIsAWarning(t *testing.T) {
	r := newRig(t, 500)
	r.fake.Add(adguardtest.Query(t0.Add(-48*time.Hour), "192.168.10.44", "before.example", "A"))
	r.now = t0.Add(-47 * time.Hour)
	p, _ := r.poller("a1")
	if err := p.poll(ctx); err != nil {
		t.Fatal(err)
	}
	// Flowmon is off for two days; AdGuard Home's log rotated in the meantime.
	r.fake.Add(adguardtest.Query(t0.Add(-time.Hour), "192.168.10.44", "after.example", "A"))
	r.fake.DropBefore(t0.Add(-2 * time.Hour))
	r.now = t0
	p2, rep := r.poller("a1")
	if err := p2.poll(ctx); err != nil {
		t.Fatal(err)
	}
	if len(rep.warnings) != 1 || !strings.Contains(rep.warnings[0], "no longer had the queries from 2026-11-22T12:00:00Z") {
		t.Errorf("warnings %v", rep.warnings)
	}
	if got := names(r.stored()); !slices.Equal(got, []string{"before.example", "after.example"}) {
		t.Errorf("stored %v", got)
	}
}

func TestWarnsWhenQueriesComeThroughTheRouter(t *testing.T) {
	for name, tc := range map[string]struct {
		client string
		warn   bool
	}{"router": {"192.168.10.1", true}, "one busy device": {"192.168.10.44", false}} {
		t.Run(name, func(t *testing.T) {
			r := newRig(t, 500)
			for i := 0; i < 300; i++ {
				r.fake.Add(adguardtest.Query(t0.Add(-time.Duration(i)*time.Second), tc.client, "x.example", "A"))
			}
			r.fake.Add(adguardtest.Query(t0.Add(-time.Hour), "192.168.10.45", "y.example", "A"))
			p, rep := r.poller("a1")
			if err := p.poll(ctx); err != nil {
				t.Fatal(err)
			}
			warned := len(rep.warnings) == 1 && strings.Contains(rep.warnings[0], "through the router")
			if warned != tc.warn {
				t.Errorf("warnings %v", rep.warnings)
			}
		})
	}
}

func TestTwoSourcesMergeIntoOneLog(t *testing.T) {
	r := newRig(t, 500)
	r.fake.Add(adguardtest.Query(t0.Add(-time.Minute), "192.168.10.44", "a.example", "A"))
	other := adguardtest.New("admin", "pw")
	other.Add(adguardtest.Query(t0.Add(-30*time.Second), "192.168.10.44", "b.example", "A"))
	srv := httptest.NewServer(other)
	defer srv.Close()

	pa, _ := r.poller("a1")
	pb := &poller{k: r.kind, src: "b2", c: newClient(srv.URL, "admin", "pw", false), r: &reports{}}
	for _, p := range []*poller{pa, pb, pa, pb} {
		if err := p.poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	got := r.stored()
	if len(got) != 2 || got[0].Source != "a1" || got[1].Source != "b2" {
		t.Errorf("merged log %+v", got)
	}
}

func TestTest(t *testing.T) {
	r := newRig(t, 500)
	msg, err := r.kind.Test(ctx, r.settings())
	if err != nil || !strings.Contains(msg, "AdGuard Home v0.107.57") || !strings.Contains(msg, "keeps 90 days") {
		t.Errorf("Test = %q, %v", msg, err)
	}
	bad := r.settings()
	bad["password"] = "wrong"
	if _, err := r.kind.Test(ctx, bad); err == nil || !strings.Contains(err.Error(), "refused") {
		t.Errorf("wrong password: %v", err)
	}
	r.fake.LogOff = true
	if _, err := r.kind.Test(ctx, r.settings()); err == nil || !strings.Contains(err.Error(), "query log is off") {
		t.Errorf("log off: %v", err)
	}
}

func TestRunReportsFailures(t *testing.T) {
	r := newRig(t, 500)
	k := New(Options{Store: r.store, Interval: 10 * time.Millisecond})
	rep := &reports{}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		k.Run(runCtx, datasource.Source{ID: "a1", Settings: datasource.Settings{"url": r.url, "username": "admin", "password": "nope"}}, rep)
		close(done)
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		rep.mu.Lock()
		n := len(rep.failures)
		rep.mu.Unlock()
		if n >= 2 {
			break
		}
	}
	cancel()
	<-done
	if len(rep.failures) < 2 || !strings.Contains(rep.failures[0].Error(), "refused") || len(rep.successes) != 0 {
		t.Errorf("failures %v, successes %v", rep.failures, rep.successes)
	}
}

// AdGuard Home writes times to the nanosecond; the Flow Store keeps microseconds. The
// newest stored query still matches its log entry after a restart.
func TestNanosecondTimesAreStoredOnceAcrossRestarts(t *testing.T) {
	r := newRig(t, 500)
	r.fake.Add(adguardtest.Query(t0.Add(-time.Minute+123456789*time.Nanosecond), "192.168.10.44", "www.example.com", "A"))
	for range 2 {
		p, _ := r.poller("a1")
		if err := p.poll(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := r.stored(); len(got) != 1 {
		t.Errorf("stored %v; want the query once", names(got))
	}
}
