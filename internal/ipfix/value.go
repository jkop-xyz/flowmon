package ipfix

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"net"
	"net/netip"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jkop-xyz/flowmon/internal/ipfix/iana"
)

// Kind is the shape of a decoded Value.
type Kind uint8

// Value kinds.
const (
	KindOctets Kind = iota // raw bytes: octetArray, unknown and enterprise fields, unsigned256
	KindUnsigned
	KindSigned
	KindFloat
	KindBool
	KindMAC
	KindAddr
	KindString
	KindTime
	// KindList holds RFC 6313 structured data. The model is ready for it; decoding
	// the list contents is not implemented yet, so List fields currently decode as
	// KindOctets and ErrListsNotDecoded.
	KindList
)

// Value is a typed view of a Field. Only the member matching Kind is set.
type Value struct {
	Kind   Kind
	Uint   uint64
	Int    int64
	Float  float64
	Bool   bool
	MAC    net.HardwareAddr
	Addr   netip.Addr
	String string
	Time   time.Time
	Octets []byte
	List   *List
}

// List is RFC 6313 structured data: a basicList of values, or a subTemplateList or
// subTemplateMultiList of records.
type List struct {
	// Semantic is the list semantic (RFC 6313 §4.5.6), e.g. 0xFF undefined, 3 allOf.
	Semantic uint8
	// Element is set for a basicList: the spec every element shares.
	Element *FieldSpec
	Values  []Value
	Records []Record
}

// ErrListsNotDecoded is returned for RFC 6313 list fields until list decoding exists.
// The raw bytes are still returned as KindOctets.
var ErrListsNotDecoded = errors.New("ipfix: RFC 6313 list decoding not implemented")

// ntpEpochOffset is the number of seconds between 1900-01-01 (NTP) and 1970-01-01 (Unix).
const ntpEpochOffset = 2208988800

// Value decodes the field according to its IANA abstract data type. Enterprise and
// unknown fields decode as KindOctets. When the bytes don't fit the type (for
// example a 3-byte IPv4 address) the raw bytes are returned with an error, so the
// caller can report a deviation without losing the data.
func (f Field) Value() (Value, error) {
	raw := Value{Kind: KindOctets, Octets: f.Raw}
	elem, ok := f.Spec.Element()
	if !ok {
		return raw, nil
	}
	v, err := decodeTyped(elem.Type, f.Raw)
	if err != nil {
		return raw, fmt.Errorf("%s (%s, %d bytes): %w", elem.Name, elem.Type, len(f.Raw), err)
	}
	return v, nil
}

var errLength = errors.New("invalid length for type")

func decodeTyped(t iana.DataType, b []byte) (Value, error) {
	n := len(b)
	native := t.NativeLength()
	if native != 0 {
		if n > native || n == 0 || (n < native && !t.AllowsReducedSize()) {
			return Value{}, errLength
		}
	}

	switch t {
	case iana.Unsigned8, iana.Unsigned16, iana.Unsigned32, iana.Unsigned64:
		return Value{Kind: KindUnsigned, Uint: beUint(b)}, nil
	case iana.Signed8, iana.Signed16, iana.Signed32, iana.Signed64:
		// Reduced-size signed values are sign-extended from their top bit.
		u := beUint(b)
		shift := 64 - 8*uint(n)
		return Value{Kind: KindSigned, Int: int64(u<<shift) >> shift}, nil
	case iana.Float32:
		return Value{Kind: KindFloat, Float: float64(math.Float32frombits(binary.BigEndian.Uint32(b)))}, nil
	case iana.Float64:
		if n == 4 { // float64 may be sent as float32 (reduced size)
			return Value{Kind: KindFloat, Float: float64(math.Float32frombits(binary.BigEndian.Uint32(b)))}, nil
		}
		if n != 8 {
			return Value{}, errLength
		}
		return Value{Kind: KindFloat, Float: math.Float64frombits(binary.BigEndian.Uint64(b))}, nil
	case iana.Boolean:
		switch b[0] {
		case 1:
			return Value{Kind: KindBool, Bool: true}, nil
		case 2:
			return Value{Kind: KindBool, Bool: false}, nil
		}
		return Value{}, fmt.Errorf("boolean must be 1 or 2, got %d", b[0])
	case iana.MACAddress:
		return Value{Kind: KindMAC, MAC: net.HardwareAddr(b)}, nil
	case iana.IPv4Address:
		return Value{Kind: KindAddr, Addr: netip.AddrFrom4([4]byte(b))}, nil
	case iana.IPv6Address:
		return Value{Kind: KindAddr, Addr: netip.AddrFrom16([16]byte(b))}, nil
	case iana.String:
		if !utf8.Valid(b) {
			return Value{}, errors.New("string is not valid UTF-8")
		}
		// A fixed-length string field is padded with zero bytes, as NetFlow v9 exporters
		// send interface names; the padding isn't part of the string.
		return Value{Kind: KindString, String: strings.TrimRight(string(b), "\x00")}, nil
	case iana.DateTimeSeconds:
		return Value{Kind: KindTime, Time: time.Unix(int64(binary.BigEndian.Uint32(b)), 0).UTC()}, nil
	case iana.DateTimeMilliseconds:
		return Value{Kind: KindTime, Time: time.UnixMilli(int64(binary.BigEndian.Uint64(b))).UTC()}, nil
	case iana.DateTimeMicroseconds, iana.DateTimeNanoseconds:
		return Value{Kind: KindTime, Time: ntpTime(binary.BigEndian.Uint64(b), t == iana.DateTimeMicroseconds)}, nil
	case iana.BasicList, iana.SubTemplateList, iana.SubTemplateMultiList:
		return Value{}, ErrListsNotDecoded
	}
	// octetArray, unsigned256 and anything without a richer representation.
	return Value{Kind: KindOctets, Octets: b}, nil
}

func beUint(b []byte) uint64 {
	var u uint64
	for _, c := range b {
		u = u<<8 | uint64(c)
	}
	return u
}

// ntpTime converts an NTP-format timestamp (RFC 7011 §6.1.9/§6.1.10). For
// dateTimeMicroseconds the low 11 bits of the fraction are ignored.
func ntpTime(v uint64, micro bool) time.Time {
	secs := int64(v >> 32)
	frac := v & 0xFFFFFFFF
	if micro {
		frac &^= 0x7FF
	}
	nanos := int64((frac * 1_000_000_000) >> 32)
	return time.Unix(secs-ntpEpochOffset, nanos).UTC()
}
