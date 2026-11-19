package flowstore

import (
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func internalFlow(start, end time.Time, bytes uint64) flow.Flow {
	f := mkFlow(flow.DirectionInternal, start, end, bytes)
	f.Dst, f.Peer, f.PeerPort = netip.MustParseAddr("10.0.20.14"), netip.MustParseAddr("10.0.20.14"), 22
	return f
}

func (s *Store) rollupSum(t *testing.T, table, col string) (float64, int) {
	t.Helper()
	var sum float64
	var rows int
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(sum(`+col+`), 0), count(*) FROM `+table).Scan(&sum, &rows); err != nil {
		t.Fatal(err)
	}
	return sum, rows
}

func TestRollupsAreWrittenWithRawFlows(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{
		mkFlow(flow.DirectionOutbound, t0, t0.Add(2*time.Minute), 1200),
		mkFlow(flow.DirectionInbound, t0, t0, 500),
		mkFlow(flow.DirectionTransit, t0, t0, 9999),
	}); err != nil {
		t.Fatal(err)
	}
	tx, _ := s.rollupSum(t, "rollup_minute_20261118", "bytes_tx")
	rx, _ := s.rollupSum(t, "rollup_minute_20261118", "bytes_rx")
	htx, _ := s.rollupSum(t, "rollup_hour_202611", "bytes_tx")
	if math.Abs(tx-1200) > 1e-6 || rx != 500 || math.Abs(htx-1200) > 1e-6 {
		t.Errorf("minute tx %v rx %v, hour tx %v (transit must not count)", tx, rx, htx)
	}
}

func TestCompactionMergesPartialRowsAndKeepsTotals(t *testing.T) {
	s := open(t)
	for i := 0; i < 5; i++ { // five write batches touching the same keys
		if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, t0, t0, 100)}); err != nil {
			t.Fatal(err)
		}
	}
	before, rowsBefore := s.rollupSum(t, "rollup_minute_20261118", "bytes_tx")
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	after, rowsAfter := s.rollupSum(t, "rollup_minute_20261118", "bytes_tx")
	if rowsBefore != 5 || rowsAfter != 1 || before != after || after != 500 {
		t.Errorf("rows %d → %d, sum %v → %v", rowsBefore, rowsAfter, before, after)
	}
	var flows uint64
	if err := s.db.QueryRowContext(ctx, `SELECT flows FROM rollup_minute_20261118`).Scan(&flows); err != nil || flows != 5 {
		t.Errorf("flows %d %v", flows, err)
	}
	// Late Flows for the same minute are added after compaction too.
	if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, t0, t0, 100)}); err != nil {
		t.Fatal(err)
	}
	if sum, _ := s.rollupSum(t, "rollup_minute_20261118", "bytes_tx"); sum != 600 {
		t.Errorf("after a late Flow: %v", sum)
	}
}

func TestBandwidthPicksTheCoarsestStore(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{
		mkFlow(flow.DirectionOutbound, t0, t0.Add(10*time.Minute), 6_000_000),
		internalFlow(t0, t0, 1000),
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		rng  time.Duration
		want Source
	}{
		{time.Hour, SourceRaw},
		{24 * time.Hour, SourceMinute},
		{30 * 24 * time.Hour, SourceHour},
	}
	for _, c := range cases {
		res, err := s.Bandwidth(ctx, BandwidthQuery{From: t0.Add(-time.Minute), To: t0.Add(-time.Minute).Add(c.rng)})
		if err != nil {
			t.Fatal(err)
		}
		var out, internal float64
		for _, p := range res.Points {
			out += p.OutboundBps * res.StepS / 8
			internal += p.InternalBps * res.StepS / 8
		}
		if res.Source != c.want || math.Abs(out-6_000_000) > 1 || math.Abs(internal-1000) > 1e-6 {
			t.Errorf("%v: source %s, out %.1f internal %.1f", c.rng, res.Source, out, internal)
		}
	}
}

func TestTop(t *testing.T) {
	s := open(t)
	vm := mkFlow(flow.DirectionInbound, t0, t0, 9000)
	vm.Local, vm.LocalPort, vm.PeerPort, vm.Protocol = netip.MustParseAddr("10.0.20.15"), 50000, 53, 17
	if err := s.Write(ctx, []flow.Flow{
		mkFlow(flow.DirectionOutbound, t0, t0, 1000),
		mkFlow(flow.DirectionInbound, t0, t0, 4000),
		vm,
		internalFlow(t0, t0, 500),
	}); err != nil {
		t.Fatal(err)
	}
	q := TopQuery{From: t0.Add(-time.Hour), To: t0.Add(time.Hour)}

	q.Dimension = TopLocalHosts
	hosts, err := s.Top(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	// 192.168.10.44: 1000 + 4000 + 500 sent internally; 10.0.20.15: 9000; 10.0.20.14: 500 received.
	if len(hosts.Items) != 3 || hosts.Items[0].Host != "10.0.20.15" || hosts.Items[1].TotalBytes != 5500 || hosts.Source != SourceMinute {
		t.Errorf("hosts %+v", hosts)
	}

	// Count covers every Local Host with traffic, not just the listed ones.
	q.Limit = 1
	if one, _ := s.Top(ctx, q); len(one.Items) != 1 || one.Count != 3 {
		t.Errorf("count with limit 1: %d items, count %d", len(one.Items), one.Count)
	}
	q.Limit = 0

	q.Dimension = TopServicePorts
	ports, err := s.Top(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(ports.Items) != 3 || ports.Items[0].ServicePort != 53 || ports.Items[0].Protocol != 17 || ports.Items[1].ServicePort != 443 || ports.Items[1].TotalBytes != 5000 {
		t.Errorf("ports %+v", ports.Items)
	}
	// Network Total counts the Internal Flow once: 1000 + 4000 + 9000 + 500.
	if ports.TotalBytes != 14500 {
		t.Errorf("network total %v", ports.TotalBytes)
	}

	q.Dimension = TopProtocols
	protos, err := s.Top(ctx, q)
	if err != nil || len(protos.Items) != 2 || protos.Items[0].Protocol != 17 {
		t.Errorf("protocols %+v %v", protos, err)
	}

	q.Dimension = "colours"
	if _, err := s.Top(ctx, q); err != ErrUnknownDimension {
		t.Errorf("unknown dimension: %v", err)
	}
	q.Dimension, q.From, q.To = TopLocalHosts, t0.Add(-48*time.Hour), t0.Add(time.Hour)
	if res, _ := s.Top(ctx, q); res.Source != SourceHour || len(res.Items) != 3 {
		t.Errorf("long range: %+v", res)
	}
}

func TestRetentionDropsWholeTables(t *testing.T) {
	s := open(t)
	old := t0.Add(-10 * 24 * time.Hour)
	if err := s.Write(ctx, []flow.Flow{
		mkFlow(flow.DirectionOutbound, old, old, 100),
		mkFlow(flow.DirectionOutbound, t0, t0, 100),
	}); err != nil {
		t.Fatal(err)
	}
	dropped, err := s.ApplyRetention(ctx, DefaultRetention(), t0, 37*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) != 1 || dropped[0] != "flows_20261108" {
		t.Errorf("dropped %v; only the 10-day-old raw table should go", dropped)
	}
	// Its Rollups stay, so history survives.
	if res, _ := s.Top(ctx, TopQuery{Dimension: TopLocalHosts, From: old.Add(-time.Hour), To: old.Add(time.Hour)}); len(res.Items) != 1 {
		t.Errorf("Rollups of the dropped day: %+v", res)
	}

	short := Retention{Raw: time.Hour, Minute: time.Hour, Hour: 24 * time.Hour}
	dropped, err = s.ApplyRetention(ctx, short, t0.Add(60*24*time.Hour), 37*time.Minute)
	if err != nil || len(dropped) != 4 {
		t.Errorf("dropped %v %v", dropped, err)
	}
	if got := s.rollupTablesFor(Minute, old, t0.Add(time.Hour)); len(got) != 0 {
		t.Errorf("left %v", got)
	}
}

func TestRetentionKeepsTheDayUntilTheWatermarkPassed(t *testing.T) {
	s := open(t)
	day := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, day, day, 1)}); err != nil {
		t.Fatal(err)
	}
	r := Retention{Raw: time.Minute, Minute: 365 * 24 * time.Hour, Hour: 365 * 24 * time.Hour}
	justAfter := time.Date(2026, 11, 11, 0, 10, 0, 0, time.UTC)
	if dropped, _ := s.ApplyRetention(ctx, r, justAfter, 37*time.Minute); len(dropped) != 0 {
		t.Errorf("dropped %v while late Flows could still arrive", dropped)
	}
	if dropped, _ := s.ApplyRetention(ctx, r, justAfter.Add(time.Hour), 37*time.Minute); len(dropped) != 1 {
		t.Errorf("dropped %v after the Watermark passed", dropped)
	}
}

func TestReprocessReclassifiesOneExporter(t *testing.T) {
	s := open(t)
	other := mkFlow(flow.DirectionOutbound, t0, t0, 700)
	other.Exporter = netip.MustParseAddr("10.9.9.9")
	mine := mkFlow(flow.DirectionTransit, t0.Add(-25*time.Hour), t0.Add(-25*time.Hour), 1000) // the day before
	mine2 := mkFlow(flow.DirectionTransit, t0, t0, 2000)
	if err := s.Write(ctx, []flow.Flow{other, mine, mine2}); err != nil {
		t.Fatal(err)
	}
	hostTotal := func(from time.Time) float64 {
		res, err := s.Top(ctx, TopQuery{Dimension: TopLocalHosts, From: from, To: t0.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return res.TotalBytes
	}
	if got := hostTotal(t0.Add(-time.Hour)); got != 700 {
		t.Fatalf("before: %v (transit Flows aren't in Rollups)", got)
	}

	// Reprocess from today: the classifier now says these Flows are outbound.
	n, window, err := s.Reprocess(ctx, exporter, t0, func(f *flow.Flow) { f.Direction = flow.DirectionOutbound }, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || !window.Equal(t0.Truncate(24*time.Hour)) {
		t.Errorf("rewrote %d flows, window %v; only today's Flow of this Exporter", n, window)
	}
	if got := hostTotal(t0.Add(-time.Hour)); got != 2700 {
		t.Errorf("after: %v, want 700 (other Exporter, untouched) + 2000", got)
	}
	if got := hostTotal(t0.Add(-26 * time.Hour)); got != 2700 {
		t.Errorf("yesterday is outside the window and stays as it was: %v", got)
	}

	// Reprocessing again gives the same totals: Rollups were rebuilt, not added to.
	if _, _, err := s.Reprocess(ctx, exporter, t0, func(f *flow.Flow) { f.Direction = flow.DirectionOutbound }, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.Compact(ctx)
	if got := hostTotal(t0.Add(-time.Hour)); got != 2700 {
		t.Errorf("reprocessing twice: %v", got)
	}
	// And the raw rows themselves were rewritten.
	var dir uint8
	if err := s.db.QueryRowContext(ctx, `SELECT direction FROM flows_20261118 WHERE exporter = ?`, exporter.String()).Scan(&dir); err != nil || flow.Direction(dir) != flow.DirectionOutbound {
		t.Errorf("raw direction %v %v", dir, err)
	}
}
