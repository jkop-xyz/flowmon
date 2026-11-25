package snmp

import (
	"net/netip"
	"sort"
	"testing"

	"github.com/gosnmp/gosnmp"
)

func TestReadNeighborsIPNetToPhysical(t *testing.T) {
	m := fakeMIB{}
	entry := func(idx string, mac []byte, typ, state int) {
		m.set(oidIPNetToPhysicalPhysAddress+"."+idx, gosnmp.OctetString, mac)
		m.set(oidIPNetToPhysicalType+"."+idx, gosnmp.Integer, typ)
		m.set(oidIPNetToPhysicalState+"."+idx, gosnmp.Integer, state)
	}
	phone := []byte{0x3c, 0x5a, 0xb4, 0, 0, 0x44}
	entry("2.1.4.192.168.10.44", phone, 3, 1) // dynamic, reachable
	// The same phone over IPv6 on ifIndex 2.
	entry("2.2.16.32.1.13.184.0.16.0.0.0.0.0.0.0.0.0.68", phone, 3, 2)
	entry("2.1.4.192.168.10.1", []byte{0x48, 0xa9, 0x8a, 0, 0, 1}, 5, 1)     // the router's own address
	entry("2.1.4.192.168.10.99", []byte{0, 0, 0, 0, 0, 0}, 3, 7)             // incomplete
	entry("2.1.4.192.168.10.98", []byte{0xb8, 0x27, 0xeb, 0, 0, 0x45}, 2, 1) // invalidated
	ns, err := ReadNeighbors(m)
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i].Address.Less(ns[j].Address) })
	if len(ns) != 2 || ns[0].Address != netip.MustParseAddr("192.168.10.44") || ns[0].IfIndex != 2 || ns[0].MAC.String() != "3c:5a:b4:00:00:44" ||
		ns[1].Address != netip.MustParseAddr("2001:db8:10::44") {
		t.Errorf("neighbors %+v", ns)
	}
}

// A device without ipNetToPhysicalTable gives its IPv4 ARP table from ipNetToMediaTable.
func TestReadNeighborsIPNetToMedia(t *testing.T) {
	m := fakeMIB{}
	m.set(oidIPNetToMediaPhysAddress+".7.192.168.88.20", gosnmp.OctetString, []byte{0x00, 0x1b, 0xa9, 0, 0, 0x60})
	m.set(oidIPNetToMediaType+".7.192.168.88.20", gosnmp.Integer, 3)
	m.set(oidIPNetToMediaPhysAddress+".7.192.168.88.21", gosnmp.OctetString, []byte{0x00, 0x1b, 0xa9, 0, 0, 0x61})
	m.set(oidIPNetToMediaType+".7.192.168.88.21", gosnmp.Integer, 2) // invalid
	ns, err := ReadNeighbors(m)
	if err != nil || len(ns) != 1 || ns[0].Address != netip.MustParseAddr("192.168.88.20") || ns[0].IfIndex != 7 {
		t.Errorf("neighbors %+v %v", ns, err)
	}
}
