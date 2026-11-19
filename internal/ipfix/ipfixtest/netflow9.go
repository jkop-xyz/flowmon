package ipfixtest

import (
	"encoding/binary"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// NetFlowV9 builds one NetFlow v9 export packet (RFC 3954).
type NetFlowV9 struct {
	SourceID   uint32
	Sequence   uint32
	ExportTime time.Time
	// SysUptime is how long the Exporter has been up; it wraps at 2³² ms like the field.
	SysUptime time.Duration
	// Count overrides the header's record count when non-zero.
	Count   uint16
	sets    [][]byte
	records int
}

// NewNetFlowV9 starts a packet for a Source ID from an Exporter that booted at boot.
func NewNetFlowV9(sourceID, seq uint32, exportTime, boot time.Time) *NetFlowV9 {
	return &NetFlowV9{SourceID: sourceID, Sequence: seq, ExportTime: exportTime, SysUptime: exportTime.Sub(boot)}
}

// TemplateFlowSet adds a Template FlowSet (ID 0). Fields with Enterprise
// ipfix.EnterpriseNetFlowV9 are written with their ID as the field type.
func (m *NetFlowV9) TemplateFlowSet(templates ...ipfix.Template) *NetFlowV9 {
	var body []byte
	for _, t := range templates {
		body = binary.BigEndian.AppendUint16(body, t.ID)
		body = binary.BigEndian.AppendUint16(body, uint16(len(t.Fields)))
		body = appendNetFlowV9Specs(body, t.Fields)
		m.records++
	}
	return m.RawFlowSet(0, body)
}

// OptionsTemplateFlowSet adds an Options Template FlowSet (ID 1). The first
// ScopeFieldCount fields are scope fields, whose IDs are NetFlow v9 scope types
// (1 System, 2 Interface, 3 Line Card, 4 Cache, 5 Template).
func (m *NetFlowV9) OptionsTemplateFlowSet(templates ...ipfix.Template) *NetFlowV9 {
	var body []byte
	for _, t := range templates {
		body = binary.BigEndian.AppendUint16(body, t.ID)
		body = binary.BigEndian.AppendUint16(body, t.ScopeFieldCount*4)
		body = binary.BigEndian.AppendUint16(body, uint16(len(t.Fields)-int(t.ScopeFieldCount))*4)
		body = appendNetFlowV9Specs(body, t.Fields)
		m.records++
	}
	return m.RawFlowSet(1, body)
}

// DataFlowSet adds a data FlowSet for template t: one []byte per field per record.
func (m *NetFlowV9) DataFlowSet(t ipfix.Template, records ...[][]byte) *NetFlowV9 {
	var body []byte
	for _, rec := range records {
		for _, v := range rec {
			body = append(body, v...)
		}
		m.records++
	}
	return m.RawFlowSet(t.ID, body)
}

// RawFlowSet adds a FlowSet with any ID and body, padded to a multiple of 4 bytes as
// RFC 3954 recommends.
func (m *NetFlowV9) RawFlowSet(id uint16, body []byte) *NetFlowV9 {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	set := binary.BigEndian.AppendUint16(nil, id)
	set = binary.BigEndian.AppendUint16(set, uint16(4+len(body)))
	m.sets = append(m.sets, append(set, body...))
	return m
}

// Bytes encodes the packet.
func (m *NetFlowV9) Bytes() []byte {
	count := m.Count
	if count == 0 {
		count = uint16(m.records)
	}
	b := binary.BigEndian.AppendUint16(nil, ipfix.VersionNetFlowV9)
	b = binary.BigEndian.AppendUint16(b, count)
	b = binary.BigEndian.AppendUint32(b, uint32(m.SysUptime.Milliseconds()))
	b = binary.BigEndian.AppendUint32(b, uint32(m.ExportTime.Unix()))
	b = binary.BigEndian.AppendUint32(b, m.Sequence)
	b = binary.BigEndian.AppendUint32(b, m.SourceID)
	for _, s := range m.sets {
		b = append(b, s...)
	}
	return b
}

func appendNetFlowV9Specs(b []byte, specs []ipfix.FieldSpec) []byte {
	for _, s := range specs {
		b = binary.BigEndian.AppendUint16(b, s.ID)
		b = binary.BigEndian.AppendUint16(b, s.Length)
	}
	return b
}

// NetFlowV9Template is a typical NetFlow v9 IPv4 Template, as softflowd and most
// routers send: addresses, ports, protocol, interfaces, 4-byte counters and
// FIRST_SWITCHED/LAST_SWITCHED in milliseconds of uptime.
func NetFlowV9Template(id uint16) ipfix.Template {
	return ipfix.Template{ID: id, Fields: []ipfix.FieldSpec{
		{ID: 8, Length: 4},  // IPV4_SRC_ADDR
		{ID: 12, Length: 4}, // IPV4_DST_ADDR
		{ID: 7, Length: 2},  // L4_SRC_PORT
		{ID: 11, Length: 2}, // L4_DST_PORT
		{ID: 4, Length: 1},  // PROTOCOL
		{ID: 10, Length: 2}, // INPUT_SNMP (2 bytes, the RFC 3954 default)
		{ID: 14, Length: 2}, // OUTPUT_SNMP
		{ID: 1, Length: 4},  // IN_BYTES
		{ID: 2, Length: 4},  // IN_PKTS
		{ID: 22, Length: 4}, // FIRST_SWITCHED
		{ID: 21, Length: 4}, // LAST_SWITCHED
	}}
}

// UptimeMillis encodes a time as NetFlow v9 FIRST_SWITCHED/LAST_SWITCHED: milliseconds
// since boot, modulo 2³².
func UptimeMillis(t, boot time.Time) []byte {
	return U32(uint32(t.Sub(boot).Milliseconds()))
}
