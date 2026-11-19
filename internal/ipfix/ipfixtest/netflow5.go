package ipfixtest

import (
	"encoding/binary"
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// NetFlowV5Record is one v5 flow record.
type NetFlowV5Record struct {
	Src, Dst, NextHop netip.Addr
	Input, Output     uint16
	Packets, Octets   uint32
	// First and Last are encoded as milliseconds of uptime, modulo 2³².
	First, Last        time.Time
	SrcPort, DstPort   uint16
	TCPFlags, Protocol uint8
	ToS                uint8
	SrcAS, DstAS       uint16
	SrcMask, DstMask   uint8
}

// NetFlowV5 builds one NetFlow v5 packet.
type NetFlowV5 struct {
	Sequence   uint32
	ExportTime time.Time
	Boot       time.Time
	EngineType uint8
	EngineID   uint8
	// Sampling is the header's sampling field: mode in the top two bits, interval below.
	Sampling uint16
	// Count overrides the header's record count when non-zero.
	Count   uint16
	Records []NetFlowV5Record
}

func addr4(a netip.Addr) []byte {
	if !a.IsValid() {
		return make([]byte, 4)
	}
	b := a.As4()
	return b[:]
}

// Bytes encodes the packet.
func (m *NetFlowV5) Bytes() []byte {
	count := m.Count
	if count == 0 {
		count = uint16(len(m.Records))
	}
	uptime := func(t time.Time) uint32 { return uint32(t.Sub(m.Boot).Milliseconds()) }
	b := binary.BigEndian.AppendUint16(nil, ipfix.VersionNetFlowV5)
	b = binary.BigEndian.AppendUint16(b, count)
	b = binary.BigEndian.AppendUint32(b, uptime(m.ExportTime))
	b = binary.BigEndian.AppendUint32(b, uint32(m.ExportTime.Unix()))
	b = binary.BigEndian.AppendUint32(b, uint32(m.ExportTime.Nanosecond()))
	b = binary.BigEndian.AppendUint32(b, m.Sequence)
	b = append(b, m.EngineType, m.EngineID)
	b = binary.BigEndian.AppendUint16(b, m.Sampling)
	for _, r := range m.Records {
		b = append(b, addr4(r.Src)...)
		b = append(b, addr4(r.Dst)...)
		b = append(b, addr4(r.NextHop)...)
		b = binary.BigEndian.AppendUint16(b, r.Input)
		b = binary.BigEndian.AppendUint16(b, r.Output)
		b = binary.BigEndian.AppendUint32(b, r.Packets)
		b = binary.BigEndian.AppendUint32(b, r.Octets)
		b = binary.BigEndian.AppendUint32(b, uptime(r.First))
		b = binary.BigEndian.AppendUint32(b, uptime(r.Last))
		b = binary.BigEndian.AppendUint16(b, r.SrcPort)
		b = binary.BigEndian.AppendUint16(b, r.DstPort)
		b = append(b, 0, r.TCPFlags, r.Protocol, r.ToS)
		b = binary.BigEndian.AppendUint16(b, r.SrcAS)
		b = binary.BigEndian.AppendUint16(b, r.DstAS)
		b = append(b, r.SrcMask, r.DstMask, 0, 0)
	}
	return b
}
