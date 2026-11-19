package ipfix_test

import (
	"bytes"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

type recorder struct {
	templates  []ipfix.TemplateEvent
	records    []ipfix.Record
	deviations map[ipfix.Deviation]int
}

func newRecorder() *recorder { return &recorder{deviations: map[ipfix.Deviation]int{}} }

func (r *recorder) Template(_ uint32, _ *ipfix.Template, ev ipfix.TemplateEvent) {
	r.templates = append(r.templates, ev)
}
func (r *recorder) Records(recs []ipfix.Record)           { r.records = append(r.records, recs...) }
func (r *recorder) Deviation(d ipfix.Deviation, _ string) { r.deviations[d]++ }

func (r *recorder) onlyDeviations(t *testing.T, want ...ipfix.Deviation) {
	t.Helper()
	wantSet := map[ipfix.Deviation]bool{}
	for _, d := range want {
		wantSet[d] = true
		if r.deviations[d] == 0 {
			t.Errorf("expected deviation %s, got %v", d, r.deviations)
		}
	}
	for d := range r.deviations {
		if !wantSet[d] {
			t.Errorf("unexpected deviation %s (all: %v)", d, r.deviations)
		}
	}
}

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func newSession() *ipfix.Session { return ipfix.NewSession(ipfix.TransportUDP, ipfix.DefaultLimits()) }

func decode(t *testing.T, s *ipfix.Session, msg []byte, now time.Time) *recorder {
	t.Helper()
	r := newRecorder()
	if _, err := s.Decode(msg, now, r); err != nil {
		t.Fatalf("Decode: %v", err)
	}
	return r
}

func ipv4(a, b, c, d byte) []byte { return []byte{a, b, c, d} }

func mikrotikRecord(srcPort uint16, octets uint32) [][]byte {
	return [][]byte{
		ipv4(192, 168, 10, 44), ipv4(1, 1, 1, 1),
		ipfixtest.U16(srcPort), ipfixtest.U16(443), ipfixtest.U8(6),
		ipfixtest.U32(3), ipfixtest.U32(1),
		ipfixtest.U32(octets), ipfixtest.U32(10),
		ipfixtest.U32(1000), ipfixtest.U32(2000),
		ipv4(203, 0, 113, 7), ipv4(1, 1, 1, 1),
	}
}

func TestRoundTripTemplateAndData(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	want := [][][]byte{mikrotikRecord(50000, 1500), mikrotikRecord(50001, 3000)}
	msg := ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).DataSet(tmpl, want...).Bytes()

	s := newSession()
	r := decode(t, s, msg, t0)
	r.onlyDeviations(t)

	if len(r.templates) != 1 || r.templates[0] != ipfix.TemplateNew {
		t.Fatalf("template events %v", r.templates)
	}
	if len(r.records) != len(want) {
		t.Fatalf("got %d records", len(r.records))
	}
	for i, rec := range r.records {
		if rec.TemplateID != 256 || rec.ObservationDomain != 1 || !rec.ExportTime.Equal(t0) {
			t.Errorf("record %d header %+v", i, rec)
		}
		for j, f := range rec.Fields {
			if f.Spec != tmpl.Fields[j] || !bytes.Equal(f.Raw, want[i][j]) {
				t.Errorf("record %d field %d: spec %+v raw %x, want %x", i, j, f.Spec, f.Raw, want[i][j])
			}
		}
	}
	if got := s.Templates(); len(got) != 1 || got[0].Template.ID != 256 {
		t.Errorf("Templates() = %+v", got)
	}
}

func TestRecordsOwnTheirBytes(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	msg := ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).DataSet(tmpl, mikrotikRecord(1, 2)).Bytes()
	r := decode(t, newSession(), msg, t0)
	for i := range msg {
		msg[i] = 0xEE // the collector reuses its read buffer
	}
	if got := r.records[0].Fields[0].Raw; !bytes.Equal(got, ipv4(192, 168, 10, 44)) {
		t.Errorf("record bytes changed with the input buffer: %x", got)
	}
}

func TestVariableLengthAndEnterpriseFields(t *testing.T) {
	tmpl := ipfix.Template{ID: 300, Fields: []ipfix.FieldSpec{
		{ID: 82, Length: ipfix.VariableLength},                    // interfaceName (string)
		{ID: 1, Enterprise: 14988, Length: 4},                     // MikroTik's PEN, unknown element
		{ID: 313, Length: ipfix.VariableLength},                   // ipHeaderPacketSection
		{ID: 77, Enterprise: 29305, Length: ipfix.VariableLength}, // enterprise, variable
	}}
	long := bytes.Repeat([]byte{0xAB}, 300) // needs the 3-byte length prefix
	rec := [][]byte{[]byte("ether1-WAN"), {1, 2, 3, 4}, long, {}}
	msg := ipfixtest.NewMessage(7, 0, t0).TemplateSet(tmpl).DataSet(tmpl, rec).Bytes()

	r := decode(t, newSession(), msg, t0)
	r.onlyDeviations(t)
	if len(r.records) != 1 {
		t.Fatalf("got %d records", len(r.records))
	}
	for i, f := range r.records[0].Fields {
		if !bytes.Equal(f.Raw, rec[i]) {
			t.Errorf("field %d: %x, want %x", i, f.Raw, rec[i])
		}
	}
	if f := r.records[0].Fields[1]; f.Spec.Enterprise != 14988 || f.Spec.Name() != "enterprise14988.ie1" {
		t.Errorf("enterprise field %+v %q", f.Spec, f.Spec.Name())
	}
}

func TestDataBeforeTemplateIsBufferedAndReplayed(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()

	r := decode(t, s, ipfixtest.NewMessage(1, 0, t0).DataSet(tmpl, mikrotikRecord(1, 100)).Bytes(), t0)
	r.onlyDeviations(t)
	if len(r.records) != 0 {
		t.Fatalf("records decoded without a Template")
	}

	r = decode(t, s, ipfixtest.NewMessage(1, 1, t0).TemplateSet(tmpl).Bytes(), t0.Add(2*time.Second))
	r.onlyDeviations(t)
	if len(r.records) != 1 {
		t.Fatalf("buffered record not replayed: %d", len(r.records))
	}
	if !r.records[0].ExportTime.Equal(t0) {
		t.Errorf("replayed record should keep its own export time")
	}
}

func TestBufferedDataExpires(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()
	decode(t, s, ipfixtest.NewMessage(1, 0, t0).DataSet(tmpl, mikrotikRecord(1, 100)).Bytes(), t0)

	later := t0.Add(ipfix.DefaultLimits().PendingMaxAge + time.Second)
	r := decode(t, s, ipfixtest.NewMessage(1, 1, later).TemplateSet(tmpl).Bytes(), later)
	r.onlyDeviations(t, ipfix.DevUnknownTemplate)
	if len(r.records) != 0 {
		t.Errorf("expired data was replayed")
	}
}

func TestBufferFullDropsData(t *testing.T) {
	limits := ipfix.DefaultLimits()
	limits.PendingMaxBytes = 10
	s := ipfix.NewSession(ipfix.TransportUDP, limits)
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	r := decode(t, s, ipfixtest.NewMessage(1, 0, t0).DataSet(tmpl, mikrotikRecord(1, 100)).Bytes(), t0)
	r.onlyDeviations(t, ipfix.DevUnknownTemplate)
}

func TestWithdrawalIgnoredOverUDP(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()
	decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).Bytes(), t0)

	r := decode(t, s, ipfixtest.NewMessage(1, 0, t0).Withdrawal(256).DataSet(tmpl, mikrotikRecord(1, 1)).Bytes(), t0)
	r.onlyDeviations(t, ipfix.DevWithdrawalIgnored)
	if len(r.records) != 1 {
		t.Errorf("Template should still be usable after an ignored withdrawal")
	}
}

func TestTemplateRefreshAndRedefinition(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()
	decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).Bytes(), t0)

	r := decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).Bytes(), t0)
	if len(r.templates) != 1 || r.templates[0] != ipfix.TemplateRefreshed {
		t.Errorf("events %v", r.templates)
	}

	redefined := ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 8, Length: 4}}}
	r = decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(redefined).DataSet(redefined, [][]byte{ipv4(10, 0, 0, 1)}).Bytes(), t0)
	if len(r.templates) != 1 || r.templates[0] != ipfix.TemplateRedefined {
		t.Errorf("events %v", r.templates)
	}
	if len(r.records) != 1 || len(r.records[0].Fields) != 1 {
		t.Errorf("records should use the redefined Template: %+v", r.records)
	}
}

func TestTemplatesAreScopedByObservationDomain(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()
	decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).Bytes(), t0)

	r := decode(t, s, ipfixtest.NewMessage(2, 0, t0).DataSet(tmpl, mikrotikRecord(1, 1)).Bytes(), t0)
	if len(r.records) != 0 {
		t.Error("a Template from domain 1 must not decode data from domain 2")
	}
}

func TestTemplateExpiry(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()
	decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).Bytes(), t0)

	later := t0.Add(ipfix.DefaultLimits().TemplateTimeout + time.Minute)
	r := decode(t, s, ipfixtest.NewMessage(1, 0, later).DataSet(tmpl, mikrotikRecord(1, 1)).Bytes(), later)
	if len(r.records) != 0 || len(s.Templates()) != 0 {
		t.Error("an expired Template must not be used")
	}
}

func TestOptionsTemplate(t *testing.T) {
	opts := ipfix.Template{ID: 257, ScopeFieldCount: 1, Fields: []ipfix.FieldSpec{
		{ID: 10, Length: 4},                    // ingressInterface (scope)
		{ID: 82, Length: ipfix.VariableLength}, // interfaceName
		{ID: 83, Length: ipfix.VariableLength}, // interfaceDescription
	}}
	msg := ipfixtest.NewMessage(1, 0, t0).OptionsTemplateSet(opts).
		DataSet(opts, [][]byte{ipfixtest.U32(3), []byte("ether1"), []byte("WAN")}).Bytes()
	r := decode(t, newSession(), msg, t0)
	r.onlyDeviations(t)
	if len(r.records) != 1 || !r.records[0].Options() || r.records[0].ScopeFieldCount != 1 {
		t.Fatalf("records %+v", r.records)
	}
}

func TestPaddingAtEndOfDataSet(t *testing.T) {
	tmpl := ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 8, Length: 4}, {ID: 4, Length: 1}}}
	rec := append(ipv4(10, 0, 0, 1), 6)

	zero := ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).RawSet(256, append(append([]byte{}, rec...), 0, 0, 0)).Bytes()
	r := decode(t, newSession(), zero, t0)
	r.onlyDeviations(t)
	if len(r.records) != 1 {
		t.Errorf("zero padding: %d records", len(r.records))
	}

	junk := ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).RawSet(256, append(append([]byte{}, rec...), 9, 9)).Bytes()
	r = decode(t, newSession(), junk, t0)
	r.onlyDeviations(t, ipfix.DevTruncatedRecord)
	if len(r.records) != 1 {
		t.Errorf("non-zero leftover: %d records", len(r.records))
	}
}

func TestSequenceGap(t *testing.T) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	s := newSession()
	decode(t, s, ipfixtest.NewMessage(1, 100, t0).TemplateSet(tmpl).DataSet(tmpl, mikrotikRecord(1, 1), mikrotikRecord(2, 1)).Bytes(), t0)

	r := decode(t, s, ipfixtest.NewMessage(1, 102, t0).DataSet(tmpl, mikrotikRecord(3, 1)).Bytes(), t0)
	r.onlyDeviations(t) // 100 + 2 records = 102: in order

	r = decode(t, s, ipfixtest.NewMessage(1, 110, t0).DataSet(tmpl, mikrotikRecord(4, 1)).Bytes(), t0)
	r.onlyDeviations(t, ipfix.DevSequenceGap)
}

func TestTooManyTemplates(t *testing.T) {
	limits := ipfix.DefaultLimits()
	limits.MaxTemplates = 2
	s := ipfix.NewSession(ipfix.TransportUDP, limits)
	a, b, c := ipfixtest.MikroTikLikeTemplate(256), ipfixtest.MikroTikLikeTemplate(257), ipfixtest.MikroTikLikeTemplate(258)
	r := decode(t, s, ipfixtest.NewMessage(1, 0, t0).TemplateSet(a, b, c).Bytes(), t0)
	r.onlyDeviations(t, ipfix.DevTooManyTemplates)
	if len(s.Templates()) != 2 {
		t.Errorf("kept %d Templates", len(s.Templates()))
	}
}

func TestMalformedTemplates(t *testing.T) {
	big := ipfix.Template{ID: 256}
	for i := 0; i < 600; i++ {
		big.Fields = append(big.Fields, ipfix.FieldSpec{ID: 8, Length: 4})
	}
	cases := []struct {
		name string
		msg  []byte
		want ipfix.Deviation
	}{
		{"template ID below 256", ipfixtest.NewMessage(1, 0, t0).TemplateSet(ipfix.Template{ID: 5, Fields: []ipfix.FieldSpec{{ID: 8, Length: 4}}}).Bytes(), ipfix.DevInvalidTemplateID},
		{"too many fields", ipfixtest.NewMessage(1, 0, t0).TemplateSet(big).Bytes(), ipfix.DevTemplateTooLarge},
		{"scope count zero", ipfixtest.NewMessage(1, 0, t0).OptionsTemplateSet(ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 10, Length: 4}}}).Bytes(), ipfix.DevInvalidScopeCount},
		{"zero-length record", ipfixtest.NewMessage(1, 0, t0).TemplateSet(ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 8, Length: 0}}}).Bytes(), ipfix.DevEmptyTemplate},
		{"forged field count", ipfixtest.NewMessage(1, 0, t0).RawSet(2, []byte{1, 0, 0xFF, 0xFF, 0, 8, 0, 4}).Bytes(), ipfix.DevTruncatedTemplate},
		{"bad field length", ipfixtest.NewMessage(1, 0, t0).TemplateSet(ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 8, Length: 3}}}).Bytes(), ipfix.DevFieldLengthInvalid},
		{"reserved set ID", ipfixtest.NewMessage(1, 0, t0).RawSet(100, []byte{1, 2, 3, 4}).Bytes(), ipfix.DevReservedSetID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := decode(t, newSession(), tc.msg, t0)
			r.onlyDeviations(t, tc.want)
		})
	}
}

func TestMalformedMessages(t *testing.T) {
	good := ipfixtest.NewMessage(1, 0, t0).TemplateSet(ipfixtest.MikroTikLikeTemplate(256)).Bytes()

	setLen := func(b []byte, n uint16) []byte {
		c := append([]byte{}, b...)
		binary.BigEndian.PutUint16(c[18:], n)
		return c
	}

	t.Run("short", func(t *testing.T) {
		r := newRecorder()
		if _, err := newSession().Decode(good[:10], t0, r); !errors.Is(err, ipfix.ErrNotIPFIX) {
			t.Errorf("err %v", err)
		}
		r.onlyDeviations(t, ipfix.DevShortMessage)
	})
	t.Run("netflow v5", func(t *testing.T) {
		v5 := append([]byte{}, good...)
		binary.BigEndian.PutUint16(v5, 5)
		r := newRecorder()
		if _, err := newSession().Decode(v5, t0, r); !errors.Is(err, ipfix.ErrNotIPFIX) {
			t.Errorf("err %v", err)
		}
		r.onlyDeviations(t, ipfix.DevWrongVersion)
	})
	t.Run("truncated message", func(t *testing.T) {
		r := decode(t, newSession(), good[:len(good)-6], t0)
		r.onlyDeviations(t, ipfix.DevTruncatedMessage, ipfix.DevTruncatedSet)
	})
	t.Run("trailing bytes", func(t *testing.T) {
		r := decode(t, newSession(), append(append([]byte{}, good...), 1, 2, 3), t0)
		r.onlyDeviations(t, ipfix.DevTrailingBytes)
		if len(r.templates) != 1 {
			t.Error("the message itself should still decode")
		}
	})
	t.Run("set length too small", func(t *testing.T) {
		r := decode(t, newSession(), setLen(good, 2), t0)
		r.onlyDeviations(t, ipfix.DevBadSetLength)
	})
	t.Run("set length too large", func(t *testing.T) {
		r := decode(t, newSession(), setLen(good, 9000), t0)
		r.onlyDeviations(t, ipfix.DevTruncatedSet)
	})
}

func FuzzDecode(f *testing.F) {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	varTmpl := ipfix.Template{ID: 300, Fields: []ipfix.FieldSpec{{ID: 82, Length: ipfix.VariableLength}, {ID: 1, Enterprise: 9, Length: 2}}}
	opts := ipfix.Template{ID: 257, ScopeFieldCount: 1, Fields: []ipfix.FieldSpec{{ID: 10, Length: 4}, {ID: 82, Length: ipfix.VariableLength}}}
	f.Add(ipfixtest.NewMessage(1, 0, t0).TemplateSet(tmpl).DataSet(tmpl, mikrotikRecord(1, 2)).Bytes())
	f.Add(ipfixtest.NewMessage(1, 0, t0).TemplateSet(varTmpl).DataSet(varTmpl, [][]byte{bytes.Repeat([]byte{'a'}, 300), {1, 2}}).Bytes())
	f.Add(ipfixtest.NewMessage(1, 0, t0).OptionsTemplateSet(opts).DataSet(opts, [][]byte{ipfixtest.U32(1), []byte("x")}).Bytes())
	f.Add(ipfixtest.NewMessage(1, 0, t0).Withdrawal(256).Bytes())

	f.Fuzz(func(t *testing.T, msg []byte) {
		s := newSession()
		r := newRecorder()
		// Decode twice so data buffered by the first call can be replayed by the second.
		for i := 0; i < 2; i++ {
			_, _ = s.Decode(msg, t0, r)
		}
		for _, rec := range r.records {
			for _, f := range rec.Fields {
				if !f.Spec.Variable() && len(f.Raw) != int(f.Spec.Length) {
					t.Fatalf("fixed field %s has %d bytes, spec says %d", f.Spec.Name(), len(f.Raw), f.Spec.Length)
				}
				_, _ = f.Value()
			}
		}
	})
}
