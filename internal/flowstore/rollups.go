package flowstore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sort"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/rollup"
)

// Granularity is a Rollup bucket size.
type Granularity string

// Rollup granularities. Minute Rollups live in one table per UTC day, hour Rollups in
// one table per UTC month, so retention drops whole tables.
const (
	Minute Granularity = "minute"
	Hour   Granularity = "hour"
)

func (g Granularity) bucket() time.Duration {
	if g == Hour {
		return time.Hour
	}
	return time.Minute
}

// period returns the start of the table period that holds bucket t.
func (g Granularity) period(t time.Time) time.Time {
	t = t.UTC()
	if g == Hour {
		return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
	}
	return t.Truncate(24 * time.Hour)
}

// periodEnd returns the end of the period that starts at p.
func (g Granularity) periodEnd(p time.Time) time.Time {
	if g == Hour {
		return p.AddDate(0, 1, 0)
	}
	return p.Add(24 * time.Hour)
}

func (g Granularity) table(p time.Time) string {
	if g == Hour {
		return "rollup_hour_" + p.Format("200601")
	}
	return "rollup_minute_" + p.Format("20060102")
}

const rollupColumns = `(
	bucket TIMESTAMP NOT NULL,
	exporter VARCHAR NOT NULL,
	local_host VARCHAR NOT NULL,
	local_is_exporter_host BOOLEAN NOT NULL,
	peer_kind UTINYINT NOT NULL,
	peer_local_host VARCHAR NOT NULL,
	peer_asn UINTEGER NOT NULL,
	peer_country VARCHAR NOT NULL,
	protocol UTINYINT NOT NULL,
	service_port USMALLINT NOT NULL,
	bytes_rx DOUBLE NOT NULL,
	bytes_tx DOUBLE NOT NULL,
	packets_rx DOUBLE NOT NULL,
	packets_tx DOUBLE NOT NULL,
	flows UBIGINT NOT NULL
)`

// rollupKeyColumns are the grouping columns, in table order.
const rollupKeyColumns = `bucket, exporter, local_host, local_is_exporter_host, peer_kind, peer_local_host, peer_asn, peer_country, protocol, service_port`

func (s *Store) ensureRollup(ctx context.Context, g Granularity, period time.Time) (string, error) {
	name := g.table(period)
	s.mu.Lock()
	exists := s.rollups[name]
	s.mu.Unlock()
	if exists {
		return name, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+name+` `+rollupColumns); err != nil {
		return "", fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO rollup_partitions VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, string(g), period, name); err != nil {
		return "", fmt.Errorf("registering %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.rollups[name] = true
	s.mu.Unlock()
	return name, nil
}

// writeRollups appends the Rollup contributions of flows. Rows are pre-aggregated per
// batch; the same key can appear in many batches, so queries always sum, and Compact
// merges rows later. Because Rollups only add, late Flows are simply added too.
// Contributions to buckets before minBucket are skipped (reprocessing keeps those).
func (s *Store) writeRollups(ctx context.Context, dc driver.Conn, flows []flow.Flow, minBucket time.Time) error {
	for _, g := range []Granularity{Minute, Hour} {
		var rows []rollup.Row
		for i := range flows {
			for _, r := range rollup.Contributions(&flows[i], g.bucket()) {
				if !r.Key.Bucket.Before(minBucket) {
					rows = append(rows, r)
				}
			}
		}
		byTable := map[string][]rollup.Key{}
		agg := rollup.Aggregate(rows)
		for k := range agg {
			name, err := s.ensureRollup(ctx, g, g.period(k.Bucket))
			if err != nil {
				return err
			}
			byTable[name] = append(byTable[name], k)
		}
		names := make([]string, 0, len(byTable))
		for n := range byTable {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, name := range names {
			app, err := duckdb.NewAppenderFromConn(dc, "", name)
			if err != nil {
				return fmt.Errorf("appender for %s: %w", name, err)
			}
			for _, k := range byTable[name] {
				m := agg[k]
				if err := app.AppendRow(k.Bucket, k.Exporter, k.LocalHost, k.LocalIsExporterHost, uint8(k.PeerKind), k.PeerLocalHost,
					k.PeerASN, k.PeerCountry, k.Protocol, k.ServicePort, m.BytesRx, m.BytesTx, m.PacketsRx, m.PacketsTx, m.Flows); err != nil {
					_ = app.Close()
					return fmt.Errorf("appending to %s: %w", name, err)
				}
			}
			if err := app.Close(); err != nil {
				return fmt.Errorf("flushing %s: %w", name, err)
			}
			s.mu.Lock()
			s.dirty[name] = true
			s.mu.Unlock()
		}
	}
	return nil
}

// Compact merges the partial rows each write batch appends, so every key has one row
// per bucket again. It rewrites only tables written to since the last compaction.
func (s *Store) Compact(ctx context.Context) error {
	s.mu.Lock()
	var names []string
	for n := range s.dirty {
		names = append(names, n)
	}
	s.dirty = map[string]bool{}
	s.mu.Unlock()
	sort.Strings(names)

	for _, name := range names {
		if err := s.compactTable(ctx, name); err != nil {
			s.mu.Lock()
			s.dirty[name] = true // try again next time
			s.mu.Unlock()
			return err
		}
	}
	return nil
}

func (s *Store) compactTable(ctx context.Context, name string) error {
	// Writes and compaction exclude each other, so no row appended mid-rewrite is lost.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tmp := name + "_compact"
	stmts := []string{
		`DROP TABLE IF EXISTS ` + tmp,
		`CREATE TABLE ` + tmp + ` ` + rollupColumns,
		`INSERT INTO ` + tmp + ` SELECT ` + rollupKeyColumns + `, sum(bytes_rx), sum(bytes_tx), sum(packets_rx), sum(packets_tx), sum(flows)::UBIGINT
			FROM ` + name + ` GROUP BY ` + rollupKeyColumns,
		`DROP TABLE ` + name,
		`ALTER TABLE ` + tmp + ` RENAME TO ` + name,
	}
	for _, q := range stmts {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("compacting %s: %w", name, err)
		}
	}
	return tx.Commit()
}

// Retention says how long each kind of data is kept.
type Retention struct {
	Raw    time.Duration
	Minute time.Duration
	Hour   time.Duration
}

// DefaultRetention is 7 days of raw Flows, 90 days of minute and 2 years of hour Rollups.
func DefaultRetention() Retention {
	return Retention{Raw: 7 * 24 * time.Hour, Minute: 90 * 24 * time.Hour, Hour: 730 * 24 * time.Hour}
}

// ApplyRetention drops every table whose whole period is older than its retention.
// Rollups are written in the same batch as raw Flows, so a raw day table never holds
// Flows that haven't been rolled up; it is still kept until its day is more than
// safeAfter in the past (the largest Watermark), so late Flows aren't dropped with it.
func (s *Store) ApplyRetention(ctx context.Context, r Retention, now time.Time, safeAfter time.Duration) ([]string, error) {
	type victim struct{ name, catalog, key string }
	var victims []victim

	s.mu.Lock()
	for name := range s.days {
		day, err := time.Parse("20060102", name[len("flows_"):])
		if err == nil {
			end := day.Add(24 * time.Hour)
			if end.Before(now.Add(-r.Raw)) && end.Before(now.Add(-safeAfter)) {
				victims = append(victims, victim{name, "flow_days", "table_name"})
			}
		}
	}
	for name := range s.rollups {
		g, layout, prefix := Minute, "20060102", "rollup_minute_"
		if len(name) > len("rollup_hour_") && name[:len("rollup_hour_")] == "rollup_hour_" {
			g, layout, prefix = Hour, "200601", "rollup_hour_"
		}
		p, err := time.Parse(layout, name[len(prefix):])
		if err != nil {
			continue
		}
		keep := r.Minute
		if g == Hour {
			keep = r.Hour
		}
		if g.periodEnd(p).Before(now.Add(-keep)) {
			victims = append(victims, victim{name, "rollup_partitions", "table_name"})
		}
	}
	s.mu.Unlock()

	var dropped []string
	for _, v := range victims {
		s.writeMu.Lock()
		err := func() error {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return err
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := tx.ExecContext(ctx, `DROP TABLE IF EXISTS `+v.name); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM `+v.catalog+` WHERE `+v.key+` = ?`, v.name); err != nil {
				return err
			}
			return tx.Commit()
		}()
		s.writeMu.Unlock()
		if err != nil {
			return dropped, fmt.Errorf("dropping %s: %w", v.name, err)
		}
		s.mu.Lock()
		delete(s.days, v.name)
		delete(s.rollups, v.name)
		delete(s.dirty, v.name)
		s.mu.Unlock()
		dropped = append(dropped, v.name)
	}
	sort.Strings(dropped)
	return dropped, nil
}

// rollupTablesFor returns the existing tables of granularity g covering [from, to).
func (s *Store) rollupTablesFor(g Granularity, from, to time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for p := g.period(from); p.Before(to); p = g.periodEnd(p) {
		if name := g.table(p); s.rollups[name] {
			out = append(out, name)
		}
	}
	return out
}
