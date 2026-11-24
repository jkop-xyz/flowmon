package flowstore

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// FillDomains gives a domain to raw Flows that ended at or after since and have none:
// those stored before the DNS lookup that names them was collected. name returns the
// name for a Flow's Local Host, Remote IP and start, or "". It returns how many Flows
// got one.
func (s *Store) FillDomains(ctx context.Context, since time.Time, name func(local, remote netip.Addr, start time.Time) string) (int, error) {
	filled := 0
	for _, t := range s.tablesFor(since, time.Now().Add(24*time.Hour)) {
		n, err := s.fillDomains(ctx, t, since, name)
		filled += n
		if err != nil {
			return filled, err
		}
	}
	return filled, nil
}

func (s *Store) fillDomains(ctx context.Context, table string, since time.Time, name func(local, remote netip.Addr, start time.Time) string) (int, error) {
	// Rows are only appended while the write lock is held, so rowids stay put.
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rows, err := s.db.QueryContext(ctx, `SELECT rowid, local_host, peer, start_ts FROM `+table+`
		WHERE domain = '' AND end_ts >= ? AND direction IN (?, ?) AND local_host IS NOT NULL AND peer IS NOT NULL`,
		since.UTC(), uint8(flow.DirectionInbound), uint8(flow.DirectionOutbound))
	if err != nil {
		return 0, err
	}
	type fill struct {
		rowid int64
		name  string
	}
	var fills []fill
	for rows.Next() {
		var rowid int64
		var local, peer string
		var start time.Time
		if err := rows.Scan(&rowid, &local, &peer, &start); err != nil {
			rows.Close()
			return 0, err
		}
		l, err1 := netip.ParseAddr(local)
		p, err2 := netip.ParseAddr(peer)
		if err1 != nil || err2 != nil {
			continue
		}
		if n := name(l, p, start.UTC()); n != "" {
			fills = append(fills, fill{rowid, n})
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, err
	}
	rows.Close()
	if len(fills) == 0 {
		return 0, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `UPDATE `+table+` SET domain = ? WHERE rowid = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	for _, f := range fills {
		if _, err := stmt.ExecContext(ctx, f.name, f.rowid); err != nil {
			return 0, fmt.Errorf("filling domains in %s: %w", table, err)
		}
	}
	return len(fills), tx.Commit()
}
