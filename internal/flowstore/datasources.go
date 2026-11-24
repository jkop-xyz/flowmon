package flowstore

import (
	"context"
	"time"
)

// DataSource is a stored Data Source. Settings is a JSON object whose secret fields are
// already sealed; the datasource package reads and writes it.
type DataSource struct {
	ID       string
	Kind     string
	Name     string
	Enabled  bool
	Settings string
	Created  time.Time
}

// DataSources returns every stored Data Source, oldest first.
func (s *Store) DataSources(ctx context.Context) ([]DataSource, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, kind, name, enabled, settings, created FROM data_sources ORDER BY created, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DataSource
	for rows.Next() {
		var d DataSource
		if err := rows.Scan(&d.ID, &d.Kind, &d.Name, &d.Enabled, &d.Settings, &d.Created); err != nil {
			return nil, err
		}
		d.Created = d.Created.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// PutDataSource creates or replaces a Data Source.
func (s *Store) PutDataSource(ctx context.Context, d DataSource) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO data_sources VALUES (?, ?, ?, ?, ?, ?)`,
		d.ID, d.Kind, d.Name, d.Enabled, d.Settings, d.Created.UTC())
	return err
}

// DeleteDataSource removes a Data Source. Removing one that doesn't exist is not an error.
func (s *Store) DeleteDataSource(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM data_sources WHERE id = ?`, id)
	return err
}
