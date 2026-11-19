package classify

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// These tests are the counting table from ADR 0003/0004 for classification by address.
// They were written before the classifier: each row is a real traffic situation on a
// home network behind a NATing router, and what Flowmon must make of it.

var (
	exporter = netip.MustParseAddr("192.168.10.1")
	wan      = netip.MustParseAddr("203.0.113.7") // the router's public address
	lanHost  = netip.MustParseAddr("192.168.10.44")
	lanVM    = netip.MustParseAddr("10.0.20.14")
	remote   = netip.MustParseAddr("142.250.74.46")
	remote2  = netip.MustParseAddr("1.1.1.1")
	cgnat    = netip.MustParseAddr("100.64.3.9")
	now      = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

var none = netip.Addr{}

type row struct {
	name                   string
	src, dst               netip.Addr
	postSrc, postDst       netip.Addr
	srcPort, dstPort       uint16
	postSrcPort, postDstPt uint16

	direction flow.Direction
	local     netip.Addr
	peer      netip.Addr
	localIsEH bool
	localPort uint16
	peerPort  uint16
}

func (r row) flow() flow.Flow {
	return flow.Flow{
		Exporter: exporter,
		Src:      r.src, Dst: r.dst, SrcPort: r.srcPort, DstPort: r.dstPort,
		PostNATSrc: r.postSrc, PostNATDst: r.postDst, PostNATSrcPort: r.postSrcPort, PostNATDstPort: r.postDstPt,
	}
}

// learned returns a classifier that has already seen the router's WAN address on a
// NATed outbound Flow.
func learned() *Classifier {
	c := New(DefaultLocalNetworks())
	f := flow.Flow{Exporter: exporter, Src: lanHost, Dst: remote2, PostNATSrc: wan, PostNATDst: remote2}
	c.Classify(&f, now)
	return c
}

// addressRows are the counting table for classification by address.
func addressRows() []row {
	return []row{
		{
			name: "outbound through NAT (MikroTik: pre-NAT source is the LAN host)",
			src:  lanHost, dst: remote, postSrc: wan, postDst: remote, srcPort: 50000, dstPort: 443,
			direction: flow.DirectionOutbound, local: lanHost, peer: remote, localPort: 50000, peerPort: 443,
		},
		{
			name: "download reply: the LAN host is only in the post-NAT destination",
			src:  remote, dst: wan, postSrc: remote, postDst: lanHost, srcPort: 443, dstPort: 61000, postDstPt: 50000,
			direction: flow.DirectionInbound, local: lanHost, peer: remote, localPort: 50000, peerPort: 443,
		},
		{
			name: "inbound port forward to a VM",
			src:  remote, dst: wan, postDst: lanVM, srcPort: 40000, dstPort: 8443, postDstPt: 443,
			direction: flow.DirectionInbound, local: lanVM, peer: remote, localPort: 443, peerPort: 40000,
		},
		{
			name: "inter-VLAN traffic routed by the router",
			src:  lanHost, dst: lanVM, srcPort: 50000, dstPort: 22,
			direction: flow.DirectionInternal, local: lanHost, peer: lanVM, localPort: 50000, peerPort: 22,
		},
		{
			name: "hairpin NAT: LAN host reaches a VM through the WAN address",
			src:  lanHost, dst: wan, postSrc: wan, postDst: lanVM, srcPort: 50000, dstPort: 8443, postDstPt: 443,
			direction: flow.DirectionInternal, local: lanHost, peer: lanVM, localPort: 50000, peerPort: 443,
		},
		{
			name: "the router itself talks to the internet (Exporter Host, attributed to the Exporter)",
			src:  wan, dst: remote2, srcPort: 33000, dstPort: 53,
			direction: flow.DirectionOutbound, local: exporter, localIsEH: true, peer: remote2, localPort: 33000, peerPort: 53,
		},
		{
			name: "a reply to the router itself",
			src:  remote2, dst: wan, srcPort: 53, dstPort: 33000,
			direction: flow.DirectionInbound, local: exporter, localIsEH: true, peer: remote2, localPort: 33000, peerPort: 53,
		},
		{
			name: "CGNAT range is not local by default",
			src:  lanHost, dst: cgnat, srcPort: 50000, dstPort: 41641,
			direction: flow.DirectionOutbound, local: lanHost, peer: cgnat, localPort: 50000, peerPort: 41641,
		},
		{
			name: "multicast and broadcast stay on the local side",
			src:  lanHost, dst: netip.MustParseAddr("224.0.0.251"), srcPort: 5353, dstPort: 5353,
			direction: flow.DirectionInternal, local: lanHost, peer: netip.MustParseAddr("224.0.0.251"), localPort: 5353, peerPort: 5353,
		},
		{
			name: "IPv6 ULA to a global address is outbound",
			src:  netip.MustParseAddr("fd00:10::44"), dst: netip.MustParseAddr("2a00:1450:4001::200e"), srcPort: 50000, dstPort: 443,
			direction: flow.DirectionOutbound, local: netip.MustParseAddr("fd00:10::44"), peer: netip.MustParseAddr("2a00:1450:4001::200e"), localPort: 50000, peerPort: 443,
		},
		{
			name: "IPv6 link-local is internal",
			src:  netip.MustParseAddr("fe80::1"), dst: netip.MustParseAddr("fe80::2"), srcPort: 546, dstPort: 547,
			direction: flow.DirectionInternal, local: netip.MustParseAddr("fe80::1"), peer: netip.MustParseAddr("fe80::2"), localPort: 546, peerPort: 547,
		},
		{
			name: "two remote addresses: transit",
			src:  remote, dst: remote2, srcPort: 1, dstPort: 2,
			direction: flow.DirectionTransit,
		},
	}
}

func TestCountingTableByAddress(t *testing.T) {
	for _, r := range addressRows() {
		t.Run(r.name, func(t *testing.T) { r.check(t, r.flow()) })
	}
}

// check classifies f, the row's Flow however it was made, and compares.
func (r row) check(t *testing.T, f flow.Flow) {
	t.Helper()
	c := learned()
	c.Classify(&f, now)
	if f.Direction != r.direction || f.Local != r.local || f.Peer != r.peer || f.LocalIsExporterHost != r.localIsEH {
		t.Errorf("got %v local=%v peer=%v eh=%v; want %v local=%v peer=%v eh=%v",
			f.Direction, f.Local, f.Peer, f.LocalIsExporterHost, r.direction, r.local, r.peer, r.localIsEH)
	}
	if f.LocalPort != r.localPort || f.PeerPort != r.peerPort {
		t.Errorf("ports local=%d peer=%d; want local=%d peer=%d", f.LocalPort, f.PeerPort, r.localPort, r.peerPort)
	}
}

func TestExporterHostIsLearnedFromNAT(t *testing.T) {
	c := New(DefaultLocalNetworks())
	routerTraffic := flow.Flow{Exporter: exporter, Src: wan, Dst: remote2}

	f := routerTraffic
	c.Classify(&f, now)
	if f.Direction != flow.DirectionTransit {
		t.Fatalf("before the WAN address is known the router's own traffic can't be placed: %v", f.Direction)
	}

	nat := flow.Flow{Exporter: exporter, Src: lanHost, Dst: remote2, PostNATSrc: wan, PostNATDst: remote2}
	c.Classify(&nat, now)

	f = routerTraffic
	c.Classify(&f, now)
	if f.Direction != flow.DirectionOutbound || !f.LocalIsExporterHost {
		t.Errorf("after learning: %v eh=%v", f.Direction, f.LocalIsExporterHost)
	}
	if got := c.ExporterHosts(exporter, now); len(got) != 1 || got[0] != wan {
		t.Errorf("ExporterHosts = %v", got)
	}
}

func TestLearnedExporterHostExpires(t *testing.T) {
	c := learned()
	later := now.Add(ExporterHostTTL + time.Minute)
	f := flow.Flow{Exporter: exporter, Src: wan, Dst: remote2}
	c.Classify(&f, later)
	if f.Direction != flow.DirectionTransit {
		t.Errorf("a DHCP WAN address that hasn't been seen for a day must stop counting as local: %v", f.Direction)
	}
}

func TestExporterHostsArePerExporter(t *testing.T) {
	c := learned()
	other := netip.MustParseAddr("192.168.10.2")
	f := flow.Flow{Exporter: other, Src: wan, Dst: remote2}
	c.Classify(&f, now)
	if f.Direction != flow.DirectionTransit {
		t.Errorf("another Exporter must not inherit this Exporter's WAN address: %v", f.Direction)
	}
}

func TestTheExporterAddressIsAnExporterHost(t *testing.T) {
	// An Exporter whose own address isn't in a Local Network, e.g. a VPS exporting over
	// the internet: its own traffic is still local.
	vps := netip.MustParseAddr("198.51.100.10")
	c := New(DefaultLocalNetworks())
	f := flow.Flow{Exporter: vps, Src: vps, Dst: remote}
	c.Classify(&f, now)
	if f.Direction != flow.DirectionOutbound || f.Local != vps || !f.LocalIsExporterHost { // its own address either way
		t.Errorf("%v local=%v eh=%v", f.Direction, f.Local, f.LocalIsExporterHost)
	}
}

func TestExtraLocalNetworks(t *testing.T) {
	extra := append(DefaultLocalNetworks(), netip.MustParsePrefix("100.64.0.0/10"))
	c := New(extra)
	f := flow.Flow{Exporter: exporter, Src: lanHost, Dst: cgnat}
	c.Classify(&f, now)
	if f.Direction != flow.DirectionInternal {
		t.Errorf("with 100.64/10 added (e.g. Tailscale), the peer is local: %v", f.Direction)
	}
}

func TestParseLocalNetworks(t *testing.T) {
	got, err := ParseLocalNetworks(" 100.64.0.0/10, 2001:db8::/32 ,")
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := ParseLocalNetworks("10.0.0.0/33"); err == nil {
		t.Error("expected an error for an invalid prefix")
	}
}
