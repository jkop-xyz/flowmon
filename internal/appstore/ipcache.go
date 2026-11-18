package appstore

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// CachedIPInfo returns a cached IP Enrichment value of kind (e.g. "rdns", "rdap") for
// ip and when it was fetched, or ErrNotFound when there is none or it has expired.
func (s *Store) CachedIPInfo(ctx context.Context, kind, ip string, now time.Time) (string, time.Time, error) {
	var value string
	var fetched int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT value, fetched_at FROM ip_cache WHERE kind = ? AND ip = ? AND expires_at > ?`),
		kind, ip, now.UnixMilli()).Scan(&value, &fetched)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, ErrNotFound
	}
	if err != nil {
		return "", time.Time{}, err
	}
	return value, time.UnixMilli(fetched).UTC(), nil
}

// CacheIPInfo stores an IP Enrichment value until now+ttl, replacing any older one.
func (s *Store) CacheIPInfo(ctx context.Context, kind, ip, value string, now time.Time, ttl time.Duration) error {
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO ip_cache (kind, ip, value, fetched_at, expires_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (kind, ip) DO UPDATE SET value = excluded.value, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at`),
		kind, ip, value, now.UnixMilli(), now.Add(ttl).UnixMilli())
	return err
}

// DeleteExpiredIPInfo removes expired cache entries.
func (s *Store) DeleteExpiredIPInfo(ctx context.Context, now time.Time) (int64, error) {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM ip_cache WHERE expires_at <= ?`), now.UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
