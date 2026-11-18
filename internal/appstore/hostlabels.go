package appstore

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"time"
)

// HostLabel is a name the user gave to a Local Host, or to a prefix so that a host's
// rotating addresses stay together.
type HostLabel struct {
	ID string
	// Prefix is masked; a single address is a full-length prefix.
	Prefix    netip.Prefix
	Name      string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// HostLabels returns every Host Label.
func (s *Store) HostLabels(ctx context.Context) ([]HostLabel, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, prefix, name, created_at, updated_at FROM host_labels ORDER BY name, prefix`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HostLabel
	for rows.Next() {
		var l HostLabel
		var prefix string
		var created, updated int64
		if err := rows.Scan(&l.ID, &prefix, &l.Name, &created, &updated); err != nil {
			return nil, err
		}
		if l.Prefix, err = netip.ParsePrefix(prefix); err != nil {
			return nil, err
		}
		l.CreatedAt, l.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
		out = append(out, l)
	}
	return out, rows.Err()
}

// CreateHostLabel adds a Host Label. Each prefix has at most one; ErrExists says it has.
func (s *Store) CreateHostLabel(ctx context.Context, prefix netip.Prefix, name string, now time.Time) (HostLabel, error) {
	now = now.UTC().Truncate(time.Millisecond)
	l := HostLabel{ID: newID(), Prefix: prefix.Masked(), Name: name, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO host_labels (id, prefix, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`),
		l.ID, l.Prefix.String(), l.Name, now.UnixMilli(), now.UnixMilli())
	if err != nil {
		if isUniqueViolation(err) {
			return HostLabel{}, ErrExists
		}
		return HostLabel{}, err
	}
	return l, nil
}

// UpdateHostLabel changes a Host Label's prefix and name.
func (s *Store) UpdateHostLabel(ctx context.Context, id string, prefix netip.Prefix, name string, now time.Time) (HostLabel, error) {
	now = now.UTC().Truncate(time.Millisecond)
	res, err := s.db.ExecContext(ctx, s.q(`UPDATE host_labels SET prefix = ?, name = ?, updated_at = ? WHERE id = ?`),
		prefix.Masked().String(), name, now.UnixMilli(), id)
	if err != nil {
		if isUniqueViolation(err) {
			return HostLabel{}, ErrExists
		}
		return HostLabel{}, err
	}
	if n, err := res.RowsAffected(); err != nil {
		return HostLabel{}, err
	} else if n == 0 {
		return HostLabel{}, ErrNotFound
	}
	return s.hostLabel(ctx, id)
}

func (s *Store) hostLabel(ctx context.Context, id string) (HostLabel, error) {
	var l HostLabel
	var prefix string
	var created, updated int64
	err := s.db.QueryRowContext(ctx, s.q(`SELECT id, prefix, name, created_at, updated_at FROM host_labels WHERE id = ?`), id).
		Scan(&l.ID, &prefix, &l.Name, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return HostLabel{}, ErrNotFound
	}
	if err != nil {
		return HostLabel{}, err
	}
	if l.Prefix, err = netip.ParsePrefix(prefix); err != nil {
		return HostLabel{}, err
	}
	l.CreatedAt, l.UpdatedAt = time.UnixMilli(created).UTC(), time.UnixMilli(updated).UTC()
	return l, nil
}

// DeleteHostLabel removes a Host Label.
func (s *Store) DeleteHostLabel(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM host_labels WHERE id = ?`), id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err != nil {
		return err
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}
