package ipfix_test

import (
	"encoding/binary"
	"errors"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

var boot = t0.Add(-72 * time.Hour)

func v9Record(srcPort uint16, start, end time.Time, b time.Time) [][]byte {
	return [][]byte{
		ipv4(192, 168, 10, 44), ipv4(1, 1, 1, 1),
		ipfixtest.U16(srcPort), ipfixtest.U16(443), ipfixtest.U8(6),
		ipfixtest.U16(2), ipfixtest.U16(1),
		ipfixtest.U32(1500), ipfixtest.U32(3),
		ipfixtest.UptimeMillis(start, b), ipfixtest.UptimeMillis(end, b),
	}
}

func TestNetFlowV9DecodesLikeIPFIX(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(256)
	s := newSession()
	msg := ipfixtest.NewNetFlowV9(7, 1, t0, boot).
		TemplateFlowSet(tmpl).
		DataFlowSet(tmpl, v9Record(50000, t0.Add(-10*time.Second), t0.Add(-time.Second), boot), v9Record(50001, t0, t0, boot)).
		Bytes()
	r := newRecorder()
	hdr, err := s.Decode(msg, t0, r)
	if err != nil {
		t.Fatal(err)
	}
	r.onlyDeviations(t)
	if hdr.Version != 9 || hdr.ObservationDomain != 7 || hdr.SysUptime != 72*time.Hour || !hdr.ExportTime.Equal(t0) || hdr.Count != 3 {
		t.Errorf("header %+v", hdr)
	}
	if len(r.records) != 2 {
		t.Fatalf("%d records", len(r.records))
	}
	rec := r.records[0]
	if rec.ObservationDomain != 7 || rec.TemplateID != 256 || !rec.ExportTime.Equal(t0) || !rec.SystemInitTime.Equal(boot) {
		t.Errorf("record %+v", rec)
	}
	if f, ok := rec.Get(7); !ok || f.Spec.Name() != "sourceTransportPort" {
		t.Errorf("field types 1-127 are IPFIX elements: %+v", f)
	}
	ts := s.Templates()
	if len(ts) != 1 || ts[0].Template.Version != ipfix.VersionNetFlowV9 || ts[0].ObservationDomain != 7 {
		t.Errorf("templates %+v", ts)
	}
}

func TestNetFlowV9DataBeforeTemplate(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(300)
	s := newSession()
	r := decode(t, s, ipfixtest.NewNetFlowV9(0, 1, t0, boot).DataFlowSet(tmpl, v9Record(1, t0, t0, boot)).Bytes(), t0)
	if len(r.records) != 0 {
		t.Fatal("decoded without a Template")
	}
	r = decode(t, s, ipfixtest.NewNetFlowV9(0, 2, t0.Add(time.Second), boot).TemplateFlowSet(tmpl).Bytes(), t0.Add(time.Second))
	if len(r.records) != 1 || !r.records[0].SystemInitTime.Equal(boot) {
		t.Errorf("buffered data set: %+v", r.records)
	}
}

func TestNetFlowV9OptionsAndVendorFields(t *testing.T) {
	opts := ipfix.Template{ID: 257, ScopeFieldCount: 1, Fields: []ipfix.FieldSpec{
		{ID: 2, Length: 4},   // scope: Interface
		{ID: 82, Length: 16}, // IF_NAME
	}}
	vendor := ipfix.Template{ID: 258, Fields: []ipfix.FieldSpec{
		{ID: 8, Length: 4}, {ID: 12, Length: 4},
		{Enterprise: ipfix.EnterpriseNetFlowV9, ID: 57554, Length: 2}, // e.g. an nProbe field
	}}
	name := make([]byte, 16)
	copy(name, "ether1")
	r := decode(t, newSession(), ipfixtest.NewNetFlowV9(0, 1, t0, boot).
		OptionsTemplateFlowSet(opts).TemplateFlowSet(vendor).
		DataFlowSet(opts, [][]byte{ipfixtest.U32(5), name}).
		DataFlowSet(vendor, [][]byte{ipv4(10, 0, 0, 1), ipv4(10, 0, 0, 2), ipfixtest.U16(9)}).
		Bytes(), t0)
	r.onlyDeviations(t)
	if len(r.records) != 2 {
		t.Fatalf("%d records", len(r.records))
	}
	o := r.records[0]
	if !o.Options() || o.ScopeFieldCount != 1 || o.Fields[0].Spec.ID != 10 || o.Fields[0].Spec.Enterprise != 0 {
		t.Errorf("Interface scope should read as ingressInterface: %+v", o.Fields[0].Spec)
	}
	if v, err := o.Fields[1].Value(); err != nil || v.String != "ether1" {
		t.Errorf("interface name %+v %v", v, err)
	}
	f := r.records[1].Fields[2]
	if f.Spec.Enterprise != ipfix.EnterpriseNetFlowV9 || f.Spec.ID != 57554 || f.Spec.Name() != "netflow9.vendor57554" {
		t.Errorf("vendor field %+v %s", f.Spec, f.Spec.Name())
	}
	if _, ok := f.Spec.Element(); ok {
		t.Error("a vendor field must not resolve to an IANA element")
	}
}

func TestNetFlowV9ExporterRestart(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(256)
	s := newSession()
	decode(t, s, ipfixtest.NewNetFlowV9(0, 1, t0, boot).TemplateFlowSet(tmpl).Bytes(), t0)

	// Rebooted a minute later: uptime 20 s, and it reuses Template 256 for other fields.
	now := t0.Add(time.Minute)
	reboot := now.Add(-20 * time.Second)
	other := ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 8, Length: 4}, {ID: 12, Length: 4}}}
	r := decode(t, s, ipfixtest.NewNetFlowV9(0, 1, now, reboot).DataFlowSet(other, [][]byte{ipv4(10, 0, 0, 1), ipv4(10, 0, 0, 2)}).Bytes(), now)
	r.onlyDeviations(t, ipfix.DevExporterRestarted)
	if len(r.records) != 0 || len(s.Templates()) != 0 {
		t.Errorf("the old Template must not decode the restarted Exporter's data: %d records, %d templates", len(r.records), len(s.Templates()))
	}
	// Its new Template arrives and the buffered data decodes with it.
	r = decode(t, s, ipfixtest.NewNetFlowV9(0, 2, now.Add(time.Second), reboot).TemplateFlowSet(other).Bytes(), now.Add(time.Second))
	r.onlyDeviations(t)
	if len(r.records) != 1 || len(r.records[0].Fields) != 2 || !r.records[0].SystemInitTime.Equal(reboot) {
		t.Errorf("after the restart: %+v", r.records)
	}
}

func TestNetFlowV9LatePacketIsNotARestart(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(256)
	s := newSession()
	decode(t, s, ipfixtest.NewNetFlowV9(0, 2, t0, boot).TemplateFlowSet(tmpl).Bytes(), t0)
	// Packet 1, sent two seconds earlier, arrives after packet 2.
	r := decode(t, s, ipfixtest.NewNetFlowV9(0, 1, t0.Add(-2*time.Second), boot).DataFlowSet(tmpl, v9Record(1, t0, t0, boot)).Bytes(), t0)
	if r.deviations[ipfix.DevExporterRestarted] != 0 || len(r.records) != 1 {
		t.Errorf("deviations %v, %d records", r.deviations, len(r.records))
	}
}

func TestNetFlowV9UptimeWrap(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(256)
	s := newSession()
	wrap := (1 << 32) * time.Millisecond
	oldBoot := t0.Add(-wrap + 5*time.Second) // the uptime wraps 5 s after t0
	decode(t, s, ipfixtest.NewNetFlowV9(0, 1, t0, oldBoot).TemplateFlowSet(tmpl).Bytes(), t0)

	later := t0.Add(10 * time.Second)
	r := decode(t, s, ipfixtest.NewNetFlowV9(0, 2, later, oldBoot).DataFlowSet(tmpl, v9Record(1, later.Add(-time.Second), later, oldBoot)).Bytes(), later)
	r.onlyDeviations(t)
	if len(r.records) != 1 {
		t.Fatal("the wrap was taken for a restart")
	}
	if got := r.records[0].SystemInitTime; !got.Equal(oldBoot.Add(wrap)) {
		t.Errorf("boot time %v, want %v (the wrap counted)", got, oldBoot.Add(wrap))
	}
}

func TestNetFlowV9BootTimeTakesTheLatestEstimate(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(256)
	s := newSession()
	// The export time is whole seconds: at 12:00:00.900 the header says 12:00:00 and an
	// uptime ending in .900, which puts the boot 0.9 s early.
	exact := boot.Add(100 * time.Millisecond)
	m := ipfixtest.NewNetFlowV9(0, 1, t0, exact)
	m.SysUptime = t0.Add(900 * time.Millisecond).Sub(exact)
	decode(t, s, m.TemplateFlowSet(tmpl).Bytes(), t0)
	m = ipfixtest.NewNetFlowV9(0, 2, t0.Add(5*time.Second), exact) // sent on the second
	r := decode(t, s, m.DataFlowSet(tmpl, v9Record(1, t0, t0, exact)).Bytes(), t0.Add(5*time.Second))
	if got := r.records[0].SystemInitTime; !got.Equal(exact) {
		t.Errorf("boot %v, want %v", got, exact)
	}
}

func TestNetFlowV9Sequence(t *testing.T) {
	tmpl := ipfixtest.NetFlowV9Template(256)
	rec := v9Record(1, t0, t0, boot)
	for _, c := range []struct {
		name string
		seqs []uint32
		gaps int
	}{
		{"counting packets (RFC 3954)", []uint32{10, 11, 12}, 0},
		{"counting data records", []uint32{10, 13, 16}, 0},
		{"a lost packet", []uint32{10, 11, 13}, 1},
	} {
		t.Run(c.name, func(t *testing.T) {
			s := newSession()
			decode(t, s, ipfixtest.NewNetFlowV9(0, c.seqs[0]-1, t0, boot).TemplateFlowSet(tmpl).Bytes(), t0)
			gaps := 0
			for _, seq := range c.seqs {
				m := ipfixtest.NewNetFlowV9(0, seq, t0, boot).DataFlowSet(tmpl, rec, rec, rec)
				gaps += decode(t, s, m.Bytes(), t0).deviations[ipfix.DevSequenceGap]
			}
			if gaps != c.gaps {
				t.Errorf("%d gaps, want %d", gaps, c.gaps)
			}
		})
	}
}

func TestNetFlowV9Malformed(t *testing.T) {
	good := ipfixtest.NewNetFlowV9(0, 1, t0, boot).TemplateFlowSet(ipfixtest.NetFlowV9Template(256)).Bytes()
	cases := []struct {
		name string
		msg  []byte
		want []ipfix.Deviation
	}{
		{"variable length has no meaning in v9", ipfixtest.NewNetFlowV9(0, 1, t0, boot).
			TemplateFlowSet(ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 8, Length: 4}, {ID: 82, Length: 0xFFFF}}}).Bytes(),
			[]ipfix.Deviation{ipfix.DevFieldLengthInvalid}},
		{"options scope length not whole fields", ipfixtest.NewNetFlowV9(0, 1, t0, boot).
			RawFlowSet(1, []byte{1, 1, 0, 3, 0, 4, 0, 2, 0, 4, 0, 82, 0, 8}).Bytes(),
			[]ipfix.Deviation{ipfix.DevInvalidScopeCount}},
		{"options without a scope", ipfixtest.NewNetFlowV9(0, 1, t0, boot).
			OptionsTemplateFlowSet(ipfix.Template{ID: 256, Fields: []ipfix.FieldSpec{{ID: 82, Length: 8}}}).Bytes(),
			[]ipfix.Deviation{ipfix.DevInvalidScopeCount}},
		{"template ID below 256", ipfixtest.NewNetFlowV9(0, 1, t0, boot).TemplateFlowSet(ipfixtest.NetFlowV9Template(255)).Bytes(),
			[]ipfix.Deviation{ipfix.DevInvalidTemplateID}},
		{"reserved FlowSet ID", ipfixtest.NewNetFlowV9(0, 1, t0, boot).RawFlowSet(7, []byte{1, 2, 3, 4}).Bytes(),
			[]ipfix.Deviation{ipfix.DevReservedSetID}},
		{"FlowSet cut off", good[:len(good)-8], []ipfix.Deviation{ipfix.DevTruncatedSet}},
		{"template cut off", func() []byte {
			b := append([]byte{}, good...)
			binary.BigEndian.PutUint16(b[24+2:], 200) // field count far beyond the FlowSet
			return b
		}(), []ipfix.Deviation{ipfix.DevTruncatedTemplate}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			decode(t, newSession(), c.msg, t0).onlyDeviations(t, c.want...)
		})
	}
	t.Run("short header", func(t *testing.T) {
		r := newRecorder()
		if _, err := newSession().Decode(good[:12], t0, r); !errors.Is(err, ipfix.ErrNotIPFIX) {
			t.Errorf("err %v", err)
		}
		r.onlyDeviations(t, ipfix.DevShortMessage)
	})
}
