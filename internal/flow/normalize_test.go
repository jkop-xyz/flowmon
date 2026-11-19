package flow

import (
	"bytes"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

var (
	exp     = netip.MustParseAddr("192.168.10.1")
	exportT = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	recvT   = exportT.Add(200 * time.Millisecond)
)

func f(id uint16, raw []byte) ipfix.Field {
	return ipfix.Field{Spec: ipfix.FieldSpec{ID: id, Length: uint16(len(raw))}, Raw: raw}
}

func baseFields() []ipfix.Field {
	return []ipfix.Field{
		f(ieSourceIPv4Address, []byte{192, 168, 10, 44}),
		f(ieDestinationIPv4Address, []byte{1, 1, 1, 1}),
		f(ieSourceTransportPort, ipfixtest.U16(50000)),
		f(ieDestinationTransportPort, ipfixtest.U16(443)),
		f(ieProtocolIdentifier, ipfixtest.U8(6)),
		f(ieOctetDeltaCount, ipfixtest.U32(1500)),
		f(iePacketDeltaCount, ipfixtest.U32(3)),
		f(ieIngressInterface, ipfixtest.U32(3)),
		f(ieEgressInterface, ipfixtest.U32(1)),
	}
}

func record(extra ...ipfix.Field) ipfix.Record {
	return ipfix.Record{ObservationDomain: 0, TemplateID: 256, ExportTime: exportT, Fields: append(baseFields(), extra...)}
}

func TestNormalizeBasics(t *testing.T) {
	n := NewNormalizer()
	got, err := n.Normalize(exp, record(
		f(iePostNATSourceIPv4Address, []byte{203, 0, 113, 7}),
		f(iePostNAPTSourceTransportPort, ipfixtest.U16(61000)),
		f(ieFlowStartMilliseconds, ipfixtest.U64(uint64(exportT.Add(-10*time.Second).UnixMilli()))),
		f(ieFlowEndMilliseconds, ipfixtest.U64(uint64(exportT.Add(-time.Second).UnixMilli()))),
	), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if got.Src != netip.MustParseAddr("192.168.10.44") || got.Dst != netip.MustParseAddr("1.1.1.1") ||
		got.SrcPort != 50000 || got.DstPort != 443 || got.Protocol != 6 || got.Bytes != 1500 || got.Packets != 3 ||
		got.IngressInterface != 3 || got.EgressInterface != 1 {
		t.Errorf("fields %+v", got)
	}
	if got.PostNATSrc != netip.MustParseAddr("203.0.113.7") || got.PostNATSrcPort != 61000 || got.PostNATDst.IsValid() {
		t.Errorf("NAT %+v", got)
	}
	if !got.Start.Equal(exportT.Add(-10*time.Second)) || !got.End.Equal(exportT.Add(-time.Second)) || got.TimeEstimated {
		t.Errorf("times %v %v %v", got.Start, got.End, got.TimeEstimated)
	}
	if !got.Received.Equal(recvT) || !got.ExportTime.Equal(exportT) {
		t.Errorf("received/export %v %v", got.Received, got.ExportTime)
	}
}

func TestSysUpTimeWithInitTimeInRecord(t *testing.T) {
	boot := exportT.Add(-48 * time.Hour)
	got, err := NewNormalizer().Normalize(exp, record(
		f(ieSystemInitTimeMilliseconds, ipfixtest.U64(uint64(boot.UnixMilli()))),
		f(ieFlowStartSysUpTime, ipfixtest.U32(uint32((48*time.Hour-20*time.Second).Milliseconds()))),
		f(ieFlowEndSysUpTime, ipfixtest.U32(uint32((48*time.Hour-5*time.Second).Milliseconds()))),
	), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Start.Equal(exportT.Add(-20*time.Second)) || !got.End.Equal(exportT.Add(-5*time.Second)) || got.TimeEstimated {
		t.Errorf("times %v %v estimated=%v", got.Start, got.End, got.TimeEstimated)
	}
}

func TestSysInitTimeFromOptionsRecord(t *testing.T) {
	n := NewNormalizer()
	boot := exportT.Add(-time.Hour)
	opts := ipfix.Record{ScopeFieldCount: 1, ExportTime: exportT, Fields: []ipfix.Field{
		f(144, ipfixtest.U32(1)), // exportingProcessId as scope
		f(ieSystemInitTimeMilliseconds, ipfixtest.U64(uint64(boot.UnixMilli()))),
	}}
	if _, err := n.Normalize(exp, opts, recvT); !errors.Is(err, ErrOptionsRecord) {
		t.Fatalf("options record: %v", err)
	}
	got, err := n.Normalize(exp, record(
		f(ieFlowStartSysUpTime, ipfixtest.U32(uint32((time.Hour-30*time.Second).Milliseconds()))),
		f(ieFlowEndSysUpTime, ipfixtest.U32(uint32((time.Hour-10*time.Second).Milliseconds()))),
	), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if !got.End.Equal(exportT.Add(-10*time.Second)) || got.TimeEstimated {
		t.Errorf("end %v estimated=%v", got.End, got.TimeEstimated)
	}
}

func TestSysUpTimeWithoutInitTimeIsEstimated(t *testing.T) {
	got, err := NewNormalizer().Normalize(exp, record(
		f(ieFlowStartSysUpTime, ipfixtest.U32(1_000)),
		f(ieFlowEndSysUpTime, ipfixtest.U32(16_000)),
	), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if !got.TimeEstimated || !got.End.Equal(exportT) || got.End.Sub(got.Start) != 15*time.Second {
		t.Errorf("start %v end %v estimated=%v", got.Start, got.End, got.TimeEstimated)
	}
}

func TestSysUpTimeWrap(t *testing.T) {
	boot := exportT.Add(-50 * 24 * time.Hour) // 32-bit milliseconds wrap after ~49.7 days
	got, _ := NewNormalizer().Normalize(exp, record(
		f(ieSystemInitTimeMilliseconds, ipfixtest.U64(uint64(boot.UnixMilli()))),
		f(ieFlowStartSysUpTime, ipfixtest.U32(0xFFFFF000)),
		f(ieFlowEndSysUpTime, ipfixtest.U32(0x00001000)),
	), recvT)
	if d := got.End.Sub(got.Start); d != 0x2000*time.Millisecond {
		t.Errorf("duration across the wrap = %v", d)
	}
}

func TestDeltaMicrosecondsAndDuration(t *testing.T) {
	got, _ := NewNormalizer().Normalize(exp, record(
		f(ieFlowStartDeltaMicroseconds, ipfixtest.U32(30_000_000)),
		f(ieFlowEndDeltaMicroseconds, ipfixtest.U32(2_000_000)),
	), recvT)
	if !got.Start.Equal(exportT.Add(-30*time.Second)) || !got.End.Equal(exportT.Add(-2*time.Second)) {
		t.Errorf("delta: %v %v", got.Start, got.End)
	}

	got, _ = NewNormalizer().Normalize(exp, record(
		f(ieFlowStartSeconds, ipfixtest.U32(uint32(exportT.Add(-time.Minute).Unix()))),
		f(ieFlowDurationMilliseconds, ipfixtest.U32(45_000)),
	), recvT)
	if !got.End.Equal(exportT.Add(-15 * time.Second)) {
		t.Errorf("duration: %v %v", got.Start, got.End)
	}
}

func TestNoTimesUsesExportTime(t *testing.T) {
	got, _ := NewNormalizer().Normalize(exp, record(), recvT)
	if !got.TimeEstimated || !got.Start.Equal(exportT) || !got.End.Equal(exportT) {
		t.Errorf("%v %v %v", got.Start, got.End, got.TimeEstimated)
	}
}

func TestClockSkewIsDistrusted(t *testing.T) {
	// The router's clock is a day behind: its absolute times can't be used as they are.
	wrong := exportT.Add(-24 * time.Hour)
	got, _ := NewNormalizer().Normalize(exp, record(
		f(ieFlowStartMilliseconds, ipfixtest.U64(uint64(wrong.Add(-10*time.Second).UnixMilli()))),
		f(ieFlowEndMilliseconds, ipfixtest.U64(uint64(wrong.UnixMilli()))),
	), recvT)
	if !got.TimeEstimated || !got.End.Equal(exportT) || got.End.Sub(got.Start) != 10*time.Second {
		t.Errorf("%v %v %v", got.Start, got.End, got.TimeEstimated)
	}
}

func TestRecordsWithoutAddressesAreRejected(t *testing.T) {
	rec := ipfix.Record{ExportTime: exportT, Fields: []ipfix.Field{f(ieOctetDeltaCount, ipfixtest.U32(1))}}
	if _, err := NewNormalizer().Normalize(exp, rec, recvT); !errors.Is(err, ErrNoAddresses) {
		t.Errorf("err %v", err)
	}
}

func TestIPv6(t *testing.T) {
	src := netip.MustParseAddr("fd00:10::44").As16()
	dst := netip.MustParseAddr("2a00:1450:4001::200e").As16()
	rec := ipfix.Record{ExportTime: exportT, Fields: []ipfix.Field{f(ieSourceIPv6Address, src[:]), f(ieDestinationIPv6Address, dst[:])}}
	got, err := NewNormalizer().Normalize(exp, rec, recvT)
	if err != nil || got.Src != netip.MustParseAddr("fd00:10::44") || got.Dst != netip.MustParseAddr("2a00:1450:4001::200e") {
		t.Errorf("%+v %v", got, err)
	}
}

func TestFieldsRoundTrip(t *testing.T) {
	in := append(baseFields(),
		ipfix.Field{Spec: ipfix.FieldSpec{Enterprise: 14988, ID: 77, Length: 3}, Raw: []byte{1, 2, 3}},
		ipfix.Field{Spec: ipfix.FieldSpec{ID: 82, Length: ipfix.VariableLength}, Raw: []byte("ether1")},
	)
	out, err := DecodeFields(EncodeFields(in))
	if err != nil || len(out) != len(in) {
		t.Fatalf("%v %v", out, err)
	}
	for i := range in {
		if out[i].Spec.Enterprise != in[i].Spec.Enterprise || out[i].Spec.ID != in[i].Spec.ID || !bytes.Equal(out[i].Raw, in[i].Raw) {
			t.Errorf("field %d: %+v, want %+v", i, out[i], in[i])
		}
	}
	if _, err := DecodeFields([]byte{5, 0}); err == nil {
		t.Error("corrupt input must fail")
	}
}

func FuzzDecodeFields(f *testing.F) {
	f.Add(EncodeFields(baseFields()))
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeFields(b)
	})
}

func TestNetFlowV9BootTimeFromTheHeader(t *testing.T) {
	boot := exportT.Add(-72 * time.Hour)
	rec := record(
		f(ieFlowStartSysUpTime, ipfixtest.UptimeMillis(exportT.Add(-20*time.Second), boot)),
		f(ieFlowEndSysUpTime, ipfixtest.UptimeMillis(exportT.Add(-5*time.Second), boot)),
	)
	rec.SystemInitTime = boot
	got, err := NewNormalizer().Normalize(exp, rec, recvT)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Start.Equal(exportT.Add(-20*time.Second)) || !got.End.Equal(exportT.Add(-5*time.Second)) || got.TimeEstimated {
		t.Errorf("times %v %v estimated=%v", got.Start, got.End, got.TimeEstimated)
	}
}

func TestFlowRecordedBeforeTheUptimeWrapped(t *testing.T) {
	// The uptime wrapped 2 s before the export; the boot time counts the wrap, but this
	// Flow's times were taken just before it.
	wrap := (1 << 32) * time.Millisecond
	boot := exportT.Add(-2 * time.Second) // uptime 2 s into the new round
	rec := record(
		f(ieFlowStartSysUpTime, ipfixtest.U32(0xFFFFFFFF-9_999)),
		f(ieFlowEndSysUpTime, ipfixtest.U32(0xFFFFFFFF-999)),
	)
	rec.SystemInitTime = boot
	got, _ := NewNormalizer().Normalize(exp, rec, recvT)
	wantEnd := boot.Add(-wrap).Add(time.Duration(0xFFFFFFFF-999) * time.Millisecond)
	if !got.End.Equal(wantEnd) || got.End.Sub(got.Start) != 9*time.Second || got.TimeEstimated {
		t.Errorf("end %v (want %v), duration %v, estimated=%v", got.End, wantEnd, got.End.Sub(got.Start), got.TimeEstimated)
	}
}

func TestUnspecifiedPostNATAddressMeansNoNAT(t *testing.T) {
	got, err := NewNormalizer().Normalize(exp, record(
		f(iePostNATSourceIPv4Address, []byte{0, 0, 0, 0}),
		f(iePostNATDestinationIPv4Address, []byte{0, 0, 0, 0}),
	), recvT)
	if err != nil {
		t.Fatal(err)
	}
	if got.PostNATSrc.IsValid() || got.PostNATDst.IsValid() {
		t.Errorf("post-NAT %v %v, want none", got.PostNATSrc, got.PostNATDst)
	}
}
