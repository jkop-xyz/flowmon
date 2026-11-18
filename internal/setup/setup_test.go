package setup

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
)

var ctx = context.Background()

func newController(t *testing.T, locked bool) (*Controller, *appstore.Holder, string) {
	t.Helper()
	dir := t.TempDir()
	h := &appstore.Holder{}
	t.Cleanup(func() {
		if s := h.Get(); s != nil {
			s.Close()
		}
	})
	m := auth.NewManager(h.Get, auth.Options{})
	c := New(Options{Holder: h, Auth: m, BootstrapPath: filepath.Join(dir, "bootstrap.json"),
		DefaultSQLitePath: filepath.Join(dir, "flowmon.db"), Locked: locked})
	return c, h, dir
}

func TestFullSetupWithSQLite(t *testing.T) {
	c, h, dir := newController(t, false)
	st, _ := c.Status(ctx)
	if st.Step != StepDatabase || st.DatabaseUnavailable {
		t.Fatalf("status %+v", st)
	}

	if err := c.ConfigureDatabase(ctx, "wrong", Database{Driver: appstore.SQLite}); !errors.Is(err, ErrBadToken) {
		t.Errorf("wrong token: %v", err)
	}
	// Tokens are accepted without dashes and in lower case: people retype them.
	if err := c.ConfigureDatabase(ctx, " "+lowerNoDashes(c.Token())+" ", Database{Driver: appstore.SQLite}); err != nil {
		t.Fatal(err)
	}
	if h.Get() == nil {
		t.Fatal("store not installed")
	}
	info, err := os.Stat(filepath.Join(dir, "bootstrap.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("bootstrap.json: %v %v", info, err)
	}
	f, _ := Load(filepath.Join(dir, "bootstrap.json"))
	if f.Database.Driver != appstore.SQLite || f.Database.Path != filepath.Join(dir, "flowmon.db") {
		t.Errorf("saved %+v", f)
	}
	if err := c.ConfigureDatabase(ctx, c.Token(), Database{}); !errors.Is(err, ErrWrongStep) {
		t.Errorf("second database step: %v", err)
	}

	if st, _ := c.Status(ctx); st.Step != StepAdmin {
		t.Fatalf("status %+v", st)
	}
	if err := c.CreateAdmin(ctx, httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), c.Token(), "bad name!", "long enough password"); !errors.Is(err, ErrBadUsername) {
		t.Errorf("bad username: %v", err)
	}
	if err := c.CreateAdmin(ctx, httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), c.Token(), "admin", "short"); !errors.Is(err, auth.ErrWeakPassword) {
		t.Errorf("weak password: %v", err)
	}
	w := httptest.NewRecorder()
	if err := c.CreateAdmin(ctx, w, httptest.NewRequest("POST", "/", nil), c.Token(), "admin", "long enough password"); err != nil {
		t.Fatal(err)
	}
	if len(w.Result().Cookies()) != 1 {
		t.Error("creating the admin logs them in")
	}
	if st, _ := c.Status(ctx); st.Step != StepDone {
		t.Errorf("status %+v", st)
	}
	if err := c.CreateAdmin(ctx, httptest.NewRecorder(), httptest.NewRequest("POST", "/", nil), c.Token(), "second", "long enough password"); !errors.Is(err, ErrWrongStep) {
		t.Errorf("a second admin through setup: %v", err)
	}
}

func lowerNoDashes(s string) string {
	out := []rune{}
	for _, r := range s {
		if r == '-' {
			continue
		}
		if r >= 'A' && r <= 'Z' {
			r += 'a' - 'A'
		}
		out = append(out, r)
	}
	return string(out)
}

func TestLockedDatabase(t *testing.T) {
	c, _, _ := newController(t, true)
	if err := c.ConfigureDatabase(ctx, c.Token(), Database{}); !errors.Is(err, ErrLocked) {
		t.Errorf("%v", err)
	}
	if st, _ := c.Status(ctx); !st.DatabaseLocked || !st.DatabaseUnavailable {
		t.Errorf("status %+v", st)
	}
}

func TestPostgresConfig(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	_ = os.WriteFile(pw, []byte("s3cret\n"), 0o600)
	cfg, err := Database{Driver: appstore.Postgres, Host: "db.lan", Database: "lab", User: "flowmon", PasswordFile: pw, Schema: "flowmon", SSLMode: "require"}.Config("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "postgres://flowmon@db.lan:5432/lab?sslmode=require" || cfg.Password != "s3cret" || cfg.Schema != "flowmon" {
		t.Errorf("%+v", cfg)
	}
	for _, bad := range []Database{
		{Driver: appstore.Postgres, Host: "db"},
		{Driver: appstore.Postgres, Host: "db", Database: "d", User: "u", SSLMode: "yes-please"},
		{Driver: appstore.Postgres, Host: "db", Database: "d", User: "u", Port: 70000},
		{Driver: "mysql"},
	} {
		if _, err := bad.Config(""); err == nil {
			t.Errorf("%+v: expected an error", bad)
		}
	}
}

func TestPostgresSetup(t *testing.T) {
	raw := os.Getenv("FLOWMON_TEST_POSTGRES_URL")
	if raw == "" {
		t.Skip("FLOWMON_TEST_POSTGRES_URL not set")
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	password, _ := u.User.Password()
	c, h, _ := newController(t, false)
	d := Database{Driver: appstore.Postgres, Host: u.Hostname(), Port: port, Database: strings.TrimPrefix(u.Path, "/"),
		User: u.User.Username(), Password: password, Schema: fmt.Sprintf("setup_test_%d", time.Now().UnixNano()), SSLMode: "disable"}
	if err := c.TestDatabase(ctx, c.Token(), d); err != nil {
		t.Fatalf("test connection: %v", err)
	}
	bad := d
	bad.Password = "wrong"
	if err := c.TestDatabase(ctx, c.Token(), bad); err == nil {
		t.Error("a wrong password must fail the connection test")
	}
	if err := c.ConfigureDatabase(ctx, c.Token(), d); err != nil {
		t.Fatal(err)
	}
	if h.Get().Driver() != appstore.Postgres {
		t.Error("expected Postgres")
	}
	// Clean up the schema the test created.
	cleanup, err := appstore.Open(ctx, appstore.Config{Driver: appstore.Postgres, URL: raw})
	if err == nil {
		_ = cleanup.DropSchemaForTest(ctx, d.Schema)
		cleanup.Close()
	}
}

func TestSeedAdmin(t *testing.T) {
	s, err := appstore.Open(ctx, appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if ok, err := SeedAdmin(ctx, s, "", "", time.Now()); ok || err != nil {
		t.Errorf("no env: %v %v", ok, err)
	}
	if ok, err := SeedAdmin(ctx, s, "admin", "long enough password", time.Now()); !ok || err != nil {
		t.Errorf("seed: %v %v", ok, err)
	}
	if ok, err := SeedAdmin(ctx, s, "other", "long enough password", time.Now()); ok || err != nil {
		t.Errorf("only when no account exists: %v %v", ok, err)
	}
}
