package classify

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// fakeInterfaces: 1 = ether1-WAN, 2 = bridge-LAN, 3 = wg0 (Tunnel), 9 = unlabelled.
type fakeInterfaces struct{ byInterface bool }

func (f fakeInterfaces) ByInterface(netip.Addr) bool { return f.byInterface }
func (fakeInterfaces) Role(_ netip.Addr, idx uint32, _ time.Time) InterfaceRole {
	switch idx {
	case 0:
		return IfSelf
	case 1:
		return IfWAN
	case 2:
		return IfLAN
	case 3:
		return IfTunnel
	}
	return IfUnknown
}

// The counting table for Classification Mode "by interface" (ADR 0003), written before
// the code. Addresses are deliberately unhelpful (a public LAN, a CGNAT tunnel) to show
// the interfaces decide.
type interfaceRow struct {
	name         string
	in, out      uint32
	src, dst     netip.Addr
	postSrc      netip.Addr
	postDst      netip.Addr
	dir          flow.Direction
	local, peer  netip.Addr
	exporterHost bool
}

func (r interfaceRow) flow() flow.Flow {
	return flow.Flow{Exporter: exporter, IngressInterface: r.in, EgressInterface: r.out,
		Src: r.src, Dst: r.dst, PostNATSrc: r.postSrc, PostNATDst: r.postDst}
}

func (r interfaceRow) check(t *testing.T, f flow.Flow) {
	t.Helper()
	c := New(DefaultLocalNetworks())
	c.SetInterfaces(fakeInterfaces{byInterface: true})
	c.Classify(&f, now)
	if f.Direction != r.dir || f.Local != r.local || f.Peer != r.peer || f.LocalIsExporterHost != r.exporterHost {
		t.Errorf("got %v local=%v peer=%v eh=%v; want %v local=%v peer=%v eh=%v",
			f.Direction, f.Local, f.Peer, f.LocalIsExporterHost, r.dir, r.local, r.peer, r.exporterHost)
	}
}

func TestCountingTableByInterface(t *testing.T) {
	for _, r := range interfaceRows() {
		t.Run(r.name, func(t *testing.T) { r.check(t, r.flow()) })
	}
}

// interfaceRows are the counting table for classification by interface.
func interfaceRows() []interfaceRow {
	publicLAN := netip.MustParseAddr("198.51.100.20") // a LAN that isn't in any Local Network
	tunnelPeer := netip.MustParseAddr("100.64.0.7")   // a WireGuard road-warrior
	return []interfaceRow{
		{name: "LAN → WAN is outbound, even for a public LAN", in: 2, out: 1, src: publicLAN, dst: remote, postSrc: wan,
			dir: flow.DirectionOutbound, local: publicLAN, peer: remote},
		{name: "WAN → LAN download reply: the host is the post-NAT destination", in: 1, out: 2, src: remote, dst: wan, postDst: lanHost,
			dir: flow.DirectionInbound, local: lanHost, peer: remote},
		{name: "LAN ↔ LAN routed between VLANs", in: 2, out: 2, src: lanHost, dst: lanVM,
			dir: flow.DirectionInternal, local: lanHost, peer: lanVM},
		{name: "a Tunnel peer browsing the internet is a Local Host", in: 3, out: 1, src: tunnelPeer, dst: remote, postSrc: wan,
			dir: flow.DirectionOutbound, local: tunnelPeer, peer: remote},
		{name: "a Tunnel peer reaching the LAN is internal", in: 3, out: 2, src: tunnelPeer, dst: lanVM,
			dir: flow.DirectionInternal, local: tunnelPeer, peer: lanVM},
		{name: "the router itself to the internet (no input interface)", in: 0, out: 1, src: wan, dst: remote2,
			dir: flow.DirectionOutbound, local: exporter, peer: remote2, exporterHost: true},
		{name: "the internet to the router itself (no output interface)", in: 1, out: 0, src: remote2, dst: wan,
			dir: flow.DirectionInbound, local: exporter, peer: remote2, exporterHost: true},
		{name: "WAN → WAN is transit", in: 1, out: 1, src: remote, dst: remote2,
			dir: flow.DirectionTransit},
	}
}

func TestUnknownInterfacesFallBackToAddresses(t *testing.T) {
	c := New(DefaultLocalNetworks())
	c.SetInterfaces(fakeInterfaces{byInterface: true})
	f := flow.Flow{Exporter: exporter, IngressInterface: 9, EgressInterface: 1, Src: lanHost, Dst: remote}
	c.Classify(&f, now)
	if f.Direction != flow.DirectionOutbound || f.Local != lanHost {
		t.Errorf("an interface without a role is classified by address: %v %v", f.Direction, f.Local)
	}
	f = flow.Flow{Exporter: exporter, Src: lanHost, Dst: remote} // no interface numbers at all
	c.Classify(&f, now)
	if f.Direction != flow.DirectionOutbound {
		t.Errorf("no interface numbers: %v", f.Direction)
	}
}

func TestAddressModeIgnoresInterfaces(t *testing.T) {
	c := New(DefaultLocalNetworks())
	c.SetInterfaces(fakeInterfaces{byInterface: false})
	publicLAN := netip.MustParseAddr("198.51.100.20")
	f := flow.Flow{Exporter: exporter, IngressInterface: 2, EgressInterface: 1, Src: publicLAN, Dst: remote}
	c.Classify(&f, now)
	if f.Direction != flow.DirectionTransit {
		t.Errorf("by address, a public LAN isn't local: %v", f.Direction)
	}
}
