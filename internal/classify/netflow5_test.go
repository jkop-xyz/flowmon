package classify

import (
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

// The counting tables again, through NetFlow v5 (ADR 0004). v5 records carry IPv4
// addresses, ports and interfaces, but no NAT fields and no IPv6, so the rows that need
// either can't be sent over it and are skipped.

func expressibleInV5(f flow.Flow) bool {
	return f.Src.Is4() && f.Dst.Is4() && !f.PostNATSrc.IsValid() && !f.PostNATDst.IsValid() &&
		f.PostNATSrcPort == 0 && f.PostNATDstPort == 0
}

func viaNetFlowV5(t *testing.T, f flow.Flow) flow.Flow {
	t.Helper()
	if !expressibleInV5(f) {
		t.Skip("NetFlow v5 has no NAT fields or IPv6")
	}
	boot := now.Add(-30 * 24 * time.Hour)
	msg := (&ipfixtest.NetFlowV5{ExportTime: now, Boot: boot, Records: []ipfixtest.NetFlowV5Record{{
		Src: f.Src, Dst: f.Dst, Input: uint16(f.IngressInterface), Output: uint16(f.EgressInterface),
		Packets: 2, Octets: 1200, First: now.Add(-4 * time.Second), Last: now.Add(-time.Second),
		SrcPort: f.SrcPort, DstPort: f.DstPort, Protocol: 6,
	}}}).Bytes()
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

func TestCountingTableByAddressOverNetFlowV5(t *testing.T) {
	for _, r := range addressRows() {
		t.Run(r.name, func(t *testing.T) { r.check(t, viaNetFlowV5(t, r.flow())) })
	}
}

func TestCountingTableByInterfaceOverNetFlowV5(t *testing.T) {
	for _, r := range interfaceRows() {
		t.Run(r.name, func(t *testing.T) { r.check(t, viaNetFlowV5(t, r.flow())) })
	}
}

func TestServicePortOverNetFlowV5(t *testing.T) {
	for _, r := range addressRows() {
		if r.direction == flow.DirectionTransit {
			continue
		}
		t.Run(r.name, func(t *testing.T) {
			f := viaNetFlowV5(t, r.flow())
			c := learned()
			c.Classify(&f, now)
			want := flow.ServicePort(6, r.localPort, r.peerPort)
			if got := flow.ServicePort(f.Protocol, f.LocalPort, f.PeerPort); got != want {
				t.Errorf("Service Port %d, want %d", got, want)
			}
		})
	}
}
