package flowstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jkop-xyz/flowmon/internal/series"
)

// Device Health (ADR 0005) is each SNMP device's readings per minute (health_minute)
// and per hour (health_hour: total and count for the mean, and the extremes), under
// the Rollups' retentions. snmp_boots holds each device's boot time, so a restart is
// noticed even across Flowmon restarts; snmp_restarts lists them. snmp_wifi_clients is
// each device's Wi-Fi registrations as last polled.

// bootJitter is how far a boot time computed from two polls may differ without a
// restart: the uptime and the poll's clock aren't read at the same instant.
const bootJitter = 2 * time.Minute

// uptimeWrap is when a TimeTicks uptime (32 bits of 1/100 s) wraps: 497 days, which
// moves the computed boot time forward without a restart.
const uptimeWrap = time.Duration(1<<32) * 10 * time.Millisecond

// WriteHealth records a poll's readings in its minute and hour, and notes a restart
// when the device's boot time moved forward (series.HealthWriter).
func (s *Store) WriteHealth(ctx context.Context, h series.Health) (time.Time, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return time.Time{}, err
	}
	defer func() { _ = tx.Rollback() }()
	minute, hour := h.At.UTC().Truncate(time.Minute), h.At.UTC().Truncate(time.Hour)
	// A second poll in the same minute (polls drift) is dropped, so the minute and the
	// hour's mean count it once.
	var already int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM health_minute WHERE bucket = ? AND source = ?`, minute, h.Source).Scan(&already); err != nil {
		return time.Time{}, err
	}
	for name, v := range h.Metrics {
		if already > 0 {
			break
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO health_minute VALUES (?, ?, ?, ?)`, minute, h.Source, name, v); err != nil {
			return time.Time{}, fmt.Errorf("saving health: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO health_hour VALUES (?, ?, ?, ?, 1, ?, ?)
			ON CONFLICT (bucket, source, metric) DO UPDATE SET total = health_hour.total + excluded.total, n = health_hour.n + 1,
				lo = least(health_hour.lo, excluded.lo), hi = greatest(health_hour.hi, excluded.hi)`, hour, h.Source, name, v, v, v); err != nil {
			return time.Time{}, fmt.Errorf("saving health: %w", err)
		}
	}
	restart, err := noteBoot(ctx, tx, h.Source, h.Boot.UTC())
	if err != nil {
		return time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM snmp_wifi_clients WHERE source = ?`, h.Source); err != nil {
		return time.Time{}, err
	}
	for _, c := range h.Wifi {
		if _, err := tx.ExecContext(ctx, `INSERT INTO snmp_wifi_clients VALUES (?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT DO NOTHING`,
			h.Source, c.MAC, c.IfIndex, c.Signal, c.TxRate, c.RxRate, c.Connected.Seconds(), h.At.UTC()); err != nil {
			return time.Time{}, fmt.Errorf("saving Wi-Fi clients: %w", err)
		}
	}
	return restart, tx.Commit()
}

// noteBoot compares a poll's boot time with the stored one, and records a restart when
// it moved forward by more than jitter and an uptime wrap.
func noteBoot(ctx context.Context, tx *sql.Tx, source string, boot time.Time) (time.Time, error) {
	if boot.IsZero() {
		return time.Time{}, nil
	}
	var prev time.Time
	err := tx.QueryRowContext(ctx, `SELECT boot_at FROM snmp_boots WHERE source = ?`, source).Scan(&prev)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		_, err = tx.ExecContext(ctx, `INSERT INTO snmp_boots VALUES (?, ?)`, source, boot)
		return time.Time{}, err
	case err != nil:
		return time.Time{}, err
	}
	prev = prev.UTC()
	d := boot.Sub(prev)
	switch {
	case d > bootJitter && !isWrap(d):
		if _, err := tx.ExecContext(ctx, `INSERT INTO snmp_restarts VALUES (?, ?) ON CONFLICT DO NOTHING`, source, boot); err != nil {
			return time.Time{}, err
		}
		_, err := tx.ExecContext(ctx, `UPDATE snmp_boots SET boot_at = ? WHERE source = ?`, boot, source)
		return boot, err
	case d < -bootJitter:
		// The clock went back, or the device's did: follow it without a restart.
		_, err := tx.ExecContext(ctx, `UPDATE snmp_boots SET boot_at = ? WHERE source = ?`, boot, source)
		return time.Time{}, err
	}
	return time.Time{}, nil
}

func isWrap(d time.Duration) bool {
	if d < uptimeWrap-bootJitter {
		return false
	}
	r := d % uptimeWrap
	return r < bootJitter || r > uptimeWrap-bootJitter
}

// HealthNow is a device's latest readings.
type HealthNow struct {
	At      time.Time          `json:"at"`
	Metrics map[string]float64 `json:"metrics"`
	// Boot is when the device last started, as far as polling knows.
	Boot *time.Time `json:"boot,omitempty"`
}

// LatestHealth returns each device's latest minute of readings ("" for every device's).
func (s *Store) LatestHealth(ctx context.Context, source string) (map[string]HealthNow, error) {
	q := `SELECT h.source, h.bucket, h.metric, h.value, b.boot_at FROM health_minute h
		JOIN (SELECT source, max(bucket) AS bucket FROM health_minute GROUP BY source) l USING (source, bucket)
		LEFT JOIN snmp_boots b ON b.source = h.source`
	var args []any
	if source != "" {
		q += ` WHERE h.source = ?`
		args = append(args, source)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]HealthNow{}
	for rows.Next() {
		var src, metric string
		var at time.Time
		var v float64
		var boot sql.NullTime
		if err := rows.Scan(&src, &at, &metric, &v, &boot); err != nil {
			return nil, err
		}
		n, ok := out[src]
		if !ok {
			n = HealthNow{At: at.UTC(), Metrics: map[string]float64{}}
			if boot.Valid {
				b := boot.Time.UTC()
				n.Boot = &b
			}
		}
		n.Metrics[metric] = v
		out[src] = n
	}
	return out, rows.Err()
}

// HealthPoint is one bucket of a device's readings: each the mean over the bucket.
type HealthPoint struct {
	T       time.Time          `json:"t"`
	Metrics map[string]float64 `json:"metrics"`
}

// HealthSeries returns a device's readings over [from, to): minute buckets for ranges
// under a day, hour buckets otherwise. Buckets without a poll are left out.
func (s *Store) HealthSeries(ctx context.Context, source string, from, to time.Time) ([]HealthPoint, error) {
	if !to.After(from) || to.Sub(from) > MaxRange {
		return nil, ErrInvalidRange
	}
	q := `SELECT bucket, metric, value FROM health_minute WHERE source = ? AND bucket >= ? AND bucket < ?`
	g := time.Minute
	if to.Sub(from) >= 24*time.Hour {
		q, g = `SELECT bucket, metric, total / n FROM health_hour WHERE source = ? AND bucket >= ? AND bucket < ?`, time.Hour
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY bucket`, source, from.UTC().Truncate(g), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []HealthPoint{}
	for rows.Next() {
		var t time.Time
		var metric string
		var v float64
		if err := rows.Scan(&t, &metric, &v); err != nil {
			return nil, err
		}
		t = t.UTC()
		if len(out) == 0 || !out[len(out)-1].T.Equal(t) {
			out = append(out, HealthPoint{T: t, Metrics: map[string]float64{}})
		}
		out[len(out)-1].Metrics[metric] = v
	}
	return out, rows.Err()
}

// Restarts returns when a device restarted in [from, to), oldest first.
func (s *Store) Restarts(ctx context.Context, source string, from, to time.Time) ([]time.Time, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT restarted_at FROM snmp_restarts WHERE source = ? AND restarted_at >= ? AND restarted_at < ? ORDER BY restarted_at`,
		source, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []time.Time{}
	for rows.Next() {
		var t time.Time
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t.UTC())
	}
	return out, rows.Err()
}

// WifiClient is a station registered to an SNMP device as last polled.
type WifiClient struct {
	MAC       string `json:"mac"`
	IfIndex   uint32 `json:"ifIndex"`
	Interface string `json:"interface,omitempty"`
	// Signal is in dBm; rates in bits per second.
	Signal           int       `json:"signal"`
	TxRate           uint64    `json:"txRate"`
	RxRate           uint64    `json:"rxRate"`
	ConnectedSeconds float64   `json:"connectedSeconds"`
	Seen             time.Time `json:"seen"`
}

// WifiClients returns a device's Wi-Fi registrations, strongest signal first.
func (s *Store) WifiClients(ctx context.Context, source string) ([]WifiClient, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT c.mac, c.if_index, coalesce(i.name, ''), c.signal, c.tx_rate, c.rx_rate, c.connected_seconds, c.seen
		FROM snmp_wifi_clients c LEFT JOIN snmp_interfaces i USING (source, if_index) WHERE c.source = ?`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []WifiClient{}
	for rows.Next() {
		var c WifiClient
		if err := rows.Scan(&c.MAC, &c.IfIndex, &c.Interface, &c.Signal, &c.TxRate, &c.RxRate, &c.ConnectedSeconds, &c.Seen); err != nil {
			return nil, err
		}
		c.Seen = c.Seen.UTC()
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Signal > out[j].Signal })
	return out, rows.Err()
}

// applyHealthRetention drops readings older than the minute and hour retentions, and
// restarts older than the hour retention.
func (s *Store) applyHealthRetention(ctx context.Context, now time.Time, r Retention) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, d := range []struct {
		q    string
		keep time.Duration
	}{
		{`DELETE FROM health_minute WHERE bucket < ?`, r.Minute},
		{`DELETE FROM health_hour WHERE bucket < ?`, r.Hour},
		{`DELETE FROM snmp_restarts WHERE restarted_at < ?`, r.Hour},
	} {
		if _, err := s.db.ExecContext(ctx, d.q, now.Add(-d.keep).UTC()); err != nil {
			return fmt.Errorf("health retention: %w", err)
		}
	}
	return nil
}

var _ series.HealthWriter = (*Store)(nil)
