package appstore

import (
	"context"
	"time"
)

// ServiceName is a name the user gave a Service Port. Protocol 0 names the port for
// every protocol.
type ServiceName struct {
	Protocol  uint8
	Port      uint16
	Name      string
	UpdatedAt time.Time
}

// ServiceNames returns every user-defined Service Name, by port.
func (s *Store) ServiceNames(ctx context.Context) ([]ServiceName, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT protocol, port, name, updated_at FROM service_names ORDER BY port, protocol`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ServiceName
	for rows.Next() {
		var n ServiceName
		var proto, port int
		var updated int64
		if err := rows.Scan(&proto, &port, &n.Name, &updated); err != nil {
			return nil, err
		}
		n.Protocol, n.Port, n.UpdatedAt = uint8(proto), uint16(port), time.UnixMilli(updated).UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

// SetServiceName names a Service Port, replacing any name it had.
func (s *Store) SetServiceName(ctx context.Context, protocol uint8, port uint16, name string, now time.Time) (ServiceName, error) {
	n := ServiceName{Protocol: protocol, Port: port, Name: name, UpdatedAt: now.UTC().Truncate(time.Millisecond)}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO service_names (protocol, port, name, updated_at) VALUES (?, ?, ?, ?)
		ON CONFLICT (protocol, port) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`),
		int(protocol), int(port), name, n.UpdatedAt.UnixMilli())
	return n, err
}

// DeleteServiceName removes a user-defined Service Name.
func (s *Store) DeleteServiceName(ctx context.Context, protocol uint8, port uint16) error {
	res, err := s.db.ExecContext(ctx, s.q(`DELETE FROM service_names WHERE protocol = ? AND port = ?`), int(protocol), int(port))
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
