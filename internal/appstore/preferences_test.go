package appstore

import (
	"errors"
	"testing"
	"time"
)

func TestUserPreferences(t *testing.T) {
	forEachDriver(t, func(t *testing.T, s *Store) {
		u, err := s.CreateUser(ctx, "alice", "h", false, t0)
		if err != nil {
			t.Fatal(err)
		}
		other, err := s.CreateUser(ctx, "bob", "h", false, t0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UserPreference(ctx, u.ID, "overview-layout"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("new user: %v", err)
		}
		if err := s.SetUserPreference(ctx, u.ID, "overview-layout", `{"a":1}`, t0); err != nil {
			t.Fatal(err)
		}
		if v, err := s.UserPreference(ctx, u.ID, "overview-layout"); err != nil || v != `{"a":1}` {
			t.Fatalf("read: %q %v", v, err)
		}
		if err := s.SetUserPreference(ctx, u.ID, "overview-layout", `{"a":2}`, t0.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if v, _ := s.UserPreference(ctx, u.ID, "overview-layout"); v != `{"a":2}` {
			t.Errorf("replace: %q", v)
		}
		if _, err := s.UserPreference(ctx, other.ID, "overview-layout"); !errors.Is(err, ErrNotFound) {
			t.Errorf("another user's: %v", err)
		}
		if err := s.DeleteUserPreference(ctx, u.ID, "overview-layout"); err != nil {
			t.Fatal(err)
		}
		if err := s.DeleteUserPreference(ctx, u.ID, "overview-layout"); err != nil {
			t.Errorf("deleting twice: %v", err)
		}
		if _, err := s.UserPreference(ctx, u.ID, "overview-layout"); !errors.Is(err, ErrNotFound) {
			t.Errorf("after delete: %v", err)
		}

		// The row goes with its user.
		if err := s.SetUserPreference(ctx, other.ID, "overview-layout", `{}`, t0); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.ExecContext(ctx, s.q(`DELETE FROM users WHERE id = ?`), other.ID); err != nil {
			t.Fatal(err)
		}
		var n int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM user_preferences`).Scan(&n); err != nil || n != 0 {
			t.Errorf("rows left after deleting the user: %d %v", n, err)
		}
	})
}
