package flow

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

func rf(id uint16, raw []byte) ipfix.Field {
	return ipfix.Field{Spec: ipfix.FieldSpec{Enterprise: ipfix.EnterpriseReverse, ID: id, Length: uint16(len(raw))}, Raw: raw}
}

func TestReverseFieldsAreNamedAndTyped(t *testing.T) {
	f := rf(ieOctetDeltaCount, ipfixtest.U32(90_000))
	if f.Spec.Name() != "reverseOctetDeltaCount" {
		t.Errorf("name %q", f.Spec.Name())
	}
	if v, err := f.Value(); err != nil || v.Kind != ipfix.KindUnsigned || v.Uint != 90_000 {
		t.Errorf("%+v %v", v, err)
	}
}

func TestReverseOfAUniflowRecord(t *testing.T) {
	rec := record()
	fw, _ := NewNormalizer().Normalize(exp, rec, recvT)
	if _, ok := Reverse(fw, rec); ok || IsBiflow(rec) {
		t.Error("a record without reverse counters has no reverse direction")
	}
	rec.Fields = append(rec.Fields, rf(ieOctetDeltaCount, ipfixtest.U32(0)), rf(iePacketDeltaCount, ipfixtest.U32(0)))
	if _, ok := Reverse(fw, rec); ok || !IsBiflow(rec) {
		t.Error("an unanswered biflow has no reverse Flow")
	}
}

func TestReverseSwapsEndsNATAndInterfaces(t *testing.T) {
	rec := record(
		f(iePostNATSourceIPv4Address, []byte{203, 0, 113, 7}),
		f(iePostNAPTSourceTransportPort, ipfixtest.U16(61000)),
		f(ieFlowStartMilliseconds, ipfixtest.U64(uint64(exportT.Add(-10*time.Second).UnixMilli()))),
		f(ieFlowEndMilliseconds, ipfixtest.U64(uint64(exportT.Add(-time.Second).UnixMilli()))),
		rf(ieOctetDeltaCount, ipfixtest.U32(90_000)), rf(iePacketDeltaCount, ipfixtest.U32(70)),
		rf(ieFlowStartMilliseconds, ipfixtest.U64(uint64(exportT.Add(-9*time.Second).UnixMilli()))),
		rf(ieFlowEndMilliseconds, ipfixtest.U64(uint64(exportT.Add(-2*time.Second).UnixMilli()))),
	)
	fw, err := NewNormalizer().Normalize(exp, rec, recvT)
	if err != nil {
		t.Fatal(err)
	}
	r, ok := Reverse(fw, rec)
	if !ok {
		t.Fatal("no reverse Flow")
	}
	// Forward: 192.168.10.44:50000 → 1.1.1.1:443, translated to 203.0.113.7:61000.
	// The replies arrive from 1.1.1.1:443 at 203.0.113.7:61000 and go on to the LAN host.
	wan, lan, remote := netip.MustParseAddr("203.0.113.7"), netip.MustParseAddr("192.168.10.44"), netip.MustParseAddr("1.1.1.1")
	if r.Src != remote || r.SrcPort != 443 || r.Dst != wan || r.DstPort != 61000 {
		t.Errorf("pre-NAT %v:%d → %v:%d", r.Src, r.SrcPort, r.Dst, r.DstPort)
	}
	if r.PostNATSrc.IsValid() || r.PostNATDst != lan || r.PostNATDstPort != 50000 {
		t.Errorf("post-NAT %v → %v:%d", r.PostNATSrc, r.PostNATDst, r.PostNATDstPort)
	}
	if r.IngressInterface != fw.EgressInterface || r.EgressInterface != fw.IngressInterface {
		t.Errorf("interfaces %d → %d", r.IngressInterface, r.EgressInterface)
	}
	if r.Bytes != 90_000 || r.Packets != 70 || !r.Start.Equal(exportT.Add(-9*time.Second)) || !r.End.Equal(exportT.Add(-2*time.Second)) {
		t.Errorf("counts and times %+v", r)
	}
	if fw.Bytes != 1500 || fw.Src != lan {
		t.Errorf("the forward Flow changed: %+v", fw)
	}
}
