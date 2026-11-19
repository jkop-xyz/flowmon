package classify

import (
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

// The counting tables through RFC 5103 biflow records (ADR 0004): each row becomes the
// forward direction of a biflow whose reverse direction carries the reply. The forward
// half must classify as the row says; the reverse half as the reply does: the same
// Local Host and Peer, the opposite Direction (for Internal traffic, the other Local
// Host is now the sender), and the same Service Port.

// viaBiflow sends f as the forward direction of an IPFIX biflow record and returns the
// two Flows ingest makes of it.
func viaBiflow(t *testing.T, f flow.Flow) (forward, reverse flow.Flow) {
	t.Helper()
	v6 := f.Src.Is6()
	addrLen := uint16(4)
	src, dst, postSrc, postDst := uint16(8), uint16(12), uint16(225), uint16(226)
	if v6 {
		addrLen, src, dst, postSrc, postDst = 16, 27, 28, 281, 282
	}
	rev := func(id, length uint16) ipfix.FieldSpec {
		return ipfix.FieldSpec{Enterprise: ipfix.EnterpriseReverse, ID: id, Length: length}
	}
	tmpl := ipfix.Template{ID: 300, Fields: []ipfix.FieldSpec{
		{ID: src, Length: addrLen}, {ID: dst, Length: addrLen}, {ID: postSrc, Length: addrLen}, {ID: postDst, Length: addrLen},
		{ID: 7, Length: 2}, {ID: 11, Length: 2}, {ID: 227, Length: 2}, {ID: 228, Length: 2},
		{ID: 4, Length: 1}, {ID: 10, Length: 4}, {ID: 14, Length: 4},
		{ID: 1, Length: 8}, {ID: 2, Length: 8}, rev(1, 8), rev(2, 8),
		{ID: 152, Length: 8}, {ID: 153, Length: 8},
	}}
	ms := func(at time.Time) []byte { return ipfixtest.U64(uint64(at.UnixMilli())) }
	rec := [][]byte{
		addrBytes(f.Src, v6), addrBytes(f.Dst, v6), addrBytes(f.PostNATSrc, v6), addrBytes(f.PostNATDst, v6),
		ipfixtest.U16(f.SrcPort), ipfixtest.U16(f.DstPort), ipfixtest.U16(f.PostNATSrcPort), ipfixtest.U16(f.PostNATDstPort),
		ipfixtest.U8(6), ipfixtest.U32(f.IngressInterface), ipfixtest.U32(f.EgressInterface),
		ipfixtest.U64(1200), ipfixtest.U64(4), ipfixtest.U64(90_000), ipfixtest.U64(70),
		ms(now.Add(-4 * time.Second)), ms(now.Add(-time.Second)),
	}
	msg := ipfixtest.NewMessage(0, 0, now).TemplateSet(tmpl).DataSet(tmpl, rec).Bytes()
	h := &nopHandler{}
	if _, err := ipfix.NewSession(ipfix.TransportUDP, ipfix.DefaultLimits()).Decode(msg, now, h); err != nil || len(h.records) != 1 {
		t.Fatalf("decoding: %v, %d records", err, len(h.records))
	}
	fw, err := flow.NewNormalizer().Normalize(f.Exporter, h.records[0], now)
	if err != nil {
		t.Fatal(err)
	}
	rv, ok := flow.Reverse(fw, h.records[0])
	if !ok || rv.Bytes != 90_000 || rv.Packets != 70 || fw.Bytes != 1200 {
		t.Fatalf("reverse %v: %+v", ok, rv)
	}
	return fw, rv
}

// reply is what the row's reply must classify as.
func (r row) reply() row {
	rp := r
	switch r.direction {
	case flow.DirectionOutbound:
		rp.direction = flow.DirectionInbound
	case flow.DirectionInbound:
		rp.direction = flow.DirectionOutbound
	case flow.DirectionInternal:
		rp.local, rp.peer, rp.localPort, rp.peerPort = r.peer, r.local, r.peerPort, r.localPort
	}
	return rp
}

func TestCountingTableByAddressOverBiflows(t *testing.T) {
	for _, r := range addressRows() {
		t.Run(r.name, func(t *testing.T) {
			if r.dst.IsMulticast() {
				t.Skip("nothing replies from a multicast group")
			}
			fw, rv := viaBiflow(t, r.flow())
			r.check(t, fw)
			r.reply().check(t, rv)
			if r.direction != flow.DirectionTransit {
				c := learned()
				c.Classify(&fw, now)
				c.Classify(&rv, now)
				if a, b := flow.ServicePort(6, fw.LocalPort, fw.PeerPort), flow.ServicePort(6, rv.LocalPort, rv.PeerPort); a != b {
					t.Errorf("Service Port %d forward, %d reverse", a, b)
				}
			}
		})
	}
}

func TestCountingTableByInterfaceOverBiflows(t *testing.T) {
	for _, r := range interfaceRows() {
		t.Run(r.name, func(t *testing.T) {
			fw, rv := viaBiflow(t, r.flow())
			r.check(t, fw)
			reply := r
			switch r.dir {
			case flow.DirectionOutbound:
				reply.dir = flow.DirectionInbound
			case flow.DirectionInbound:
				reply.dir = flow.DirectionOutbound
			case flow.DirectionInternal:
				reply.local, reply.peer = r.peer, r.local
			}
			reply.check(t, rv)
		})
	}
}
