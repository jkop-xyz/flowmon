package flowstore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sort"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/jkop-xyz/flowmon/internal/series"
)

// Interface Counters (ADR 0005) are SNMP interface counter deltas per minute and per hour,
// in tables like the Rollups' (iface_minute_YYYYMMDD, iface_hour_YYYYMM, listed in
// iface_partitions) under the same retentions. seconds says how much of a bucket polling
// covered, so gaps show. Interfaces themselves are in snmp_interfaces.

const ifaceColumns = `(
	bucket TIMESTAMP NOT NULL,
	source VARCHAR NOT NULL,
	if_index UINTEGER NOT NULL,
	in_octets UBIGINT NOT NULL,
	out_octets UBIGINT NOT NULL,
	in_packets UBIGINT NOT NULL,
	out_packets UBIGINT NOT NULL,
	in_errors UBIGINT NOT NULL,
	out_errors UBIGINT NOT NULL,
	in_discards UBIGINT NOT NULL,
	out_discards UBIGINT NOT NULL,
	seconds DOUBLE NOT NULL
)`

const ifaceKeyColumns = `bucket, source, if_index`

const ifaceSums = `sum(in_octets)::UBIGINT, sum(out_octets)::UBIGINT, sum(in_packets)::UBIGINT, sum(out_packets)::UBIGINT,
	sum(in_errors)::UBIGINT, sum(out_errors)::UBIGINT, sum(in_discards)::UBIGINT, sum(out_discards)::UBIGINT, sum(seconds)`

func ifaceTable(g Granularity, p time.Time) string {
	if g == Hour {
		return "iface_hour_" + p.Format("200601")
	}
	return "iface_minute_" + p.Format("20060102")
}

func parseIfaceTable(name string) (Granularity, time.Time, bool) {
	if rest, ok := strings.CutPrefix(name, "iface_hour_"); ok {
		p, err := time.Parse("200601", rest)
		return Hour, p, err == nil
	}
	if rest, ok := strings.CutPrefix(name, "iface_minute_"); ok {
		p, err := time.Parse("20060102", rest)
		return Minute, p, err == nil
	}
	return "", time.Time{}, false
}

func (s *Store) ensureIfaceTable(ctx context.Context, g Granularity, period time.Time) (string, error) {
	name := ifaceTable(g, period)
	s.mu.Lock()
	exists := s.ifaceTables[name]
	s.mu.Unlock()
	if exists {
		return name, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+name+` `+ifaceColumns); err != nil {
		return "", fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO iface_partitions VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, string(g), period, name); err != nil {
		return "", fmt.Errorf("registering %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.ifaceTables[name] = true
	s.mu.Unlock()
	return name, nil
}

// WriteInterfaces records interfaces as they are now and their counter samples, each in
// the minute and hour bucket its poll ended in (series.Writer).
func (s *Store) WriteInterfaces(ctx context.Context, now time.Time, ifaces []series.Interface, samples []series.InterfaceSample) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	for _, i := range ifaces {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO snmp_interfaces VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (source, if_index) DO UPDATE SET name = excluded.name, alias = excluded.alias, speed = excluded.speed,
				up = excluded.up, last_seen = excluded.last_seen`,
			i.Source, i.IfIndex, i.Name, i.Alias, i.Speed, i.Up, now.UTC()); err != nil {
			return fmt.Errorf("saving interfaces: %w", err)
		}
	}
	if len(samples) == 0 {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		for _, g := range []Granularity{Minute, Hour} {
			byTable := map[string][]series.InterfaceSample{}
			for _, smp := range samples {
				name, err := s.ensureIfaceTable(ctx, g, g.period(smp.At.UTC().Truncate(g.bucket())))
				if err != nil {
					return err
				}
				byTable[name] = append(byTable[name], smp)
			}
			for name, rows := range byTable {
				app, err := duckdb.NewAppenderFromConn(dc.(driver.Conn), "", name)
				if err != nil {
					return fmt.Errorf("appender for %s: %w", name, err)
				}
				for _, smp := range rows {
					if err := app.AppendRow(smp.At.UTC().Truncate(g.bucket()), smp.Source, smp.IfIndex, smp.InOctets, smp.OutOctets,
						smp.InPackets, smp.OutPackets, smp.InErrors, smp.OutErrors, smp.InDiscards, smp.OutDiscards, smp.Seconds); err != nil {
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

func (s *Store) compactIfaceTable(ctx context.Context, name string) error {
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
		`CREATE TABLE ` + tmp + ` ` + ifaceColumns,
		`INSERT INTO ` + tmp + ` SELECT ` + ifaceKeyColumns + `, ` + ifaceSums + ` FROM ` + name + ` GROUP BY ` + ifaceKeyColumns,
		`DROP TABLE ` + name,
		`ALTER TABLE ` + tmp + ` RENAME TO ` + name,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("compacting %s: %w", name, err)
		}
	}
	return tx.Commit()
}

// SNMPInterface is an interface of an SNMP source as last polled.
type SNMPInterface struct {
	Source   string    `json:"source"`
	IfIndex  uint32    `json:"ifIndex"`
	Name     string    `json:"name"`
	Alias    string    `json:"alias,omitempty"`
	Speed    uint64    `json:"speed"`
	Up       bool      `json:"up"`
	LastSeen time.Time `json:"lastSeen"`
}

// SNMPInterfaces returns an SNMP source's interfaces ("" for every source's), by name.
func (s *Store) SNMPInterfaces(ctx context.Context, source string) ([]SNMPInterface, error) {
	q := `SELECT source, if_index, name, alias, speed, up, last_seen FROM snmp_interfaces`
	var args []any
	if source != "" {
		q += ` WHERE source = ?`
		args = append(args, source)
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY source, name, if_index`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SNMPInterface{}
	for rows.Next() {
		var i SNMPInterface
		if err := rows.Scan(&i.Source, &i.IfIndex, &i.Name, &i.Alias, &i.Speed, &i.Up, &i.LastSeen); err != nil {
			return nil, err
		}
		i.LastSeen = i.LastSeen.UTC()
		out = append(out, i)
	}
	return out, rows.Err()
}

// InterfacePoint is one bucket of an interface's counters, as rates.
type InterfacePoint struct {
	T time.Time `json:"t"`
	// InBps and OutBps are bits per second over the seconds polling covered.
	InBps, OutBps float64
	// Errors and Discards are counts in the bucket, both directions.
	Errors, Discards uint64
	// Coverage is the share of the bucket polling covered, 0 to 1.
	Coverage float64
}

// InterfaceSeries returns an interface's counters over [from, to): minute buckets for
// ranges under a day, hour buckets otherwise. Buckets without samples are left out.
func (s *Store) InterfaceSeries(ctx context.Context, source string, ifIndex uint32, from, to time.Time) ([]InterfacePoint, error) {
	if !to.After(from) || to.Sub(from) > MaxRange {
		return nil, ErrInvalidRange
	}
	g := Minute
	if to.Sub(from) >= 24*time.Hour {
		g = Hour
	}
	s.mu.Lock()
	var parts []string
	for name := range s.ifaceTables {
		if tg, p, ok := parseIfaceTable(name); ok && tg == g && p.Before(to) && g.periodEnd(p).After(from) {
			parts = append(parts, `SELECT * FROM `+name)
		}
	}
	s.mu.Unlock()
	out := []InterfacePoint{}
	if len(parts) == 0 {
		return out, nil
	}
	sort.Strings(parts)
	rows, err := s.db.QueryContext(ctx, `SELECT bucket, sum(in_octets), sum(out_octets), sum(in_errors + out_errors), sum(in_discards + out_discards), sum(seconds)
		FROM (`+strings.Join(parts, ` UNION ALL `)+`) WHERE source = ? AND if_index = ? AND bucket >= ? AND bucket < ?
		GROUP BY bucket ORDER BY bucket`, source, ifIndex, from.UTC().Truncate(g.bucket()), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var p InterfacePoint
		var in, outB float64
		var secs float64
		if err := rows.Scan(&p.T, &in, &outB, &p.Errors, &p.Discards, &secs); err != nil {
			return nil, err
		}
		p.T = p.T.UTC()
		if secs > 0 {
			p.InBps, p.OutBps = in*8/secs, outB*8/secs
		}
		p.Coverage = min(secs/g.bucket().Seconds(), 1)
		out = append(out, p)
	}
	return out, rows.Err()
}
