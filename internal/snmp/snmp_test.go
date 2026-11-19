package snmp

import (
	"context"
	"encoding/json"
	"net/netip"
	"path/filepath"
	"sync"
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

type fakeAgent struct {
	names    map[uint32]string
	counters map[uint32]Counters
}

func (a *fakeAgent) InterfaceNames(context.Context) (map[uint32]string, error) { return a.names, nil }
func (a *fakeAgent) Counters(_ context.Context, idxs []uint32) (map[uint32]Counters, error) {
	out := map[uint32]Counters{}
	for _, i := range idxs {
		out[i] = a.counters[i]
	}
	return out, nil
}
func (a *fakeAgent) Close() error { return nil }

type memSink struct {
	mu    sync.Mutex
	names map[uint32]string
	wan   map[string]uint32
	store *flowstore.Store
}

func (s *memSink) Name(_ netip.Addr, idx uint32, name string, _ time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.names[idx] = name
}
func (s *memSink) WANInterfaces(netip.Addr) map[string]uint32 { return s.wan }
func (s *memSink) AddSNMPDelta(ctx context.Context, e netip.Addr, n string, d flowstore.SNMPHour) error {
	return s.store.AddSNMPDelta(ctx, e, n, d)
}

func openStore(t *testing.T) *flowstore.Store {
	t.Helper()
	s, err := flowstore.Open(ctx, filepath.Join(t.TempDir(), "f.duckdb"), flowstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func setup(t *testing.T) (*Poller, *fakeAgent, *memSink, *time.Time) {
	store := openStore(t)
	agent := &fakeAgent{names: map[uint32]string{1: "ether1", 2: "bridge"}, counters: map[uint32]Counters{}}
	sink := &memSink{names: map[uint32]string{}, wan: map[string]uint32{"ether1": 1}, store: store}
	now := t0.Add(-30 * time.Second)
	p := NewPoller(func(Config, netip.Addr) (Agent, error) { return agent, nil }, sink,
		map[netip.Addr]Config{exp: {Enabled: true, Version: "v2c", Community: "public"}}, nil, func() time.Time { return now })
	return p, agent, sink, &now
}

func TestPollerNamesAndHourlyDeltas(t *testing.T) {
	p, agent, sink, now := setup(t)
	agent.counters[1] = Counters{InOctets: 1000, OutOctets: 100}
	if err := p.Poll(ctx, exp); err != nil {
		t.Fatal(err)
	}
	if sink.names[2] != "bridge" || p.Status(exp).Interfaces != 2 {
		t.Errorf("names %v status %+v", sink.names, p.Status(exp))
	}
	// A minute later, across the hour boundary at t0: 30 s in each hour.
	*now = t0.Add(30 * time.Second)
	agent.counters[1] = Counters{InOctets: 1000 + 6000, OutOctets: 100 + 600, InPackets: 10}
	if err := p.Poll(ctx, exp); err != nil {
		t.Fatal(err)
	}
	hours, _ := sink.store.SNMPHours(ctx, exp, "ether1", t0.Add(-time.Hour), t0.Add(time.Hour))
	if len(hours) != 2 || hours[0].InOctets != 3000 || hours[1].InOctets != 3000 || hours[1].InPackets != 5 {
		t.Fatalf("hours %+v", hours)
	}
	if hours[0].Complete || !hours[1].Complete {
		t.Errorf("the hour polling started in is incomplete, the next isn't: %+v", hours)
	}
}

func TestCounterResetAndGapsMakeHoursIncomplete(t *testing.T) {
	p, agent, sink, now := setup(t)
	*now = t0.Add(10 * time.Minute)
	agent.counters[1] = Counters{InOctets: 5000}
	_ = p.Poll(ctx, exp)
	*now = t0.Add(11 * time.Minute)
	agent.counters[1] = Counters{InOctets: 6000}
	_ = p.Poll(ctx, exp)
	*now = t0.Add(12 * time.Minute)
	agent.counters[1] = Counters{InOctets: 10} // PPPoE reconnect reset the counters
	_ = p.Poll(ctx, exp)
	hours, _ := sink.store.SNMPHours(ctx, exp, "ether1", t0, t0.Add(time.Hour))
	if len(hours) != 1 || hours[0].Complete || hours[0].InOctets != 1000 {
		t.Errorf("reset: %+v", hours)
	}

	*now = t0.Add(2 * time.Hour) // nothing polled for a long time
	agent.counters[1] = Counters{InOctets: 999999}
	_ = p.Poll(ctx, exp)
	hours, _ = sink.store.SNMPHours(ctx, exp, "ether1", t0.Add(2*time.Hour), t0.Add(3*time.Hour))
	if len(hours) != 1 || hours[0].Complete || hours[0].InOctets != 0 {
		t.Errorf("gap: %+v", hours)
	}
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
	pub := good.Public()
	if pub.AuthPassword != "" || pub.PrivPassword != "" || !pub.HasAuthPassword {
		t.Errorf("public view leaks secrets: %+v", pub)
	}
	merged := Config{Version: "v3", User: "flowmon"}.Merge(good)
	if merged.AuthPassword != "authpass1" {
		t.Error("an update without passwords keeps the stored ones")
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
