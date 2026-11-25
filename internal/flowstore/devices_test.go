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

func TestTopDevices(t *testing.T) {
	s := open(t)
	routerMAC := flow.MAC{0x48, 0xa9, 0x8a, 0, 0, 1}
	if err := s.AddRouterMAC(ctx, RouterMAC{MAC: routerMAC, Exporter: exporter, Reason: "many addresses", Since: t0}); err != nil {
		t.Fatal(err)
	}
	privateMAC := flow.MAC{0xda, 0xa1, 0x19, 0, 0, 7}
	if err := s.Write(ctx, []flow.Flow{
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 100), "192.168.10.44", phoneMAC),
		// The phone's next lease, later: its current address.
		withDevice(mkFlow(flow.DirectionOutbound, t0.Add(2*time.Minute), t0.Add(2*time.Minute), 200), "192.168.10.77", phoneMAC),
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 50), "192.168.10.99", flow.MAC{}),
		// A reply that arrived before the phone sent anything: no MAC, but its address had
		// only the phone's.
		withDevice(mkFlow(flow.DirectionInbound, t0, t0, 25), "192.168.10.77", flow.MAC{}),
		// Assigned to a MAC before it was found to be a router's: counted by address.
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 70), "192.168.30.5", routerMAC),
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 30), "192.168.10.60", privateMAC),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Top(ctx, TopQuery{Dimension: TopDevices, From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 4 || res.Count != 4 || res.TotalBytes != 475 {
		t.Fatalf("items %+v, count %d, total %v", res.Items, res.Count, res.TotalBytes)
	}
	phone := res.Items[0]
	if phone.Mac != phoneMAC.String() || phone.Host != "192.168.10.77" || phone.Addresses != 2 || phone.TotalBytes != 325 || phone.Vendor != "Google" {
		t.Errorf("phone %+v; want both addresses under its MAC, named by the latest, vendor Google", phone)
	}
	if phone.LastSeen == nil || !phone.LastSeen.Equal(t0.Add(2*time.Minute)) {
		t.Errorf("phone last seen %v, want its latest minute", phone.LastSeen)
	}
	byHost := map[string]TopItem{}
	for _, it := range res.Items {
		byHost[it.Host] = it
	}
	if it := byHost["192.168.30.5"]; it.Mac != "" || it.TotalBytes != 70 {
		t.Errorf("router-hop MAC's traffic: %+v; want it by address", it)
	}
	if it := byHost["192.168.10.99"]; it.Mac != "" || it.TotalBytes != 50 {
		t.Errorf("traffic without a MAC: %+v", it)
	}
	if it := byHost["192.168.10.60"]; !it.PrivateMac || it.Vendor != "" {
		t.Errorf("private MAC: %+v", it)
	}
}

func TestHostQueriesByDevice(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 100), "192.168.10.44", phoneMAC),
		withDevice(mkFlow(flow.DirectionOutbound, t0.Add(time.Minute), t0.Add(time.Minute), 200), "192.168.10.77", phoneMAC),
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 50), "192.168.10.99", laptopMAC),
	}); err != nil {
		t.Fatal(err)
	}
	q := HostQuery{MAC: phoneMAC.String(), From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}
	addrs, err := s.HostAddresses(ctx, q)
	if err != nil || len(addrs) != 2 || addrs[0] != "192.168.10.44" || addrs[1] != "192.168.10.77" {
		t.Errorf("addresses %v %v", addrs, err)
	}
	sum, err := s.HostDetail(ctx, q)
	if err != nil || sum.SentBytes != 300 {
		t.Errorf("the phone's traffic %+v %v; want both addresses' 300 bytes", sum, err)
	}
	page, err := s.Flows(ctx, FlowQuery{From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Device: phoneMAC.String(), Limit: 10})
	if err != nil || page.Total != 2 {
		t.Errorf("Flows of the phone: %d %v", page.Total, err)
	}
	of, err := s.DeviceAddressesOf(ctx, phoneMAC)
	if err != nil || len(of) != 2 || of[0].Address != netip.MustParseAddr("192.168.10.77") {
		t.Errorf("addresses of the phone, latest first: %+v %v", of, err)
	}
	at, err := s.DevicesAt(ctx, netip.MustParseAddr("192.168.10.99"))
	if err != nil || len(at) != 1 || at[0].MAC != laptopMAC {
		t.Errorf("devices at .99: %+v %v", at, err)
	}
}

func TestDeviceMerges(t *testing.T) {
	s := open(t)
	oldPhone, newPhone, third := flow.MAC{0xda, 0xa1, 0x19, 0, 0, 1}, flow.MAC{0x6e, 0, 0, 0, 0, 2}, flow.MAC{0xaa, 0, 0, 0, 0, 3}
	if err := s.Write(ctx, []flow.Flow{
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 100), "192.168.10.60", oldPhone),
		withDevice(mkFlow(flow.DirectionOutbound, t0.Add(time.Minute), t0.Add(time.Minute), 200), "192.168.10.61", newPhone),
		withDevice(mkFlow(flow.DirectionOutbound, t0.Add(2*time.Minute), t0.Add(2*time.Minute), 400), "192.168.10.62", third),
	}); err != nil {
		t.Fatal(err)
	}
	top := func() []TopItem {
		t.Helper()
		res, err := s.Top(ctx, TopQuery{Dimension: TopDevices, From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return res.Items
	}
	if err := s.MergeDevice(ctx, newPhone, oldPhone, "auto", t0); err != nil {
		t.Fatal(err)
	}
	// Merging the old phone into a third Device takes the new one along: one step deep.
	if err := s.MergeDevice(ctx, oldPhone, third, "manual", t0); err != nil {
		t.Fatal(err)
	}
	items := top()
	if len(items) != 1 || items[0].Mac != third.String() || items[0].TotalBytes != 700 || items[0].Addresses != 3 {
		t.Fatalf("after merging %+v; want one Device of 700 bytes at 3 addresses", items)
	}
	merges, _ := s.DeviceMerges(ctx)
	for _, m := range merges {
		if m.Into != third {
			t.Errorf("merge %+v not one step deep", m)
		}
	}
	if err := s.MergeDevice(ctx, third, newPhone, "manual", t0); err != ErrMergeSelf {
		t.Errorf("merging a Device into one merged into it: %v", err)
	}
	addrs, _ := s.HostAddresses(ctx, HostQuery{MAC: third.String(), From: t0.Add(-time.Hour), To: t0.Add(time.Hour)})
	if len(addrs) != 3 {
		t.Errorf("the merged Device's addresses %v", addrs)
	}
	if page, _ := s.Flows(ctx, FlowQuery{From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Device: third.String(), Limit: 10}); page.Total != 3 {
		t.Errorf("the merged Device's Flows: %d", page.Total)
	}
	if of, _ := s.DeviceAddressesOf(ctx, third); len(of) != 3 {
		t.Errorf("addresses of the merged Device: %+v", of)
	}

	if err := s.SplitDevice(ctx, oldPhone); err != nil {
		t.Fatal(err)
	}
	if items := top(); len(items) != 2 {
		t.Errorf("after splitting the old phone off: %+v", items)
	}
	spans, err := s.MACSpans(ctx)
	if err != nil || len(spans) != 3 {
		t.Fatalf("spans %+v %v", spans, err)
	}
}

func TestDNSServers(t *testing.T) {
	s := open(t)
	dns := func(dir flow.Direction, server string, port uint16) flow.Flow {
		f := withDevice(mkFlow(dir, t0, t0, 100), "192.168.10.44", phoneMAC)
		f.Peer, f.Dst, f.PeerPort, f.DstPort = netip.MustParseAddr(server), netip.MustParseAddr(server), port, port
		return f
	}
	if err := s.Write(ctx, []flow.Flow{
		dns(flow.DirectionInternal, "192.168.10.2", 53), dns(flow.DirectionInternal, "192.168.10.2", 53),
		dns(flow.DirectionOutbound, "8.8.8.8", 53),
		dns(flow.DirectionOutbound, "1.1.1.1", 853),
		dns(flow.DirectionOutbound, "1.1.1.1", 443), // HTTPS, not DNS this can tell
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.DNSServers(ctx, []string{"192.168.10.44"}, t0.Add(-time.Hour), t0.Add(time.Hour))
	if err != nil || len(got) != 3 || got[0].Server != "192.168.10.2" || got[0].Flows != 2 {
		t.Fatalf("servers %+v %v", got, err)
	}
	if other, _ := s.DNSServers(ctx, []string{"192.168.10.45"}, t0.Add(-time.Hour), t0.Add(time.Hour)); len(other) != 0 {
		t.Errorf("another host's servers: %+v", other)
	}
}

func TestTopDevicesGroupsMACLessTrafficByHostLabelPrefix(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 100), "192.168.10.44", phoneMAC),
		// No MACs, as from a router that sends none: one Local Host by their Host Label's prefix.
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 40), "192.168.20.5", flow.MAC{}),
		withDevice(mkFlow(flow.DirectionOutbound, t0.Add(time.Minute), t0.Add(time.Minute), 30), "192.168.20.6", flow.MAC{}),
		withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 20), "192.168.30.9", flow.MAC{}),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Top(ctx, TopQuery{Dimension: TopDevices, From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Limit: 2,
		HostGroups: []netip.Prefix{prefix("192.168.20.0/24")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || res.Count != 3 || res.TotalBytes != 190 {
		t.Fatalf("items %+v, count %d, total %v; want the top 2 of 3", res.Items, res.Count, res.TotalBytes)
	}
	if it := res.Items[0]; it.Mac != phoneMAC.String() || it.TotalBytes != 100 || it.Vendor != "Google" {
		t.Errorf("Device %+v", it)
	}
	g := res.Items[1]
	if g.Host != "192.168.20.0/24" || g.Mac != "" || g.Addresses != 2 || g.TotalBytes != 70 {
		t.Errorf("prefix %+v; want both addresses as one", g)
	}
	if g.LastSeen == nil || !g.LastSeen.Equal(t0.Add(time.Minute)) {
		t.Errorf("prefix last seen %v, want its latest minute", g.LastSeen)
	}
}
