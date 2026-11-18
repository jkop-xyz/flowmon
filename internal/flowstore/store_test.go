package flowstore

import (
	"context"
	"math"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

var (
	ctx      = context.Background()
	exporter = netip.MustParseAddr("192.168.10.1")
	t0       = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "flows.duckdb"), Options{MemoryLimit: "256MB", Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mkFlow(dir flow.Direction, start, end time.Time, bytes uint64) flow.Flow {
	return flow.Flow{
		Exporter: exporter, Received: end, ExportTime: end, Start: start, End: end,
		Src: netip.MustParseAddr("192.168.10.44"), Dst: netip.MustParseAddr("1.1.1.1"),
		SrcPort: 50000, DstPort: 443, Protocol: 6, Bytes: bytes, Packets: 1,
		Direction: dir, Local: netip.MustParseAddr("192.168.10.44"), Peer: netip.MustParseAddr("1.1.1.1"),
		LocalPort: 50000, PeerPort: 443, Fields: []byte{0},
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.duckdb")
	for i := 0; i < 2; i++ {
		s, err := Open(ctx, path, Options{})
		if err != nil {
			t.Fatal(err)
		}
		v, err := s.SchemaVersion(ctx)
		if err != nil || v != len(migrations) {
			t.Errorf("open %d: version %d, %v", i, v, err)
		}
		s.Close()
	}
}

func TestDayTablesSplitByEndTime(t *testing.T) {
	s := open(t)
	midnight := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	err := s.Write(ctx, []flow.Flow{
		mkFlow(flow.DirectionOutbound, midnight.Add(-time.Minute), midnight.Add(-30*time.Second), 100),
		mkFlow(flow.DirectionOutbound, midnight.Add(-30*time.Second), midnight.Add(30*time.Second), 100),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := s.tablesFor(midnight.Add(-time.Hour), midnight.Add(time.Hour)); len(got) != 2 || got[0] != "flows_20261118" || got[1] != "flows_20261119" {
		t.Errorf("tables %v", got)
	}

	// Reopening finds the day tables again.
	path := s.path
	s.Close()
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if got := s2.tablesFor(midnight.Add(-time.Hour), midnight.Add(time.Hour)); len(got) != 2 {
		t.Errorf("after reopen: %v", got)
	}
}

func TestBandwidthSpreadsBytesOverDuration(t *testing.T) {
	s := open(t)
	// 60 s outbound Flow of 6,000,000 bytes: 100 kB/s, i.e. 800 kbit/s in every second.
	if err := s.Write(ctx, []flow.Flow{
		mkFlow(flow.DirectionOutbound, t0, t0.Add(60*time.Second), 6_000_000),
		mkFlow(flow.DirectionInbound, t0.Add(30*time.Second), t0.Add(30*time.Second), 1_000), // zero duration
		mkFlow(flow.DirectionTransit, t0, t0.Add(60*time.Second), 999_999),                   // never counted
	}); err != nil {
		t.Fatal(err)
	}

	res, err := s.Bandwidth(ctx, BandwidthQuery{From: t0, To: t0.Add(2 * time.Minute), MaxPoints: 12})
	if err != nil {
		t.Fatal(err)
	}
	if res.Step != 10*time.Second || len(res.Points) != 12 {
		t.Fatalf("step %v, %d points", res.Step, len(res.Points))
	}
	var totalOut, totalIn float64
	for i, p := range res.Points {
		totalOut += p.OutboundBps * res.StepS / 8
		totalIn += p.InboundBps * res.StepS / 8
		if i < 6 && math.Abs(p.OutboundBps-800_000) > 1 {
			t.Errorf("bucket %d: %.0f bps, want 800000", i, p.OutboundBps)
		}
		if i >= 6 && p.OutboundBps != 0 {
			t.Errorf("bucket %d after the Flow ended: %.0f bps", i, p.OutboundBps)
		}
	}
	if math.Abs(totalOut-6_000_000) > 1 || math.Abs(totalIn-1_000) > 0.001 {
		t.Errorf("totals out %.1f in %.1f", totalOut, totalIn)
	}
	if res.Points[3].InboundBps == 0 {
		t.Error("the zero-duration Flow belongs in the bucket of its end time")
	}
}

func TestBandwidthClipsToRange(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, t0, t0.Add(100*time.Second), 1_000_000)}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Bandwidth(ctx, BandwidthQuery{From: t0.Add(50 * time.Second), To: t0.Add(60 * time.Second), MaxPoints: 1})
	if err != nil {
		t.Fatal(err)
	}
	// 10 of the 100 seconds fall in range: 100,000 bytes over 10 s.
	if len(res.Points) != 1 || math.Abs(res.Points[0].OutboundBps-80_000) > 1 {
		t.Errorf("points %+v", res.Points)
	}
}

func TestBandwidthExporterFilterAndEmpty(t *testing.T) {
	s := open(t)
	res, err := s.Bandwidth(ctx, BandwidthQuery{From: t0, To: t0.Add(time.Hour)})
	if err != nil || len(res.Points) == 0 {
		t.Fatalf("empty store: %v %v", res, err)
	}
	if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, t0, t0.Add(time.Second), 1000)}); err != nil {
		t.Fatal(err)
	}
	res, err = s.Bandwidth(ctx, BandwidthQuery{From: t0, To: t0.Add(time.Minute), Exporter: netip.MustParseAddr("10.9.9.9")})
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range res.Points {
		if p.OutboundBps != 0 {
			t.Error("another Exporter's traffic leaked through the filter")
		}
	}
}

func TestInvalidRanges(t *testing.T) {
	s := open(t)
	for _, q := range []BandwidthQuery{{From: t0, To: t0}, {From: t0, To: t0.Add(800 * 24 * time.Hour)}} {
		if _, err := s.Bandwidth(ctx, q); err != ErrInvalidRange {
			t.Errorf("%v → %v", q, err)
		}
	}
}

func TestStepFor(t *testing.T) {
	cases := map[time.Duration]time.Duration{
		15 * time.Minute:   10 * time.Second,
		time.Hour:          15 * time.Second,
		24 * time.Hour:     5 * time.Minute,
		7 * 24 * time.Hour: time.Hour,
	}
	for rng, want := range cases {
		if got := StepFor(rng, 300); got != want {
			t.Errorf("StepFor(%v) = %v, want %v", rng, got, want)
		}
	}
}

func TestExporterRecords(t *testing.T) {
	s := open(t)
	r := ExporterRecord{Address: exporter, Status: "approved", Role: "edge", ClassificationMode: "address", Name: "MikroTik", DecidedAt: t0, DecidedBy: "admin"}
	if err := s.SaveExporterRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	r.Name = "hEX"
	if err := s.SaveExporterRecord(ctx, r); err != nil {
		t.Fatal(err)
	}
	got, err := s.ExporterRecords(ctx)
	if err != nil || len(got) != 1 || got[0] != r {
		t.Fatalf("%+v %v", got, err)
	}
	if err := s.DeleteExporterRecord(ctx, exporter); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.ExporterRecords(ctx); len(got) != 0 {
		t.Errorf("after delete: %+v", got)
	}
}
