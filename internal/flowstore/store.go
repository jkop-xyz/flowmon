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
	"github.com/jkop-xyz/flowmon/internal/rollup"
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
	days    map[string]bool   // raw day tables that exist, by name
	dnsDays map[string]bool   // DNS Query day tables that exist, by name
	rollups map[string]bool   // Rollup tables that exist, by name
	dirty   map[string]bool   // Rollup tables written since the last compaction
	asns    map[uint32]string // ASN Owner names, as stored in asn_names

	topBuilt map[period]bool // Top Remote IPs periods built
	topDirty map[period]bool // built periods that Flows were added to since

	capMu     sync.Mutex
	capStatus CapStatus

	snapshotMu sync.Mutex
}

// Open opens (or creates) the Flow Store at path and applies pending migrations.
func Open(ctx context.Context, path string, opts Options) (*Store, error) {
	removeStaleSnapshots(path)
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
		days: map[string]bool{}, dnsDays: map[string]bool{}, rollups: map[string]bool{}, dirty: map[string]bool{}, asns: map[uint32]string{},
		topBuilt: map[period]bool{}, topDirty: map[period]bool{}}
	if err := s.migrate(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadDays(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadASNNames(ctx); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.loadTopRemoteBuilt(ctx); err != nil {
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

// migration is one schema change, applied in a transaction.
type migration func(ctx context.Context, tx *sql.Tx) error

// stmt is a migration of one statement.
func stmt(q string) migration {
	return func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, q)
		return err
	}
}

func queryStrings(ctx context.Context, tx *sql.Tx, q string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// migrations are applied in order and recorded in flowmon_migrations. Never edit an
// applied migration; add a new one. Changes to the day-table shape must also alter every
// existing day table.
var migrations = []migration{
	// 1: catalogue of day tables.
	stmt(`CREATE TABLE flow_days (day DATE PRIMARY KEY, table_name VARCHAR NOT NULL UNIQUE)`),
	// 2: catalogue of Rollup tables (minute: one per day, hour: one per month).
	stmt(`CREATE TABLE rollup_partitions (granularity VARCHAR NOT NULL, period DATE NOT NULL, table_name VARCHAR NOT NULL UNIQUE, PRIMARY KEY (granularity, period))`),
	// 3: approved and ignored Exporters. Kept here, not in the App Store, because
	// collection needs them even when the App Store is unreachable (ADR 0001).
	stmt(`CREATE TABLE exporters (
		address VARCHAR PRIMARY KEY,
		status VARCHAR NOT NULL,
		role VARCHAR NOT NULL,
		classification_mode VARCHAR NOT NULL,
		name VARCHAR NOT NULL,
		decided_at TIMESTAMP NOT NULL,
		decided_by VARCHAR NOT NULL
	)`),
	// 4: interface number → name per Exporter, with the time range each mapping was valid,
	// so reprocessing uses the mapping that applied when a Flow happened (ADR 0003/0004).
	stmt(`CREATE TABLE interface_mappings (
		exporter VARCHAR NOT NULL,
		if_index UINTEGER NOT NULL,
		name VARCHAR NOT NULL,
		source VARCHAR NOT NULL,
		valid_from TIMESTAMP NOT NULL,
		valid_to TIMESTAMP
	)`),
	// 5: Interface Roles, by interface name.
	stmt(`CREATE TABLE interface_roles (exporter VARCHAR NOT NULL, name VARCHAR NOT NULL, role VARCHAR NOT NULL, PRIMARY KEY (exporter, name))`),
	// 6: when an Exporter's classification changed, for chart markers.
	stmt(`CREATE TABLE classification_changes (exporter VARCHAR NOT NULL, changed_at TIMESTAMP NOT NULL, description VARCHAR NOT NULL)`),
	// 7: SNMP settings per Exporter, as JSON with secrets encrypted.
	stmt(`CREATE TABLE snmp_configs (exporter VARCHAR PRIMARY KEY, config VARCHAR NOT NULL)`),
	// 8: SNMP interface counter deltas per hour, for Reconciliation.
	stmt(`CREATE TABLE snmp_hours (
		exporter VARCHAR NOT NULL,
		if_name VARCHAR NOT NULL,
		hour TIMESTAMP NOT NULL,
		in_octets UBIGINT NOT NULL,
		out_octets UBIGINT NOT NULL,
		in_packets UBIGINT NOT NULL,
		out_packets UBIGINT NOT NULL,
		complete BOOLEAN NOT NULL,
		PRIMARY KEY (exporter, if_name, hour)
	)`),
	// 9: GeoIP enrichment of the Peer on every raw Flow, and ASN Owner names.
	func(ctx context.Context, tx *sql.Tx) error {
		tables, err := queryStrings(ctx, tx, `SELECT table_name FROM flow_days`)
		if err != nil {
			return err
		}
		for _, t := range tables {
			for _, q := range []string{
				`ALTER TABLE ` + t + ` ADD COLUMN peer_country VARCHAR DEFAULT ''`,
				`ALTER TABLE ` + t + ` ADD COLUMN peer_asn UINTEGER DEFAULT 0`,
			} {
				if _, err := tx.ExecContext(ctx, q); err != nil {
					return fmt.Errorf("%s: %w", t, err)
				}
			}
		}
		_, err = tx.ExecContext(ctx, `CREATE TABLE asn_names (asn UINTEGER PRIMARY KEY, name VARCHAR NOT NULL, updated TIMESTAMP NOT NULL)`)
		return err
	},
	// 10: Top Remote IPs, per hour and per day, and which periods have been built.
	func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{topRemoteMigration, topRemoteBuiltMigration} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	},
	// 11: collection settings set in the UI (Local Networks, retention). Kept here, not in
	// the App Store, so collection never depends on the App Store (ADR 0001).
	stmt(`CREATE TABLE settings (key VARCHAR PRIMARY KEY, value VARCHAR NOT NULL)`),
	// 12: sampling. Raw Flows keep the rate their counts were scaled by; Rollups and Top
	// Remote IPs keep how much of their bytes are such estimates; Exporters can have a
	// rate set by hand.
	func(ctx context.Context, tx *sql.Tx) error {
		alters := []string{`ALTER TABLE exporters ADD COLUMN IF NOT EXISTS sampling_rate UINTEGER DEFAULT 0`}
		days, err := queryStrings(ctx, tx, `SELECT table_name FROM flow_days`)
		if err != nil {
			return err
		}
		for _, t := range days {
			alters = append(alters, `ALTER TABLE `+t+` ADD COLUMN IF NOT EXISTS sampling_rate UINTEGER DEFAULT 0`)
		}
		rollups, err := queryStrings(ctx, tx, `SELECT table_name FROM rollup_partitions`)
		if err != nil {
			return err
		}
		for _, t := range append(rollups, "top_remote") {
			alters = append(alters, `ALTER TABLE `+t+` ADD COLUMN IF NOT EXISTS estimated_rx DOUBLE DEFAULT 0`,
				`ALTER TABLE `+t+` ADD COLUMN IF NOT EXISTS estimated_tx DOUBLE DEFAULT 0`)
		}
		for _, q := range alters {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return fmt.Errorf("%s: %w", q, err)
			}
		}
		return nil
	},
	// 13: Internal Exporters: their LAN subnets, and whether an Exporter sees the same
	// traffic at several points (so duplicates are removed).
	func(ctx context.Context, tx *sql.Tx) error {
		for _, q := range []string{
			`ALTER TABLE exporters ADD COLUMN IF NOT EXISTS subnets VARCHAR DEFAULT ''`,
			`ALTER TABLE exporters ADD COLUMN IF NOT EXISTS multiple_points BOOLEAN DEFAULT false`,
		} {
			if _, err := tx.ExecContext(ctx, q); err != nil {
				return err
			}
		}
		return nil
	},
	// 14: Data Sources (v2): integrations other than Exporters, such as AdGuard Home.
	// settings is a JSON object whose secret fields are sealed with secret.key.
	stmt(`CREATE TABLE IF NOT EXISTS data_sources (id VARCHAR PRIMARY KEY, kind VARCHAR NOT NULL, name VARCHAR NOT NULL, enabled BOOLEAN NOT NULL, settings VARCHAR NOT NULL, created TIMESTAMP NOT NULL)`),
	// 15: DNS Queries (v2), one table per UTC day like raw Flows (see dns.go).
	stmt(`CREATE TABLE IF NOT EXISTS dns_days (day DATE PRIMARY KEY, table_name VARCHAR NOT NULL UNIQUE)`),
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
		if err := migrations[v-1](ctx, tx); err != nil {
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
		`SELECT table_name FROM dns_days`:          s.dnsDays,
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
	fields BLOB NOT NULL,
	peer_country VARCHAR DEFAULT '',
	peer_asn UINTEGER DEFAULT 0,
	sampling_rate UINTEGER DEFAULT 0
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
		if err := s.writeRollups(ctx, dc.(driver.Conn), flows, time.Time{}); err != nil {
			return err
		}
		s.markTopDirty(remoteSpans(flows))
		return s.saveASNNames(ctx, flows)
	})
}

// remoteSpans returns the time each Flow with a Remote IP is spread over.
func remoteSpans(flows []flow.Flow) []rollupSpan {
	var out []rollupSpan
	for i := range flows {
		f := &flows[i]
		if f.Direction != flow.DirectionInbound && f.Direction != flow.DirectionOutbound {
			continue
		}
		start, end := f.Start.UTC(), f.End.UTC()
		if d := end.Sub(start); d <= 0 || d > rollup.MaxSpread {
			start = end
		}
		out = append(out, rollupSpan{start, end})
	}
	return out
}

func (s *Store) loadASNNames(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT asn, name FROM asn_names`)
	if err != nil {
		return fmt.Errorf("loading ASN names: %w", err)
	}
	defer rows.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var asn uint32
		var name string
		if err := rows.Scan(&asn, &name); err != nil {
			return err
		}
		s.asns[asn] = name
	}
	return rows.Err()
}

// saveASNNames records ASN Owner names that are new or changed. Names are kept per ASN,
// latest wins, rather than on every Flow.
func (s *Store) saveASNNames(ctx context.Context, flows []flow.Flow) error {
	changed := map[uint32]string{}
	s.mu.Lock()
	for i := range flows {
		f := &flows[i]
		if f.PeerASN != 0 && f.PeerASNOrg != "" && s.asns[f.PeerASN] != f.PeerASNOrg {
			changed[f.PeerASN] = f.PeerASNOrg
		}
	}
	s.mu.Unlock()
	now := time.Now().UTC()
	for asn, name := range changed {
		if _, err := s.db.ExecContext(ctx, `INSERT INTO asn_names VALUES (?, ?, ?)
			ON CONFLICT (asn) DO UPDATE SET name = excluded.name, updated = excluded.updated`, asn, name, now); err != nil {
			return fmt.Errorf("saving ASN names: %w", err)
		}
		s.mu.Lock()
		s.asns[asn] = name
		s.mu.Unlock()
	}
	return nil
}

// ASNName returns the stored ASN Owner name for asn, or "".
func (s *Store) ASNName(asn uint32) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.asns[asn]
}

// flowRow is a Flow in day-table column order.
func flowRow(f *flow.Flow) []driver.Value {
	return []driver.Value{
		f.Exporter.String(), f.ObservationDomain, f.Received.UTC(), f.ExportTime.UTC(), f.Start.UTC(), f.End.UTC(), f.TimeEstimated,
		f.Src.String(), f.Dst.String(), f.SrcPort, f.DstPort, f.Protocol,
		nullableAddr(f.PostNATSrc), nullableAddr(f.PostNATDst), f.PostNATSrcPort, f.PostNATDstPort,
		f.Bytes, f.Packets, f.IngressInterface, f.EgressInterface,
		uint8(f.Direction), nullableAddr(f.Local), f.LocalIsExporterHost, nullableAddr(f.Peer), f.LocalPort, f.PeerPort,
		f.Fields, f.PeerCountry, f.PeerASN, f.SamplingRate,
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
