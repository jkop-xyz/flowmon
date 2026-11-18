package flowstore

import (
	"context"
	"database/sql"
	"errors"
	"os"
)

// Setting returns a stored setting, and whether there is one.
func (s *Store) Setting(ctx context.Context, key string) (string, bool, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

// SetSettings stores settings together; an empty value deletes its key.
func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for k, v := range values {
		if v == "" {
			_, err = tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, k)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT OR REPLACE INTO settings VALUES (?, ?)`, k, v)
		}
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

// DiskUsage is the size of the Flow Store's files: the database and its write-ahead log.
func (s *Store) DiskUsage() int64 {
	var n int64
	for _, p := range []string{s.path, s.path + ".wal"} {
		if fi, err := os.Stat(p); err == nil {
			n += fi.Size()
		}
	}
	return n
}
