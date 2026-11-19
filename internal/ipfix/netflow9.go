package ipfix

import (
	"fmt"
	"time"
)

// NetFlow v9 (RFC 3954) is IPFIX's predecessor and shares its model: Templates, Options
// Templates and data records, with field types 1–127 the same as the IPFIX Information
// Elements that took over their numbers. It differs in the header (uptime as well as
// export time, no length), the FlowSet IDs, Options Templates that give their scope
// and option sizes in bytes, flow times in milliseconds of uptime, no variable-length
// fields and no enterprise numbers. Packets decode into the same Templates and Records
// as IPFIX messages, so everything after the decoder treats both alike.

// NetFlowV9HeaderLength is the size of the NetFlow v9 packet header.
const NetFlowV9HeaderLength = 20

// NetFlow v9 FlowSet IDs (RFC 3954 §5.2); 2–255 are reserved, 256 and up are data.
const (
	netflow9TemplateFlowSet = 0
	netflow9OptionsFlowSet  = 1
)

// netflow9Scopes maps NetFlow v9 scope field types (RFC 3954 §6.1) onto the IPFIX
// elements with the same meaning, so Options records read the same as IPFIX ones: an
// interface name scoped by Interface is scoped by ingressInterface.
var netflow9Scopes = map[uint16]uint16{
	1: 144, // System → exportingProcessId
	2: 10,  // Interface → ingressInterface
	3: 141, // Line Card → lineCardId
	4: 143, // Cache → meteringProcessId
	5: 145, // Template → templateId
}

// netflow9Spec turns a NetFlow v9 field type and length into a FieldSpec. Types of
// 32768 and up are vendor-specific.
func netflow9Spec(typ, length uint16) FieldSpec {
	if typ >= 0x8000 {
		return FieldSpec{Enterprise: EnterpriseNetFlowV9, ID: typ, Length: length}
	}
	return FieldSpec{ID: typ, Length: length}
}

func netflow9ScopeSpec(typ, length uint16) FieldSpec {
	if id, ok := netflow9Scopes[typ]; ok {
		return FieldSpec{ID: id, Length: length}
	}
	// An unknown scope type: kept, but never mistaken for an IANA element.
	return FieldSpec{Enterprise: EnterpriseNetFlowV9, ID: typ, Length: length}
}

// netflow9Domain is what a Session knows about one NetFlow v9 Source ID: the Exporter's
// clock, to convert uptime-relative flow times and notice restarts, and its sequence.
type netflow9Domain struct {
	uptime uint32    // sysUptime (ms) of the newest packet
	unix   uint32    // its export time (s)
	boot   time.Time // best estimate of when the Exporter booted

	seq      uint32
	records  uint32 // data records in the previous packet
	count    uint16 // records the previous packet's header claimed
	seqValid bool
}

const (
	// uptimeWrap is when the 32-bit millisecond uptime starts again from zero: 49.7 days.
	uptimeWrap = (1 << 32) * time.Millisecond
	// lateTolerance: a packet whose uptime is at most this far behind the newest one
	// arrived late (UDP doesn't keep order); further back, the Exporter restarted.
	lateTolerance = time.Minute
	// clockTolerance bounds how far a boot time estimate may move before it is taken as
	// the Exporter's clock being set, rather than the export time's one-second rounding.
	clockTolerance = 2 * time.Second
)

func (s *Session) decodeNetFlowV9(d *decodeState, msg []byte) (Header, error) {
	if len(msg) < NetFlowV9HeaderLength {
		d.deviate(DevShortMessage, "%d bytes", len(msg))
		return Header{}, fmt.Errorf("%w: %d bytes is shorter than the NetFlow v9 header", ErrNotIPFIX, len(msg))
	}
	c := newCursor(msg)
	d.hdr.Version, _ = c.u16()
	d.hdr.Count, _ = c.u16()
	uptime, _ := c.u32()
	secs, _ := c.u32()
	d.hdr.Sequence, _ = c.u32()
	d.hdr.ObservationDomain, _ = c.u32()
	d.hdr.Length = uint16(min(len(msg), 0xFFFF))
	d.hdr.SysUptime = time.Duration(uptime) * time.Millisecond
	d.hdr.ExportTime = time.Unix(int64(secs), 0).UTC()

	s.expire(d)
	dom := s.netflow9Clock(d, uptime, secs)
	d.base = recordBase{exportTime: d.hdr.ExportTime, sysInit: dom.boot}
	s.checkNetFlow9Sequence(d, dom)

	sets := newCursor(msg[NetFlowV9HeaderLength:])
	for sets.remaining() > 0 {
		if sets.remaining() < 4 {
			// Some exporters pad the packet; anything else is a cut-off FlowSet.
			if !allZero(sets.rest()) {
				d.deviate(DevTruncatedSet, "%d bytes left, too few for a FlowSet header", sets.remaining())
				d.countUnknown = true
			}
			break
		}
		id, _ := sets.u16()
		length, _ := sets.u16()
		if length < 4 {
			d.deviate(DevBadSetLength, "FlowSet %d has length %d", id, length)
			d.countUnknown = true
			break
		}
		content, err := sets.bytes(int(length) - 4)
		if err != nil {
			d.deviate(DevTruncatedSet, "FlowSet %d says %d bytes, %d left", id, length, sets.remaining()+4)
			d.countUnknown = true
			break
		}
		switch {
		case id == netflow9TemplateFlowSet:
			s.decodeNetFlow9Templates(d, content)
		case id == netflow9OptionsFlowSet:
			s.decodeNetFlow9OptionsTemplates(d, content)
		case id < MinDataSetID:
			d.deviate(DevReservedSetID, "FlowSet ID %d", id)
		default:
			s.decodeOrBufferDataSet(d, templateKey{od: d.hdr.ObservationDomain, id: id}, content)
		}
	}

	if dom.tracked {
		dom.seq, dom.records, dom.count, dom.seqValid = d.hdr.Sequence, d.recordCount, d.hdr.Count, !d.countUnknown
	}
	if len(d.records) > 0 {
		d.h.Records(d.records)
	}
	return d.hdr, nil
}

// trackedDomain is a Source ID's state, and whether the Session keeps it (it holds a
// bounded number of them).
type trackedDomain struct {
	*netflow9Domain
	tracked bool
}

// netflow9Clock follows the Exporter's uptime: it estimates the boot time that flow
// times count from, and notices when the uptime starts again from zero, either because
// the 32-bit counter wrapped or because the Exporter restarted. A restarted Exporter
// numbers its Templates afresh, so its old ones are dropped.
func (s *Session) netflow9Clock(d *decodeState, uptime, secs uint32) trackedDomain {
	od := d.hdr.ObservationDomain
	// The export time is whole seconds, so this is up to a second early; the newest
	// (latest) estimate is the best.
	est := time.Unix(int64(secs), 0).UTC().Add(-time.Duration(uptime) * time.Millisecond)
	dom := s.netflow9[od]
	if dom == nil {
		dom = &netflow9Domain{uptime: uptime, unix: secs, boot: est}
		if len(s.netflow9) >= s.limits.MaxTemplates {
			return trackedDomain{dom, false} // bounded like Templates
		}
		s.netflow9[od] = dom
		return trackedDomain{dom, true}
	}

	switch {
	case uptime >= dom.uptime:
	case time.Duration(dom.uptime-uptime)*time.Millisecond <= lateTolerance:
		return trackedDomain{dom, true} // a late packet; the newest one sets the clock
	case dom.wrapped(uptime, secs):
		dom.boot = dom.boot.Add(uptimeWrap)
	default:
		d.deviate(DevExporterRestarted, "uptime went from %s to %s (Source ID %d): the Exporter restarted, so its Templates were dropped until it sends them again",
			(time.Duration(dom.uptime) * time.Millisecond).Round(time.Second), (time.Duration(uptime) * time.Millisecond).Round(time.Second), od)
		s.forgetDomain(od)
		dom = &netflow9Domain{uptime: uptime, unix: secs, boot: est}
		s.netflow9[od] = dom
		return trackedDomain{dom, true}
	}
	dom.uptime, dom.unix = uptime, secs
	if diff := est.Sub(dom.boot); diff > 0 || diff < -clockTolerance {
		// A later estimate is a better one; one much earlier means the clock was set back.
		dom.boot = est
	}
	return trackedDomain{dom, true}
}

// wrapped reports whether a smaller uptime is the 32-bit counter wrapping: the uptime
// that passed, counted modulo 2³², matches the time that passed by the export clock.
func (dom *netflow9Domain) wrapped(uptime, secs uint32) bool {
	if secs < dom.unix {
		return false
	}
	byUptime := time.Duration(uptime-dom.uptime) * time.Millisecond // wraps like the counter
	byClock := time.Duration(secs-dom.unix) * time.Second
	diff := byUptime - byClock
	return diff < 5*time.Second && diff > -5*time.Second
}

// forgetDomain drops what a Session holds for an Observation Domain (Source ID).
func (s *Session) forgetDomain(od uint32) {
	for k := range s.templates {
		if k.od == od {
			delete(s.templates, k)
		}
	}
	kept := s.pending[:0]
	for _, p := range s.pending {
		if p.key.od == od {
			s.pendingBytes -= len(p.data)
			continue
		}
		kept = append(kept, p)
	}
	s.pending = kept
	delete(s.netflow9, od)
}

// checkNetFlow9Sequence compares the packet's sequence number with the previous one.
// RFC 3954 counts export packets, but older exporters count data records, so both
// steps are accepted, as is the record count the previous header gave.
func (s *Session) checkNetFlow9Sequence(d *decodeState, dom trackedDomain) {
	if !dom.tracked || !dom.seqValid {
		return
	}
	step := d.hdr.Sequence - dom.seq
	if step != 1 && step != dom.records && step != uint32(dom.count) {
		d.deviate(DevSequenceGap, "expected sequence %d, got %d (Source ID %d)", dom.seq+1, d.hdr.Sequence, d.hdr.ObservationDomain)
	}
}

func (s *Session) decodeNetFlow9Templates(d *decodeState, content []byte) {
	c := newCursor(content)
	for c.remaining() >= 4 {
		id, _ := c.u16()
		count, _ := c.u16()
		if int(count)*4 > c.remaining() {
			d.deviate(DevTruncatedTemplate, "Template %d declares %d fields, only %d bytes left", id, count, c.remaining())
			return
		}
		fields, ok := s.readNetFlow9Specs(d, c, id, int(count), netflow9Spec)
		switch {
		case id < MinDataSetID:
			d.deviate(DevInvalidTemplateID, "Template ID %d is below %d", id, MinDataSetID)
		case count == 0:
			d.deviate(DevEmptyTemplate, "Template %d has no fields", id)
		case ok:
			s.keepTemplate(d, &Template{Version: VersionNetFlowV9, ID: id, Fields: fields})
		}
	}
	if !allZero(c.rest()) {
		d.deviate(DevNonZeroPadding, "template FlowSet padding is not zero")
	}
}

func (s *Session) decodeNetFlow9OptionsTemplates(d *decodeState, content []byte) {
	c := newCursor(content)
	for c.remaining() >= 6 {
		id, _ := c.u16()
		scopeLen, _ := c.u16()
		optionLen, _ := c.u16()
		if scopeLen%4 != 0 || optionLen%4 != 0 {
			// Each field takes 4 bytes; without whole fields the rest can't be found.
			d.deviate(DevInvalidScopeCount, "options Template %d: scope length %d and option length %d aren't whole fields", id, scopeLen, optionLen)
			return
		}
		if int(scopeLen)+int(optionLen) > c.remaining() {
			d.deviate(DevTruncatedTemplate, "options Template %d declares %d bytes of fields, only %d bytes left", id, int(scopeLen)+int(optionLen), c.remaining())
			return
		}
		scope, okScope := s.readNetFlow9Specs(d, c, id, int(scopeLen)/4, netflow9ScopeSpec)
		options, okOptions := s.readNetFlow9Specs(d, c, id, int(optionLen)/4, netflow9Spec)
		switch {
		case id < MinDataSetID:
			d.deviate(DevInvalidTemplateID, "Template ID %d is below %d", id, MinDataSetID)
		case scopeLen == 0:
			d.deviate(DevInvalidScopeCount, "options Template %d has no scope", id)
		case okScope && okOptions:
			if len(scope)+len(options) > s.limits.MaxFieldsPerTemplate {
				d.deviate(DevTemplateTooLarge, "Template %d has %d fields, limit is %d", id, len(scope)+len(options), s.limits.MaxFieldsPerTemplate)
				continue
			}
			s.keepTemplate(d, &Template{Version: VersionNetFlowV9, ID: id, ScopeFieldCount: uint16(len(scope)), Fields: append(scope, options...)})
		}
	}
	if !allZero(c.rest()) {
		d.deviate(DevNonZeroPadding, "options template FlowSet padding is not zero")
	}
}

// readNetFlow9Specs reads n field specifiers, which the caller has checked are there.
// ok is false when the Template can't be used: too many fields, or a length of 65535,
// which means variable-length in IPFIX but has no such meaning in NetFlow v9.
func (s *Session) readNetFlow9Specs(d *decodeState, c *cursor, id uint16, n int, spec func(typ, length uint16) FieldSpec) ([]FieldSpec, bool) {
	ok := n <= s.limits.MaxFieldsPerTemplate
	if !ok {
		d.deviate(DevTemplateTooLarge, "Template %d has %d fields, limit is %d", id, n, s.limits.MaxFieldsPerTemplate)
	}
	var fields []FieldSpec
	if ok {
		fields = make([]FieldSpec, 0, n)
	}
	for range n {
		typ, _ := c.u16()
		length, _ := c.u16()
		if length == VariableLength && ok {
			d.deviate(DevFieldLengthInvalid, "Template %d: field type %d is %d bytes long, which NetFlow v9 doesn't allow", id, typ, length)
			ok = false
		}
		if ok {
			fields = append(fields, spec(typ, length))
		}
	}
	return fields, ok
}
