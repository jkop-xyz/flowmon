package hosts

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/enrichment"
	"github.com/jkop-xyz/flowmon/internal/storecache"
)

type fakeStore struct {
	labels []appstore.HostLabel
	err    error
	reads  int
}

func (f *fakeStore) HostLabels(context.Context) ([]appstore.HostLabel, error) {
	f.reads++
	return append([]appstore.HostLabel(nil), f.labels...), f.err
}

func (f *fakeStore) CreateHostLabel(_ context.Context, p netip.Prefix, name string, now time.Time) (appstore.HostLabel, error) {
	for _, l := range f.labels {
		if l.Prefix == p {
			return appstore.HostLabel{}, appstore.ErrExists
		}
	}
	l := appstore.HostLabel{ID: name, Prefix: p, Name: name, CreatedAt: now, UpdatedAt: now}
	f.labels = append(f.labels, l)
	return l, nil
}

func (f *fakeStore) UpdateHostLabel(_ context.Context, id string, p netip.Prefix, name string, now time.Time) (appstore.HostLabel, error) {
	for i, l := range f.labels {
		if l.ID == id {
			f.labels[i].Prefix, f.labels[i].Name = p, name
			return f.labels[i], nil
		}
	}
	return appstore.HostLabel{}, appstore.ErrNotFound
}

func (f *fakeStore) DeleteHostLabel(_ context.Context, id string) error {
	for i, l := range f.labels {
		if l.ID == id {
			f.labels = append(f.labels[:i], f.labels[i+1:]...)
			return nil
		}
	}
	return appstore.ErrNotFound
}

type fakeDNS map[netip.Addr]enrichment.Name

func (f fakeDNS) Names(ips []netip.Addr) map[netip.Addr]enrichment.Name {
	out := map[netip.Addr]enrichment.Name{}
	for _, ip := range ips {
		n, ok := f[ip]
		if !ok {
			n = enrichment.Name{Status: enrichment.NamePending}
		}
		out[ip] = n
	}
	return out
}

func key(s string) netip.Prefix {
	p, err := ParseKey(s)
	if err != nil {
		panic(err)
	}
	return p
}

func TestParseKey(t *testing.T) {
	for in, want := range map[string]string{
		"10.0.0.5":            "10.0.0.5",
		"::ffff:10.0.0.5":     "10.0.0.5",
		"10.0.0.77/24":        "10.0.0.0/24",
		"2001:db8::1234/64":   "2001:db8::/64",
		"::ffff:10.0.0.0/120": "10.0.0.0/24",
		"2001:db8::1":         "2001:db8::1",
		"2001:db8::1/128":     "2001:db8::1",
	} {
		p, err := ParseKey(in)
		if err != nil || KeyString(p) != want {
			t.Errorf("ParseKey(%q) = %v, %v; want %s", in, KeyString(p), err, want)
		}
	}
	for _, in := range []string{"", "host", "fe80::1%eth0", "10.0.0.0/33"} {
		if _, err := ParseKey(in); err == nil {
			t.Errorf("ParseKey(%q) accepted", in)
		}
	}
}

func TestNames(t *testing.T) {
	store := &fakeStore{labels: []appstore.HostLabel{
		{ID: "lan", Prefix: key("2001:db8::/64"), Name: "Phone"},
		{ID: "nas", Prefix: key("192.168.1.10"), Name: "NAS"},
		{ID: "pool", Prefix: key("192.168.1.0/24"), Name: "LAN"},
	}}
	dns := fakeDNS{netip.MustParseAddr("10.0.0.2"): {Name: "printer.lan", Status: enrichment.NameResolved}}
	d := NewDirectory(func() LabelStore { return store }, dns)
	got := d.Names(context.Background(), []netip.Prefix{
		key("192.168.1.10"), key("192.168.1.11"), key("2001:db8::abcd"), key("2001:db8::/64"),
		key("10.0.0.2"), key("10.0.0.3"), key("10.9.0.0/16"),
	})
	want := map[string]Name{
		"192.168.1.10":   {Name: "NAS", Source: SourceLabel, Status: "resolved", Label: &LabelRef{"nas", "192.168.1.10", "NAS"}},
		"192.168.1.11":   {Name: "LAN", Source: SourceLabel, Status: "resolved", Label: &LabelRef{"pool", "192.168.1.0/24", "LAN"}},
		"2001:db8::abcd": {Name: "Phone", Source: SourceLabel, Status: "resolved", Label: &LabelRef{"lan", "2001:db8::/64", "Phone"}},
		"2001:db8::/64":  {Name: "Phone", Source: SourceLabel, Status: "resolved", Label: &LabelRef{"lan", "2001:db8::/64", "Phone"}},
		"10.0.0.2":       {Name: "printer.lan", Source: SourceDNS, Status: "resolved"},
		"10.0.0.3":       {Status: "pending"},
		"10.9.0.0/16":    {Status: "none"},
	}
	for k, w := range want {
		g := got[key(k)]
		if g.Name != w.Name || g.Source != w.Source || g.Status != w.Status || (g.Label == nil) != (w.Label == nil) || (g.Label != nil && *g.Label != *w.Label) {
			t.Errorf("%s: got %+v (label %+v), want %+v", k, g, g.Label, w)
		}
	}
	groups := d.Groups(context.Background())
	if len(groups) != 3 || !slices.Contains(groups, key("192.168.1.10")) || !slices.Contains(groups, key("2001:db8::/64")) || !slices.Contains(groups, key("192.168.1.0/24")) {
		t.Errorf("groups %v", groups)
	}
}

func TestLabelChanges(t *testing.T) {
	store := &fakeStore{}
	var current LabelStore
	d := NewDirectory(func() LabelStore { return current }, nil)
	ctx := context.Background()
	if _, err := d.Create(ctx, LabelInput{Prefix: "10.0.0.1", Name: "x"}); !errors.Is(err, ErrUnavailable) {
		t.Errorf("without an App Store: %v", err)
	}
	current = store
	for _, in := range []LabelInput{{Prefix: "10.0.0.1", Name: "  "}, {Prefix: "nope", Name: "x"}, {Prefix: "0.0.0.0/0", Name: "x"}, {Prefix: "10.0.0.1", Name: "a\nb"}} {
		if _, err := d.Create(ctx, in); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", in, err)
		}
	}
	l, err := d.Create(ctx, LabelInput{Prefix: " 10.0.0.1 ", Name: " Desk "})
	if err != nil || l.Name != "Desk" || l.Prefix != key("10.0.0.1") {
		t.Fatalf("created %+v %v", l, err)
	}
	if n := d.Names(ctx, []netip.Prefix{key("10.0.0.1")})[key("10.0.0.1")]; n.Name != "Desk" {
		t.Errorf("name after create: %+v", n)
	}
	if _, err := d.Update(ctx, l.ID, LabelInput{Prefix: "10.0.0.0/30", Name: "Desk"}); err != nil {
		t.Fatal(err)
	}
	if g := d.Groups(ctx); len(g) != 1 || g[0] != key("10.0.0.0/30") {
		t.Errorf("groups after update: %v", g)
	}
	if err := d.Delete(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if n := d.Names(ctx, []netip.Prefix{key("10.0.0.1")})[key("10.0.0.1")]; n.Name != "" || n.Status != "none" {
		t.Errorf("name after delete: %+v", n)
	}
}

func TestLabelsRetryWhileFailing(t *testing.T) {
	store := &fakeStore{err: errors.New("down")}
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	d := NewDirectory(func() LabelStore { return store }, nil)
	d.labels.SetClock(func() time.Time { return now })
	ctx := context.Background()
	d.Labels(ctx)
	d.Labels(ctx)
	if store.reads != 1 {
		t.Errorf("reads while failing: %d", store.reads)
	}
	store.err = nil
	store.labels = []appstore.HostLabel{{ID: "a", Prefix: key("10.0.0.1"), Name: "A"}}
	now = now.Add(storecache.RetryAfter)
	if l := d.Labels(ctx); len(l) != 1 {
		t.Errorf("labels after recovery: %v", l)
	}
	d.Labels(ctx)
	if store.reads != 2 {
		t.Errorf("reads once loaded: %d", store.reads)
	}
}
