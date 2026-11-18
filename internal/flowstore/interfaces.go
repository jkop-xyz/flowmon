package flowstore

import (
	"context"
	"database/sql"

	"net/netip"
	"strings"
	"time"
)

// MappingRecord is one interface number → name mapping and when it was valid.
type MappingRecord struct {
	Exporter  netip.Addr
	Index     uint32
	Name      string
	Source    string
	ValidFrom time.Time
	// ValidTo is zero while the mapping is current.
	ValidTo time.Time
}

// InterfaceMappings returns every mapping, current and past.
func (s *Store) InterfaceMappings(ctx context.Context) ([]MappingRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exporter, if_index, name, source, valid_from, valid_to FROM interface_mappings ORDER BY exporter, if_index, valid_from`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MappingRecord
	for rows.Next() {
		var m MappingRecord
		var exp string
		var to sql.NullTime
		if err := rows.Scan(&exp, &m.Index, &m.Name, &m.Source, &m.ValidFrom, &to); err != nil {
			return nil, err
		}
		if m.Exporter, err = netip.ParseAddr(exp); err != nil {
			continue
		}
		m.ValidFrom = m.ValidFrom.UTC()
		if to.Valid {
			m.ValidTo = to.Time.UTC()
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// ReplaceMapping ends the current mapping of an interface number at m.ValidFrom (if
// there is one) and starts m.
func (s *Store) ReplaceMapping(ctx context.Context, m MappingRecord) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `UPDATE interface_mappings SET valid_to = ? WHERE exporter = ? AND if_index = ? AND valid_to IS NULL`,
		m.ValidFrom.UTC(), m.Exporter.String(), m.Index); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO interface_mappings VALUES (?, ?, ?, ?, ?, NULL)`,
		m.Exporter.String(), m.Index, m.Name, m.Source, m.ValidFrom.UTC()); err != nil {
		return err
	}
	return tx.Commit()
}

// InterfaceRoles returns every Interface Role: exporter → interface name → role.
func (s *Store) InterfaceRoles(ctx context.Context) (map[netip.Addr]map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exporter, name, role FROM interface_roles`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[netip.Addr]map[string]string{}
	for rows.Next() {
		var exp, name, role string
		if err := rows.Scan(&exp, &name, &role); err != nil {
			return nil, err
		}
		a, err := netip.ParseAddr(exp)
		if err != nil {
			continue
		}
		if out[a] == nil {
			out[a] = map[string]string{}
		}
		out[a][name] = role
	}
	return out, rows.Err()
}

// SetInterfaceRoles replaces all roles of an Exporter.
func (s *Store) SetInterfaceRoles(ctx context.Context, exporter netip.Addr, roles map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM interface_roles WHERE exporter = ?`, exporter.String()); err != nil {
		return err
	}
	for name, role := range roles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO interface_roles VALUES (?, ?, ?)`, exporter.String(), name, role); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ClassificationChange marks when an Exporter's classification changed.
type ClassificationChange struct {
	Exporter    string    `json:"exporter"`
	At          time.Time `json:"t"`
	Description string    `json:"description"`
}

// AddClassificationChange records a change, for chart markers.
func (s *Store) AddClassificationChange(ctx context.Context, exporter netip.Addr, at time.Time, description string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO classification_changes VALUES (?, ?, ?)`, exporter.String(), at.UTC(), description)
	return err
}

// ClassificationChanges returns changes in [from, to), optionally for one Exporter.
func (s *Store) ClassificationChanges(ctx context.Context, from, to time.Time, exporter netip.Addr) ([]ClassificationChange, error) {
	q := `SELECT exporter, changed_at, description FROM classification_changes WHERE changed_at >= ? AND changed_at < ?`
	args := []any{from.UTC(), to.UTC()}
	if exporter.IsValid() {
		q += ` AND exporter = ?`
		args = append(args, exporter.String())
	}
	rows, err := s.db.QueryContext(ctx, q+` ORDER BY changed_at`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ClassificationChange{}
	for rows.Next() {
		var c ClassificationChange
		if err := rows.Scan(&c.Exporter, &c.At, &c.Description); err != nil {
			return nil, err
		}
		c.At = c.At.UTC()
		out = append(out, c)
	}
	return out, rows.Err()
}

// RawDays returns the raw day tables whose day ends after from, oldest first.
func (s *Store) RawDays(from time.Time) []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Time
	for name := range s.days {
		day, err := time.Parse("20060102", strings.TrimPrefix(name, "flows_"))
		if err == nil && day.Add(24*time.Hour).After(from) {
			out = append(out, day.UTC())
		}
	}
	sortTimes(out)
	return out
}

func sortTimes(ts []time.Time) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].Before(ts[j-1]); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
