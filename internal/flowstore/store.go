// Package flowstore is the Flow Store (ADR 0001): raw Flows in embedded DuckDB, one
// table per UTC day so retention can drop whole days. It is reached only through named
// query methods, one per screen or widget, never through ad-hoc SQL from callers.
package flowstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// Options configure the store.
type Options struct {
	// MemoryLimit is DuckDB's memory_limit, e.g. "1GB". Empty uses DuckDB's default.
	MemoryLimit string
	// Threads is DuckDB's thread count. 0 uses DuckDB's default.
	Threads int
}

// Store is safe for concurrent use.
type Store struct {
	path      string
	db        *sql.DB
	connector *duckdb.Connector

	// writeMu serialises writes with compaction and retention, which rewrite or drop tables.
	writeMu sync.Mutex

	mu      sync.Mutex
	days    map[string]bool // raw day tables that exist, by name
	rollups map[string]bool // Rollup tables that exist, by name
	dirty   map[string]bool // Rollup tables written since the last compaction
}

// Open opens (or creates) the Flow Store at path and applies pending migrations.
func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	connector, err := duckdb.NewConnector(path, func(execer driver.ExecerContext) error {
		var settings []string
		if opts.MemoryLimit != "" {
			settings = append(settings, fmt.Sprintf("SET memory_limit = '%s'", strings.ReplaceAll(opts.MemoryLimit, "'", "")))
		}
		if opts.Threads > 0 {
			settings = append(settings, fmt.Sprintf("SET threads = %d", opts.Threads))
		}
		settings = append(settings, "SET TimeZone = 'UTC'")
		for _, s := range settings {
			if _, err := execer.ExecContext(context.Background(), s, nil); err != nil {
				return fmt.Errorf("%s: %w", s, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("opening flow store: %w", err)
	}
	s := &Store{path: path, db: sql.OpenDB(connector), connector: connector,
		days: map[string]bool{}, rollups: map[string]bool{}, dirty: map[string]bool{}}
	if err := s.migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadDays(ctx); err != nil {
		s.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the store.
func (s *Store) Close() error {
	err := s.db.Close()
	if cerr := s.connector.Close(); err == nil {
		err = cerr
	}
	return err
}

// migrations are applied in order and recorded in flowmon_migrations. Never edit an
// applied migration; add a new one. Changes to the day-table shape must also alter every
// existing day table.
var migrations = []string{
	// 1: catalogue of day tables.
	`CREATE TABLE flow_days (day DATE PRIMARY KEY, table_name VARCHAR NOT NULL UNIQUE)`,
	// 2: catalogue of Rollup tables (minute: one per day, hour: one per month).
	`CREATE TABLE rollup_partitions (granularity VARCHAR NOT NULL, period DATE NOT NULL, table_name VARCHAR NOT NULL UNIQUE, PRIMARY KEY (granularity, period))`,
	// 3: approved and ignored Exporters. Kept here, not in the App Store, because
	// collection needs them even when the App Store is unreachable (ADR 0001).
	`CREATE TABLE exporters (
		address VARCHAR PRIMARY KEY,
		status VARCHAR NOT NULL,
		role VARCHAR NOT NULL,
		classification_mode VARCHAR NOT NULL,
		name VARCHAR NOT NULL,
		decided_at TIMESTAMP NOT NULL,
		decided_by VARCHAR NOT NULL
	)`,
	// 4: interface number → name per Exporter, with the time range each mapping was valid,
	// so reprocessing uses the mapping that applied when a Flow happened (ADR 0003/0004).
	`CREATE TABLE interface_mappings (
		exporter VARCHAR NOT NULL,
		if_index UINTEGER NOT NULL,
		name VARCHAR NOT NULL,
		source VARCHAR NOT NULL,
		valid_from TIMESTAMP NOT NULL,
		valid_to TIMESTAMP
	)`,
	// 5: Interface Roles, by interface name.
	`CREATE TABLE interface_roles (exporter VARCHAR NOT NULL, name VARCHAR NOT NULL, role VARCHAR NOT NULL, PRIMARY KEY (exporter, name))`,
	// 6: when an Exporter's classification changed, for chart markers.
	`CREATE TABLE classification_changes (exporter VARCHAR NOT NULL, changed_at TIMESTAMP NOT NULL, description VARCHAR NOT NULL)`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS flowmon_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP NOT NULL)`); err != nil {
		return fmt.Errorf("flow store migrations: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM flowmon_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("flow store migrations: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("flow store is at schema version %d, newer than this Flowmon (%d); upgrade Flowmon", current, len(migrations))
	}
	for v := current + 1; v <= len(migrations); v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v-1]); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("flow store migration %d: %w", v, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO flowmon_migrations VALUES (?, ?)`, v, time.Now().UTC()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("flow store migration %d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("flow store migration %d: %w", v, err)
		}
	}
	return nil
}

// SchemaVersion returns the applied schema version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT coalesce(max(version), 0) FROM flowmon_migrations`).Scan(&v)
	return v, err
}

func (s *Store) loadDays(ctx context.Context) error {
	for query, into := range map[string]map[string]bool{
		`SELECT table_name FROM flow_days`:         s.days,
		`SELECT table_name FROM rollup_partitions`: s.rollups,
	} {
		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			s.mu.Lock()
			into[name] = true
			s.mu.Unlock()
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return err
		}
		rows.Close()
	}
	// Tables may hold uncompacted rows from before a restart.
	s.mu.Lock()
	for name := range s.rollups {
		s.dirty[name] = true
	}
	s.mu.Unlock()
	return nil
}

// dayTable names the table for the UTC day of t.
func dayTable(t time.Time) string { return "flows_" + t.UTC().Format("20060102") }

// dayColumns is the day-table shape. Addresses are stored as text: readable, and fast
// enough at home-network scale.
const dayColumns = `(
	exporter VARCHAR NOT NULL,
	observation_domain UINTEGER NOT NULL,
	received TIMESTAMP NOT NULL,
	export_time TIMESTAMP NOT NULL,
	start_ts TIMESTAMP NOT NULL,
	end_ts TIMESTAMP NOT NULL,
	time_estimated BOOLEAN NOT NULL,
	src VARCHAR NOT NULL,
	dst VARCHAR NOT NULL,
	src_port USMALLINT NOT NULL,
	dst_port USMALLINT NOT NULL,
	protocol UTINYINT NOT NULL,
	post_nat_src VARCHAR,
	post_nat_dst VARCHAR,
	post_nat_src_port USMALLINT NOT NULL,
	post_nat_dst_port USMALLINT NOT NULL,
	bytes UBIGINT NOT NULL,
	packets UBIGINT NOT NULL,
	ingress_interface UINTEGER NOT NULL,
	egress_interface UINTEGER NOT NULL,
	direction UTINYINT NOT NULL,
	local_host VARCHAR,
	local_is_exporter_host BOOLEAN NOT NULL,
	peer VARCHAR,
	local_port USMALLINT NOT NULL,
	peer_port USMALLINT NOT NULL,
	fields BLOB NOT NULL
)`

func (s *Store) ensureDay(ctx context.Context, day time.Time) (string, error) {
	name := dayTable(day)
	s.mu.Lock()
	exists := s.days[name]
	s.mu.Unlock()
	if exists {
		return name, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+name+` `+dayColumns); err != nil {
		return "", fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO flow_days VALUES (?, ?) ON CONFLICT DO NOTHING`, day.UTC().Truncate(24*time.Hour), name); err != nil {
		return "", fmt.Errorf("registering %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.days[name] = true
	s.mu.Unlock()
	return name, nil
}

func nullableAddr(a netip.Addr) driver.Value {
	if !a.IsValid() {
		return nil
	}
	return a.String()
}

// Write appends Flows and their Rollup contributions. Each Flow goes into the table of
// the UTC day its End falls on.
func (s *Store) Write(ctx context.Context, flows []flow.Flow) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	byDay := map[string][]*flow.Flow{}
	var order []string
	for i := range flows {
		f := &flows[i]
		name, err := s.ensureDay(ctx, f.End)
		if err != nil {
			return err
		}
		if _, ok := byDay[name]; !ok {
			order = append(order, name)
		}
		byDay[name] = append(byDay[name], f)
	}
	sort.Strings(order)

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		for _, name := range order {
			app, err := duckdb.NewAppenderFromConn(dc.(driver.Conn), "", name)
			if err != nil {
				return fmt.Errorf("appender for %s: %w", name, err)
			}
			for _, f := range byDay[name] {
				if err := app.AppendRow(flowRow(f)...); err != nil {
					_ = app.Close()
					return fmt.Errorf("appending to %s: %w", name, err)
				}
			}
			if err := app.Close(); err != nil {
				return fmt.Errorf("flushing %s: %w", name, err)
			}
		}
		return s.writeRollups(ctx, dc.(driver.Conn), flows, time.Time{})
	})
}

// flowRow is a Flow in day-table column order.
func flowRow(f *flow.Flow) []driver.Value {
	return []driver.Value{
		f.Exporter.String(), f.ObservationDomain, f.Received.UTC(), f.ExportTime.UTC(), f.Start.UTC(), f.End.UTC(), f.TimeEstimated,
		f.Src.String(), f.Dst.String(), f.SrcPort, f.DstPort, f.Protocol,
		nullableAddr(f.PostNATSrc), nullableAddr(f.PostNATDst), f.PostNATSrcPort, f.PostNATDstPort,
		f.Bytes, f.Packets, f.IngressInterface, f.EgressInterface,
		uint8(f.Direction), nullableAddr(f.Local), f.LocalIsExporterHost, nullableAddr(f.Peer), f.LocalPort, f.PeerPort,
		f.Fields,
	}
}

// tablesFor returns the existing day tables that can hold Flows overlapping [from, to).
// Flows are filed by end time and last at most a few active timeouts, so the day after
// to is included for Flows that started in range and ended after it.
func (s *Store) tablesFor(from, to time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for d := from.UTC().Truncate(24 * time.Hour); !d.After(to.Add(time.Hour)); d = d.Add(24 * time.Hour) {
		if name := dayTable(d); s.days[name] {
			out = append(out, name)
		}
	}
	return out
}

// ErrInvalidRange is returned for a query whose time range is empty or too long.
var ErrInvalidRange = errors.New("flowstore: invalid time range")
