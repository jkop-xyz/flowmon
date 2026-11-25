package geoip

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/secret"
)

type memSettings map[string]string

func (m memSettings) Setting(_ context.Context, k string) (string, bool, error) {
	v, ok := m[k]
	return v, ok, nil
}

func (m memSettings) SetSettings(_ context.Context, v map[string]string) error {
	maps.Copy(m, v)
	return nil
}

func sources(m *Manager) []Source {
	var out []Source
	for _, d := range m.Status().Databases {
		out = append(out, d.Source)
	}
	return out
}

// The MaxMind account is entered in Settings, not the environment: a wrong key keeps
// DB-IP, the right one is tried at once and kept sealed across restarts.
func TestMaxMindAccountFromSettings(t *testing.T) {
	ctx := context.Background()
	srv := downloadServer(t)
	now := func() time.Time { return time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC) }
	dl := t.TempDir()
	box, err := secret.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	store := memSettings{}
	open := func() (*Manager, *Settings) {
		m := NewManager(Options{DownloadDir: dl, Download: true, MaxMindURL: srv.URL + "/maxmind", DBIPURL: srv.URL + "/dbip", Now: now}, &Resolver{})
		s, err := LoadSettings(ctx, store, box, m)
		if err != nil {
			t.Fatal(err)
		}
		return m, s
	}
	m, s := open()
	m.Refresh(ctx)
	if got := sources(m); got[0] != SourceDBIP || got[1] != SourceDBIP {
		t.Fatalf("without an account: %v", got)
	}

	str := func(v string) *string { return &v }
	if err := s.Update(ctx, Update{MaxMindAccountID: str("12345"), MaxMindLicenseKey: str("wrong")}); err != nil {
		t.Fatal(err)
	}
	m.Refresh(ctx)
	if got := sources(m); got[0] != SourceDBIP || !strings.Contains(m.Status().Databases[0].Error, "MaxMind: refused") {
		t.Fatalf("a wrong key: %v %+v", got, m.Status().Databases[0])
	}
	// The corrected key is tried now, not after RetryAfter; the account ID stays.
	if err := s.Update(ctx, Update{MaxMindLicenseKey: str("good")}); err != nil {
		t.Fatal(err)
	}
	m.Refresh(ctx)
	if got := sources(m); got[0] != SourceMaxMind || got[1] != SourceMaxMind {
		t.Fatalf("the right key: %v %+v", got, m.Status())
	}
	if st := m.Status(); st.MaxMindAccountID != "12345" || !st.MaxMindConfigured {
		t.Errorf("status %+v", st)
	}
	if strings.Contains(store[keyLicenseKey], "good") || store[keyAccountID] != "12345" {
		t.Errorf("stored %v: the key must be sealed", store)
	}

	// After a restart the stored account is used.
	m, s = open()
	m.Load()
	if got := sources(m); got[0] != SourceMaxMind {
		t.Fatalf("after a restart: %v", got)
	}

	// Removing the account goes back to DB-IP.
	if err := s.Update(ctx, Update{MaxMindAccountID: str("")}); err != nil {
		t.Fatal(err)
	}
	m.Refresh(ctx)
	if got := sources(m); got[0] != SourceDBIP || m.Status().MaxMindConfigured || store[keyLicenseKey] != "" {
		t.Fatalf("removed: %v %v", got, store)
	}

	// Downloads off: only mounted files.
	off := false
	if err := s.Update(ctx, Update{Download: &off}); err != nil {
		t.Fatal(err)
	}
	m.Refresh(ctx)
	if got := sources(m); got[0] != "" || m.Status().DownloadsEnabled {
		t.Fatalf("downloads off: %v", got)
	}
}

func TestGeoIPSettingsValidationAndLocks(t *testing.T) {
	ctx := context.Background()
	box, _ := secret.LoadOrCreate(filepath.Join(t.TempDir(), "secret.key"))
	str := func(v string) *string { return &v }
	m := NewManager(Options{Download: true}, &Resolver{})
	s, _ := LoadSettings(ctx, memSettings{}, box, m)
	for _, u := range []Update{
		{MaxMindAccountID: str("my-account"), MaxMindLicenseKey: str("k")},
		{MaxMindAccountID: str("12345")},
		{MaxMindAccountID: str("12345"), MaxMindLicenseKey: str("has space")},
	} {
		if err := s.Update(ctx, u); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v, want invalid", u, err)
		}
	}

	locked := NewManager(Options{Download: true, MaxMindAccountID: "1", MaxMindLicenseKey: "env", Locks: Locks{Download: true, MaxMind: true}}, &Resolver{})
	store := memSettings{keyAccountID: "2", keyDownload: "false"}
	ls, _ := LoadSettings(ctx, store, box, locked)
	if c := locked.config(); c.MaxMindAccountID != "1" || !c.Download {
		t.Errorf("stored settings must not override the environment: %+v", c)
	}
	off := false
	if err := ls.Update(ctx, Update{Download: &off}); !errors.Is(err, ErrLocked) {
		t.Errorf("download: %v", err)
	}
	if err := ls.Update(ctx, Update{MaxMindAccountID: str("3"), MaxMindLicenseKey: str("k")}); !errors.Is(err, ErrLocked) {
		t.Errorf("account: %v", err)
	}
	if st := locked.Status(); !st.Locks.Download || !st.Locks.MaxMind {
		t.Errorf("locks %+v", st.Locks)
	}
}

// A license key sealed with a key that is gone leaves MaxMind off, with a warning.
func TestUnreadableLicenseKey(t *testing.T) {
	ctx := context.Background()
	old, _ := secret.LoadOrCreate(filepath.Join(t.TempDir(), "old"))
	sealed, _ := old.Seal("good")
	lost, _ := secret.LoadOrCreate(filepath.Join(t.TempDir(), "new"))
	m := NewManager(Options{Download: true}, &Resolver{})
	if _, err := LoadSettings(ctx, memSettings{keyAccountID: "12345", keyLicenseKey: sealed}, lost, m); err != nil {
		t.Fatal(err)
	}
	m.Load()
	if st := m.Status(); st.MaxMindConfigured || st.MaxMindWarning == "" || st.MaxMindAccountID != "12345" {
		t.Errorf("status %+v", st)
	}
}
