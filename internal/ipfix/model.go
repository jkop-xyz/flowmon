// Package ipfix decodes IPFIX messages (RFC 7011) into a lossless record model.
//
// Decoding follows ADR 0002: build to the standard, decode leniently and report
// strictly. Anything the standard lets an exporter choose (reduced-size integers,
// which timestamp elements it sends, enterprise-specific elements) is handled
// generically. Data is rejected only when decoding it would be unsafe, and every
// deviation from the standard is reported to the caller by kind.
package ipfix

import (
	"fmt"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix/iana"
)

// Version is the IPFIX message version number.
const Version = 10

// VariableLength is the field length that marks a variable-length field (RFC 7011 §7).
const VariableLength = 0xFFFF

// Set IDs with a fixed meaning (RFC 7011 §3.3.2).
const (
	SetIDTemplate        = 2
	SetIDOptionsTemplate = 3
	// MinDataSetID is the lowest set ID that refers to a Template.
	MinDataSetID = 256
)

// FieldSpec identifies one field in a Template: an Information Element, optionally
// enterprise-specific, and the length the exporter sends it in.
type FieldSpec struct {
	// Enterprise is the Private Enterprise Number, or 0 for IANA elements.
	Enterprise uint32
	// ID is the Information Element ID without the enterprise bit.
	ID uint16
	// Length is the encoded length in bytes, or VariableLength.
	Length uint16
}

// Variable reports whether the field is variable-length.
func (s FieldSpec) Variable() bool { return s.Length == VariableLength }

// Element returns the IANA definition of an IANA field. Enterprise-specific fields have none.
func (s FieldSpec) Element() (iana.Element, bool) {
	if s.Enterprise != 0 {
		return iana.Element{}, false
	}
	return iana.Lookup(s.ID)
}

// Name is the IANA name, or a stable placeholder for unknown and enterprise fields.
func (s FieldSpec) Name() string {
	if e, ok := s.Element(); ok {
		return e.Name
	}
	if s.Enterprise != 0 {
		return fmt.Sprintf("enterprise%d.ie%d", s.Enterprise, s.ID)
	}
	return fmt.Sprintf("ie%d", s.ID)
}

// Template describes the layout of the data records an exporter sends for one Template ID.
type Template struct {
	ID uint16
	// ScopeFieldCount is non-zero for Options Templates: the first ScopeFieldCount fields are scope fields.
	ScopeFieldCount uint16
	Fields          []FieldSpec
}

// Options reports whether this is an Options Template.
func (t *Template) Options() bool { return t.ScopeFieldCount > 0 }

// MinRecordLength is the smallest number of bytes one data record can occupy:
// fixed fields at their length, variable fields at one length byte.
func (t *Template) MinRecordLength() int {
	n := 0
	for _, f := range t.Fields {
		if f.Variable() {
			n++
		} else {
			n += int(f.Length)
		}
	}
	return n
}

func (t *Template) equal(o *Template) bool {
	if t.ID != o.ID || t.ScopeFieldCount != o.ScopeFieldCount || len(t.Fields) != len(o.Fields) {
		return false
	}
	for i := range t.Fields {
		if t.Fields[i] != o.Fields[i] {
			return false
		}
	}
	return true
}

// Field is one field of a data record: its spec and the bytes exactly as received.
type Field struct {
	Spec FieldSpec
	Raw  []byte
}

// Record is one data record. Records own their bytes; they stay valid after the
// datagram buffer they were decoded from is reused.
type Record struct {
	ObservationDomain uint32
	TemplateID        uint16
	// ScopeFieldCount is non-zero for records described by an Options Template.
	ScopeFieldCount uint16
	// ExportTime is the export time from the message header.
	ExportTime time.Time
	Fields     []Field
}

// Options reports whether the record was described by an Options Template.
func (r *Record) Options() bool { return r.ScopeFieldCount > 0 }

// Get returns the first IANA field with the given element ID.
func (r *Record) Get(id uint16) (Field, bool) {
	for _, f := range r.Fields {
		if f.Spec.Enterprise == 0 && f.Spec.ID == id {
			return f, true
		}
	}
	return Field{}, false
}

// Header is the IPFIX message header (RFC 7011 §3.1).
type Header struct {
	Version           uint16
	Length            uint16
	ExportTime        time.Time
	Sequence          uint32
	ObservationDomain uint32
}

// HeaderLength is the size of the message header.
const HeaderLength = 16
