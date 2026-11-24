package flowstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"net/netip"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

const reprocessBatch = 20_000

func parseAddr(s sql.NullString) netip.Addr {
	if !s.Valid {
		return netip.Addr{}
	}
	a, _ := netip.ParseAddr(s.String)
	return a
}

func scanFlow(rows *sql.Rows) (flow.Flow, error) {
	var f flow.Flow
	var exporter, src, dst string
	var postSrc, postDst, local, peer sql.NullString
	var direction uint8
	var localMAC, peerMAC string
	err := rows.Scan(&exporter, &f.ObservationDomain, &f.Received, &f.ExportTime, &f.Start, &f.End, &f.TimeEstimated,
		&src, &dst, &f.SrcPort, &f.DstPort, &f.Protocol, &postSrc, &postDst, &f.PostNATSrcPort, &f.PostNATDstPort,
		&f.Bytes, &f.Packets, &f.IngressInterface, &f.EgressInterface, &direction, &local, &f.LocalIsExporterHost, &peer,
		&f.LocalPort, &f.PeerPort, &f.Fields, &f.PeerCountry, &f.PeerASN, &f.SamplingRate, &localMAC, &peerMAC, &f.Domain)
	if err != nil {
		return f, err
	}
	f.Exporter, _ = netip.ParseAddr(exporter)
	f.Src, _ = netip.ParseAddr(src)
	f.Dst, _ = netip.ParseAddr(dst)
	f.PostNATSrc, f.PostNATDst, f.Local, f.Peer = parseAddr(postSrc), parseAddr(postDst), parseAddr(local), parseAddr(peer)
	f.Direction = flow.Direction(direction)
	f.LocalMAC, _ = flow.ParseMAC(localMAC)
	f.PeerMAC, _ = flow.ParseMAC(peerMAC)
	f.Received, f.ExportTime, f.Start, f.End = f.Received.UTC(), f.ExportTime.UTC(), f.Start.UTC(), f.End.UTC()
	return f, nil
}

// Reprocess re-classifies one Exporter's raw Flows from the start of from's UTC day (or
// the oldest raw day, if later) and rebuilds that Exporter's Rollups from there.
// Rollups before the window, and other Exporters' data, are left as they were. Each
// day is rewritten under the write lock on its own, so ingest only pauses briefly.
// progress, if set, is called after each day. It returns the number of Flows rewritten
// and the start of the window.
func (s *Store) Reprocess(ctx context.Context, exporter netip.Addr, from time.Time, classify func(*flow.Flow), progress func(day time.Time, flows int)) (int, time.Time, error) {
	days := s.RawDays(from)
	if len(days) == 0 {
		return 0, time.Time{}, nil
	}
	window := from.UTC().Truncate(24 * time.Hour)
	if days[0].After(window) {
		window = days[0]
	}

	// Drop this Exporter's Rollups from the window on; they are rebuilt from raw Flows.
	s.writeMu.Lock()
	s.mu.Lock()
	var rollupTables []string
	for name := range s.rollups {
		rollupTables = append(rollupTables, name)
	}
	s.mu.Unlock()
	for _, t := range rollupTables {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+t+` WHERE exporter = ? AND bucket >= ?`, exporter.String(), window); err != nil {
			s.writeMu.Unlock()
			return 0, window, fmt.Errorf("clearing %s: %w", t, err)
		}
		s.mu.Lock()
		s.dirty[t] = true
		s.mu.Unlock()
	}
	s.writeMu.Unlock()

	total := 0
	for _, day := range days {
		if day.Add(24 * time.Hour).Before(window) {
			continue
		}
		n, err := s.reprocessDay(ctx, dayTable(day), exporter, window, classify)
		total += n
		if err != nil {
			return total, window, err
		}
		if progress != nil {
			progress(day, n)
		}
	}
	// Top Remote IPs from the window on are built again from the reprocessed Flows, and
	// Domain Rollups counted again.
	s.markTopDirtyFrom(window)
	s.writeMu.Lock()
	err := s.rebuildDomainRollups(ctx, exporter, window)
	s.writeMu.Unlock()
	if err != nil {
		return total, window, fmt.Errorf("recounting Domain Rollups: %w", err)
	}
	return total, window, nil
}

func (s *Store) reprocessDay(ctx context.Context, table string, exporter netip.Addr, window time.Time, classify func(*flow.Flow)) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	tmp := table + "_reprocess"
	for _, q := range []string{
		`DROP TABLE IF EXISTS ` + tmp,
		`CREATE TABLE ` + tmp + ` AS SELECT * FROM ` + table + ` WHERE exporter <> '` + exporter.String() + `'`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			return 0, fmt.Errorf("reprocessing %s: %w", table, err)
		}
	}

	rows, err := s.db.QueryContext(ctx, `SELECT * FROM `+table+` WHERE exporter = ?`, exporter.String())
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	conn, err := s.db.Conn(ctx)
	if err != nil {
		return 0, err
	}
	defer conn.Close()

	n := 0
	batch := make([]flow.Flow, 0, reprocessBatch)
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		err := conn.Raw(func(dc any) error {
			app, err := duckdb.NewAppenderFromConn(dc.(driver.Conn), "", tmp)
			if err != nil {
				return err
			}
			for i := range batch {
				if err := app.AppendRow(flowRow(&batch[i])...); err != nil {
					_ = app.Close()
					return err
				}
			}
			if err := app.Close(); err != nil {
				return err
			}
			if err := s.writeRollups(ctx, dc.(driver.Conn), batch, window); err != nil {
				return err
			}
			if err := s.saveDeviceAddresses(ctx, batch); err != nil {
				return err
			}
			return s.saveASNNames(ctx, batch)
		})
		n += len(batch)
		batch = batch[:0]
		return err
	}
	for rows.Next() {
		f, err := scanFlow(rows)
		if err != nil {
			return n, err
		}
		classify(&f)
		batch = append(batch, f)
		if len(batch) == reprocessBatch {
			if err := flush(); err != nil {
				return n, err
			}
		}
	}
	if err := rows.Err(); err != nil {
		return n, err
	}
	rows.Close()
	if err := flush(); err != nil {
		return n, err
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return n, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, q := range []string{`DROP TABLE ` + table, `ALTER TABLE ` + tmp + ` RENAME TO ` + table} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return n, fmt.Errorf("swapping %s: %w", table, err)
		}
	}
	return n, tx.Commit()
}
