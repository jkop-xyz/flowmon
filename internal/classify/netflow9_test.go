package classify

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

// ADR 0004: every decoder must pass the same counting tables. These run each row's
// Flow through a NetFlow v9 packet, the decoder and the normalizer, then classify it.

type nopHandler struct{ records []ipfix.Record }

func (*nopHandler) Template(uint32, *ipfix.Template, ipfix.TemplateEvent) {}
func (h *nopHandler) Records(r []ipfix.Record)                            { h.records = append(h.records, r...) }
func (*nopHandler) Deviation(ipfix.Deviation, string)                     {}

// addrBytes encodes an address in the family's width; none is all zeros, which is what
// an exporter whose Template carries NAT fields sends for a Flow without NAT.
func addrBytes(a netip.Addr, v6 bool) []byte {
	if v6 {
		b := a.As16()
		if !a.IsValid() {
			b = [16]byte{}
		}
		return b[:]
	}
	if !a.IsValid() {
		return make([]byte, 4)
	}
	b := a.As4()
	return b[:]
}

// viaNetFlowV9 sends f's addresses, ports and interfaces through a NetFlow v9 packet
// and returns the Flow the normalizer makes of it.
func viaNetFlowV9(t *testing.T, f flow.Flow) flow.Flow {
	t.Helper()
	v6 := f.Src.Is6()
	addrLen := uint16(4)
	src, dst, postSrc, postDst := uint16(8), uint16(12), uint16(225), uint16(226)
	if v6 {
		addrLen, src, dst, postSrc, postDst = 16, 27, 28, 281, 282
	}
	tmpl := ipfix.Template{ID: 260, Fields: []ipfix.FieldSpec{
		{ID: src, Length: addrLen}, {ID: dst, Length: addrLen},
		{ID: postSrc, Length: addrLen}, {ID: postDst, Length: addrLen},
		{ID: 7, Length: 2}, {ID: 11, Length: 2}, {ID: 227, Length: 2}, {ID: 228, Length: 2},
		{ID: 4, Length: 1}, {ID: 10, Length: 4}, {ID: 14, Length: 4},
		{ID: 1, Length: 4}, {ID: 2, Length: 4}, {ID: 22, Length: 4}, {ID: 21, Length: 4},
	}}
	boot := now.Add(-30 * 24 * time.Hour)
	rec := [][]byte{
		addrBytes(f.Src, v6), addrBytes(f.Dst, v6), addrBytes(f.PostNATSrc, v6), addrBytes(f.PostNATDst, v6),
		ipfixtest.U16(f.SrcPort), ipfixtest.U16(f.DstPort), ipfixtest.U16(f.PostNATSrcPort), ipfixtest.U16(f.PostNATDstPort),
		ipfixtest.U8(6), ipfixtest.U32(f.IngressInterface), ipfixtest.U32(f.EgressInterface),
		ipfixtest.U32(1200), ipfixtest.U32(2),
		ipfixtest.UptimeMillis(now.Add(-4*time.Second), boot), ipfixtest.UptimeMillis(now.Add(-time.Second), boot),
	}
	msg := ipfixtest.NewNetFlowV9(1, 1, now, boot).TemplateFlowSet(tmpl).DataFlowSet(tmpl, rec).Bytes()
	h := &nopHandler{}
	if _, err := ipfix.NewSession(ipfix.TransportUDP, ipfix.DefaultLimits()).Decode(msg, now, h); err != nil || len(h.records) != 1 {
		t.Fatalf("decoding: %v, %d records", err, len(h.records))
	}
	got, err := flow.NewNormalizer().Normalize(f.Exporter, h.records[0], now)
	if err != nil {
		t.Fatal(err)
	}
	if !got.End.Equal(now.Add(-time.Second)) || got.TimeEstimated {
		t.Errorf("flow end %v estimated=%v", got.End, got.TimeEstimated)
	}
	return got
}

func TestCountingTableByAddressOverNetFlowV9(t *testing.T) {
	for _, r := range addressRows() {
		t.Run(r.name, func(t *testing.T) { r.check(t, viaNetFlowV9(t, r.flow())) })
	}
}

func TestCountingTableByInterfaceOverNetFlowV9(t *testing.T) {
	for _, r := range interfaceRows() {
		t.Run(r.name, func(t *testing.T) { r.check(t, viaNetFlowV9(t, r.flow())) })
	}
}

// The Service Port follows from the Local and Peer ports the counting table checks; this
// confirms the ports survive NetFlow v9 for the Service Port cases that involve NAT.
func TestServicePortOverNetFlowV9(t *testing.T) {
	for _, r := range addressRows() {
		if r.direction == flow.DirectionTransit {
			continue
		}
		t.Run(r.name, func(t *testing.T) {
			f := viaNetFlowV9(t, r.flow())
			c := learned()
			c.Classify(&f, now)
			want := flow.ServicePort(6, r.localPort, r.peerPort)
			if got := flow.ServicePort(f.Protocol, f.LocalPort, f.PeerPort); got != want {
				t.Errorf("Service Port %d, want %d", got, want)
			}
		})
	}
}
