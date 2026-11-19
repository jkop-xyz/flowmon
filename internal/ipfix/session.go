package ipfix

import (
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix/iana"
)

// Deviation names a way a message departed from the standard, or data that had to be
// dropped. The string values are stable: they are shown in the UI and used as metric labels.
type Deviation string

// Deviations reported by Session.Decode.
const (
	DevShortMessage       Deviation = "short_message"
	DevWrongVersion       Deviation = "wrong_version"
	DevBadMessageLength   Deviation = "bad_message_length"
	DevTruncatedMessage   Deviation = "truncated_message"
	DevTrailingBytes      Deviation = "trailing_bytes"
	DevTruncatedSet       Deviation = "truncated_set"
	DevBadSetLength       Deviation = "bad_set_length"
	DevReservedSetID      Deviation = "reserved_set_id"
	DevWithdrawalIgnored  Deviation = "template_withdrawal_ignored"
	DevInvalidTemplateID  Deviation = "invalid_template_id"
	DevInvalidScopeCount  Deviation = "invalid_scope_count"
	DevTemplateTooLarge   Deviation = "template_too_large"
	DevEmptyTemplate      Deviation = "empty_template"
	DevTruncatedTemplate  Deviation = "truncated_template"
	DevTooManyTemplates   Deviation = "too_many_templates"
	DevFieldLengthInvalid Deviation = "field_length_invalid"
	DevUnknownTemplate    Deviation = "unknown_template"
	DevTruncatedRecord    Deviation = "truncated_record"
	DevNonZeroPadding     Deviation = "nonzero_padding"
	DevSequenceGap        Deviation = "sequence_gap"
	DevExporterRestarted  Deviation = "exporter_restarted"
)

// Transport is the transport a Session receives messages over. It decides the
// Template lifecycle rules (RFC 7011 §8).
type Transport uint8

// Transports.
const (
	TransportUDP Transport = iota
)

// Limits bound what one Session will keep, so hostile or broken exporters can't
// exhaust memory.
type Limits struct {
	// MaxTemplates is the most Templates kept per Session, across Observation Domains.
	MaxTemplates int
	// MaxFieldsPerTemplate rejects Templates with more fields than this.
	MaxFieldsPerTemplate int
	// TemplateTimeout expires UDP Templates that haven't been resent for this long.
	TemplateTimeout time.Duration
	// PendingMaxBytes bounds data sets buffered while waiting for their Template.
	PendingMaxBytes int
	// PendingMaxAge is how long a buffered data set waits for its Template.
	PendingMaxAge time.Duration
}

// DefaultLimits are generous for any real exporter and small enough to be safe.
func DefaultLimits() Limits {
	return Limits{
		MaxTemplates:         1024,
		MaxFieldsPerTemplate: 512,
		TemplateTimeout:      60 * time.Minute,
		PendingMaxBytes:      256 << 10,
		PendingMaxAge:        10 * time.Second,
	}
}

// TemplateEvent says what happened to a Template.
type TemplateEvent uint8

// Template events.
const (
	TemplateNew TemplateEvent = iota
	TemplateRefreshed
	TemplateRedefined
)

// Handler receives what a Session decodes. Records passed to Records stay valid
// after Decode returns.
type Handler interface {
	Template(observationDomain uint32, t *Template, ev TemplateEvent)
	Records(records []Record)
	Deviation(d Deviation, detail string)
}

// TemplateInfo is a Template a Session currently holds.
type TemplateInfo struct {
	ObservationDomain uint32
	Template          *Template
	FirstSeen         time.Time
	LastSeen          time.Time
}

type templateKey struct {
	od uint32
	id uint16
}

type templateEntry struct {
	t         *Template
	firstSeen time.Time
	lastSeen  time.Time
}

type pendingSet struct {
	key     templateKey
	data    []byte
	base    recordBase
	arrived time.Time
}

// recordBase is what records take from the message they arrived in.
type recordBase struct {
	exportTime time.Time
	sysInit    time.Time
}

type sequenceState struct {
	next  uint32
	valid bool
}

// Session decodes the messages of one transport session: for UDP, one exporter
// address and port sending to one collector socket. Templates are scoped to the
// Session and the Observation Domain. A Session is not safe for concurrent use.
type Session struct {
	transport    Transport
	limits       Limits
	templates    map[templateKey]*templateEntry
	pending      []pendingSet
	pendingBytes int
	sequences    map[uint32]*sequenceState
	// uptimes tracks each NetFlow v9 or v5 domain's uptime, boot time and sequence.
	uptimes map[uint32]*uptimeDomain
}

// NewSession returns a Session with no Templates.
func NewSession(transport Transport, limits Limits) *Session {
	return &Session{
		transport: transport,
		limits:    limits,
		templates: map[templateKey]*templateEntry{},
		sequences: map[uint32]*sequenceState{},
		uptimes:   map[uint32]*uptimeDomain{},
	}
}

// ErrNotIPFIX is returned when a message can't be decoded as IPFIX or NetFlow v9 or v5
// at all.
var ErrNotIPFIX = errors.New("ipfix: not an IPFIX or NetFlow v9 or v5 message")

// decodeState collects the results of one Decode call.
type decodeState struct {
	h            Handler
	hdr          Header
	base         recordBase
	now          time.Time
	records      []Record
	recordCount  uint32
	countUnknown bool // a data set couldn't be counted, so the sequence check is skipped
}

func (d *decodeState) deviate(dev Deviation, format string, args ...any) {
	d.h.Deviation(dev, fmt.Sprintf(format, args...))
}

// Decode decodes one IPFIX message or NetFlow v9 or v5 packet received at now. It returns
// the header, or ErrNotIPFIX (wrapped) when the message can't be decoded at all.
// Everything else is lenient: problems are reported to h as deviations and decoding
// continues wherever that is safe.
func (s *Session) Decode(msg []byte, now time.Time, h Handler) (Header, error) {
	d := &decodeState{h: h, now: now}
	switch PeekVersion(msg) {
	case VersionNetFlowV9:
		return s.decodeNetFlowV9(d, msg)
	case VersionNetFlowV5:
		return s.decodeNetFlowV5(d, msg)
	}
	if len(msg) < HeaderLength {
		d.deviate(DevShortMessage, "%d bytes", len(msg))
		return Header{}, fmt.Errorf("%w: %d bytes is shorter than the header", ErrNotIPFIX, len(msg))
	}
	c := newCursor(msg)
	d.hdr.Version, _ = c.u16()
	d.hdr.Length, _ = c.u16()
	exportSecs, _ := c.u32()
	d.hdr.Sequence, _ = c.u32()
	d.hdr.ObservationDomain, _ = c.u32()
	d.hdr.ExportTime = time.Unix(int64(exportSecs), 0).UTC()
	d.base = recordBase{exportTime: d.hdr.ExportTime}

	if d.hdr.Version != Version {
		d.deviate(DevWrongVersion, "version %d", d.hdr.Version)
		return d.hdr, fmt.Errorf("%w: version %d", ErrNotIPFIX, d.hdr.Version)
	}
	if d.hdr.Length < HeaderLength {
		d.deviate(DevBadMessageLength, "header says %d bytes", d.hdr.Length)
		return d.hdr, fmt.Errorf("%w: message length %d", ErrNotIPFIX, d.hdr.Length)
	}
	body := msg[HeaderLength:]
	switch {
	case int(d.hdr.Length) > len(msg):
		d.deviate(DevTruncatedMessage, "header says %d bytes, received %d", d.hdr.Length, len(msg))
	case int(d.hdr.Length) < len(msg):
		d.deviate(DevTrailingBytes, "%d bytes after the message", len(msg)-int(d.hdr.Length))
		body = msg[HeaderLength:d.hdr.Length]
	}

	s.expire(d)
	s.checkSequence(d)

	sets := newCursor(body)
	for sets.remaining() > 0 {
		if sets.remaining() < 4 {
			d.deviate(DevTruncatedSet, "%d bytes left, too few for a set header", sets.remaining())
			d.countUnknown = true
			break
		}
		setID, _ := sets.u16()
		setLen, _ := sets.u16()
		if setLen < 4 {
			d.deviate(DevBadSetLength, "set %d has length %d", setID, setLen)
			d.countUnknown = true
			break
		}
		content, err := sets.bytes(int(setLen) - 4)
		if err != nil {
			d.deviate(DevTruncatedSet, "set %d says %d bytes, %d left", setID, setLen, sets.remaining()+4)
			d.countUnknown = true
			break
		}
		switch {
		case setID == SetIDTemplate:
			s.decodeTemplateSet(d, content, false)
		case setID == SetIDOptionsTemplate:
			s.decodeTemplateSet(d, content, true)
		case setID < MinDataSetID:
			d.deviate(DevReservedSetID, "set ID %d", setID)
		default:
			s.decodeOrBufferDataSet(d, templateKey{od: d.hdr.ObservationDomain, id: setID}, content)
		}
	}

	s.finishSequence(d)
	if len(d.records) > 0 {
		h.Records(d.records)
	}
	return d.hdr, nil
}

// expire drops Templates and buffered data sets that are too old.
func (s *Session) expire(d *decodeState) {
	if s.transport == TransportUDP && s.limits.TemplateTimeout > 0 {
		for k, e := range s.templates {
			if d.now.Sub(e.lastSeen) > s.limits.TemplateTimeout {
				delete(s.templates, k)
			}
		}
	}
	kept := s.pending[:0]
	for _, p := range s.pending {
		if d.now.Sub(p.arrived) > s.limits.PendingMaxAge {
			s.pendingBytes -= len(p.data)
			d.deviate(DevUnknownTemplate, "data set for Template %d (domain %d) dropped: its Template never arrived", p.key.id, p.key.od)
			continue
		}
		kept = append(kept, p)
	}
	s.pending = kept
}

// checkSequence compares the header's sequence number with the one expected from the
// previous message. IPFIX sequence numbers count data records (RFC 7011 §3.1).
func (s *Session) checkSequence(d *decodeState) {
	st := s.sequences[d.hdr.ObservationDomain]
	if st != nil && st.valid && st.next != d.hdr.Sequence {
		d.deviate(DevSequenceGap, "expected sequence %d, got %d (domain %d)", st.next, d.hdr.Sequence, d.hdr.ObservationDomain)
	}
}

func (s *Session) finishSequence(d *decodeState) {
	st := s.sequences[d.hdr.ObservationDomain]
	if st == nil {
		if len(s.sequences) >= s.limits.MaxTemplates {
			return // bounded like Templates: an exporter can't create unlimited domains
		}
		st = &sequenceState{}
		s.sequences[d.hdr.ObservationDomain] = st
	}
	st.next = d.hdr.Sequence + d.recordCount
	st.valid = !d.countUnknown
}

func (s *Session) decodeTemplateSet(d *decodeState, content []byte, options bool) {
	c := newCursor(content)
	for c.remaining() > 0 {
		if c.remaining() < 4 {
			// Too short for any template record: set padding, which must be zero.
			if !allZero(c.rest()) {
				d.deviate(DevNonZeroPadding, "template set padding is not zero")
			}
			return
		}
		id, _ := c.u16()
		count, _ := c.u16()

		if count == 0 {
			// A Template Withdrawal (id 2/3 withdraws all). Over UDP it MUST be ignored (RFC 7011 §8.4).
			d.deviate(DevWithdrawalIgnored, "withdrawal of Template %d over UDP", id)
			continue
		}

		var scope uint16
		if options {
			var err error
			if scope, err = c.u16(); err != nil {
				d.deviate(DevTruncatedTemplate, "options Template %d: no scope field count", id)
				return
			}
		}

		// Each field specifier is at least 4 bytes, so a count that can't fit is truncated
		// data; checking before allocating keeps a forged count from allocating memory.
		if int(count)*4 > c.remaining() {
			d.deviate(DevTruncatedTemplate, "Template %d declares %d fields, only %d bytes left", id, count, c.remaining())
			return
		}
		tooLarge := int(count) > s.limits.MaxFieldsPerTemplate
		var fields []FieldSpec
		if !tooLarge {
			fields = make([]FieldSpec, 0, count)
		}
		for i := 0; i < int(count); i++ {
			spec, err := readFieldSpec(c)
			if err != nil {
				d.deviate(DevTruncatedTemplate, "Template %d: field %d of %d is cut off", id, i+1, count)
				return
			}
			if !tooLarge {
				fields = append(fields, spec)
			}
		}

		switch {
		case id < MinDataSetID:
			d.deviate(DevInvalidTemplateID, "Template ID %d is below %d", id, MinDataSetID)
			continue
		case tooLarge:
			d.deviate(DevTemplateTooLarge, "Template %d has %d fields, limit is %d", id, count, s.limits.MaxFieldsPerTemplate)
			continue
		case options && (scope == 0 || scope > count):
			d.deviate(DevInvalidScopeCount, "options Template %d: scope field count %d of %d fields", id, scope, count)
			continue
		}

		s.keepTemplate(d, &Template{Version: Version, ID: id, ScopeFieldCount: scope, Fields: fields})
	}
}

// keepTemplate stores a decoded Template unless it describes zero-length records, and
// reports fields whose length their type doesn't allow.
func (s *Session) keepTemplate(d *decodeState, t *Template) {
	if t.MinRecordLength() == 0 {
		d.deviate(DevEmptyTemplate, "Template %d describes zero-length records", t.ID)
		return
	}
	for _, f := range t.Fields {
		if f.Variable() {
			continue
		}
		if e, ok := f.Element(); ok && !validFixedLength(e.Type, int(f.Length)) {
			d.deviate(DevFieldLengthInvalid, "Template %d: %s is %d bytes, which %s doesn't allow", t.ID, e.Name, f.Length, e.Type)
		}
	}
	s.storeTemplate(d, t)
}

func readFieldSpec(c *cursor) (FieldSpec, error) {
	rawID, err := c.u16()
	if err != nil {
		return FieldSpec{}, err
	}
	length, err := c.u16()
	if err != nil {
		return FieldSpec{}, err
	}
	spec := FieldSpec{ID: rawID & 0x7FFF, Length: length}
	if rawID&0x8000 != 0 {
		if spec.Enterprise, err = c.u32(); err != nil {
			return FieldSpec{}, err
		}
	}
	return spec, nil
}

// validFixedLength reports whether a fixed field length fits the element's type,
// allowing reduced-size encoding.
func validFixedLength(t iana.DataType, n int) bool {
	native := t.NativeLength()
	if native == 0 {
		return true
	}
	if t == iana.Float64 {
		return n == 4 || n == 8
	}
	if t.AllowsReducedSize() {
		return n >= 1 && n <= native
	}
	return n == native
}

func (s *Session) storeTemplate(d *decodeState, t *Template) {
	key := templateKey{od: d.hdr.ObservationDomain, id: t.ID}
	if e, ok := s.templates[key]; ok {
		ev := TemplateRefreshed
		if !e.t.equal(t) {
			// Over UDP an exporter may redefine a Template ID, e.g. after a restart.
			ev = TemplateRedefined
			e.t = t
			e.firstSeen = d.now
		}
		e.lastSeen = d.now
		d.h.Template(key.od, e.t, ev)
	} else {
		if len(s.templates) >= s.limits.MaxTemplates {
			d.deviate(DevTooManyTemplates, "Template %d (domain %d) not kept: limit of %d reached", t.ID, key.od, s.limits.MaxTemplates)
			return
		}
		s.templates[key] = &templateEntry{t: t, firstSeen: d.now, lastSeen: d.now}
		d.h.Template(key.od, t, TemplateNew)
	}
	s.replayPending(d, key)
}

func (s *Session) decodeOrBufferDataSet(d *decodeState, key templateKey, content []byte) {
	if e, ok := s.templates[key]; ok {
		s.decodeDataSet(d, e.t, key.od, d.base, content)
		return
	}
	d.countUnknown = true
	if s.pendingBytes+len(content) > s.limits.PendingMaxBytes {
		d.deviate(DevUnknownTemplate, "data set for Template %d (domain %d) dropped: no Template and the wait buffer is full", key.id, key.od)
		return
	}
	data := make([]byte, len(content))
	copy(data, content)
	s.pending = append(s.pending, pendingSet{key: key, data: data, base: d.base, arrived: d.now})
	s.pendingBytes += len(data)
}

// replayPending decodes buffered data sets whose Template has now arrived.
func (s *Session) replayPending(d *decodeState, key templateKey) {
	kept := s.pending[:0]
	var ready []pendingSet
	for _, p := range s.pending {
		if p.key == key {
			ready = append(ready, p)
			s.pendingBytes -= len(p.data)
			continue
		}
		kept = append(kept, p)
	}
	s.pending = kept
	t := s.templates[key].t
	for _, p := range ready {
		// Replayed records belong to earlier messages, so they don't count toward this
		// message's sequence number.
		before := d.recordCount
		s.decodeDataSet(d, t, key.od, p.base, p.data)
		d.recordCount = before
	}
}

func (s *Session) decodeDataSet(d *decodeState, t *Template, od uint32, base recordBase, content []byte) {
	// One copy per set; every record's fields point into it, so records own their bytes.
	data := make([]byte, len(content))
	copy(data, content)

	minLen := t.MinRecordLength()
	c := newCursor(data)
	for c.remaining() > 0 {
		if c.remaining() < minLen {
			// Too short for a record: set padding (RFC 7011 §3.3.1), which must be zero.
			// Some exporters (RouterOS) pad with whatever was in the buffer; the records
			// before it decoded fine.
			if n := c.remaining(); !allZero(c.rest()) {
				d.deviate(DevNonZeroPadding, "Template %d: %d bytes of padding are not zero", t.ID, n)
			}
			return
		}
		rec := Record{
			ObservationDomain: od,
			TemplateID:        t.ID,
			ScopeFieldCount:   t.ScopeFieldCount,
			ExportTime:        base.exportTime,
			SystemInitTime:    base.sysInit,
			Fields:            make([]Field, len(t.Fields)),
		}
		for i, spec := range t.Fields {
			raw, err := readFieldValue(c, spec)
			if err != nil {
				d.deviate(DevTruncatedRecord, "Template %d: record cut off in %s", t.ID, spec.Name())
				return
			}
			rec.Fields[i] = Field{Spec: spec, Raw: raw}
		}
		d.records = append(d.records, rec)
		d.recordCount++
	}
}

func readFieldValue(c *cursor, spec FieldSpec) ([]byte, error) {
	if !spec.Variable() {
		return c.bytes(int(spec.Length))
	}
	n, err := c.u8()
	if err != nil {
		return nil, err
	}
	length := int(n)
	if n == 255 {
		l16, err := c.u16()
		if err != nil {
			return nil, err
		}
		length = int(l16)
	}
	return c.bytes(length)
}

func allZero(b []byte) bool {
	for _, x := range b {
		if x != 0 {
			return false
		}
	}
	return true
}

// Templates returns the Templates the Session currently holds, ordered by
// Observation Domain and Template ID.
func (s *Session) Templates() []TemplateInfo {
	out := make([]TemplateInfo, 0, len(s.templates))
	for k, e := range s.templates {
		out = append(out, TemplateInfo{ObservationDomain: k.od, Template: e.t, FirstSeen: e.firstSeen, LastSeen: e.lastSeen})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ObservationDomain != out[j].ObservationDomain {
			return out[i].ObservationDomain < out[j].ObservationDomain
		}
		return out[i].Template.ID < out[j].Template.ID
	})
	return out
}

// PeekVersion returns the version field of a flow export datagram (NetFlow v5/v9 and
// IPFIX all start with it), or 0 if the datagram is too short.
func PeekVersion(b []byte) uint16 {
	if len(b) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
