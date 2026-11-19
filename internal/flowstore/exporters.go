package flowstore

import (
	"context"
	"net/netip"
	"time"
)

// ExporterRecord is a decision about an Exporter, as stored.
type ExporterRecord struct {
	Address            netip.Addr
	Status             string
	Role               string
	ClassificationMode string
	Name               string
	DecidedAt          time.Time
	DecidedBy          string
	// SamplingRate is set by hand for an Exporter that samples without announcing it.
	SamplingRate uint32
}

// ExporterRecords returns every stored Exporter decision.
func (s *Store) ExporterRecords(ctx context.Context) ([]ExporterRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT address, status, role, classification_mode, name, decided_at, decided_by, coalesce(sampling_rate, 0) FROM exporters ORDER BY address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ExporterRecord
	for rows.Next() {
		var r ExporterRecord
		var addr string
		if err := rows.Scan(&addr, &r.Status, &r.Role, &r.ClassificationMode, &r.Name, &r.DecidedAt, &r.DecidedBy, &r.SamplingRate); err != nil {
			return nil, err
		}
		if r.Address, err = netip.ParseAddr(addr); err != nil {
			continue
		}
		r.DecidedAt = r.DecidedAt.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveExporterRecord inserts or replaces a decision.
func (s *Store) SaveExporterRecord(ctx context.Context, r ExporterRecord) error {
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO exporters (address, status, role, classification_mode, name, decided_at, decided_by, sampling_rate)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.Address.String(), r.Status, r.Role, r.ClassificationMode, r.Name, r.DecidedAt.UTC(), r.DecidedBy, r.SamplingRate)
	return err
}

// DeleteExporterRecord forgets a decision, so the Exporter is pending again.
func (s *Store) DeleteExporterRecord(ctx context.Context, addr netip.Addr) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM exporters WHERE address = ?`, addr.String())
	return err
}
