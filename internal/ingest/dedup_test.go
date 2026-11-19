package ingest

import (
	"context"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

func dedupFlow(src, dst string, sp, dp uint16, start, end time.Time) flow.Flow {
	return flow.Flow{Src: netip.MustParseAddr(src), Dst: netip.MustParseAddr(dst), SrcPort: sp, DstPort: dp, Protocol: 6,
		Start: start, End: end, Bytes: 1000}
}

func TestDuplicatesAcrossObservationPoints(t *testing.T) {
	at := now.Add(-time.Minute)
	// The LAN side reports the connection with what NAT made of it.
	lanSide := dedupFlow("192.168.10.44", "1.1.1.1", 50000, 443, at, at.Add(10*time.Second))
	lanSide.PostNATSrc, lanSide.PostNATSrcPort = netip.MustParseAddr("203.0.113.7"), 61000
	d := &dedup{}
	for _, c := range []struct {
		name string
		f    flow.Flow
		dup  bool
	}{
		{"first observation", lanSide, false},
		{"the same packets at the WAN side, after NAT: the NAT-aware key matches",
			dedupFlow("203.0.113.7", "1.1.1.1", 61000, 443, at.Add(5*time.Millisecond), at.Add(10*time.Second+5*time.Millisecond)), true},
		{"the same packets at another LAN point",
			dedupFlow("192.168.10.44", "1.1.1.1", 50000, 443, at.Add(time.Second), at.Add(9*time.Second)), true},
		{"the reply direction is other traffic",
			dedupFlow("1.1.1.1", "192.168.10.44", 443, 50000, at, at.Add(10*time.Second)), false},
		{"the connection's next record, after the first ended",
			dedupFlow("192.168.10.44", "1.1.1.1", 50000, 443, at.Add(11*time.Second), at.Add(20*time.Second)), false},
		{"another connection", dedupFlow("192.168.10.44", "1.1.1.1", 50001, 443, at, at.Add(10*time.Second)), false},
		{"a one-packet Flow", dedupFlow("192.168.10.44", "9.9.9.9", 5353, 53, at, at), false},
		{"the same one-packet Flow at another point", dedupFlow("192.168.10.44", "9.9.9.9", 5353, 53, at, at), true},
	} {
		if got := d.duplicate(&c.f); got != c.dup {
			t.Errorf("%s: duplicate %v, want %v", c.name, got, c.dup)
		}
	}
	if d.removed != 3 {
		t.Errorf("removed %d", d.removed)
	}
}

func TestDuplicatesAreForgottenAfterTheWindow(t *testing.T) {
	d := &dedup{}
	at := now.Add(-time.Hour)
	f := dedupFlow("192.168.10.44", "1.1.1.1", 50000, 443, at, at.Add(time.Second))
	d.duplicate(&f)
	later := dedupFlow("192.168.10.50", "1.1.1.1", 50000, 443, now, now.Add(time.Second))
	d.duplicate(&later)
	if len(d.seen) != 1 {
		t.Errorf("%d keys remembered; the hour-old one should be forgotten", len(d.seen))
	}
}

func TestOnlyExportersObservingMultiplePointsAreDeduplicated(t *testing.T) {
	w := &memWriter{}
	multi := netip.MustParseAddr("192.168.10.2")
	p := New(Options{Writer: w, Now: func() time.Time { return now },
		MultiplePoints: func(a netip.Addr) bool { return a == multi }})
	rec := outboundRecord(now)
	p.Records(exporter, []ipfix.Record{rec, rec})
	p.Records(multi, []ipfix.Record{rec, rec})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	if w.count() != 3 {
		t.Errorf("%d Flows: both of the plain Exporter's, one of the other's", w.count())
	}
	if st := p.Counting(multi); st == nil || st.DuplicatesRemoved != 1 || st.Flows != 1 {
		t.Errorf("status %+v", st)
	}
	if st := p.Counting(exporter); st == nil || st.DuplicatesRemoved != 0 || st.Flows != 2 {
		t.Errorf("plain Exporter %+v", st)
	}
}
