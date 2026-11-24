package flowstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/rollup"
)

// Domain Rollups (ADR 0005) are traffic totals by Local Host, its Device and Registrable
// Domain, per minute and per hour, kept apart from the Rollups so domains don't multiply
// their size. Like Rollups, minute tables hold a UTC day and hour tables a UTC month,
// under the same retentions. Traffic with no domain is counted under "".
//
// They are built from raw Flows DomainRollupDelay after the Flows were received, once
// Domain Attribution has named Flows that arrived before their DNS lookup was polled,
// so a built total never changes. Each Flow is counted once: by when it was received.

// DomainRollupDelay is how long after a Flow is received it is counted in Domain
// Rollups.
const DomainRollupDelay = 5 * time.Minute

// domainBuildChunk bounds how much one build reads, after Flowmon was off for a while.
const domainBuildChunk = time.Hour

const domainRollupColumns = `(
	bucket TIMESTAMP NOT NULL,
	exporter VARCHAR NOT NULL,
	local_host VARCHAR NOT NULL,
	local_mac VARCHAR NOT NULL,
	registrable VARCHAR NOT NULL,
	bytes_rx DOUBLE NOT NULL,
	bytes_tx DOUBLE NOT NULL,
	packets_rx DOUBLE NOT NULL,
	packets_tx DOUBLE NOT NULL,
	flows UBIGINT NOT NULL
)`

const domainKeyColumns = `bucket, exporter, local_host, local_mac, registrable`

// keyDomainsBuilt is the settings key that records how far Domain Rollups are built: every
// Flow received before it is counted.
const keyDomainsBuilt = "domain_rollups_built"

func domainTable(g Granularity, p time.Time) string {
	if g == Hour {
		return "domain_hour_" + p.Format("200601")
	}
	return "domain_minute_" + p.Format("20060102")
}

// parseDomainTable reads a Domain Rollup table's name.
func parseDomainTable(name string) (Granularity, time.Time, bool) {
	if rest, ok := strings.CutPrefix(name, "domain_hour_"); ok {
		p, err := time.Parse("200601", rest)
		return Hour, p, err == nil
	}
	if rest, ok := strings.CutPrefix(name, "domain_minute_"); ok {
		p, err := time.Parse("20060102", rest)
		return Minute, p, err == nil
	}
	return "", time.Time{}, false
}

func (s *Store) ensureDomainRollup(ctx context.Context, g Granularity, period time.Time) (string, error) {
	name := domainTable(g, period)
	s.mu.Lock()
	exists := s.domainRollups[name]
	s.mu.Unlock()
	if exists {
		return name, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+name+` `+domainRollupColumns); err != nil {
		return "", fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_rollup_partitions VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, string(g), period, name); err != nil {
		return "", fmt.Errorf("registering %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.domainRollups[name] = true
	s.mu.Unlock()
	return name, nil
}

// DomainRollupsBuilt returns how far Domain Rollups are built: every Flow received before
// it is counted. Zero until the first build.
func (s *Store) DomainRollupsBuilt(ctx context.Context) (time.Time, error) {
	v, ok, err := s.Setting(ctx, keyDomainsBuilt)
	if err != nil || !ok {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, v)
}

// BuildDomainRollups counts the Flows received up to now minus DomainRollupDelay that
// aren't counted yet. The first build starts at that point: domains start when this
// Flowmon starts counting them. It returns how many Flows it counted.
func (s *Store) BuildDomainRollups(ctx context.Context, now time.Time) (int, error) {
	through := now.Add(-DomainRollupDelay).UTC().Truncate(time.Minute)
	built, err := s.DomainRollupsBuilt(ctx)
	if err != nil {
		return 0, err
	}
	if built.IsZero() {
		return 0, s.SetSettings(ctx, map[string]string{keyDomainsBuilt: through.Format(time.RFC3339Nano)})
	}
	total := 0
	for built.Before(through) {
		end := built.Add(domainBuildChunk)
		if end.After(through) {
			end = through
		}
		n, err := s.buildDomainRollups(ctx, built, end)
		total += n
		if err != nil {
			return total, err
		}
		built = end
	}
	return total, nil
}

// buildDomainRollups counts the Flows received in [from, to) and records to as built,
// under the write lock so reprocessing can't run in between.
func (s *Store) buildDomainRollups(ctx context.Context, from, to time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	flows, err := s.flowsForDomains(ctx, `received >= ? AND received < ?`, from, to)
	if err != nil {
		return 0, err
	}
	if err := s.appendDomainRollups(ctx, flows, time.Time{}); err != nil {
		return 0, err
	}
	return len(flows), s.SetSettings(ctx, map[string]string{keyDomainsBuilt: to.Format(time.RFC3339Nano)})
}

// flowsForDomains reads the Inbound and Outbound Flows matching where, with what Domain
// Rollups need. Flows are filed by end time, which follows received time closely, so the
// day before the first argument's day is read too.
func (s *Store) flowsForDomains(ctx context.Context, where string, from, to time.Time, args ...any) ([]flow.Flow, error) {
	var out []flow.Flow
	for _, t := range s.tablesFor(from.Add(-24*time.Hour), to) {
		q := `SELECT exporter, local_host, local_mac, start_ts, end_ts, bytes, packets, direction, domain FROM ` + t +
			` WHERE ` + where + ` AND direction IN (?, ?) AND local_host IS NOT NULL`
		rows, err := s.db.QueryContext(ctx, q, append(append([]any{from.UTC(), to.UTC()}, args...), uint8(flow.DirectionInbound), uint8(flow.DirectionOutbound))...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f flow.Flow
			var exporter, local, mac string
			var dir uint8
			if err := rows.Scan(&exporter, &local, &mac, &f.Start, &f.End, &f.Bytes, &f.Packets, &dir, &f.Domain); err != nil {
				rows.Close()
				return nil, err
			}
			f.Exporter, _ = netip.ParseAddr(exporter)
			f.Local, _ = netip.ParseAddr(local)
			f.LocalMAC, _ = flow.ParseMAC(mac)
			f.Direction = flow.Direction(dir)
			out = append(out, f)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type domainKey struct {
	bucket                                time.Time
	exporter, localHost, mac, registrable string
}

type domainMeasures struct {
	bytesRx, bytesTx, packetsRx, packetsTx float64
	flows                                  uint64
}

// appendDomainRollups adds flows' contributions, spread over buckets as Rollups spread
// them, skipping buckets before minBucket. s.writeMu must be held.
func (s *Store) appendDomainRollups(ctx context.Context, flows []flow.Flow, minBucket time.Time) error {
	if len(flows) == 0 {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		for _, g := range []Granularity{Minute, Hour} {
			agg := map[domainKey]*domainMeasures{}
			for i := range flows {
				f := &flows[i]
				reg := domains.Registrable(f.Domain)
				for _, r := range rollup.Contributions(f, g.bucket()) {
					if r.Key.Bucket.Before(minBucket) {
						continue
					}
					k := domainKey{r.Key.Bucket, r.Key.Exporter, r.Key.LocalHost, r.Key.LocalMAC, reg}
					m := agg[k]
					if m == nil {
						m = &domainMeasures{}
						agg[k] = m
					}
					m.bytesRx += r.BytesRx
					m.bytesTx += r.BytesTx
					m.packetsRx += r.PacketsRx
					m.packetsTx += r.PacketsTx
					m.flows += r.Flows
				}
			}
			byTable := map[string][]domainKey{}
			for k := range agg {
				name, err := s.ensureDomainRollup(ctx, g, g.period(k.bucket))
				if err != nil {
					return err
				}
				byTable[name] = append(byTable[name], k)
			}
			for name, keys := range byTable {
				app, err := duckdb.NewAppenderFromConn(dc.(driver.Conn), "", name)
				if err != nil {
					return fmt.Errorf("appender for %s: %w", name, err)
				}
				for _, k := range keys {
					m := agg[k]
					if err := app.AppendRow(k.bucket, k.exporter, k.localHost, k.mac, k.registrable,
						m.bytesRx, m.bytesTx, m.packetsRx, m.packetsTx, m.flows); err != nil {
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
	})
}

// rebuildDomainRollups recounts exporter's Flows from window on, after reprocessing
// rewrote them: their totals are removed and the Flows already counted are counted
// again. s.writeMu must be held.
func (s *Store) rebuildDomainRollups(ctx context.Context, exporter netip.Addr, window time.Time) error {
	built, err := s.DomainRollupsBuilt(ctx)
	if err != nil || built.IsZero() {
		return err
	}
	s.mu.Lock()
	var tables []string
	for name := range s.domainRollups {
		if g, p, ok := parseDomainTable(name); ok && g.periodEnd(p).After(window) {
			tables = append(tables, name)
		}
	}
	s.mu.Unlock()
	sort.Strings(tables)
	for _, t := range tables {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+t+` WHERE exporter = ? AND bucket >= ?`, exporter.String(), window.UTC()); err != nil {
			return fmt.Errorf("clearing %s: %w", t, err)
		}
	}
	flows, err := s.flowsForDomains(ctx, `end_ts >= ? AND received < ? AND exporter = ?`, window, built, exporter.String())
	if err != nil {
		return err
	}
	return s.appendDomainRollups(ctx, flows, window)
}

func (s *Store) compactDomainTable(ctx context.Context, name string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tmp := name + "_compact"
	for _, q := range []string{
		`DROP TABLE IF EXISTS ` + tmp,
		`CREATE TABLE ` + tmp + ` ` + domainRollupColumns,
		`INSERT INTO ` + tmp + ` SELECT ` + domainKeyColumns + `, sum(bytes_rx), sum(bytes_tx), sum(packets_rx), sum(packets_tx), sum(flows)::UBIGINT
			FROM ` + name + ` GROUP BY ` + domainKeyColumns,
		`DROP TABLE ` + name,
		`ALTER TABLE ` + tmp + ` RENAME TO ` + name,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("compacting %s: %w", name, err)
		}
	}
	return tx.Commit()
}

// DomainTotal is one Registrable Domain's traffic ("" for traffic with no domain).
type DomainTotal struct {
	Registrable      string
	BytesRx, BytesTx float64
	Flows            uint64
	// LocalHosts and Devices count who used it.
	LocalHosts, Devices int
}

// DomainTotals sums Domain Rollups over [from, to) by Registrable Domain, largest first.
// Ranges of a day or more read hour tables, shorter ones minute tables.
func (s *Store) DomainTotals(ctx context.Context, from, to time.Time) ([]DomainTotal, error) {
	g := Minute
	if to.Sub(from) >= 24*time.Hour {
		g = Hour
	}
	s.mu.Lock()
	var parts []string
	for name := range s.domainRollups {
		if tg, p, ok := parseDomainTable(name); ok && tg == g && p.Before(to) && g.periodEnd(p).After(from) {
			parts = append(parts, `SELECT * FROM `+name)
		}
	}
	s.mu.Unlock()
	if len(parts) == 0 {
		return nil, nil
	}
	sort.Strings(parts)
	rows, err := s.db.QueryContext(ctx, `SELECT registrable, sum(bytes_rx), sum(bytes_tx), sum(flows)::UBIGINT,
		count(DISTINCT local_host), count(DISTINCT local_mac) FILTER (WHERE local_mac <> '')
		FROM (`+strings.Join(parts, ` UNION ALL `)+`) WHERE bucket >= ? AND bucket < ?
		GROUP BY registrable ORDER BY sum(bytes_rx + bytes_tx) DESC, registrable`, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DomainTotal
	for rows.Next() {
		var d DomainTotal
		var macs sql.NullInt64
		if err := rows.Scan(&d.Registrable, &d.BytesRx, &d.BytesTx, &d.Flows, &d.LocalHosts, &macs); err != nil {
			return nil, err
		}
		d.Devices = int(macs.Int64)
		out = append(out, d)
	}
	return out, rows.Err()
}
