package snmp

import (
	"context"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gosnmp/gosnmp"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// Neighbor tables (ADR 0005, "Devices are MAC addresses"): the address-to-MAC mappings a
// router or switch has learned, IPv4 (ARP) and IPv6 (neighbor discovery), from IP-MIB's
// ipNetToPhysicalTable, or RFC 1213's ipNetToMediaTable (IPv4 only) on devices without it.
const (
	oidIPNetToPhysicalPhysAddress = ".1.3.6.1.2.1.4.35.1.4"
	oidIPNetToPhysicalType        = ".1.3.6.1.2.1.4.35.1.6"
	oidIPNetToPhysicalState       = ".1.3.6.1.2.1.4.35.1.7"
	oidIPNetToMediaPhysAddress    = ".1.3.6.1.2.1.4.22.1.2"
	oidIPNetToMediaType           = ".1.3.6.1.2.1.4.22.1.4"
)

// Entry types and neighbor states that aren't a device seen on the network: invalid(2)
// and local(5) types (the device's own addresses); invalid(5) and incomplete(7) states.
var (
	skipTypes  = map[int64]bool{2: true, 5: true}
	skipStates = map[int64]bool{5: true, 7: true}
)

// Neighbor is one mapping in a device's neighbor table.
type Neighbor struct {
	IfIndex uint32
	Address netip.Addr
	MAC     flow.MAC
}

// NeighborReader is an agent that reads neighbor tables.
type NeighborReader interface {
	Neighbors(ctx context.Context) ([]Neighbor, error)
}

// Neighbors reads the agent's neighbor tables.
func (a *gosnmpAgent) Neighbors(ctx context.Context) ([]Neighbor, error) {
	a.g.Context = ctx
	return ReadNeighbors(a.g)
}

// ReadNeighbors reads ipNetToPhysicalTable, or ipNetToMediaTable when the device has no
// ipNetToPhysicalTable. Entries without a usable MAC are left out.
func ReadNeighbors(r PDUReader) ([]Neighbor, error) {
	macs, err := column(r, oidIPNetToPhysicalPhysAddress)
	if err != nil {
		return nil, err
	}
	parse, types, states := physicalIndex, map[string]gosnmp.SnmpPDU{}, map[string]gosnmp.SnmpPDU{}
	if len(macs) > 0 {
		if types, err = column(r, oidIPNetToPhysicalType); err != nil {
			return nil, err
		}
		if states, err = column(r, oidIPNetToPhysicalState); err != nil {
			return nil, err
		}
	} else {
		if macs, err = column(r, oidIPNetToMediaPhysAddress); err != nil {
			return nil, err
		}
		if types, err = column(r, oidIPNetToMediaType); err != nil {
			return nil, err
		}
		parse = mediaIndex
	}
	out := []Neighbor{}
	for idx, p := range macs {
		if t, ok := types[idx]; ok && skipTypes[num(t)] {
			continue
		}
		if s, ok := states[idx]; ok && skipStates[num(s)] {
			continue
		}
		b, _ := p.Value.([]byte)
		if len(b) != 6 {
			continue
		}
		mac := flow.MAC(b)
		if mac.IsZero() || mac.Group() {
			continue
		}
		ifIndex, addr, ok := parse(idx)
		if !ok {
			continue
		}
		out = append(out, Neighbor{IfIndex: ifIndex, Address: addr, MAC: mac})
	}
	return out, nil
}

func subIDs(idx string) ([]uint64, bool) {
	parts := strings.Split(idx, ".")
	out := make([]uint64, len(parts))
	for i, p := range parts {
		n, err := strconv.ParseUint(p, 10, 32)
		if err != nil {
			return nil, false
		}
		out[i] = n
	}
	return out, true
}

// physicalIndex parses ipNetToPhysicalTable's index: ifIndex, address type (1 IPv4,
// 2 IPv6), the address's length, then its octets.
func physicalIndex(idx string) (uint32, netip.Addr, bool) {
	ids, ok := subIDs(idx)
	if !ok || len(ids) < 3 || int(ids[2]) != len(ids)-3 {
		return 0, netip.Addr{}, false
	}
	b := make([]byte, len(ids)-3)
	for i, v := range ids[3:] {
		if v > 255 {
			return 0, netip.Addr{}, false
		}
		b[i] = byte(v)
	}
	switch {
	case ids[1] == 1 && len(b) == 4:
		return uint32(ids[0]), netip.AddrFrom4([4]byte(b)), true
	case ids[1] == 2 && len(b) == 16:
		return uint32(ids[0]), netip.AddrFrom16([16]byte(b)), true
	}
	return 0, netip.Addr{}, false
}

// mediaIndex parses ipNetToMediaTable's index: ifIndex, then the IPv4 address.
func mediaIndex(idx string) (uint32, netip.Addr, bool) {
	ids, ok := subIDs(idx)
	if !ok || len(ids) != 5 {
		return 0, netip.Addr{}, false
	}
	var b [4]byte
	for i, v := range ids[1:] {
		if v > 255 {
			return 0, netip.Addr{}, false
		}
		b[i] = byte(v)
	}
	return uint32(ids[0]), netip.AddrFrom4(b), true
}
