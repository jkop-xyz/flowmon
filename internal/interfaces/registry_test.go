package interfaces

import (
	"context"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

var (
	ctx = context.Background()
	exp = netip.MustParseAddr("192.168.10.1")
	t0  = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

func openStore(t *testing.T) *flowstore.Store {
	t.Helper()
	s, err := flowstore.Open(ctx, filepath.Join(t.TempDir(), "f.duckdb"), flowstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestMappingsHaveValidityRanges(t *testing.T) {
	s := openStore(t)
	r, _ := Load(ctx, s)
	if ok, err := r.SetName(ctx, exp, 3, "pppoe-out1", SourceUser, t0); !ok || err != nil {
		t.Fatalf("%v %v", ok, err)
	}
	// After a PPPoE reconnect the interface comes back with a new number.
	_, _ = r.SetName(ctx, exp, 3, "ether5", SourceUser, t0.Add(time.Hour))
	_, _ = r.SetName(ctx, exp, 7, "pppoe-out1", SourceUser, t0.Add(time.Hour))

	for _, c := range []struct {
		idx  uint32
		at   time.Time
		want string
	}{
		{3, t0.Add(30 * time.Minute), "pppoe-out1"},
		{3, t0.Add(2 * time.Hour), "ether5"},
		{7, t0.Add(2 * time.Hour), "pppoe-out1"},
		{3, t0.Add(-time.Hour), "pppoe-out1"}, // before the first mapping: the earliest name
	} {
		if got, _ := r.NameAt(exp, c.idx, c.at); got != c.want {
			t.Errorf("index %d at %v: %q, want %q", c.idx, c.at, got, c.want)
		}
	}
	if ch := r.Changes(exp, t0.Add(2*time.Hour)); len(ch) != 1 || ch[0].OldName != "pppoe-out1" || ch[0].NewName != "ether5" {
		t.Errorf("changes %+v", ch)
	}

	// Everything survives a restart.
	r2, err := Load(ctx, s)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := r2.NameAt(exp, 3, t0.Add(30*time.Minute)); got != "pppoe-out1" {
		t.Errorf("after reload: %q", got)
	}
	if cur := r2.Current(exp); len(cur) != 2 || cur[0].Name != "ether5" || cur[1].Name != "pppoe-out1" {
		t.Errorf("current %+v", cur)
	}
}

func TestUserNamesWinOverReportedOnes(t *testing.T) {
	r, _ := Load(ctx, nil)
	_, _ = r.SetName(ctx, exp, 1, "ether1", SourceOptions, t0)
	if ok, _ := r.SetName(ctx, exp, 1, "WAN uplink", SourceUser, t0.Add(time.Minute)); !ok {
		t.Fatal("the user can rename")
	}
	if ok, _ := r.SetName(ctx, exp, 1, "ether1", SourceSNMP, t0.Add(2*time.Minute)); ok {
		t.Error("SNMP must not override a name the user set")
	}
	ch := r.Changes(exp, t0.Add(time.Hour))
	if len(ch) != 2 || !ch[1].Conflict || ch[1].NewName != "ether1" {
		t.Errorf("changes %+v", ch)
	}
	if _, err := r.SetName(ctx, exp, 1, "", SourceUser, t0); err != ErrBadName {
		t.Errorf("empty name: %v", err)
	}
}

func TestRolesByName(t *testing.T) {
	s := openStore(t)
	r, _ := Load(ctx, s)
	_, _ = r.SetName(ctx, exp, 1, "ether1", SourceUser, t0)
	_, _ = r.SetName(ctx, exp, 2, "bridge", SourceUser, t0)
	if err := r.SetRoles(ctx, exp, map[string]Role{"ether1": RoleWAN, "bridge": RoleLAN, "wg0": RoleTunnel}); err != nil {
		t.Fatal(err)
	}
	if got := r.RoleAt(exp, 1, t0.Add(time.Minute)); got != classify.IfWAN {
		t.Errorf("ether1: %v", got)
	}
	if got := r.RoleAt(exp, 0, t0); got != classify.IfSelf {
		t.Errorf("index 0: %v", got)
	}
	if got := r.RoleAt(exp, 9, t0); got != classify.IfUnknown {
		t.Errorf("unmapped: %v", got)
	}
	// The role follows the name, not the number: renumbering keeps it.
	_, _ = r.SetName(ctx, exp, 5, "ether1", SourceUser, t0.Add(time.Hour))
	_, _ = r.SetName(ctx, exp, 1, "ether9", SourceUser, t0.Add(time.Hour))
	if r.RoleAt(exp, 5, t0.Add(2*time.Hour)) != classify.IfWAN || r.RoleAt(exp, 1, t0.Add(2*time.Hour)) != classify.IfUnknown {
		t.Error("roles must follow the interface name")
	}
	if err := r.SetRoles(ctx, exp, map[string]Role{"x": "dmz"}); err != ErrBadRole {
		t.Errorf("bad role: %v", err)
	}
	r2, _ := Load(ctx, s)
	if got := r2.Roles(exp); got["ether1"] != RoleWAN || got["wg0"] != RoleTunnel {
		t.Errorf("after reload %+v", got)
	}
}
