package capture

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"time"
)

// Datagram is one UDP datagram read from a capture.
type Datagram struct {
	Time     time.Time
	Src, Dst netip.AddrPort
	Payload  []byte
}

// Link types Read understands.
const (
	linkTypeEthernet  = 1
	linkTypeLinuxSLL  = 113
	linkTypeLinuxSLL2 = 276
)

// Read returns the UDP datagrams in a classic pcap file: what FLOWMON_DEBUG_CAPTURE
// writes, or a tcpdump capture of flow export (Ethernet, raw IP or Linux "any"). Other
// packets are skipped; IP fragments can't be reassembled and are skipped too. pcapng,
// Wireshark's default, isn't read: save as "pcap" instead.
func Read(r io.Reader) ([]Datagram, error) {
	var hdr [24]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, fmt.Errorf("capture: reading the pcap header: %w", err)
	}
	var order binary.ByteOrder
	nanos := false
	switch binary.LittleEndian.Uint32(hdr[:]) {
	case 0xa1b2c3d4:
		order = binary.LittleEndian
	case 0xa1b23c4d:
		order, nanos = binary.LittleEndian, true
	case 0xd4c3b2a1:
		order = binary.BigEndian
	case 0x4d3cb2a1:
		order, nanos = binary.BigEndian, true
	case 0x0a0d0d0a:
		return nil, errors.New("capture: this is pcapng; save it as pcap")
	default:
		return nil, errors.New("capture: not a pcap file")
	}
	link := order.Uint32(hdr[20:])

	var out []Datagram
	var rec [16]byte
	for {
		if _, err := io.ReadFull(r, rec[:]); err == io.EOF {
			return out, nil
		} else if err != nil {
			return out, fmt.Errorf("capture: reading a packet header: %w", err)
		}
		sec, frac := order.Uint32(rec[0:]), order.Uint32(rec[4:])
		n := order.Uint32(rec[8:])
		if n > 1<<20 {
			return out, fmt.Errorf("capture: packet of %d bytes", n)
		}
		data := make([]byte, n)
		if _, err := io.ReadFull(r, data); err != nil {
			return out, fmt.Errorf("capture: reading a packet: %w", err)
		}
		ts := time.Unix(int64(sec), int64(frac)*1000).UTC()
		if nanos {
			ts = time.Unix(int64(sec), int64(frac)).UTC()
		}
		ip, ok := linkPayload(link, data)
		if !ok {
			continue
		}
		if d, ok := udpDatagram(ip); ok {
			d.Time = ts
			out = append(out, d)
		}
	}
}

// linkPayload strips the link-layer header, returning the IP packet.
func linkPayload(link uint32, b []byte) ([]byte, bool) {
	var etherType uint16
	switch link {
	case linkTypeRaw:
		return b, true
	case linkTypeEthernet:
		if len(b) < 14 {
			return nil, false
		}
		etherType, b = binary.BigEndian.Uint16(b[12:]), b[14:]
		for etherType == 0x8100 && len(b) >= 4 { // VLAN tags
			etherType, b = binary.BigEndian.Uint16(b[2:]), b[4:]
		}
	case linkTypeLinuxSLL:
		if len(b) < 16 {
			return nil, false
		}
		etherType, b = binary.BigEndian.Uint16(b[14:]), b[16:]
	case linkTypeLinuxSLL2:
		if len(b) < 20 {
			return nil, false
		}
		etherType, b = binary.BigEndian.Uint16(b[0:]), b[20:]
	default:
		return nil, false
	}
	return b, etherType == 0x0800 || etherType == 0x86DD
}

// udpDatagram parses an unfragmented IPv4 or IPv6 packet carrying UDP.
func udpDatagram(b []byte) (Datagram, bool) {
	if len(b) < 1 {
		return Datagram{}, false
	}
	var src, dst netip.Addr
	var udp []byte
	switch b[0] >> 4 {
	case 4:
		ihl := int(b[0]&0x0F) * 4
		if len(b) < 20 || ihl < 20 || len(b) < ihl || b[9] != 17 {
			return Datagram{}, false
		}
		if flags := binary.BigEndian.Uint16(b[6:]); flags&0x3FFF != 0 { // more fragments, or an offset
			return Datagram{}, false
		}
		total := int(binary.BigEndian.Uint16(b[2:]))
		if total < ihl || total > len(b) {
			total = len(b)
		}
		src, dst = netip.AddrFrom4([4]byte(b[12:16])), netip.AddrFrom4([4]byte(b[16:20]))
		udp = b[ihl:total]
	case 6:
		if len(b) < 40 || b[6] != 17 { // extension headers (fragments) aren't followed
			return Datagram{}, false
		}
		src, dst = netip.AddrFrom16([16]byte(b[8:24])), netip.AddrFrom16([16]byte(b[24:40]))
		udp = b[40:]
	default:
		return Datagram{}, false
	}
	if len(udp) < 8 {
		return Datagram{}, false
	}
	length := int(binary.BigEndian.Uint16(udp[4:]))
	if length < 8 || length > len(udp) {
		length = len(udp)
	}
	return Datagram{
		Src:     netip.AddrPortFrom(src, binary.BigEndian.Uint16(udp[0:])),
		Dst:     netip.AddrPortFrom(dst, binary.BigEndian.Uint16(udp[2:])),
		Payload: append([]byte(nil), udp[8:length]...),
	}, true
}
