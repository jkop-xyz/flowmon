package exporters

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type memPersister struct {
	mu   sync.Mutex
	recs map[netip.Addr]flowstore.ExporterRecord
}

func newMem() *memPersister { return &memPersister{recs: map[netip.Addr]flowstore.ExporterRecord{}} }

func (m *memPersister) ExporterRecords(context.Context) ([]flowstore.ExporterRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []flowstore.ExporterRecord
	for _, r := range m.recs {
		out = append(out, r)
	}
	return out, nil
}
func (m *memPersister) SaveExporterRecord(_ context.Context, r flowstore.ExporterRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.recs[r.Address] = r
	return nil
}
func (m *memPersister) DeleteExporterRecord(_ context.Context, a netip.Addr) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.recs, a)
	return nil
}
func (m *memPersister) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.recs)
}

var (
	ctx    = context.Background()
	router = netip.MustParseAddr("192.168.10.1")
	t0     = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

func load(t *testing.T, opts Options) *Registry {
	t.Helper()
	opts.Now = func() time.Time { return t0 }
	r, err := Load(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestNewExportersArePendingUntilApproved(t *testing.T) {
	p := newMem()
	r := load(t, Options{Persister: p})
	if s := r.Status(router); s != StatusPending {
		t.Fatalf("status %s", s)
	}
	c, err := r.Approve(ctx, router, "", "", "MikroTik", "admin")
	if err != nil || c.Role != RoleEdge || c.ClassificationMode != ByAddress || !c.DecidedAt.Equal(t0) {
		t.Fatalf("%+v %v", c, err)
	}
	if s := r.Status(router); s != StatusApproved {
		t.Errorf("status %s", s)
	}

	// Decisions survive a restart.
	r2 := load(t, Options{Persister: p})
	if c, ok := r2.Config(router); !ok || c.Name != "MikroTik" || c.Status != StatusApproved {
		t.Errorf("after reload %+v %v", c, ok)
	}

	if _, err := r.Ignore(ctx, router, "admin"); err != nil {
		t.Fatal(err)
	}
	if s := r.Status(router); s != StatusIgnored {
		t.Errorf("status %s", s)
	}
	if err := r.Forget(ctx, router); err != nil {
		t.Fatal(err)
	}
	if s := r.Status(router); s != StatusPending || p.count() != 0 {
		t.Errorf("after forget: %s, %d stored", s, p.count())
	}
}

func TestUnsupportedChoicesAreRefused(t *testing.T) {
	r := load(t, Options{})
	if _, err := r.Approve(ctx, router, RoleInternal, ByAddress, "", "admin"); !errors.Is(err, ErrUnsupportedRole) {
		t.Errorf("internal role: %v", err)
	}
	if _, err := r.Approve(ctx, router, RoleEdge, "magic", "", "admin"); !errors.Is(err, ErrUnsupportedMode) {
		t.Errorf("unknown mode: %v", err)
	}
	if _, err := r.SetMode(ctx, router, ByInterface, "admin"); !errors.Is(err, ErrNotApproved) {
		t.Errorf("mode before approval: %v", err)
	}
	if _, err := r.Approve(ctx, router, RoleEdge, ByInterface, "", "admin"); err != nil || !r.ByInterface(router) {
		t.Errorf("by interface: %v", err)
	}
	if c, err := r.SetMode(ctx, router, ByAddress, "admin"); err != nil || c.ClassificationMode != ByAddress || r.ByInterface(router) {
		t.Errorf("set mode: %+v %v", c, err)
	}
}

func TestAutoAcceptAndAllowlist(t *testing.T) {
	p := newMem()
	r := load(t, Options{Persister: p, AutoAccept: true})
	if s := r.Status(router); s != StatusApproved {
		t.Fatalf("auto-accept: %s", s)
	}
	if c, _ := r.Config(router); c.DecidedBy != "auto-accept" {
		t.Errorf("decided by %q", c.DecidedBy)
	}
	deadline := time.Now().Add(2 * time.Second)
	for p.count() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if p.count() != 1 {
		t.Error("auto-accepted Exporters are persisted, so turning auto-accept off keeps them")
	}

	a := load(t, Options{Allowlist: []netip.Prefix{netip.MustParsePrefix("192.168.10.0/24")}})
	if s := a.Status(router); s != StatusApproved {
		t.Errorf("allowlisted: %s", s)
	}
	if s := a.Status(netip.MustParseAddr("203.0.113.9")); s != StatusPending {
		t.Errorf("outside the allowlist: %s", s)
	}
	// An explicit decision wins over the allowlist.
	_, _ = a.Ignore(ctx, router, "admin")
	if s := a.Status(router); s != StatusIgnored {
		t.Errorf("ignored but allowlisted: %s", s)
	}
}

func TestSamplingRateSetByHand(t *testing.T) {
	p := newMem()
	r := load(t, Options{Persister: p})
	a := netip.MustParseAddr("192.168.10.1")
	if _, err := r.SetSamplingRate(ctx, a, 100, "admin"); !errors.Is(err, ErrNotApproved) {
		t.Errorf("before approval: %v", err)
	}
	if _, err := r.Approve(ctx, a, RoleEdge, ByAddress, "", "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetSamplingRate(ctx, a, 1<<30, "admin"); !errors.Is(err, ErrSamplingRate) {
		t.Errorf("absurd rate: %v", err)
	}
	if _, err := r.SetSamplingRate(ctx, a, 100, "admin"); err != nil {
		t.Fatal(err)
	}
	if got := load(t, Options{Persister: p}).SamplingRate(a); got != 100 {
		t.Errorf("after a restart: %d", got)
	}
	// 1 in 1 is every packet: the same as none.
	if c, err := r.SetSamplingRate(ctx, a, 1, "admin"); err != nil || c.SamplingRate != 0 {
		t.Errorf("%+v %v", c, err)
	}
}
