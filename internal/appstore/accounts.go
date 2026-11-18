package appstore

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"
)

// ErrNotFound is returned when a row doesn't exist.
var ErrNotFound = errors.New("appstore: not found")

// ErrExists is returned when a unique value is already taken.
var ErrExists = errors.New("appstore: already exists")

// User is an account.
type User struct {
	ID           string
	Username     string
	PasswordHash string
	IsAdmin      bool
	Disabled     bool
	CreatedAt    time.Time
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func isUniqueViolation(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "unique") || strings.Contains(msg, "duplicate key")
}

// CreateUser adds a user. Usernames are unique, compared case-insensitively.
func (s *Store) CreateUser(ctx context.Context, username, passwordHash string, isAdmin bool, now time.Time) (User, error) {
	u := User{ID: newID(), Username: strings.ToLower(strings.TrimSpace(username)), PasswordHash: passwordHash, IsAdmin: isAdmin, CreatedAt: now.UTC().Truncate(time.Millisecond)}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO users (id, username, password_hash, is_admin, disabled, created_at) VALUES (?, ?, ?, ?, 0, ?)`),
		u.ID, u.Username, u.PasswordHash, boolInt(isAdmin), u.CreatedAt.UnixMilli())
	if err != nil {
		if isUniqueViolation(err) {
			return User{}, ErrExists
		}
		return User{}, err
	}
	return u, nil
}

// CountUsers returns how many users exist.
func (s *Store) CountUsers(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n, err
}

func (s *Store) scanUser(row *sql.Row) (User, error) {
	var u User
	var admin, disabled int
	var created int64
	if err := row.Scan(&u.ID, &u.Username, &u.PasswordHash, &admin, &disabled, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return User{}, ErrNotFound
		}
		return User{}, err
	}
	u.IsAdmin, u.Disabled, u.CreatedAt = admin != 0, disabled != 0, time.UnixMilli(created).UTC()
	return u, nil
}

const userColumns = `id, username, password_hash, is_admin, disabled, created_at`

// UserByUsername looks a user up by username (case-insensitive).
func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, s.q(`SELECT `+userColumns+` FROM users WHERE username = ?`), strings.ToLower(strings.TrimSpace(username))))
}

// UserByID looks a user up by ID.
func (s *Store) UserByID(ctx context.Context, id string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, s.q(`SELECT `+userColumns+` FROM users WHERE id = ?`), id))
}

// SetPassword replaces a user's password hash and ends all their sessions.
func (s *Store) SetPassword(ctx context.Context, userID, passwordHash string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.ExecContext(ctx, s.q(`UPDATE users SET password_hash = ? WHERE id = ?`), passwordHash, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE user_id = ?`), userID); err != nil {
		return err
	}
	return tx.Commit()
}

// AddIdentity links an external login (for example an OIDC provider and subject) to a user.
func (s *Store) AddIdentity(ctx context.Context, provider, subject, userID string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO identities (provider, subject, user_id, created_at) VALUES (?, ?, ?, ?)`),
		provider, subject, userID, now.UnixMilli())
	if err != nil && isUniqueViolation(err) {
		return ErrExists
	}
	return err
}

// UserByIdentity finds the user an external login belongs to.
func (s *Store) UserByIdentity(ctx context.Context, provider, subject string) (User, error) {
	return s.scanUser(s.db.QueryRowContext(ctx, s.q(`SELECT u.id, u.username, u.password_hash, u.is_admin, u.disabled, u.created_at
		FROM identities i JOIN users u ON u.id = i.user_id WHERE i.provider = ? AND i.subject = ?`), provider, subject))
}

// Session is a server-side login session. Only a hash of its token is stored.
type Session struct {
	TokenHash  string
	UserID     string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	ClientIP   string
	UserAgent  string
}

// CreateSession stores a session.
func (s *Store) CreateSession(ctx context.Context, sess Session) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, client_ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)`),
		sess.TokenHash, sess.UserID, sess.CreatedAt.UnixMilli(), sess.ExpiresAt.UnixMilli(), sess.LastSeenAt.UnixMilli(), sess.ClientIP, sess.UserAgent)
	return err
}

// SessionByTokenHash returns an unexpired session.
func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string, now time.Time) (Session, error) {
	var sess Session
	var created, expires, seen int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT token_hash, user_id, created_at, expires_at, last_seen_at, client_ip, user_agent
		FROM sessions WHERE token_hash = ? AND expires_at > ?`), tokenHash, now.UnixMilli()).
		Scan(&sess.TokenHash, &sess.UserID, &created, &expires, &seen, &sess.ClientIP, &sess.UserAgent)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}
	sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC(), time.UnixMilli(seen).UTC()
	return sess, nil
}

// TouchSession extends a session's expiry (sliding sessions).
func (s *Store) TouchSession(ctx context.Context, tokenHash string, seen, expires time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`), seen.UnixMilli(), expires.UnixMilli(), tokenHash)
	return err
}

// DeleteSession ends a session.
func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE token_hash = ?`), tokenHash)
	return err
}

// DeleteExpiredSessions removes sessions that have expired.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM sessions WHERE expires_at <= ?`), now.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Setting returns a stored setting.
func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, s.q(`SELECT value FROM settings WHERE key = ?`), key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return v, err
}

// SetSetting stores a setting.
func (s *Store) SetSetting(ctx context.Context, key, value string, now time.Time) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`), key, value, now.UnixMilli())
	return err
}
