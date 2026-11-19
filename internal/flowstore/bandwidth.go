package flowstore

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// MaxRawRange is the longest range raw-Flow queries accept. Longer ranges use Rollups.
const MaxRawRange = 8 * 24 * time.Hour

// MaxRange is the longest range any query accepts: the default hour Rollup retention.
const MaxRange = 731 * 24 * time.Hour

// Source names the store a query was answered from.
type Source string

// Sources, from finest to coarsest.
const (
	SourceRaw    Source = "raw"
	SourceMinute Source = "minute"
	SourceHour   Source = "hour"
)

// sourceForStep picks the coarsest store whose buckets are no larger than step.
func sourceForStep(step time.Duration) Source {
	switch {
	case step < time.Minute:
		return SourceRaw
	case step < time.Hour:
		return SourceMinute
	}
	return SourceHour
}

// BandwidthQuery asks for traffic over time.
type BandwidthQuery struct {
	From, To time.Time
	// Exporter restricts the query to one Exporter; invalid means all Exporters.
	Exporter netip.Addr
	// MaxPoints bounds the number of buckets; the step is chosen to fit. 0 means 300.
	MaxPoints int
}

// BandwidthPoint is one time bucket. Rates are bits per second over the bucket.
type BandwidthPoint struct {
	Time        time.Time `json:"t"`
	InboundBps  float64   `json:"inboundBps"`
	OutboundBps float64   `json:"outboundBps"`
	InternalBps float64   `json:"internalBps"`
	// EstimatedBps is the part of the three that was scaled up from sampled Flows.
	EstimatedBps float64 `json:"estimatedBps,omitempty"`
}

// BandwidthResult is the series for a BandwidthQuery.
type BandwidthResult struct {
	From   time.Time        `json:"from"`
	To     time.Time        `json:"to"`
	Step   time.Duration    `json:"-"`
	StepS  float64          `json:"stepSeconds"`
	Source Source           `json:"source"`
	Points []BandwidthPoint `json:"points"`
	// EstimatedBytes is how many of the bytes shown were scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes"`
}

// niceSteps are the bucket sizes the Overview can use.
var niceSteps = []time.Duration{
	10 * time.Second, 15 * time.Second, 30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute,
	10 * time.Minute, 15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 3 * time.Hour,
	6 * time.Hour, 12 * time.Hour, 24 * time.Hour,
}

// StepFor picks the smallest nice step that keeps a range within maxPoints buckets.
func StepFor(rng time.Duration, maxPoints int) time.Duration {
	for _, s := range niceSteps {
		if rng/s <= time.Duration(maxPoints) {
			return s
		}
	}
	return niceSteps[len(niceSteps)-1]
}

// Bandwidth returns Network Totals per time bucket: inbound (received from Remote IPs),
// outbound (sent to Remote IPs) and internal (sent to Local Hosts). Each Flow's bytes are
// spread evenly over its duration, so a long download doesn't show up as a single spike
// when its record arrives. Short steps are answered from raw Flows, longer ones from
// minute or hour Rollups.
func (s *Store) Bandwidth(ctx context.Context, q BandwidthQuery) (BandwidthResult, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return BandwidthResult{}, ErrInvalidRange
	}
	if q.MaxPoints <= 0 {
		q.MaxPoints = 300
	}
	step := StepFor(q.To.Sub(q.From), q.MaxPoints)
	source := sourceForStep(step)
	if source == SourceRaw && q.To.Sub(q.From) > MaxRawRange {
		source = SourceMinute
		step = time.Minute
	}
	from := q.From.UTC().Truncate(step)
	to := q.To.UTC()

	res := BandwidthResult{From: from, To: to, Step: step, StepS: step.Seconds(), Source: source}
	buckets := map[int64]int{} // bucket start (Unix seconds) → index in res.Points
	for t := from; t.Before(to); t = t.Add(step) {
		buckets[t.Unix()] = len(res.Points)
		res.Points = append(res.Points, BandwidthPoint{Time: t})
	}

	if source != SourceRaw {
		return res, s.bandwidthFromRollups(ctx, &res, buckets, q.Exporter)
	}

	tables := s.tablesFor(from, to)
	if len(tables) == 0 {
		return res, nil
	}

	where := `end_ts >= $1 AND start_ts < $2 AND direction IN (1, 2, 3)`
	args := []any{from, to}
	if q.Exporter.IsValid() {
		where += ` AND exporter = $3`
		args = append(args, q.Exporter.String())
	}
	var parts []string
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT start_ts, end_ts, bytes, direction, coalesce(sampling_rate, 0) > 1 AS sampled FROM %s WHERE %s`, t, where))
	}
	stepSQL := fmt.Sprintf("INTERVAL %d SECOND", int64(step.Seconds()))

	// For every Flow, one row per bucket it overlaps, carrying the share of its bytes that
	// falls in that bucket. Flows with no duration put all their bytes in their end bucket.
	sql := fmt.Sprintf(`
WITH f AS (%[1]s),
b AS (
	SELECT direction, bytes, sampled, start_ts, end_ts,
		unnest(generate_series(
			time_bucket(%[2]s, greatest(start_ts, $1::TIMESTAMP)),
			least(end_ts, $2::TIMESTAMP - INTERVAL 1 MICROSECOND),
			%[2]s)) AS bucket
	FROM f
),
s AS (
	SELECT bucket, direction, sampled,
		CASE WHEN end_ts <= start_ts THEN bytes::DOUBLE
		ELSE bytes::DOUBLE
			* (epoch_us(least(end_ts, bucket + %[2]s)) - epoch_us(greatest(start_ts, bucket)))
			/ (epoch_us(end_ts) - epoch_us(start_ts))
		END AS bytes
	FROM b
)
SELECT bucket, direction, sum(bytes), sum(CASE WHEN sampled THEN bytes ELSE 0 END)
FROM s
GROUP BY bucket, direction`, strings.Join(parts, "\nUNION ALL\n"), stepSQL)

	rows, err := s.db.QueryContext(ctx, sql, args...)
	if err != nil {
		return BandwidthResult{}, fmt.Errorf("bandwidth query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var bucket time.Time
		var direction uint8
		var bytes, estimated float64
		if err := rows.Scan(&bucket, &direction, &bytes, &estimated); err != nil {
			return BandwidthResult{}, err
		}
		i, ok := buckets[bucket.UTC().Unix()]
		if !ok {
			continue
		}
		p := &res.Points[i]
		bps := bytes * 8 / step.Seconds()
		if estimated > 0 {
			p.EstimatedBps += estimated * 8 / step.Seconds()
			res.EstimatedBytes += estimated
		}
		switch flow.Direction(direction) {
		case flow.DirectionInbound:
			p.InboundBps += bps
		case flow.DirectionOutbound:
			p.OutboundBps += bps
		case flow.DirectionInternal:
			p.InternalBps += bps
		}
	}
	return res, rows.Err()
}

func (s *Store) bandwidthFromRollups(ctx context.Context, res *BandwidthResult, buckets map[int64]int, exporter netip.Addr) error {
	g := Minute
	if res.Source == SourceHour {
		g = Hour
	}
	tables := s.rollupTablesFor(g, res.From, res.To)
	if len(tables) == 0 {
		return nil
	}
	where := `bucket >= $1 AND bucket < $2`
	args := []any{res.From, res.To}
	if exporter.IsValid() {
		where += ` AND exporter = $3`
		args = append(args, exporter.String())
	}
	var parts []string
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT bucket, peer_kind, bytes_rx, bytes_tx, estimated_rx, estimated_tx FROM %s WHERE %s`, t, where))
	}
	query := fmt.Sprintf(`
SELECT time_bucket(INTERVAL %d SECOND, bucket) AS b,
	sum(CASE WHEN peer_kind = 1 THEN bytes_rx ELSE 0 END),
	sum(CASE WHEN peer_kind = 1 THEN bytes_tx ELSE 0 END),
	sum(CASE WHEN peer_kind = 2 THEN bytes_tx ELSE 0 END),
	sum(CASE WHEN peer_kind = 1 THEN estimated_rx + estimated_tx WHEN peer_kind = 2 THEN estimated_tx ELSE 0 END)
FROM (%s) GROUP BY b`, int64(res.Step.Seconds()), strings.Join(parts, "\nUNION ALL\n"))

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("bandwidth query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var b time.Time
		var in, out, internal, estimated float64
		if err := rows.Scan(&b, &in, &out, &internal, &estimated); err != nil {
			return err
		}
		i, ok := buckets[b.UTC().Unix()]
		if !ok {
			continue
		}
		perSecond := 8 / res.Step.Seconds()
		res.Points[i].InboundBps += in * perSecond
		res.Points[i].OutboundBps += out * perSecond
		res.Points[i].InternalBps += internal * perSecond
		res.Points[i].EstimatedBps += estimated * perSecond
		res.EstimatedBytes += estimated
	}
	return rows.Err()
}
