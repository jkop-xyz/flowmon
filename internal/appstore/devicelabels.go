package appstore

import (
	"context"
	"time"
)

// DeviceLabels returns the names given to Devices, by MAC address.
func (s *Store) DeviceLabels(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, name FROM device_labels`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var mac, name string
		if err := rows.Scan(&mac, &name); err != nil {
			return nil, err
		}
		out[mac] = name
	}
	return out, rows.Err()
}

// SetDeviceLabel names a Device; an empty name removes its label.
func (s *Store) SetDeviceLabel(ctx context.Context, mac, name string, now time.Time) error {
	if name == "" {
		_, err := s.db.ExecContext(ctx, s.q(`DELETE FROM device_labels WHERE mac = ?`), mac)
		return err
	}
	_, err := s.db.ExecContext(ctx, s.q(`INSERT INTO device_labels (mac, name, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (mac) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`), mac, name, now.UnixMilli())
	return err
}
