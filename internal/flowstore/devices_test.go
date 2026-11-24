package flowstore

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

var (
	phoneMAC  = flow.MAC{0x3c, 0x5a, 0xb4, 0, 0, 0x44}
	laptopMAC = flow.MAC{0xb8, 0x27, 0xeb, 0, 0, 0x45}
)

func withDevice(f flow.Flow, local string, mac flow.MAC) flow.Flow {
	f.Local, f.Src, f.LocalMAC = netip.MustParseAddr(local), netip.MustParseAddr(local), mac
	return f
}

// rollupSum sums a minute Rollup's bytes grouped by col.
func rollupSum(t *testing.T, s *Store, col string) map[string]float64 {
	t.Helper()
	rows, err := s.db.QueryContext(ctx, `SELECT `+col+`, sum(bytes_tx + bytes_rx) FROM rollup_minute_20261118 GROUP BY 1`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]float64{}
	for rows.Next() {
		var k string
		var v float64
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		out[k] = v
	}
	return out
}

func TestDevicesOnFlowsRollupsAndAddresses(t *testing.T) {
	s := open(t)
	internal := withDevice(mkFlow(flow.DirectionInternal, t0, t0, 300), "192.168.10.44", phoneMAC)
	internal.Peer, internal.Dst, internal.PeerMAC = netip.MustParseAddr("192.168.10.45"), netip.MustParseAddr("192.168.10.45"), laptopMAC
	flows := []flow.Flow{
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 100), "192.168.10.44", phoneMAC),
		// The phone's next DHCP lease.
		withDevice(mkFlow(flow.DirectionOutbound, t0.Add(30*time.Second), t0.Add(30*time.Second), 200), "192.168.10.77", phoneMAC),
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 50), "192.168.10.99", flow.MAC{}),
		internal,
	}
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	if err := s.Write(ctx, []flow.Flow{withDevice(mkFlow(flow.DirectionOutbound, t0.Add(-time.Hour), t0.Add(-time.Hour), 1), "192.168.10.44", phoneMAC)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}

	addrs, err := s.DeviceAddresses(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []DeviceAddress{
		{phoneMAC, netip.MustParseAddr("192.168.10.44"), t0.Add(-time.Hour), t0},
		{phoneMAC, netip.MustParseAddr("192.168.10.77"), t0.Add(30 * time.Second), t0.Add(30 * time.Second)},
		{laptopMAC, netip.MustParseAddr("192.168.10.45"), t0, t0},
	}
	if len(addrs) != len(want) {
		t.Fatalf("device addresses %+v", addrs)
	}
	for i := range want {
		if addrs[i] != want[i] {
			t.Errorf("address %d: %+v, want %+v", i, addrs[i], want[i])
		}
	}

	byMAC, byHost := rollupSum(t, s, "local_mac"), rollupSum(t, s, "local_host")
	if byMAC[phoneMAC.String()] != byHost["192.168.10.44"]+byHost["192.168.10.77"] || byMAC[laptopMAC.String()] != byHost["192.168.10.45"] || byMAC[""] != byHost["192.168.10.99"] {
		t.Errorf("Rollups by Device %v don't add up to by address %v", byMAC, byHost)
	}

	// Reprocessing fills in Devices for Flows stored without them.
	if _, _, err := s.Reprocess(ctx, exporter, t0, func(f *flow.Flow) {
		if f.Local == netip.MustParseAddr("192.168.10.99") {
			f.LocalMAC = laptopMAC
		}
	}, nil); err != nil {
		t.Fatal(err)
	}
	if got := rollupSum(t, s, "local_mac"); got[""] != 0 || got[laptopMAC.String()] != byHost["192.168.10.45"]+byHost["192.168.10.99"] {
		t.Errorf("after reprocessing: %v", got)
	}
}

func TestRouterMACs(t *testing.T) {
	s := open(t)
	r := RouterMAC{MAC: flow.MAC{0x48, 0xa9, 0x8a, 0, 0, 1}, Exporter: exporter, Reason: "exporter", Since: t0}
	for range 2 {
		if err := s.AddRouterMAC(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	later := r
	later.Reason, later.Since = "many addresses", t0.Add(time.Hour)
	if err := s.AddRouterMAC(ctx, later); err != nil {
		t.Fatal(err)
	}
	got, err := s.RouterMACs(ctx)
	if err != nil || len(got) != 1 || got[0] != r {
		t.Errorf("router MACs %+v %v; want the first record only", got, err)
	}
}
