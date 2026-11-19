package ipfix_test

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

func v5Record(srcPort uint16) ipfixtest.NetFlowV5Record {
	return ipfixtest.NetFlowV5Record{
		Src: netip.MustParseAddr("192.168.10.44"), Dst: netip.MustParseAddr("1.1.1.1"), NextHop: netip.MustParseAddr("192.168.10.1"),
		Input: 2, Output: 1, Packets: 3, Octets: 1500, First: t0.Add(-10 * time.Second), Last: t0.Add(-time.Second),
		SrcPort: srcPort, DstPort: 443, TCPFlags: 0x1b, Protocol: 6, ToS: 0x10, SrcAS: 0, DstAS: 13335, SrcMask: 24, DstMask: 24,
	}
}

func uintOf(t *testing.T, rec ipfix.Record, id uint16) uint64 {
	t.Helper()
	f, ok := rec.Get(id)
	if !ok {
		t.Fatalf("no field %d", id)
	}
	v, err := f.Value()
	if err != nil {
		t.Fatal(err)
	}
	return v.Uint
}

func TestNetFlowV5DecodesLikeIPFIX(t *testing.T) {
	boot := t0.Add(-5 * 24 * time.Hour).Add(123 * time.Millisecond)
	export := t0.Add(400 * time.Millisecond)
	s := newSession()
	msg := (&ipfixtest.NetFlowV5{Sequence: 100, ExportTime: export, Boot: boot, EngineType: 1, EngineID: 2, Sampling: 0x4000 | 10,
		Records: []ipfixtest.NetFlowV5Record{v5Record(50000), v5Record(50001)}}).Bytes()
	r := newRecorder()
	hdr, err := s.Decode(msg, export, r)
	if err != nil {
		t.Fatal(err)
	}
	r.onlyDeviations(t)
	if hdr.Version != 5 || hdr.Count != 2 || hdr.ObservationDomain != 0x0102 || !hdr.ExportTime.Equal(export) || hdr.Sequence != 100 {
		t.Errorf("header %+v", hdr)
	}
	if len(r.records) != 2 {
		t.Fatalf("%d records", len(r.records))
	}
	rec := r.records[1]
	// The header has nanoseconds, so the boot time is exact.
	if !rec.SystemInitTime.Equal(boot) || rec.ObservationDomain != 0x0102 || rec.Options() {
		t.Errorf("record %+v", rec)
	}
	for id, want := range map[uint16]uint64{7: 50001, 11: 443, 4: 6, 6: 0x1b, 5: 0x10, 10: 2, 14: 1, 1: 1500, 2: 3, 17: 13335, 9: 24, 13: 24, 34: 10, 35: 1,
		22: uint64((5*24*time.Hour - 123*time.Millisecond - 10*time.Second).Milliseconds())} {
		if got := uintOf(t, rec, id); got != want {
			t.Errorf("field %d = %d, want %d", id, got, want)
		}
	}
	if f, _ := rec.Get(15); f.Spec.Name() != "ipNextHopIPv4Address" {
		t.Errorf("next hop %+v", f.Spec)
	}
	ts := s.Templates()
	if len(ts) != 1 || ts[0].Template.Version != ipfix.VersionNetFlowV5 {
		t.Errorf("the fixed record layout should show as a Template: %+v", ts)
	}
}

func TestNetFlowV5Sequence(t *testing.T) {
	s := newSession()
	boot := t0.Add(-time.Hour)
	send := func(seq uint32, n int) *recorder {
		recs := make([]ipfixtest.NetFlowV5Record, n)
		for i := range recs {
			recs[i] = v5Record(uint16(1000 + i))
		}
		return decode(t, s, (&ipfixtest.NetFlowV5{Sequence: seq, ExportTime: t0, Boot: boot, Records: recs}).Bytes(), t0)
	}
	send(10, 3)
	send(13, 5).onlyDeviations(t) // sequence numbers count flows
	send(20, 1).onlyDeviations(t, ipfix.DevSequenceGap)
}

func TestNetFlowV5Restart(t *testing.T) {
	s := newSession()
	decode(t, s, (&ipfixtest.NetFlowV5{ExportTime: t0, Boot: t0.Add(-time.Hour), Records: []ipfixtest.NetFlowV5Record{v5Record(1)}}).Bytes(), t0)
	later := t0.Add(time.Minute)
	rec := v5Record(1)
	rec.First, rec.Last = later.Add(-2*time.Second), later.Add(-time.Second)
	r := decode(t, s, (&ipfixtest.NetFlowV5{Sequence: 1, ExportTime: later, Boot: later.Add(-30 * time.Second), Records: []ipfixtest.NetFlowV5Record{rec}}).Bytes(), later)
	r.onlyDeviations(t, ipfix.DevExporterRestarted)
	if len(r.records) != 1 || !r.records[0].SystemInitTime.Equal(later.Add(-30*time.Second)) {
		t.Errorf("records after the restart decode against the new boot time: %+v", r.records)
	}
}

func TestNetFlowV5Malformed(t *testing.T) {
	boot := t0.Add(-time.Hour)
	two := (&ipfixtest.NetFlowV5{ExportTime: t0, Boot: boot, Records: []ipfixtest.NetFlowV5Record{v5Record(1), v5Record(2)}}).Bytes()
	t.Run("cut off", func(t *testing.T) {
		r := decode(t, newSession(), two[:len(two)-10], t0)
		r.onlyDeviations(t, ipfix.DevTruncatedMessage)
		if len(r.records) != 1 {
			t.Errorf("%d records; the whole one should decode", len(r.records))
		}
	})
	t.Run("trailing bytes", func(t *testing.T) {
		decode(t, newSession(), append(append([]byte{}, two...), 0, 0, 0, 0), t0).onlyDeviations(t, ipfix.DevTrailingBytes)
	})
	t.Run("more than 30 records claimed", func(t *testing.T) {
		m := &ipfixtest.NetFlowV5{ExportTime: t0, Boot: boot, Count: 31, Records: []ipfixtest.NetFlowV5Record{v5Record(1)}}
		decode(t, newSession(), m.Bytes(), t0).onlyDeviations(t, ipfix.DevTooManyRecords, ipfix.DevTruncatedMessage)
	})
	t.Run("short header", func(t *testing.T) {
		r := newRecorder()
		if _, err := newSession().Decode(two[:20], t0, r); err == nil {
			t.Error("decoded a cut-off header")
		}
		r.onlyDeviations(t, ipfix.DevShortMessage)
	})
}
