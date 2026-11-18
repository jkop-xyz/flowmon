// Package appstore is the App Store (ADR 0001): users, identities, sessions and
// settings, on built-in SQLite or an external Postgres. The same migrations and
// queries run on both; SQL here is written to the common subset of the two dialects.
package appstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	_ "modernc.org/sqlite"             // registers the "sqlite" driver
)

// Driver is the App Store engine.
type Driver string

// Drivers.
const (
	SQLite   Driver = "sqlite"
	Postgres Driver = "postgres"
)

// Config says where the App Store lives.
type Config struct {
	Driver Driver
	// Path is the SQLite database file.
	Path string
	// URL is the Postgres connection URL (postgres://user@host:port/db?sslmode=…).
	// A password in Password is used instead of one in the URL.
	URL      string
	Password string
	// Schema is the Postgres schema to use (and create if missing). Empty uses "public".
	Schema string
}

// ParseURL reads a FLOWMON_DATABASE_URL: sqlite:///data/flowmon.db or postgres://….
// A search_path query parameter selects the Postgres schema.
func ParseURL(raw string) (Config, error) {
	switch {
	case strings.HasPrefix(raw, "sqlite:"):
		p := strings.TrimPrefix(strings.TrimPrefix(raw, "sqlite:"), "//")
		if p == "" {
			return Config{}, errors.New("sqlite URL has no path, e.g. sqlite:///data/flowmon.db")
		}
		return Config{Driver: SQLite, Path: p}, nil
	case strings.HasPrefix(raw, "postgres://"), strings.HasPrefix(raw, "postgresql://"):
		u, err := url.Parse(raw)
		if err != nil {
			return Config{}, fmt.Errorf("invalid Postgres URL: %w", err)
		}
		cfg := Config{Driver: Postgres}
		q := u.Query()
		cfg.Schema = q.Get("search_path")
		q.Del("search_path")
		u.RawQuery = q.Encode()
		cfg.URL = u.String()
		return cfg, nil
	}
	return Config{}, errors.New("database URL must start with sqlite: or postgres://")
}

// Redacted is the URL or path without its password, for logs and the UI.
func (c Config) Redacted() string {
	if c.Driver == SQLite {
		return "sqlite:" + c.Path
	}
	u, err := url.Parse(c.URL)
	if err != nil {
		return "postgres://(invalid)"
	}
	return u.Redacted()
}

// Store is safe for concurrent use.
type Store struct {
	db     *sql.DB
	driver Driver
}

// Open connects, creates the Postgres schema if needed, and applies migrations.
func Open(ctx context.Context, cfg Config) (*Store, error) {
	var db *sql.DB
	var err error
	switch cfg.Driver {
	case SQLite:
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o750); err != nil {
			return nil, fmt.Errorf("app store: %w", err)
		}
		dsn := "file:" + cfg.Path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
		db, err = sql.Open("sqlite", dsn)
	case Postgres:
		dsn, derr := postgresDSN(cfg)
		if derr != nil {
			return nil, derr
		}
		db, err = sql.Open("pgx", dsn)
	default:
		return nil, fmt.Errorf("app store: unknown driver %q", cfg.Driver)
	}
	if err != nil {
		return nil, fmt.Errorf("app store: %w", err)
	}
	s := &Store{db: db, driver: cfg.Driver}

	pingCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		db.Close()
		return nil, fmt.Errorf("app store: connecting to %s: %w", cfg.Redacted(), err)
	}
	if cfg.Driver == Postgres && cfg.Schema != "" {
		if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoteIdent(cfg.Schema)); err != nil {
			db.Close()
			return nil, fmt.Errorf("app store: creating schema %s: %w", cfg.Schema, err)
		}
	}
	if err := s.migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// TestConnection opens and closes a connection without migrating, for the setup screen.
func TestConnection(ctx context.Context, cfg Config) error {
	if cfg.Driver != Postgres {
		return nil
	}
	dsn, err := postgresDSN(cfg)
	if err != nil {
		return err
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return db.PingContext(ctx)
}

func postgresDSN(cfg Config) (string, error) {
	u, err := url.Parse(cfg.URL)
	if err != nil {
		return "", fmt.Errorf("invalid Postgres URL: %w", err)
	}
	if cfg.Password != "" {
		user := ""
		if u.User != nil {
			user = u.User.Username()
		}
		u.User = url.UserPassword(user, cfg.Password)
	}
	if cfg.Schema != "" {
		q := u.Query()
		q.Set("search_path", cfg.Schema)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Driver reports the engine.
func (s *Store) Driver() Driver { return s.driver }

// Ping checks the connection, for the "App Store unavailable" banner.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// q rewrites ? placeholders to $1, $2, … for Postgres. Queries in this package never
// contain a literal question mark.
func (s *Store) q(query string) string {
	if s.driver != Postgres {
		return query
	}
	var b strings.Builder
	n := 0
	for _, r := range query {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// migrations run on both SQLite and Postgres, so they use only types and syntax both
// understand: TEXT ids, BIGINT Unix milliseconds for times, INTEGER for booleans.
// Never edit an applied migration; add a new one.
var migrations = []string{
	// 1: accounts. Users and identities are separate so more users and OIDC logins
	// (provider, subject) can be added without changing these tables.
	`CREATE TABLE users (
		id TEXT PRIMARY KEY,
		username TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL DEFAULT '',
		is_admin INTEGER NOT NULL DEFAULT 0,
		disabled INTEGER NOT NULL DEFAULT 0,
		created_at BIGINT NOT NULL
	);
	CREATE TABLE identities (
		provider TEXT NOT NULL,
		subject TEXT NOT NULL,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at BIGINT NOT NULL,
		PRIMARY KEY (provider, subject)
	);
	CREATE TABLE sessions (
		token_hash TEXT PRIMARY KEY,
		user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		created_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL,
		last_seen_at BIGINT NOT NULL,
		client_ip TEXT NOT NULL DEFAULT '',
		user_agent TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX sessions_user ON sessions (user_id);
	CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at BIGINT NOT NULL
	)`,
	// 2: cached IP Enrichment that is looked up on demand (reverse DNS, RDAP), as JSON.
	`CREATE TABLE ip_cache (
		kind TEXT NOT NULL,
		ip TEXT NOT NULL,
		value TEXT NOT NULL,
		fetched_at BIGINT NOT NULL,
		expires_at BIGINT NOT NULL,
		PRIMARY KEY (kind, ip)
	);
	CREATE INDEX ip_cache_expires ON ip_cache (expires_at)`,
}

func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at BIGINT NOT NULL)`); err != nil {
		return fmt.Errorf("app store migrations: %w", err)
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return fmt.Errorf("app store migrations: %w", err)
	}
	if current > len(migrations) {
		return fmt.Errorf("app store is at schema version %d, newer than this Flowmon (%d); upgrade Flowmon", current, len(migrations))
	}
	for v := current + 1; v <= len(migrations); v++ {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		for _, stmt := range strings.Split(migrations[v-1], ";") {
			if strings.TrimSpace(stmt) == "" {
				continue
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("app store migration %d: %w", v, err)
			}
		}
		if _, err := tx.ExecContext(ctx, s.q(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`), v, time.Now().UnixMilli()); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("app store migration %d: %w", v, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("app store migration %d: %w", v, err)
		}
	}
	return nil
}

// SchemaVersion returns the applied schema version.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}

// Holder holds the App Store once it exists. Before first-run setup there is none, and
// collection keeps running regardless (ADR 0001).
type Holder struct{ p atomic.Pointer[Store] }

// Get returns the store, or nil before setup.
func (h *Holder) Get() *Store { return h.p.Load() }

// Set installs the store.
func (h *Holder) Set(s *Store) { h.p.Store(s) }

// DropSchemaForTest drops a Postgres schema. Tests only.
func (s *Store) DropSchemaForTest(ctx context.Context, schema string) error {
	_, err := s.db.ExecContext(ctx, `DROP SCHEMA IF EXISTS `+quoteIdent(schema)+` CASCADE`)
	return err
}
