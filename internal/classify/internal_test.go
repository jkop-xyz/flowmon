package classify

import (
	"net/netip"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// The counting table for Exporter roles (ADR 0003, "Counting across Exporters"),
// written before the code. An Edge Exporter counts by the tables above. An Internal
// Exporter, such as a hypervisor, sees traffic an Edge Exporter also sees, except
// between two hosts in the same subnet: only that is counted from it, and only by the
// subnets on its LAN side, never by a broad Local Network.

var hypervisor = netip.MustParseAddr("192.168.10.2")

type fakeRoles map[netip.Addr][]netip.Prefix

func (r fakeRoles) InternalSubnets(exporter netip.Addr) ([]netip.Prefix, bool) {
	s, ok := r[exporter]
	return s, ok
}

func internalClassifier() *Classifier {
	c := New(DefaultLocalNetworks())
	c.SetInterfaces(fakeInterfaces{byInterface: true})
	c.SetRoles(fakeRoles{hypervisor: {
		netip.MustParsePrefix("192.168.10.0/24"), // the LAN
		netip.MustParsePrefix("10.0.20.0/24"),    // the VM bridge
		netip.MustParsePrefix("fd00:10::/64"),
	}})
	return c
}

func TestCountingTableByExporterRole(t *testing.T) {
	vm1, vm2 := netip.MustParseAddr("10.0.20.14"), netip.MustParseAddr("10.0.20.15")
	otherVLAN := netip.MustParseAddr("10.0.30.9") // local, but a different subnet
	for _, r := range []struct {
		name     string
		exporter netip.Addr
		in, out  uint32
		src, dst netip.Addr
		dir      flow.Direction
		local    netip.Addr
	}{
		{"internal: two VMs on the same bridge are counted", hypervisor, 5, 6, vm1, vm2, flow.DirectionInternal, vm1},
		{"internal: same subnet over IPv6", hypervisor, 5, 6, netip.MustParseAddr("fd00:10::14"), netip.MustParseAddr("fd00:10::15"), flow.DirectionInternal, netip.MustParseAddr("fd00:10::14")},
		{"internal: a VM to a LAN host is routed, so the Edge Exporter counts it", hypervisor, 5, 6, vm1, lanHost, flow.DirectionUncounted, netip.Addr{}},
		{"internal: a VM to another VLAN, though both are Local Networks", hypervisor, 5, 6, vm1, otherVLAN, flow.DirectionUncounted, netip.Addr{}},
		{"internal: a VM to the internet", hypervisor, 5, 6, vm1, remote, flow.DirectionUncounted, netip.Addr{}},
		{"internal: the internet to a VM", hypervisor, 6, 5, remote, vm1, flow.DirectionUncounted, netip.Addr{}},
		{"internal: interface roles don't matter (WAN → LAN, same subnet)", hypervisor, 1, 2, vm2, vm1, flow.DirectionInternal, vm2},
		{"internal: no interface numbers, same subnet", hypervisor, 0, 0, vm1, vm2, flow.DirectionInternal, vm1},
		{"edge: the same VM-to-VM Flow is internal as usual", exporter, 2, 2, vm1, vm2, flow.DirectionInternal, vm1},
		{"edge: routed between VLANs is still counted", exporter, 2, 2, vm1, otherVLAN, flow.DirectionInternal, vm1},
		{"edge: to the internet", exporter, 2, 1, vm1, remote, flow.DirectionOutbound, vm1},
	} {
		t.Run(r.name, func(t *testing.T) {
			f := flow.Flow{Exporter: r.exporter, IngressInterface: r.in, EgressInterface: r.out, Src: r.src, Dst: r.dst, SrcPort: 50000, DstPort: 22}
			internalClassifier().Classify(&f, now)
			if f.Direction != r.dir || f.Local != r.local {
				t.Errorf("got %v local=%v; want %v local=%v", f.Direction, f.Local, r.dir, r.local)
			}
			if f.Direction == flow.DirectionUncounted && (f.Local.IsValid() || f.Peer.IsValid()) {
				t.Errorf("an uncounted Flow has no Local Host or Peer: %v %v", f.Local, f.Peer)
			}
			if f.Direction == flow.DirectionInternal && (f.Peer != r.dst || f.LocalPort != 50000 || f.PeerPort != 22) {
				t.Errorf("peer %v:%d, local port %d", f.Peer, f.PeerPort, f.LocalPort)
			}
		})
	}
}
