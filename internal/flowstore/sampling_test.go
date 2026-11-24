package flowstore

import (
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func sampledFlow(dir flow.Direction, at time.Time, bytes uint64, rate uint32) flow.Flow {
	f := mkFlow(dir, at, at, bytes)
	f.SamplingRate = rate
	return f
}

func TestSamplingEstimatesAreKept(t *testing.T) {
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{
		sampledFlow(flow.DirectionOutbound, t0, 100_000, 100), // an estimate, scaled at ingest
		mkFlow(flow.DirectionInbound, t0, t0, 500),
	}); err != nil {
		t.Fatal(err)
	}
	from, to := t0.Add(-30*time.Minute), t0.Add(30*time.Minute)

	// Rollups: the Overview's top lists and bandwidth.
	for _, d := range []TopDimension{TopLocalHosts, TopProtocols} {
		res, err := s.Top(ctx, TopQuery{Dimension: d, From: from, To: to})
		if err != nil {
			t.Fatal(err)
		}
		if res.EstimatedBytes != 100_000 || res.TotalBytes != 100_500 {
			t.Errorf("%s: estimated %v of %v", d, res.EstimatedBytes, res.TotalBytes)
		}
	}
	bw, err := s.Bandwidth(ctx, BandwidthQuery{From: t0.Add(-12 * time.Hour), To: t0.Add(12 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if bw.Source == SourceRaw || bw.EstimatedBytes != 100_000 {
		t.Errorf("bandwidth from %s: estimated %v", bw.Source, bw.EstimatedBytes)
	}
	raw, _ := s.Bandwidth(ctx, BandwidthQuery{From: from, To: to})
	if raw.Source != SourceRaw || raw.EstimatedBytes != 100_000 {
		t.Errorf("bandwidth from %s: estimated %v", raw.Source, raw.EstimatedBytes)
	}

	// Remote IPs, from raw Flows and from built lists.
	remotes, err := s.TopRemoteIPs(ctx, RemoteQuery{From: from, To: to, Now: t0.Add(time.Hour)})
	if err != nil || remotes.EstimatedBytes != 100_000 || remotes.Items[0].EstimatedBytes != 100_000 {
		t.Errorf("remote IPs: %+v %v", remotes, err)
	}
	if _, err := s.BuildTopRemote(ctx, t0.Add(48*time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}
	var est float64
	if err := s.db.QueryRowContext(ctx, `SELECT sum(estimated_rx + estimated_tx) FROM top_remote`).Scan(&est); err != nil || est != 2*100_000 {
		t.Errorf("Top Remote IPs lists (hour and day) estimate %v %v", est, err)
	}

	// Compaction keeps the estimate.
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if res, _ := s.Top(ctx, TopQuery{Dimension: TopLocalHosts, From: from, To: to}); res.EstimatedBytes != 100_000 {
		t.Errorf("after compaction: %v", res.EstimatedBytes)
	}

	// Raw Flows keep their rate, and reprocessing doesn't lose it.
	if _, _, err := s.Reprocess(ctx, exporter, t0, func(*flow.Flow) {}, nil); err != nil {
		t.Fatal(err)
	}
	page, err := s.Flows(ctx, FlowQuery{From: from, To: to, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	rates := map[uint32]int{}
	for _, it := range page.Items {
		rates[it.SamplingRate]++
	}
	if rates[100] != 1 || rates[0] != 1 {
		t.Errorf("rates %v", rates)
	}
	if res, _ := s.Top(ctx, TopQuery{Dimension: TopLocalHosts, From: from, To: to}); res.EstimatedBytes != 100_000 {
		t.Errorf("after reprocessing: %v", res.EstimatedBytes)
	}
}

func TestSamplingColumnsAddedToExistingTables(t *testing.T) {
	// A store as schema 11 left it.
	s := open(t)
	if err := s.Write(ctx, []flow.Flow{mkFlow(flow.DirectionOutbound, t0, t0, 100)}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`ALTER TABLE flows_20261118 DROP COLUMN domain`, // the shape before migrations 16 and 17
		`ALTER TABLE flows_20261118 DROP COLUMN local_mac`,
		`ALTER TABLE flows_20261118 DROP COLUMN peer_mac`,
		`ALTER TABLE rollup_minute_20261118 DROP COLUMN local_mac`,
		`ALTER TABLE flows_20261118 DROP COLUMN sampling_rate`,
		`ALTER TABLE rollup_minute_20261118 DROP COLUMN estimated_rx`,
		`ALTER TABLE rollup_minute_20261118 DROP COLUMN estimated_tx`,
		`ALTER TABLE top_remote DROP COLUMN estimated_rx`,
		`ALTER TABLE top_remote DROP COLUMN estimated_tx`,
		`DELETE FROM flowmon_migrations WHERE version >= 12`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(q, err)
		}
	}
	path := s.path
	s.Close()
	s2, err := Open(ctx, path, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if err := s2.Write(ctx, []flow.Flow{sampledFlow(flow.DirectionOutbound, t0, 1000, 10)}); err != nil {
		t.Fatal(err)
	}
	if err := s2.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	res, err := s2.Top(ctx, TopQuery{Dimension: TopLocalHosts, From: t0.Add(-time.Hour), To: t0.Add(time.Hour)})
	if err != nil || res.EstimatedBytes != 1000 || res.TotalBytes != 1100 {
		t.Errorf("%+v %v", res, err)
	}
}
