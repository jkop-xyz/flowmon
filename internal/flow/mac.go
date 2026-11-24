package flow

import (
	"fmt"
	"net/netip"
)

// MAC is a 48-bit MAC address; the zero MAC means none.
type MAC [6]byte

// IsZero reports whether m is no address.
func (m MAC) IsZero() bool { return m == MAC{} }

// Group reports whether m is a multicast or broadcast address, which never names a
// device.
func (m MAC) Group() bool { return m[0]&0x01 != 0 }

// String writes m as aa:bb:cc:dd:ee:ff, or "" for the zero MAC.
func (m MAC) String() string {
	if m.IsZero() {
		return ""
	}
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}

// ParseMAC reads what String writes; "" is the zero MAC.
func ParseMAC(s string) (MAC, error) {
	var m MAC
	if s == "" {
		return m, nil
	}
	if _, err := fmt.Sscanf(s, "%02x:%02x:%02x:%02x:%02x:%02x", &m[0], &m[1], &m[2], &m[3], &m[4], &m[5]); err != nil || len(s) != 17 {
		return MAC{}, fmt.Errorf("%q is not a MAC address such as aa:bb:cc:dd:ee:ff", s)
	}
	return m, nil
}

// IANA element IDs of the MAC addresses and addresses a record carries.
const (
	ieSourceMacAddress          = 56
	iePostDestinationMacAddress = 57
	ieDestinationMacAddress     = 80
	iePostSourceMacAddress      = 81
)

// RecordMACs are the MAC addresses of a Flow's record, as the Exporter saw the frames.
type RecordMACs struct {
	Src, Dst, PostSrc, PostDst MAC
	// src, dst, postSrc, postDst are the record's own addresses, which say which end
	// each MAC belongs to.
	src, dst, postSrc, postDst netip.Addr
}

// MACsOf reads the MAC addresses from a Flow's stored fields (EncodeFields). ok is false
// when the record has none.
func MACsOf(fields []byte) (m RecordMACs, ok bool) {
	fs, err := DecodeFields(fields)
	if err != nil {
		return m, false
	}
	for _, f := range fs {
		if f.Spec.Enterprise != 0 {
			continue
		}
		switch f.Spec.ID {
		case ieSourceMacAddress, iePostDestinationMacAddress, ieDestinationMacAddress, iePostSourceMacAddress:
			if len(f.Raw) != 6 {
				continue
			}
			mac := MAC(f.Raw)
			switch f.Spec.ID {
			case ieSourceMacAddress:
				m.Src = mac
			case iePostDestinationMacAddress:
				m.PostDst = mac
			case ieDestinationMacAddress:
				m.Dst = mac
			case iePostSourceMacAddress:
				m.PostSrc = mac
			}
			ok = true
		case ieSourceIPv4Address, ieSourceIPv6Address:
			m.src = addrOf(f.Raw)
		case ieDestinationIPv4Address, ieDestinationIPv6Address:
			m.dst = addrOf(f.Raw)
		case iePostNATSourceIPv4Address, iePostNATSourceIPv6Address:
			m.postSrc = addrOf(f.Raw)
		case iePostNATDestinationIPv4Address, iePostNATDestinationIPv6Address:
			m.postDst = addrOf(f.Raw)
		}
	}
	return m, ok
}

func addrOf(b []byte) netip.Addr {
	a, _ := netip.AddrFromSlice(b)
	return a.Unmap()
}

// Of returns the MAC of the host at address a: the source MAC when a sent the record's
// packets, the MAC they were delivered to when a received them. It is zero when a is
// neither end, or the record lacks that MAC.
//
// A received packet's MAC is taken after routing (postDestinationMacAddress) when the
// record has it: before routing, the frame was addressed to the router.
func (m RecordMACs) Of(a netip.Addr) MAC {
	a = a.Unmap()
	switch {
	case !a.IsValid():
		return MAC{}
	case a == m.src:
		return m.Src
	case a == m.postDst || a == m.dst:
		if !m.PostDst.IsZero() {
			return m.PostDst
		}
		if a == m.dst {
			return m.Dst
		}
	}
	return MAC{}
}

// IsSource reports whether a sent the record's packets, so the record's source MAC is
// its own.
func (m RecordMACs) IsSource(a netip.Addr) bool { a = a.Unmap(); return a.IsValid() && a == m.src }

// Router returns the MACs the record says belong to the Exporter itself: the frames
// arrived addressed to it (Dst) and left from it (PostSrc).
func (m RecordMACs) Router() []MAC {
	var out []MAC
	for _, r := range []MAC{m.Dst, m.PostSrc} {
		if !r.IsZero() && !r.Group() {
			out = append(out, r)
		}
	}
	return out
}
