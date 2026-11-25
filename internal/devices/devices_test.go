package devices

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

var (
	t0       = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	exporter = netip.MustParseAddr("192.168.10.1")
	wanIP    = netip.MustParseAddr("203.0.113.7")
	remote   = netip.MustParseAddr("93.184.215.14")

	phone     = mustMAC("3c:5a:b4:00:00:44")
	laptop    = mustMAC("b8:27:eb:00:00:45")
	routerLAN = mustMAC("48:a9:8a:00:00:01")
	routerWAN = mustMAC("48:a9:8a:00:00:02")
	isp       = mustMAC("00:00:0c:00:00:99")
)

func mustMAC(s string) flow.MAC {
	m, err := flow.ParseMAC(s)
	if err != nil {
		panic(err)
	}
	return m
}

// record is a RouterOS-shaped record: addresses before and after NAT, and the MACs the
// frames had before and after routing.
type record struct {
	src, dst, postSrc, postDst             netip.Addr
	srcMAC, dstMAC, postSrcMAC, postDstMAC flow.MAC
}

func (r record) fields() []byte {
	var fs []ipfix.Field
	addr := func(v4, v6 uint16, a netip.Addr) {
		if !a.IsValid() {
			return
		}
		id := v4
		if a.Is6() {
			id = v6
		}
		raw := a.AsSlice()
		fs = append(fs, ipfix.Field{Spec: ipfix.FieldSpec{ID: id, Length: uint16(len(raw))}, Raw: raw})
	}
	mac := func(id uint16, m flow.MAC) {
		if !m.IsZero() {
			fs = append(fs, ipfix.Field{Spec: ipfix.FieldSpec{ID: id, Length: 6}, Raw: m[:]})
		}
	}
	addr(8, 27, r.src)
	addr(12, 28, r.dst)
	addr(225, 281, r.postSrc)
	addr(226, 282, r.postDst)
	mac(56, r.srcMAC)
	mac(80, r.dstMAC)
	mac(81, r.postSrcMAC)
	mac(57, r.postDstMAC)
	return flow.EncodeFields(fs)
}

func classified(r record, dir flow.Direction, local, peer netip.Addr, at time.Time) flow.Flow {
	return flow.Flow{Exporter: exporter, End: at, Start: at, Src: r.src, Dst: r.dst, Direction: dir,
		Local: local, Peer: peer, Fields: r.fields()}
}

func ip(s string) netip.Addr { return netip.MustParseAddr(s) }

// outbound is the phone's packets to a web server, NATed to the router's WAN address.
func outbound(client netip.Addr, mac flow.MAC) record {
	return record{src: client, dst: remote, postSrc: wanIP, postDst: remote,
		srcMAC: mac, dstMAC: routerLAN, postSrcMAC: routerWAN, postDstMAC: isp}
}

// inbound is the reply, addressed to the WAN address and delivered to the client.
func inbound(client netip.Addr, mac flow.MAC) record {
	return record{src: remote, dst: wanIP, postSrc: remote, postDst: client,
		srcMAC: isp, dstMAC: routerWAN, postSrcMAC: routerLAN, postDstMAC: mac}
}

func TestTheLocalHostsEndGivesItsDevice(t *testing.T) {
	a := New(Options{})
	client := ip("192.168.10.44")

	out := classified(outbound(client, phone), flow.DirectionOutbound, client, remote, t0)
	a.Assign(&out)
	if out.LocalMAC != phone {
		t.Errorf("outbound: %s, want the source MAC %s", out.LocalMAC, phone)
	}

	in := classified(inbound(client, phone), flow.DirectionInbound, client, remote, t0)
	a.Assign(&in)
	if in.LocalMAC != phone {
		t.Errorf("inbound through NAT: %s, want the MAC it was delivered to %s", in.LocalMAC, phone)
	}

	// A biflow's reverse direction carries the forward record's fields: the phone is its
	// destination now, but still the record's source.
	rev := classified(outbound(client, phone), flow.DirectionInbound, client, remote, t0)
	rev.Src, rev.Dst = remote, client
	a.Assign(&rev)
	if rev.LocalMAC != phone {
		t.Errorf("biflow reverse: %s, want %s", rev.LocalMAC, phone)
	}

	// IPv6, routed without NAT: before routing the frame was for the router.
	v6 := ip("2001:db8:10::44")
	in6 := classified(record{src: ip("2606:2800:21f:cb07::1"), dst: v6, srcMAC: isp, dstMAC: routerWAN, postSrcMAC: routerLAN, postDstMAC: phone},
		flow.DirectionInbound, v6, ip("2606:2800:21f:cb07::1"), t0)
	a.Assign(&in6)
	if in6.LocalMAC != phone {
		t.Errorf("IPv6 inbound: %s, want %s", in6.LocalMAC, phone)
	}
}

func TestInternalFlowsGetBothDevices(t *testing.T) {
	a := New(Options{})
	phoneIP, laptopIP := ip("192.168.10.44"), ip("192.168.20.45")
	r := record{src: phoneIP, dst: laptopIP, srcMAC: phone, dstMAC: routerLAN, postSrcMAC: routerLAN, postDstMAC: laptop}
	f := classified(r, flow.DirectionInternal, phoneIP, laptopIP, t0)
	a.Assign(&f)
	if f.LocalMAC != phone || f.PeerMAC != laptop {
		t.Errorf("internal: local %s peer %s", f.LocalMAC, f.PeerMAC)
	}
}

func TestTheRoutersOwnMACsAreNeverDevices(t *testing.T) {
	var learned []Router
	a := New(Options{Learned: func(r Router) { learned = append(learned, r) }})
	client := ip("192.168.10.44")
	out := classified(outbound(client, phone), flow.DirectionOutbound, client, remote, t0)
	a.Assign(&out)
	if len(learned) != 2 || learned[0].MAC != routerLAN || learned[1].MAC != routerWAN || learned[0].Reason != "exporter" {
		t.Fatalf("learned %+v", learned)
	}

	// The router itself as the Local Host has no Device.
	own := classified(record{src: exporter, dst: remote, srcMAC: routerLAN, dstMAC: routerLAN, postSrcMAC: routerWAN}, flow.DirectionOutbound, exporter, remote, t0)
	own.LocalIsExporterHost = true
	a.Assign(&own)
	if !own.LocalMAC.IsZero() {
		t.Errorf("the router as Local Host got Device %s", own.LocalMAC)
	}

	// A record whose source MAC is the router's, such as traffic from a host behind
	// another subnet the router routes, gives no Device either.
	hop := classified(record{src: ip("192.168.30.5"), dst: remote, srcMAC: routerLAN, dstMAC: routerLAN}, flow.DirectionOutbound, ip("192.168.30.5"), remote, t0)
	a.Assign(&hop)
	if !hop.LocalMAC.IsZero() {
		t.Errorf("router MAC taken for a Device")
	}

	// Learned once; known ones aren't learned again.
	a2 := New(Options{Known: learned, Learned: func(r Router) { t.Errorf("relearned %s", r.MAC) }})
	again := classified(outbound(client, phone), flow.DirectionOutbound, client, remote, t0)
	a2.Assign(&again)
}

func TestAMACWithManyAddressesIsARouter(t *testing.T) {
	var learned []Router
	a := New(Options{Learned: func(r Router) { learned = append(learned, r) }})
	downstream := mustMAC("dc:2c:6e:00:00:07") // another router, NATing nothing
	for i := 1; i <= HopAddresses+1; i++ {
		client := netip.AddrFrom4([4]byte{192, 168, 30, byte(i)})
		f := classified(outbound(client, downstream), flow.DirectionOutbound, client, remote, t0.Add(time.Duration(i)*time.Minute))
		a.Assign(&f)
		if want := i <= HopAddresses; !f.LocalMAC.IsZero() != want {
			t.Errorf("address %d: Device %q", i, f.LocalMAC)
		}
	}
	if len(learned) != 3 || learned[2].MAC != downstream || learned[2].Reason != "many addresses" {
		t.Errorf("learned %+v", learned)
	}
}

func TestAnAddressChangeKeepsTheDevice(t *testing.T) {
	a := New(Options{})
	// A new DHCP lease each hour for a day: never more than HopAddresses at once.
	for h := 0; h < 24; h++ {
		client := netip.AddrFrom4([4]byte{192, 168, 10, byte(100 + h)})
		f := classified(outbound(client, phone), flow.DirectionOutbound, client, remote, t0.Add(time.Duration(h)*61*time.Minute))
		a.Assign(&f)
		if f.LocalMAC != phone {
			t.Fatalf("hour %d at %s: Device %q", h, client, f.LocalMAC)
		}
	}
}

func TestRecordsWithoutMACs(t *testing.T) {
	a := New(Options{})
	client := ip("192.168.10.44")
	f := classified(record{src: client, dst: remote}, flow.DirectionOutbound, client, remote, t0)
	f.LocalMAC = phone // a stale value is cleared
	a.Assign(&f)
	if !f.LocalMAC.IsZero() {
		t.Errorf("no MAC fields gave %s", f.LocalMAC)
	}
	bcast := classified(record{src: client, dst: ip("192.168.10.255"), srcMAC: phone, postDstMAC: mustMAC("ff:ff:ff:ff:ff:ff")}, flow.DirectionInternal, ip("192.168.10.255"), client, t0)
	a.Assign(&bcast)
	if !bcast.LocalMAC.IsZero() || bcast.PeerMAC != phone {
		t.Errorf("broadcast: local %s peer %s", bcast.LocalMAC, bcast.PeerMAC)
	}
}

// routerOSInbound is a reply as RouterOS reports it: the destination MACs, before and
// after routing, are the router's own.
func routerOSInbound(client netip.Addr) record {
	return record{src: remote, dst: wanIP, postSrc: remote, postDst: client,
		srcMAC: isp, dstMAC: routerWAN, postSrcMAC: routerLAN, postDstMAC: routerLAN}
}

func TestReceivedFlowsTakeTheMACTheAddressSentFrom(t *testing.T) {
	a := New(Options{})
	client := ip("192.168.10.44")

	early := classified(routerOSInbound(client), flow.DirectionInbound, client, remote, t0)
	a.Assign(&early)
	if !early.LocalMAC.IsZero() {
		t.Errorf("before the address sent anything: %s", early.LocalMAC)
	}

	out := classified(outbound(client, phone), flow.DirectionOutbound, client, remote, t0.Add(time.Minute))
	a.Assign(&out)
	in := classified(routerOSInbound(client), flow.DirectionInbound, client, remote, t0.Add(2*time.Minute))
	a.Assign(&in)
	if in.LocalMAC != phone {
		t.Errorf("reply after the phone sent: %q, want %s, not the router's", in.LocalMAC, phone)
	}

	stale := classified(routerOSInbound(client), flow.DirectionInbound, client, remote, t0.Add(AddressTTL+2*time.Minute))
	a.Assign(&stale)
	if !stale.LocalMAC.IsZero() {
		t.Errorf("a day later: %s", stale.LocalMAC)
	}

	// The address moved to the laptop.
	moved := classified(outbound(client, laptop), flow.DirectionOutbound, client, remote, t0.Add(3*time.Minute))
	a.Assign(&moved)
	again := classified(routerOSInbound(client), flow.DirectionInbound, client, remote, t0.Add(4*time.Minute))
	a.Assign(&again)
	if again.LocalMAC != laptop {
		t.Errorf("after the address moved: %s, want %s", again.LocalMAC, laptop)
	}
}

func TestRecentAddressesBridgeARestart(t *testing.T) {
	client := ip("192.168.10.44")
	a := New(Options{Recent: []Sighting{{Address: client, MAC: phone, At: t0}}})
	in := classified(routerOSInbound(client), flow.DirectionInbound, client, remote, t0.Add(time.Hour))
	a.Assign(&in)
	if in.LocalMAC != phone {
		t.Errorf("reply after a restart: %q, want %s from the recent addresses", in.LocalMAC, phone)
	}
}

// A quiet device that only receives, through an Exporter whose records name no MAC for
// it, gets its Device from the router's neighbor table, until the mapping ages out.
func TestNeighborTablesGiveQuietDevicesTheirMAC(t *testing.T) {
	a := New(Options{Known: []Router{{MAC: routerLAN}}})
	printer, printerMAC := ip("192.168.10.60"), mustMAC("00:1b:a9:00:00:60")
	reply := func(at time.Time) flow.Flow {
		r := record{src: remote, dst: printer, srcMAC: isp, dstMAC: routerWAN}
		return classified(r, flow.DirectionInbound, printer, remote, at)
	}
	f := reply(t0)
	a.Assign(&f)
	if !f.LocalMAC.IsZero() {
		t.Fatalf("no MAC known yet, got %s", f.LocalMAC)
	}
	if !a.Observe(printer, printerMAC, t0) {
		t.Fatal("the printer's mapping was refused")
	}
	f = reply(t0.Add(time.Minute))
	a.Assign(&f)
	if f.LocalMAC != printerMAC {
		t.Errorf("after the neighbor table: %s, want %s", f.LocalMAC, printerMAC)
	}
	// Not seen again for longer than AddressTTL: the mapping has aged out.
	f = reply(t0.Add(AddressTTL + time.Hour))
	a.Assign(&f)
	if !f.LocalMAC.IsZero() {
		t.Errorf("an aged-out mapping still used: %s", f.LocalMAC)
	}
	// The router's own MAC in a neighbor table is never a Device.
	if a.Observe(ip("192.168.10.1"), routerLAN, t0) {
		t.Error("a router hop's MAC taken as a Device")
	}
}
