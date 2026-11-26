package appstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// UserPreference returns one of a user's saved preferences, or ErrNotFound.
func (s *Store) UserPreference(ctx context.Context, userID, name string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT value FROM user_preferences WHERE user_id = ? AND name = ?`), userID, name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetUserPreference saves a preference, replacing any earlier value.
func (s *Store) SetUserPreference(ctx context.Context, userID, name, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO user_preferences (user_id, name, value, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (user_id, name) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`), userID, name, value, now.UnixMilli())
	return err
}

// DeleteUserPreference forgets a preference. Forgetting one that isn't saved is not an error.
func (s *Store) DeleteUserPreference(ctx context.Context, userID, name string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM user_preferences WHERE user_id = ? AND name = ?`), userID, name)
	return err
}
