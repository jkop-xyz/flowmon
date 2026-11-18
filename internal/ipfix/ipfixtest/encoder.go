// Package ipfixtest builds IPFIX messages for tests. It is the "test-only encoder"
// from ADR 0002: Flowmon never exports IPFIX, so nothing outside tests imports it.
package ipfixtest

import (
	"encoding/binary"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// Message builds one IPFIX message from sets.
type Message struct {
	ExportTime        time.Time
	Sequence          uint32
	ObservationDomain uint32
	sets              [][]byte
}

// NewMessage starts a message for an Observation Domain.
func NewMessage(od uint32, seq uint32, exportTime time.Time) *Message {
	return &Message{ObservationDomain: od, Sequence: seq, ExportTime: exportTime}
}

// TemplateSet adds a Template Set (set ID 2) holding the given Templates.
func (m *Message) TemplateSet(templates ...ipfix.Template) *Message {
	var body []byte
	for _, t := range templates {
		body = binary.BigEndian.AppendUint16(body, t.ID)
		body = binary.BigEndian.AppendUint16(body, uint16(len(t.Fields)))
		body = appendSpecs(body, t.Fields)
	}
	return m.RawSet(ipfix.SetIDTemplate, body)
}

// OptionsTemplateSet adds an Options Template Set (set ID 3).
func (m *Message) OptionsTemplateSet(templates ...ipfix.Template) *Message {
	var body []byte
	for _, t := range templates {
		body = binary.BigEndian.AppendUint16(body, t.ID)
		body = binary.BigEndian.AppendUint16(body, uint16(len(t.Fields)))
		body = binary.BigEndian.AppendUint16(body, t.ScopeFieldCount)
		body = appendSpecs(body, t.Fields)
	}
	return m.RawSet(ipfix.SetIDOptionsTemplate, body)
}

// Withdrawal adds a Template Withdrawal record for id in a Template Set.
func (m *Message) Withdrawal(id uint16) *Message {
	body := binary.BigEndian.AppendUint16(nil, id)
	body = binary.BigEndian.AppendUint16(body, 0)
	return m.RawSet(ipfix.SetIDTemplate, body)
}

// DataSet adds a Data Set for template t. Each record is one []byte per field, in
// template order; variable-length fields get their length prefix added.
func (m *Message) DataSet(t ipfix.Template, records ...[][]byte) *Message {
	var body []byte
	for _, rec := range records {
		for i, spec := range t.Fields {
			v := rec[i]
			if spec.Variable() {
				if len(v) < 255 {
					body = append(body, byte(len(v)))
				} else {
					body = append(body, 255)
					body = binary.BigEndian.AppendUint16(body, uint16(len(v)))
				}
			}
			body = append(body, v...)
		}
	}
	return m.RawSet(t.ID, body)
}

// RawSet adds a set with any ID and body, for building malformed messages.
func (m *Message) RawSet(id uint16, body []byte) *Message {
	set := binary.BigEndian.AppendUint16(nil, id)
	set = binary.BigEndian.AppendUint16(set, uint16(4+len(body)))
	m.sets = append(m.sets, append(set, body...))
	return m
}

// Bytes encodes the message with a correct length field.
func (m *Message) Bytes() []byte {
	length := ipfix.HeaderLength
	for _, s := range m.sets {
		length += len(s)
	}
	b := make([]byte, 0, length)
	b = binary.BigEndian.AppendUint16(b, ipfix.Version)
	b = binary.BigEndian.AppendUint16(b, uint16(length))
	b = binary.BigEndian.AppendUint32(b, uint32(m.ExportTime.Unix()))
	b = binary.BigEndian.AppendUint32(b, m.Sequence)
	b = binary.BigEndian.AppendUint32(b, m.ObservationDomain)
	for _, s := range m.sets {
		b = append(b, s...)
	}
	return b
}

func appendSpecs(b []byte, specs []ipfix.FieldSpec) []byte {
	for _, s := range specs {
		id := s.ID
		if s.Enterprise != 0 {
			id |= 0x8000
		}
		b = binary.BigEndian.AppendUint16(b, id)
		b = binary.BigEndian.AppendUint16(b, s.Length)
		if s.Enterprise != 0 {
			b = binary.BigEndian.AppendUint32(b, s.Enterprise)
		}
	}
	return b
}

// U8, U16, U32 and U64 encode big-endian integers for record values.
func U8(v uint8) []byte   { return []byte{v} }
func U16(v uint16) []byte { return binary.BigEndian.AppendUint16(nil, v) }
func U32(v uint32) []byte { return binary.BigEndian.AppendUint32(nil, v) }
func U64(v uint64) []byte { return binary.BigEndian.AppendUint64(nil, v) }

// MikroTikLikeTemplate is a typical IPv4 flow Template in the shape RouterOS Traffic Flow
// sends: addresses, ports, protocol, interfaces, counters, timestamps and NAT fields.
// It is built from the standard, not from a capture; see ADR 0002.
func MikroTikLikeTemplate(id uint16) ipfix.Template {
	return ipfix.Template{ID: id, Fields: []ipfix.FieldSpec{
		{ID: 8, Length: 4},   // sourceIPv4Address
		{ID: 12, Length: 4},  // destinationIPv4Address
		{ID: 7, Length: 2},   // sourceTransportPort
		{ID: 11, Length: 2},  // destinationTransportPort
		{ID: 4, Length: 1},   // protocolIdentifier
		{ID: 10, Length: 4},  // ingressInterface
		{ID: 14, Length: 4},  // egressInterface
		{ID: 1, Length: 4},   // octetDeltaCount (reduced size)
		{ID: 2, Length: 4},   // packetDeltaCount (reduced size)
		{ID: 22, Length: 4},  // flowStartSysUpTime
		{ID: 21, Length: 4},  // flowEndSysUpTime
		{ID: 225, Length: 4}, // postNATSourceIPv4Address
		{ID: 226, Length: 4}, // postNATDestinationIPv4Address
	}}
}
