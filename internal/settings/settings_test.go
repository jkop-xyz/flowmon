package settings

import (
	"context"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type memStore map[string]string

func (m memStore) Setting(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}

func (m memStore) SetSettings(_ context.Context, values map[string]string) error {
	for k, v := range values {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	return nil
}

const day = 24 * time.Hour

func env() Env {
	return Env{LocalNetworks: classify.DefaultLocalNetworks(), Retention: flowstore.DefaultRetention()}
}

func dur(d time.Duration) *time.Duration { return &d }

func TestDefaultsThenStoredValues(t *testing.T) {
	ctx := context.Background()
	store := memStore{}
	m, err := Load(ctx, store, env())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Current().LocalNetworks) != 6 || m.Current().Retention.Raw != 7*day {
		t.Fatalf("defaults %+v", m.Current())
	}
	var seen []Values
	m.OnChange(func(v Values) { seen = append(seen, v) })
	nets := []netip.Prefix{netip.MustParsePrefix("192.168.88.0/24"), netip.MustParsePrefix("192.168.88.7/24"), netip.MustParsePrefix("2001:db8:1::/48")}
	if _, err := m.Update(ctx, Update{LocalNetworks: nets, Raw: dur(14 * day)}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 1 || len(seen[0].LocalNetworks) != 2 || seen[0].Retention.Raw != 14*day {
		t.Errorf("change %+v", seen)
	}
	// A restart reads them back.
	m2, err := Load(ctx, store, env())
	if err != nil {
		t.Fatal(err)
	}
	if c := m2.Current(); len(c.LocalNetworks) != 2 || c.LocalNetworks[1] != netip.MustParsePrefix("2001:db8:1::/48") || c.Retention.Raw != 14*day || c.Retention.Hour != 730*day {
		t.Errorf("reloaded %+v", c)
	}
}

func TestValidation(t *testing.T) {
	ctx := context.Background()
	m, _ := Load(ctx, memStore{}, env())
	for name, u := range map[string]Update{
		"no networks":        {LocalNetworks: []netip.Prefix{}},
		"every address":      {LocalNetworks: []netip.Prefix{netip.MustParsePrefix("0.0.0.0/0")}},
		"too short":          {Raw: dur(time.Hour)},
		"too long":           {Hour: dur(1000 * day)},
		"raw beyond minute":  {Raw: dur(100 * day)},
		"minute beyond hour": {Minute: dur(800 * day)},
	} {
		if _, err := m.Update(ctx, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if c := m.Current(); c.Retention.Raw != 7*day || len(c.LocalNetworks) != 6 {
		t.Errorf("a rejected update changed settings: %+v", c)
	}
}

func TestEnvironmentLocks(t *testing.T) {
	ctx := context.Background()
	cfg := config.Config{ExtraLocalNetworks: "100.64.0.0/10", RetentionRaw: 3 * day, RetentionMinute: 90 * day, RetentionHour: 730 * day}
	cfg.RetentionFromEnv.Raw = true
	e, err := FromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	// Stored values for locked settings are ignored.
	store := memStore{keyLocalNetworks: "10.0.0.0/8", keyRetentionRaw: (20 * day).String(), keyRetentionMinute: (30 * day).String()}
	m, err := Load(ctx, store, e)
	if err != nil {
		t.Fatal(err)
	}
	c := m.Current()
	if len(c.LocalNetworks) != 7 || c.Retention.Raw != 3*day || c.Retention.Minute != 30*day || !m.Locks().LocalNetworks || !m.Locks().RetentionRaw || m.Locks().RetentionMinute {
		t.Errorf("locked %+v %+v", c, m.Locks())
	}
	if _, err := m.Update(ctx, Update{Raw: dur(5 * day)}); !errors.Is(err, ErrLocked) {
		t.Errorf("changing locked retention: %v", err)
	}
	if _, err := m.Update(ctx, Update{LocalNetworks: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}}); !errors.Is(err, ErrLocked) {
		t.Errorf("changing locked networks: %v", err)
	}
	if _, err := m.Update(ctx, Update{Hour: dur(365 * day)}); err != nil {
		t.Errorf("changing an unlocked one: %v", err)
	}
}

func TestDNSRetention(t *testing.T) {
	ctx := context.Background()
	store := memStore{}
	m, err := Load(ctx, store, env())
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Current().Retention.DNS; got != 7*day {
		t.Fatalf("default DNS retention %v", got)
	}
	// DNS Queries are kept apart: longer than raw Flows is fine.
	if _, err := m.Update(ctx, Update{DNS: dur(30 * day)}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(ctx, Update{DNS: dur(time.Hour)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("an hour of DNS: %v", err)
	}
	m2, _ := Load(ctx, store, env())
	if got := m2.Current().Retention.DNS; got != 30*day {
		t.Errorf("reloaded DNS retention %v", got)
	}

	cfg := config.Config{RetentionRaw: 7 * day, RetentionMinute: 90 * day, RetentionHour: 730 * day, RetentionDNS: 2 * day}
	cfg.RetentionFromEnv.DNS = true
	e, err := FromConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m3, _ := Load(ctx, store, e)
	if m3.Current().Retention.DNS != 2*day || !m3.Locks().RetentionDNS {
		t.Errorf("FLOWMON_RETENTION_DNS: %v, locked %v", m3.Current().Retention.DNS, m3.Locks().RetentionDNS)
	}
	if _, err := m3.Update(ctx, Update{DNS: dur(3 * day)}); !errors.Is(err, ErrLocked) {
		t.Errorf("changing locked DNS retention: %v", err)
	}
}

// The internet line's speed is kept across restarts; 0 means unknown.
func TestLine(t *testing.T) {
	ctx := context.Background()
	store := memStore{}
	m, _ := Load(ctx, store, env())
	if m.Current().Line != (Line{}) {
		t.Errorf("a line before one is set: %+v", m.Current().Line)
	}
	if _, err := m.Update(ctx, Update{Line: &Line{Down: 500_000_000, Up: 100_000_000}}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Update(ctx, Update{Line: &Line{Down: 200_000_000_000}}); !errors.Is(err, ErrInvalid) {
		t.Errorf("200 Gbit/s: %v", err)
	}
	again, err := Load(ctx, store, env())
	if err != nil || again.Current().Line != (Line{500_000_000, 100_000_000}) {
		t.Errorf("after a restart: %+v %v", again.Current().Line, err)
	}
}
