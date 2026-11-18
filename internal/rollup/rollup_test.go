package rollup

import (
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func mk(dir flow.Direction, start, end time.Time) *flow.Flow {
	return &flow.Flow{
		Exporter: netip.MustParseAddr("192.168.10.1"), Start: start, End: end,
		Protocol: 6, Bytes: 6000, Packets: 60, Direction: dir,
		Local: netip.MustParseAddr("192.168.10.44"), Peer: netip.MustParseAddr("1.1.1.1"),
		LocalPort: 50000, PeerPort: 443,
	}
}

func sum(rows []Row) Measures {
	var m Measures
	for _, r := range rows {
		m.Add(r.Measures)
	}
	return m
}

func TestOutboundIsSentInboundIsReceived(t *testing.T) {
	out := Contributions(mk(flow.DirectionOutbound, t0, t0), time.Minute)
	if len(out) != 1 || out[0].BytesTx != 6000 || out[0].BytesRx != 0 || out[0].Flows != 1 || out[0].Key.ServicePort != 443 || out[0].Key.PeerKind != PeerRemote {
		t.Errorf("outbound %+v", out)
	}
	in := Contributions(mk(flow.DirectionInbound, t0, t0), time.Minute)
	if len(in) != 1 || in[0].BytesRx != 6000 || in[0].BytesTx != 0 {
		t.Errorf("inbound %+v", in)
	}
}

func TestSpreadOverBuckets(t *testing.T) {
	// 12:00:30 → 12:02:30: 30 s, 60 s, 30 s in three minute buckets.
	rows := Contributions(mk(flow.DirectionOutbound, t0.Add(30*time.Second), t0.Add(150*time.Second)), time.Minute)
	if len(rows) != 3 {
		t.Fatalf("%d rows", len(rows))
	}
	want := []float64{1500, 3000, 1500}
	for i, r := range rows {
		if math.Abs(r.BytesTx-want[i]) > 1e-9 || !r.Key.Bucket.Equal(t0.Add(time.Duration(i)*time.Minute)) {
			t.Errorf("row %d: %v %.1f", i, r.Key.Bucket, r.BytesTx)
		}
	}
	if s := sum(rows); math.Abs(s.BytesTx-6000) > 1e-9 || s.Flows != 1 || rows[2].Flows != 1 {
		t.Errorf("totals %+v; the Flow counts once, in its end bucket", s)
	}
}

func TestInternalProducesBothSides(t *testing.T) {
	f := mk(flow.DirectionInternal, t0, t0)
	f.Peer = netip.MustParseAddr("10.0.20.14")
	rows := Contributions(f, time.Minute)
	if len(rows) != 2 {
		t.Fatalf("%d rows", len(rows))
	}
	sender, receiver := rows[0], rows[1]
	if sender.Key.LocalHost != "192.168.10.44" || sender.Key.PeerLocalHost != "10.0.20.14" || sender.BytesTx != 6000 || sender.Flows != 1 {
		t.Errorf("sender %+v", sender)
	}
	if receiver.Key.LocalHost != "10.0.20.14" || receiver.Key.PeerLocalHost != "192.168.10.44" || receiver.BytesRx != 6000 || receiver.BytesTx != 0 || receiver.Flows != 0 {
		t.Errorf("receiver %+v", receiver)
	}
	if sender.Key.PeerKind != PeerLocal || receiver.Key.PeerKind != PeerLocal {
		t.Error("Internal rows have a Local peer")
	}
}

func TestNetworkTotalCountsEachByteOnce(t *testing.T) {
	// Network Total = received from remote + sent to remote + sent to local (ADR 0004).
	flows := []*flow.Flow{mk(flow.DirectionOutbound, t0, t0), mk(flow.DirectionInbound, t0, t0), mk(flow.DirectionInternal, t0, t0)}
	flows[2].Peer = netip.MustParseAddr("10.0.20.14")
	var total float64
	for _, f := range flows {
		for _, r := range Contributions(f, time.Minute) {
			if r.Key.PeerKind == PeerRemote {
				total += r.BytesRx + r.BytesTx
			} else {
				total += r.BytesTx
			}
		}
	}
	if total != 3*6000 {
		t.Errorf("network total %v, want %v", total, 3*6000)
	}
}

func TestTransitAndImplausibleDurations(t *testing.T) {
	if rows := Contributions(mk(flow.DirectionTransit, t0, t0), time.Minute); rows != nil {
		t.Errorf("transit %+v", rows)
	}
	rows := Contributions(mk(flow.DirectionOutbound, t0.Add(-48*time.Hour), t0), time.Minute)
	if len(rows) != 1 || !rows[0].Key.Bucket.Equal(t0) {
		t.Errorf("a two-day Flow is not spread: %d rows", len(rows))
	}
}

func TestAggregate(t *testing.T) {
	a := Contributions(mk(flow.DirectionOutbound, t0, t0), time.Minute)
	b := Contributions(mk(flow.DirectionOutbound, t0.Add(10*time.Second), t0.Add(10*time.Second)), time.Minute)
	agg := Aggregate(append(a, b...))
	if len(agg) != 1 {
		t.Fatalf("%d keys", len(agg))
	}
	for _, m := range agg {
		if m.BytesTx != 12000 || m.Flows != 2 {
			t.Errorf("%+v", m)
		}
	}
}

func TestPeerGeoIsKeptForRemotePeersOnly(t *testing.T) {
	f := mk(flow.DirectionOutbound, t0, t0)
	f.PeerCountry, f.PeerASN = "AU", 13335
	if rows := Contributions(f, time.Minute); rows[0].Key.PeerCountry != "AU" || rows[0].Key.PeerASN != 13335 {
		t.Errorf("remote %+v", rows[0].Key)
	}
	// An Internal Flow never carries a country, even if one were set.
	f.Direction, f.Peer = flow.DirectionInternal, netip.MustParseAddr("192.168.10.50")
	for _, r := range Contributions(f, time.Minute) {
		if r.Key.PeerCountry != "" || r.Key.PeerASN != 0 {
			t.Errorf("internal %+v", r.Key)
		}
	}
}
