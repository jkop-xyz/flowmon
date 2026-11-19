package flowstore

import (
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// hostFlow is a Flow of a Local Host with a Peer; internal Flows' Peer is a Local Host.
func hostFlow(dir flow.Direction, local, peer string, port uint16, at time.Time, bytes uint64) flow.Flow {
	f := mkFlow(dir, at, at, bytes)
	f.Local, f.Src = netip.MustParseAddr(local), netip.MustParseAddr(local)
	f.Peer, f.Dst = netip.MustParseAddr(peer), netip.MustParseAddr(peer)
	f.PeerPort, f.DstPort = port, port
	if dir == flow.DirectionInternal || dir == flow.DirectionOutbound {
		return f
	}
	f.Src, f.Dst = f.Dst, f.Src
	return f
}

func prefix(s string) netip.Prefix {
	if p, err := netip.ParsePrefix(s); err == nil {
		return p
	}
	a := netip.MustParseAddr(s)
	return netip.PrefixFrom(a, a.BitLen())
}

func TestTopLocalHostsGroupsPrefixes(t *testing.T) {
	s := open(t)
	at := t0.Add(-30 * time.Minute)
	flows := []flow.Flow{
		hostFlow(flow.DirectionOutbound, "2001:db8::a", "1.1.1.1", 443, at, 100),
		hostFlow(flow.DirectionInbound, "2001:db8::b", "1.1.1.1", 443, at, 250),
		hostFlow(flow.DirectionOutbound, "192.168.1.5", "1.1.1.1", 443, at, 300),
		hostFlow(flow.DirectionOutbound, "192.168.1.6", "1.1.1.1", 443, at, 10),
		// Labelled on its own inside the /64: not part of the group.
		hostFlow(flow.DirectionOutbound, "2001:db8::c", "1.1.1.1", 443, at, 1),
	}
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	q := TopQuery{Dimension: TopLocalHosts, From: t0.Add(-time.Hour), To: t0, Limit: 2,
		HostGroups: []netip.Prefix{prefix("2001:db8::/64"), prefix("2001:db8::/32"), prefix("2001:db8::c")}}
	res, err := s.Top(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 {
		t.Fatalf("items %+v", res.Items)
	}
	g := res.Items[0]
	if g.Host != "2001:db8::/64" || g.Addresses != 2 || g.SentBytes != 100 || g.ReceivedBytes != 250 || g.TotalBytes != 350 {
		t.Errorf("group %+v", g)
	}
	if h := res.Items[1]; h.Host != "192.168.1.5" || h.Addresses != 0 || h.TotalBytes != 300 {
		t.Errorf("second %+v", h)
	}
	if res.TotalBytes != 661 {
		t.Errorf("total %v", res.TotalBytes)
	}
	// The group, .5, .6 and 2001:db8::c: a group counts once.
	if res.Count != 4 {
		t.Errorf("count %d, want 4", res.Count)
	}
}

func TestHostDetailAndTraffic(t *testing.T) {
	s := open(t)
	at := t0.Add(-30 * time.Minute)
	ext := func(f flow.Flow, country string, asn uint32) flow.Flow {
		f.PeerCountry, f.PeerASN = country, asn
		return f
	}
	flows := []flow.Flow{
		ext(hostFlow(flow.DirectionInbound, "192.168.1.5", "1.1.1.1", 443, at, 1000), "AU", 13335),
		ext(hostFlow(flow.DirectionOutbound, "192.168.1.5", "8.8.8.8", 53, at, 100), "US", 15169),
		ext(hostFlow(flow.DirectionOutbound, "192.168.1.5", "8.8.4.4", 53, at.Add(time.Minute), 50), "US", 15169),
		// Internal: .5 sends to .20 and .21 (one group), and receives from .20.
		hostFlow(flow.DirectionInternal, "192.168.1.5", "192.168.1.20", 445, at, 400),
		hostFlow(flow.DirectionInternal, "192.168.1.21", "192.168.1.5", 445, at, 70),
		// Another address in the queried prefix, and one outside it.
		hostFlow(flow.DirectionOutbound, "192.168.1.6", "1.1.1.1", 443, at, 5),
		hostFlow(flow.DirectionOutbound, "192.168.2.6", "1.1.1.1", 443, at, 5),
	}
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	q := HostQuery{Host: prefix("192.168.1.5"), From: t0.Add(-time.Hour), To: t0,
		HostGroups: []netip.Prefix{prefix("192.168.1.20/31")}}
	d, err := s.HostDetail(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if d.ReceivedBytes != 1070 || d.SentBytes != 550 || d.RemoteBytes != 1150 || d.InternalBytes != 470 || d.Flows != 4 {
		t.Errorf("totals rx %v tx %v remote %v internal %v flows %v", d.ReceivedBytes, d.SentBytes, d.RemoteBytes, d.InternalBytes, d.Flows)
	}
	if len(d.Addresses) != 1 || d.Addresses[0].Address != "192.168.1.5" {
		t.Errorf("addresses %+v", d.Addresses)
	}
	if len(d.Ports) != 3 || d.Ports[0].ServicePort != 443 || d.Ports[1].ServicePort != 445 || d.Ports[1].TotalBytes != 470 ||
		d.Ports[2].ServicePort != 53 || d.Ports[2].Flows != 2 {
		t.Errorf("ports %+v", d.Ports)
	}
	if len(d.LocalPeers) != 1 || d.LocalPeers[0].Host != "192.168.1.20/31" || d.LocalPeers[0].Addresses != 2 ||
		d.LocalPeers[0].SentBytes != 400 || d.LocalPeers[0].ReceivedBytes != 70 {
		t.Errorf("local peers %+v", d.LocalPeers)
	}
	if len(d.Countries) != 2 || d.Countries[0].Country != "AU" || d.Countries[1].Country != "US" || d.Countries[1].SentBytes != 150 {
		t.Errorf("countries %+v", d.Countries)
	}
	if len(d.ASNs) != 2 || d.ASNs[0].ASN != 13335 || d.ASNs[1].ASN != 15169 {
		t.Errorf("asns %+v", d.ASNs)
	}

	// A prefix covers several addresses.
	q.Host = prefix("192.168.1.0/29")
	d, err = s.HostDetail(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Addresses) != 2 || d.Addresses[0].Address != "192.168.1.5" || d.Addresses[1].Address != "192.168.1.6" || d.Addresses[1].TotalBytes != 5 {
		t.Errorf("prefix addresses %+v", d.Addresses)
	}
	addrs, err := s.HostAddresses(ctx, q)
	if err != nil || len(addrs) != 2 {
		t.Errorf("host addresses %v %v", addrs, err)
	}
	// As a host group, a prefix leaves out addresses with a longer label of their own.
	q.HostGroups = []netip.Prefix{prefix("192.168.1.0/29"), prefix("192.168.1.6")}
	if addrs, err := s.HostAddresses(ctx, q); err != nil || len(addrs) != 1 || addrs[0] != "192.168.1.5" {
		t.Errorf("group addresses %v %v", addrs, err)
	}
	q.HostGroups = []netip.Prefix{prefix("192.168.1.20/31")}

	q.Host = prefix("192.168.1.5")
	tr, err := s.HostTraffic(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if tr.StepS != 60 || tr.Source != SourceMinute || len(tr.Points) != 60 {
		t.Fatalf("step %v source %v points %d", tr.StepS, tr.Source, len(tr.Points))
	}
	p := tr.Points[30]
	if !p.Time.Equal(at) || math.Abs(p.ReceivedBps-1070*8/60.0) > 1e-9 || math.Abs(p.SentBps-500*8/60.0) > 1e-9 {
		t.Errorf("point %+v", p)
	}
	if math.Abs(tr.Points[31].SentBps-50*8/60.0) > 1e-9 {
		t.Errorf("next point %+v", tr.Points[31])
	}

	// Nothing for an address without traffic.
	q.Host = prefix("10.9.9.9")
	if d, err := s.HostDetail(ctx, q); err != nil || d.ReceivedBytes != 0 || len(d.Ports) != 0 {
		t.Errorf("unknown host %+v %v", d, err)
	}
}

func TestTopRemoteIPsForLocalHosts(t *testing.T) {
	s := open(t)
	at := t0.Add(-30 * time.Minute)
	if err := s.Write(ctx, []flow.Flow{
		hostFlow(flow.DirectionOutbound, "192.168.1.5", "1.1.1.1", 443, at, 100),
		hostFlow(flow.DirectionOutbound, "192.168.1.6", "8.8.8.8", 443, at, 200),
	}); err != nil {
		t.Fatal(err)
	}
	for _, hosts := range [][]string{{"192.168.1.5"}, {}} {
		res, err := s.TopRemoteIPs(ctx, RemoteQuery{From: t0.Add(-time.Hour), To: t0, Now: t0, LocalHosts: hosts})
		if err != nil {
			t.Fatal(err)
		}
		want := len(hosts)
		if len(res.Items) != want || (want == 1 && (res.Items[0].RemoteIP != "1.1.1.1" || res.TotalBytes != 100)) {
			t.Errorf("hosts %v: %+v", hosts, res)
		}
	}
}
