package snmp

import (
	"context"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/secret"
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

func TestReconcile(t *testing.T) {
	store := openStore(t)
	now := t0.Add(5 * time.Hour).Add(10 * time.Minute)
	gb := uint64(1 << 30)
	for h := 0; h < 5; h++ {
		hour := t0.Add(time.Duration(h) * time.Hour)
		// 1 GiB in on the interface, plus 1 M packets of 14 B overhead.
		_ = store.AddSNMPDelta(ctx, exp, "ether1", flowstore.SNMPHour{Hour: hour, InOctets: gb + 14_000_000, InPackets: 1_000_000, OutOctets: 1000, Complete: true})
		flowBytes := gb
		if h >= 2 {
			flowBytes = gb / 2 // from hour 2 on, half the traffic is missing (e.g. hardware offload)
		}
		f := flow.Flow{Exporter: exp, Start: hour.Add(time.Minute), End: hour.Add(time.Minute), Received: hour, ExportTime: hour,
			Src: netip.MustParseAddr("1.1.1.1"), Dst: netip.MustParseAddr("192.168.10.44"), Bytes: flowBytes,
			IngressInterface: 1, EgressInterface: 2, Direction: flow.DirectionInbound, Local: netip.MustParseAddr("192.168.10.44"), Fields: []byte{0}}
		if err := store.Write(ctx, []flow.Flow{f}); err != nil {
			t.Fatal(err)
		}
	}
	reps, err := Reconcile(ctx, store, exp, map[string]uint32{"ether1": 1}, 14, now)
	if err != nil {
		t.Fatal(err)
	}
	r := reps[0]
	if len(r.Hours) != 5 || r.Hours[0].In.Gap == nil || *r.Hours[0].In.Gap != 0 {
		t.Fatalf("hours %+v", r.Hours)
	}
	if g := *r.Hours[4].In.Gap; g < 0.49 || g > 0.51 {
		t.Errorf("gap in hour 4: %v", g)
	}
	if r.Hours[0].Out.Gap != nil {
		t.Error("1000 bytes out is too little traffic to compare")
	}
	if !r.Warning {
		t.Error("three hours above 15% must warn")
	}
	// Only two bad hours: no warning yet.
	reps, _ = Reconcile(ctx, store, exp, map[string]uint32{"ether1": 1}, 14, t0.Add(4*time.Hour).Add(10*time.Minute))
	if reps[0].Warning {
		t.Error("two bad hours must not warn")
	}
}

func TestConfigValidationAndSecrets(t *testing.T) {
	for _, bad := range []Config{
		{Version: "v1"},
		{Version: "v3"},
		{Version: "v3", User: "u", AuthProtocol: "CRC"},
		{Version: "v3", User: "u", PrivProtocol: "AES"},
		{Version: "v2c", Overhead: "jumbo"},
		{Version: "v2c", Overhead: "custom", OverheadBytes: 99},
	} {
		if bad.Validate() == nil {
			t.Errorf("%+v should be invalid", bad)
		}
	}
	good := Config{Enabled: true, Version: "v3", User: "flowmon", AuthProtocol: "SHA256", AuthPassword: "authpass1", PrivProtocol: "AES", PrivPassword: "privpass1", Overhead: "pppoe", Target: "192.168.10.1:1161"}
	if err := good.Validate(); err != nil || good.OverheadPerPacket() != 22 {
		t.Fatalf("%v %d", err, good.OverheadPerPacket())
	}
	// Moved onto a Data Source, every setting carries over.
	moved := ConfigOf(SettingsOf(good, exp))
	moved.Enabled = good.Enabled
	if moved != good {
		t.Errorf("moved settings %+v, want %+v", moved, good)
	}
	if a, ok := ExporterOf(SettingsOf(good, exp)); !ok || a != exp {
		t.Errorf("the moved source's Exporter: %v %v", a, ok)
	}
	if a, ok := ExporterOf(map[string]string{"address": "192.168.10.1:1161"}); !ok || a != exp {
		t.Errorf("the Exporter from an address with a port: %v %v", a, ok)
	}
	if _, ok := ExporterOf(map[string]string{"address": "router.lan"}); ok {
		t.Error("a host name taken for an Exporter")
	}

	store := openStore(t)
	box, _ := secret.LoadOrCreate(filepath.Join(t.TempDir(), "k"))
	s := NewSettings(store, box)
	if err := s.Save(ctx, exp, good); err != nil {
		t.Fatal(err)
	}
	raw, _ := store.SNMPConfig(ctx, exp)
	if contains(raw, "authpass1") || contains(raw, "privpass1") {
		t.Errorf("secrets stored in the clear: %s", raw)
	}
	all, unreadable, err := s.All(ctx)
	if err != nil || all[exp].PrivPassword != "privpass1" || len(unreadable) != 0 {
		t.Errorf("%+v %v %v", all, unreadable, err)
	}

	// With a different key (secret.key lost), the config comes back disabled, not as an error.
	lost, _ := secret.LoadOrCreate(filepath.Join(t.TempDir(), "new"))
	all, unreadable, err = NewSettings(store, lost).All(ctx)
	if err != nil || len(unreadable) != 1 || all[exp].Enabled || all[exp].AuthPassword != "" || all[exp].User != "flowmon" {
		t.Errorf("lost key: %+v %v %v", all, unreadable, err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// With SNMP on and no WAN Interface marked yet, the report is an empty list, not
// null: the Exporters page reads its length.
func TestReconcileWithoutWANInterfacesIsAnEmptyList(t *testing.T) {
	reps, err := Reconcile(ctx, openStore(t), exp, map[string]uint32{}, 14, t0)
	if err != nil {
		t.Fatal(err)
	}
	if b, _ := json.Marshal(reps); string(b) != "[]" {
		t.Errorf("got %s, want []", b)
	}
}
