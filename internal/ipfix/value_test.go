package ipfix

import (
	"bytes"
	"errors"
	"math"
	"net/netip"
	"testing"
	"time"
)

func field(id uint16, raw ...byte) Field {
	return Field{Spec: FieldSpec{ID: id, Length: uint16(len(raw))}, Raw: raw}
}

func TestUnsignedReducedSize(t *testing.T) {
	for _, raw := range [][]byte{{0x05, 0xDC}, {0, 0, 0x05, 0xDC}, {0, 0, 0, 0, 0, 0, 0x05, 0xDC}} {
		v, err := field(1, raw...).Value() // octetDeltaCount, unsigned64
		if err != nil || v.Kind != KindUnsigned || v.Uint != 1500 {
			t.Errorf("%x: %+v %v", raw, v, err)
		}
	}
}

func TestSignedSignExtension(t *testing.T) {
	// mibObjectValueInteger (434) is signed32; 0xFF sent as one byte must be -1.
	v, err := field(434, 0xFF).Value()
	if err != nil || v.Kind != KindSigned || v.Int != -1 {
		t.Errorf("%+v %v", v, err)
	}
	v, err = field(434, 0x00, 0x7F).Value()
	if err != nil || v.Int != 127 {
		t.Errorf("%+v %v", v, err)
	}
}

func TestFloat64AsFloat32(t *testing.T) {
	// samplingProbability (311) is float64; reduced size sends it as float32.
	raw := []byte{0x3E, 0x80, 0x00, 0x00} // 0.25
	v, err := field(311, raw...).Value()
	if err != nil || v.Kind != KindFloat || v.Float != 0.25 {
		t.Errorf("%+v %v", v, err)
	}
	b := make([]byte, 8)
	bits := math.Float64bits(0.125)
	for i := range b {
		b[i] = byte(bits >> (56 - 8*i))
	}
	if v, err := field(311, b...).Value(); err != nil || v.Float != 0.125 {
		t.Errorf("%+v %v", v, err)
	}
}

func TestBoolean(t *testing.T) {
	// dataRecordsReliability (276) is boolean: 1 true, 2 false, anything else invalid.
	if v, err := field(276, 1).Value(); err != nil || !v.Bool {
		t.Errorf("true: %+v %v", v, err)
	}
	if v, err := field(276, 2).Value(); err != nil || v.Bool || v.Kind != KindBool {
		t.Errorf("false: %+v %v", v, err)
	}
	if v, err := field(276, 0).Value(); err == nil || v.Kind != KindOctets {
		t.Errorf("0 must be invalid and fall back to raw: %+v %v", v, err)
	}
}

func TestAddresses(t *testing.T) {
	v, err := field(8, 10, 0, 20, 14).Value()
	if err != nil || v.Addr != netip.MustParseAddr("10.0.20.14") {
		t.Errorf("ipv4: %+v %v", v, err)
	}
	v6 := netip.MustParseAddr("2001:db8::1").As16()
	v, err = field(27, v6[:]...).Value()
	if err != nil || v.Addr != netip.MustParseAddr("2001:db8::1") {
		t.Errorf("ipv6: %+v %v", v, err)
	}
	v, err = field(56, 0xAA, 0xBB, 0xCC, 0, 1, 2).Value() // sourceMacAddress
	if err != nil || v.MAC.String() != "aa:bb:cc:00:01:02" {
		t.Errorf("mac: %+v %v", v, err)
	}
}

func TestInvalidLengthKeepsRawBytes(t *testing.T) {
	v, err := field(8, 10, 0, 1).Value() // a 3-byte IPv4 address
	if err == nil || v.Kind != KindOctets || !bytes.Equal(v.Octets, []byte{10, 0, 1}) {
		t.Errorf("%+v %v", v, err)
	}
}

func TestStrings(t *testing.T) {
	if v, err := field(82, []byte("ether1-WAN")...).Value(); err != nil || v.String != "ether1-WAN" {
		t.Errorf("%+v %v", v, err)
	}
	if v, err := field(82, 0xFF, 0xFE).Value(); err == nil || v.Kind != KindOctets {
		t.Errorf("invalid UTF-8 should fall back to raw: %+v %v", v, err)
	}
}

func TestDateTimes(t *testing.T) {
	want := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

	secs := uint32(want.Unix())
	if v, err := field(150, byte(secs>>24), byte(secs>>16), byte(secs>>8), byte(secs)).Value(); err != nil || !v.Time.Equal(want) {
		t.Errorf("seconds: %+v %v", v, err)
	}

	ms := uint64(want.UnixMilli()) + 250
	b := make([]byte, 8)
	for i := range b {
		b[i] = byte(ms >> (56 - 8*i))
	}
	if v, err := field(152, b...).Value(); err != nil || !v.Time.Equal(want.Add(250*time.Millisecond)) {
		t.Errorf("milliseconds: %+v %v", v, err)
	}

	// NTP format: seconds since 1900 in the top 32 bits, half a second in the fraction.
	ntp := uint64(want.Unix()+ntpEpochOffset)<<32 | 0x80000000
	for i := range b {
		b[i] = byte(ntp >> (56 - 8*i))
	}
	if v, err := field(156, b...).Value(); err != nil || !v.Time.Equal(want.Add(500*time.Millisecond)) {
		t.Errorf("nanoseconds: %+v %v", v, err)
	}
	if v, err := field(154, b...).Value(); err != nil || !v.Time.Equal(want.Add(500*time.Millisecond)) {
		t.Errorf("microseconds: %+v %v", v, err)
	}
}

func TestEnterpriseAndUnknownAreOctets(t *testing.T) {
	f := Field{Spec: FieldSpec{Enterprise: 14988, ID: 8, Length: 4}, Raw: []byte{1, 2, 3, 4}}
	if v, err := f.Value(); err != nil || v.Kind != KindOctets {
		t.Errorf("enterprise: %+v %v", v, err)
	}
	if v, err := field(32000, 1, 2).Value(); err != nil || v.Kind != KindOctets {
		t.Errorf("unassigned: %+v %v", v, err)
	}
}

func TestListsNotDecodedYet(t *testing.T) {
	v, err := field(291, 0xFF, 0, 8, 0, 4).Value()
	if !errors.Is(err, ErrListsNotDecoded) || v.Kind != KindOctets {
		t.Errorf("%+v %v", v, err)
	}
}
