package ipfix

import (
	"fmt"
	"time"
)

// NetFlow v5 has no Templates: every record has the same 48-byte layout. It decodes
// into the same Records as IPFIX and NetFlow v9, described by a fixed Template
// (NetFlowV5Template) whose fields are the IPFIX elements with the same meaning. Its
// flow times are milliseconds of uptime, like v9's, converted with the boot time the
// header gives (to the nanosecond, unlike v9).

// VersionNetFlowV5 is the NetFlow v5 export packet version number.
const VersionNetFlowV5 = 5

// NetFlow v5 sizes.
const (
	NetFlowV5HeaderLength = 24
	NetFlowV5RecordLength = 48
)

// NetFlowV5Template describes the fixed v5 record. The two padding bytes after the
// ports and the two at the end aren't fields; Records skip them. Its ID is 0: NetFlow
// v5 has no Template IDs, and no IPFIX or v9 Template can have that one.
var NetFlowV5Template = Template{Version: VersionNetFlowV5, ID: 0, Fields: []FieldSpec{
	{ID: 8, Length: 4},  // srcaddr → sourceIPv4Address
	{ID: 12, Length: 4}, // dstaddr → destinationIPv4Address
	{ID: 15, Length: 4}, // nexthop → ipNextHopIPv4Address
	{ID: 10, Length: 2}, // input → ingressInterface
	{ID: 14, Length: 2}, // output → egressInterface
	{ID: 2, Length: 4},  // dPkts → packetDeltaCount
	{ID: 1, Length: 4},  // dOctets → octetDeltaCount
	{ID: 22, Length: 4}, // First → flowStartSysUpTime
	{ID: 21, Length: 4}, // Last → flowEndSysUpTime
	{ID: 7, Length: 2},  // srcport → sourceTransportPort
	{ID: 11, Length: 2}, // dstport → destinationTransportPort
	{ID: 6, Length: 1},  // tcp_flags → tcpControlBits
	{ID: 4, Length: 1},  // prot → protocolIdentifier
	{ID: 5, Length: 1},  // tos → ipClassOfService
	{ID: 16, Length: 2}, // src_as → bgpSourceAsNumber
	{ID: 17, Length: 2}, // dst_as → bgpDestinationAsNumber
	{ID: 9, Length: 1},  // src_mask → sourceIPv4PrefixLength
	{ID: 13, Length: 1}, // dst_mask → destinationIPv4PrefixLength
	{ID: 34, Length: 2}, // the header's sampling interval → samplingInterval
	{ID: 35, Length: 1}, // the header's sampling mode → samplingAlgorithm
}}

// netflow5Offsets are where NetFlowV5Template's record fields start in a record; the
// last two come from the header.
var netflow5Offsets = []int{0, 4, 8, 12, 14, 16, 20, 24, 28, 32, 34, 37, 38, 39, 40, 42, 44, 45}

// DevTooManyRecords: a NetFlow v5 packet claims more than the 30 records it can hold.
const DevTooManyRecords Deviation = "too_many_records"

// netflow5MaxRecords is the most records a v5 packet holds.
const netflow5MaxRecords = 30

func (s *Session) decodeNetFlowV5(d *decodeState, msg []byte) (Header, error) {
	if len(msg) < NetFlowV5HeaderLength {
		d.deviate(DevShortMessage, "%d bytes", len(msg))
		return Header{}, fmt.Errorf("%w: %d bytes is shorter than the NetFlow v5 header", ErrNotIPFIX, len(msg))
	}
	c := newCursor(msg)
	d.hdr.Version, _ = c.u16()
	d.hdr.Count, _ = c.u16()
	uptime, _ := c.u32()
	secs, _ := c.u32()
	nsecs, _ := c.u32()
	d.hdr.Sequence, _ = c.u32()
	engineType, _ := c.u8()
	engineID, _ := c.u8()
	sampling, _ := c.u16()
	// The engine identifies the flow-switching engine, as a Source ID does in v9.
	d.hdr.ObservationDomain = uint32(engineType)<<8 | uint32(engineID)
	d.hdr.Length = uint16(min(len(msg), 0xFFFF))
	d.hdr.SysUptime = time.Duration(uptime) * time.Millisecond
	d.hdr.ExportTime = time.Unix(int64(secs), int64(nsecs)).UTC()

	count := int(d.hdr.Count)
	if count > netflow5MaxRecords {
		d.deviate(DevTooManyRecords, "header says %d records, at most %d fit", count, netflow5MaxRecords)
	}
	body := msg[NetFlowV5HeaderLength:]
	switch whole := len(body) / NetFlowV5RecordLength; {
	case count > whole:
		d.deviate(DevTruncatedMessage, "header says %d records, %d bytes hold %d", count, len(body), whole)
		count = whole
	case len(body) > count*NetFlowV5RecordLength:
		d.deviate(DevTrailingBytes, "%d bytes after %d records", len(body)-count*NetFlowV5RecordLength, count)
	}

	s.expire(d)
	dom := s.uptimeClock(d, uptime, d.hdr.ExportTime)
	if dom.tracked && dom.seqValid && d.hdr.Sequence != dom.seq+dom.records {
		// v5 sequence numbers count flows: the next packet starts where this one ends.
		d.deviate(DevSequenceGap, "expected sequence %d, got %d (engine %d/%d)", dom.seq+dom.records, d.hdr.Sequence, engineType, engineID)
	}
	// The fixed Template is stored like a sent one, so the Exporters page shows it.
	t := NetFlowV5Template
	s.storeTemplate(d, &t)

	// One copy for the packet; every record's fields point into it.
	data := make([]byte, count*NetFlowV5RecordLength)
	copy(data, body)
	header := []byte{byte(sampling>>8) & 0x3F, byte(sampling), byte(sampling >> 14)}
	for i := range count {
		rec := data[i*NetFlowV5RecordLength : (i+1)*NetFlowV5RecordLength]
		r := Record{
			ObservationDomain: d.hdr.ObservationDomain,
			TemplateID:        t.ID,
			ExportTime:        d.hdr.ExportTime,
			SystemInitTime:    dom.boot,
			Fields:            make([]Field, len(t.Fields)),
		}
		for j, off := range netflow5Offsets {
			spec := t.Fields[j]
			r.Fields[j] = Field{Spec: spec, Raw: rec[off : off+int(spec.Length)]}
		}
		n := len(netflow5Offsets)
		r.Fields[n] = Field{Spec: t.Fields[n], Raw: header[0:2]}
		r.Fields[n+1] = Field{Spec: t.Fields[n+1], Raw: header[2:3]}
		d.records = append(d.records, r)
	}
	if dom.tracked {
		dom.seq, dom.records, dom.seqValid = d.hdr.Sequence, uint32(count), true
	}
	if len(d.records) > 0 {
		d.h.Records(d.records)
	}
	return d.hdr, nil
}
