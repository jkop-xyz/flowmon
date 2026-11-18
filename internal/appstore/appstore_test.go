package appstore

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"testing"
	"time"
)

var (
	ctx = context.Background()
	t0  = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

// forEachDriver runs a test against SQLite and, when FLOWMON_TEST_POSTGRES_URL is set
// (it is in CI), against Postgres in a fresh schema, so both engines stay in step.
func forEachDriver(t *testing.T, test func(t *testing.T, s *Store)) {
	t.Run("sqlite", func(t *testing.T) {
		s, err := Open(ctx, Config{Driver: SQLite, Path: filepath.Join(t.TempDir(), "flowmon.db")})
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		test(t, s)
	})
	t.Run("postgres", func(t *testing.T) {
		raw := os.Getenv("FLOWMON_TEST_POSTGRES_URL")
		if raw == "" {
			t.Skip("FLOWMON_TEST_POSTGRES_URL not set")
		}
		cfg, err := ParseURL(raw)
		if err != nil {
			t.Fatal(err)
		}
		cfg.Schema = fmt.Sprintf("test_%d", time.Now().UnixNano())
		s, err := Open(ctx, cfg)
		if err != nil {
			t.Fatal(err)
		}
		defer func() {
			_, _ = s.db.ExecContext(ctx, `DROP SCHEMA `+quoteIdent(cfg.Schema)+` CASCADE`)
			s.Close()
		}()
		test(t, s)
	})
}

func TestMigrations(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		v, err := s.SchemaVersion(ctx)
		if err != nil || v != len(migrations) {
			t.Fatalf("version %d, %v", v, err)
		}
		if err := s.migrate(ctx); err != nil {
			t.Fatalf("re-running migrations: %v", err)
		}
	})
}

func TestUsers(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		if n, _ := s.CountUsers(ctx); n != 0 {
			t.Fatalf("%d users before any were created", n)
		}
		u, err := s.CreateUser(ctx, " Admin ", "hash1", true, t0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateUser(ctx, "admin", "hash2", false, t0); !errors.Is(err, ErrExists) {
			t.Errorf("duplicate username: %v", err)
		}
		got, err := s.UserByUsername(ctx, "ADMIN")
		if err != nil || got.ID != u.ID || got.Username != "admin" || !got.IsAdmin || got.PasswordHash != "hash1" || !got.CreatedAt.Equal(t0) {
			t.Errorf("by username: %+v %v", got, err)
		}
		if _, err := s.UserByUsername(ctx, "nobody"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing user: %v", err)
		}
		if got, err := s.UserByID(ctx, u.ID); err != nil || got.Username != "admin" {
			t.Errorf("by id: %+v %v", got, err)
		}
		if n, _ := s.CountUsers(ctx); n != 1 {
			t.Errorf("%d users", n)
		}
	})
}

func TestSessionsAndPasswordReset(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		u, _ := s.CreateUser(ctx, "admin", "old", true, t0)
		sess := Session{TokenHash: "abc", UserID: u.ID, CreatedAt: t0, ExpiresAt: t0.Add(time.Hour), LastSeenAt: t0, ClientIP: "192.168.10.44", UserAgent: "test"}
		if err := s.CreateSession(ctx, sess); err != nil {
			t.Fatal(err)
		}
		got, err := s.SessionByTokenHash(ctx, "abc", t0.Add(time.Minute))
		if err != nil || got.UserID != u.ID || got.ClientIP != "192.168.10.44" {
			t.Fatalf("%+v %v", got, err)
		}
		if _, err := s.SessionByTokenHash(ctx, "abc", t0.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
			t.Errorf("expired session still valid: %v", err)
		}
		if err := s.TouchSession(ctx, "abc", t0.Add(time.Minute), t0.Add(3*time.Hour)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SessionByTokenHash(ctx, "abc", t0.Add(2*time.Hour)); err != nil {
			t.Errorf("touched session should be valid: %v", err)
		}

		if err := s.SetPassword(ctx, u.ID, "new"); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.UserByID(ctx, u.ID); got.PasswordHash != "new" {
			t.Errorf("hash %q", got.PasswordHash)
		}
		if _, err := s.SessionByTokenHash(ctx, "abc", t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("a password reset must end existing sessions: %v", err)
		}
		if err := s.SetPassword(ctx, "nope", "x"); !errors.Is(err, ErrNotFound) {
			t.Errorf("unknown user: %v", err)
		}

		_ = s.CreateSession(ctx, Session{TokenHash: "old", UserID: u.ID, CreatedAt: t0, ExpiresAt: t0, LastSeenAt: t0})
		if n, err := s.DeleteExpiredSessions(ctx, t0.Add(time.Second)); err != nil || n != 1 {
			t.Errorf("expired cleanup: %d %v", n, err)
		}
	})
}

func TestIdentities(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		u, _ := s.CreateUser(ctx, "jakob", "", false, t0)
		if err := s.AddIdentity(ctx, "oidc:authentik", "sub-123", u.ID, t0); err != nil {
			t.Fatal(err)
		}
		if err := s.AddIdentity(ctx, "oidc:authentik", "sub-123", u.ID, t0); !errors.Is(err, ErrExists) {
			t.Errorf("duplicate identity: %v", err)
		}
		got, err := s.UserByIdentity(ctx, "oidc:authentik", "sub-123")
		if err != nil || got.ID != u.ID {
			t.Errorf("%+v %v", got, err)
		}
	})
}

func TestSettings(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		if _, err := s.Setting(ctx, "local_networks"); !errors.Is(err, ErrNotFound) {
			t.Errorf("missing: %v", err)
		}
		for _, v := range []string{"10.0.0.0/8", "10.0.0.0/8,100.64.0.0/10"} {
			if err := s.SetSetting(ctx, "local_networks", v, t0); err != nil {
				t.Fatal(err)
			}
			if got, _ := s.Setting(ctx, "local_networks"); got != v {
				t.Errorf("got %q want %q", got, v)
			}
		}
	})
}

func TestParseURL(t *testing.T) {
	cfg, err := ParseURL("sqlite:///data/flowmon.db")
	if err != nil || cfg.Driver != SQLite || cfg.Path != "/data/flowmon.db" {
		t.Errorf("%+v %v", cfg, err)
	}
	cfg, err = ParseURL("postgres://flowmon:secret@db.lan:5432/lab?sslmode=require&search_path=flowmon")
	if err != nil || cfg.Driver != Postgres || cfg.Schema != "flowmon" || cfg.URL != "postgres://flowmon:secret@db.lan:5432/lab?sslmode=require" {
		t.Errorf("%+v %v", cfg, err)
	}
	if r := cfg.Redacted(); r != "postgres://flowmon:xxxxx@db.lan:5432/lab?sslmode=require" {
		t.Errorf("redacted %q", r)
	}
	for _, bad := range []string{"mysql://x", "sqlite:"} {
		if _, err := ParseURL(bad); err == nil {
			t.Errorf("%s: expected an error", bad)
		}
	}
}

func TestPlaceholderRewrite(t *testing.T) {
	pg := &Store{driver: Postgres}
	if got := pg.q(`SELECT a FROM t WHERE b = ? AND c = ?`); got != `SELECT a FROM t WHERE b = $1 AND c = $2` {
		t.Errorf("%q", got)
	}
	sq := &Store{driver: SQLite}
	if got := sq.q(`WHERE b = ?`); got != `WHERE b = ?` {
		t.Errorf("%q", got)
	}
}

func TestIPCache(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		ctx := context.Background()
		now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
		if _, _, err := s.CachedIPInfo(ctx, "rdns", "1.1.1.1", now); !errors.Is(err, ErrNotFound) {
			t.Fatalf("empty cache: %v", err)
		}
		if err := s.CacheIPInfo(ctx, "rdns", "1.1.1.1", `{"name":"one.one.one.one"}`, now, 24*time.Hour); err != nil {
			t.Fatal(err)
		}
		if err := s.CacheIPInfo(ctx, "rdap", "1.1.1.1", `{}`, now, time.Hour); err != nil {
			t.Fatal(err)
		}
		v, fetched, err := s.CachedIPInfo(ctx, "rdns", "1.1.1.1", now.Add(23*time.Hour))
		if err != nil || v != `{"name":"one.one.one.one"}` || !fetched.Equal(now) {
			t.Errorf("cached %q %v %v", v, fetched, err)
		}
		// Replacing keeps one entry per kind and address.
		if err := s.CacheIPInfo(ctx, "rdns", "1.1.1.1", `{"name":"x"}`, now.Add(time.Hour), 24*time.Hour); err != nil {
			t.Fatal(err)
		}
		if v, _, _ := s.CachedIPInfo(ctx, "rdns", "1.1.1.1", now.Add(2*time.Hour)); v != `{"name":"x"}` {
			t.Errorf("after replacing: %q", v)
		}
		// Expired entries aren't returned and are deleted.
		if _, _, err := s.CachedIPInfo(ctx, "rdap", "1.1.1.1", now.Add(2*time.Hour)); !errors.Is(err, ErrNotFound) {
			t.Errorf("expired: %v", err)
		}
		if n, err := s.DeleteExpiredIPInfo(ctx, now.Add(2*time.Hour)); err != nil || n != 1 {
			t.Errorf("deleted %d %v", n, err)
		}
	})
}

func TestHostLabels(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		laptop, err := s.CreateHostLabel(ctx, netip.MustParsePrefix("2001:db8:0:1::17/120"), "Laptop", t0)
		if err != nil {
			t.Fatal(err)
		}
		if laptop.Prefix.String() != "2001:db8:0:1::/120" {
			t.Errorf("prefix not masked: %v", laptop.Prefix)
		}
		nas, err := s.CreateHostLabel(ctx, netip.PrefixFrom(netip.MustParseAddr("192.168.1.10"), 32), "NAS", t0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateHostLabel(ctx, netip.MustParsePrefix("192.168.1.10/32"), "Other", t0); !errors.Is(err, ErrExists) {
			t.Errorf("duplicate prefix: %v", err)
		}
		got, err := s.UpdateHostLabel(ctx, nas.ID, nas.Prefix, "Storage", t0.Add(time.Hour))
		if err != nil || got.Name != "Storage" || !got.UpdatedAt.Equal(t0.Add(time.Hour)) || !got.CreatedAt.Equal(t0) {
			t.Fatalf("updated %+v %v", got, err)
		}
		if _, err := s.UpdateHostLabel(ctx, nas.ID, laptop.Prefix, "x", t0); !errors.Is(err, ErrExists) {
			t.Errorf("update onto a taken prefix: %v", err)
		}
		if _, err := s.UpdateHostLabel(ctx, "missing", nas.Prefix, "x", t0); !errors.Is(err, ErrNotFound) {
			t.Errorf("update missing: %v", err)
		}
		all, err := s.HostLabels(ctx)
		if err != nil || len(all) != 2 || all[0].Name != "Laptop" || all[1].Prefix != nas.Prefix {
			t.Fatalf("labels %+v %v", all, err)
		}
		if err := s.DeleteHostLabel(ctx, laptop.ID); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteHostLabel(ctx, laptop.ID); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete twice: %v", err)
		}
	})
}

func TestServiceNames(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		if _, err := s.SetServiceName(ctx, 6, 8006, "proxmox", t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetServiceName(ctx, 0, 1883, "mqtt broker", t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.SetServiceName(ctx, 6, 8006, "Proxmox VE", t0.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		all, err := s.ServiceNames(ctx)
		if err != nil || len(all) != 2 || all[0].Port != 1883 || all[0].Protocol != 0 || all[1].Name != "Proxmox VE" || !all[1].UpdatedAt.Equal(t0.Add(time.Hour)) {
			t.Fatalf("names %+v %v", all, err)
		}
		if err := s.DeleteServiceName(ctx, 6, 8006); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteServiceName(ctx, 6, 8006); !errors.Is(err, ErrNotFound) {
			t.Errorf("delete twice: %v", err)
		}
	})
}
