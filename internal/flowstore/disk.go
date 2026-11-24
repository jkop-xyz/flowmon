package flowstore

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Disk is the space the Flow Store takes.
type Disk struct {
	// Used is what the data takes: the database's used blocks plus its write-ahead log.
	// Dropped tables free their blocks at the next checkpoint for reuse, but the file
	// doesn't shrink, so File can be larger than Used.
	Used int64
	// File is the size of the database and write-ahead log files.
	File int64
}

// Disk measures the space the Flow Store takes.
func (s *Store) Disk(ctx context.Context) (Disk, error) {
	var blockSize, usedBlocks int64
	err := s.db.QueryRowContext(ctx, `SELECT block_size, used_blocks FROM pragma_database_size() WHERE database_name = current_database()`).
		Scan(&blockSize, &usedBlocks)
	if err != nil {
		return Disk{}, err
	}
	d := Disk{Used: blockSize * usedBlocks}
	if fi, err := os.Stat(s.path); err == nil {
		d.File += fi.Size()
	}
	if fi, err := os.Stat(s.path + ".wal"); err == nil {
		d.File += fi.Size()
		d.Used += fi.Size()
	}
	return d, nil
}

// Memory is DuckDB's memory use and settings.
type Memory struct {
	Used int64
	// Limit is DuckDB's memory_limit as it reports it, such as "953.6 MiB".
	Limit   string
	Threads int
}

// Memory reports DuckDB's memory use, memory limit and thread count.
func (s *Store) Memory(ctx context.Context) (Memory, error) {
	var m Memory
	err := s.db.QueryRowContext(ctx, `SELECT (SELECT coalesce(sum(memory_usage_bytes), 0) FROM duckdb_memory()),
		current_setting('memory_limit'), current_setting('threads')`).Scan(&m.Used, &m.Limit, &m.Threads)
	return m, err
}

// checkpoint writes the write-ahead log into the database, which also frees the blocks
// of dropped tables.
func (s *Store) checkpoint(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.db.ExecContext(ctx, `CHECKPOINT`)
	return err
}

// CapStatus is what the disk cap found and did.
type CapStatus struct {
	// Max is the cap in bytes; 0 means there is none.
	Max int64
	// Checked is when the cap was last enforced.
	Checked time.Time
	// Over is set when the Flow Store was still above Max after dropping everything the
	// cap may drop.
	Over bool
	// LastDrop is when the cap last dropped data, and Dropped the tables it dropped then.
	LastDrop time.Time
	Dropped  []string
}

// CapStatus returns what the disk cap last found and did.
func (s *Store) CapStatus() CapStatus {
	s.capMu.Lock()
	defer s.capMu.Unlock()
	c := s.capStatus
	c.Dropped = append([]string(nil), c.Dropped...)
	return c
}

// capKeepMinute is how much of the newest minute Rollups the cap never drops: once raw
// days are gone, they are the finest data left for recent charts.
const capKeepMinute = 24 * time.Hour

// EnforceCap drops the oldest data until the Flow Store uses at most max bytes: whole days
// of DNS Queries first, oldest first, then raw days, then minute Rollup days. Every Flow is rolled
// up as it is written, and a raw day's daily Top Remote IPs are built before it goes, so
// only detail is lost. It never drops a raw day that may still receive late Flows (it
// ended less than safeAfter, the largest Watermark, ago), nor a DNS day that recent, the last day of minute
// Rollups, or hour Rollups, which hold the long history and take little space. When
// that isn't enough, CapStatus says so.
func (s *Store) EnforceCap(ctx context.Context, max int64, now time.Time, safeAfter time.Duration) ([]string, error) {
	var dropped []string
	over := false
	err := func() error {
		for {
			if err := s.checkpoint(ctx); err != nil {
				return fmt.Errorf("checkpoint: %w", err)
			}
			d, err := s.Disk(ctx)
			if err != nil {
				return err
			}
			if d.Used <= max {
				return nil
			}
			name, catalog, ok := s.capVictim(now, safeAfter)
			if !ok {
				over = true
				return nil
			}
			if day, ok := rawDay(name); ok && catalog == "flow_days" {
				if err := s.buildDayTopBeforeDrop(ctx, day); err != nil {
					return err
				}
			}
			if err := s.dropTable(ctx, name, catalog); err != nil {
				return err
			}
			dropped = append(dropped, name)
		}
	}()

	s.capMu.Lock()
	s.capStatus.Max = max
	if err == nil {
		s.capStatus.Checked, s.capStatus.Over = now, over
	}
	if len(dropped) > 0 {
		s.capStatus.LastDrop, s.capStatus.Dropped = now, dropped
	}
	s.capMu.Unlock()
	return dropped, err
}

// capVictim picks the next table the cap drops.
func (s *Store) capVictim(now time.Time, safeAfter time.Duration) (name, catalog string, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dns, raw, minute []string
	for name := range s.dnsDays {
		if day, ok := dnsDay(name); ok && day.Add(24*time.Hour).Before(now.Add(-safeAfter)) {
			dns = append(dns, name)
		}
	}
	for name := range s.days {
		if day, ok := rawDay(name); ok && day.Add(24*time.Hour).Before(now.Add(-safeAfter)) {
			raw = append(raw, name)
		}
	}
	for name := range s.rollups {
		if !strings.HasPrefix(name, "rollup_minute_") {
			continue
		}
		p, err := time.Parse("20060102", name[len("rollup_minute_"):])
		if err == nil && Minute.periodEnd(p).Before(now.Add(-capKeepMinute-safeAfter)) {
			minute = append(minute, name)
		}
	}
	// Names sort by date.
	sort.Strings(dns)
	sort.Strings(raw)
	sort.Strings(minute)
	switch {
	case len(dns) > 0:
		return dns[0], "dns_days", true
	case len(raw) > 0:
		return raw[0], "flow_days", true
	case len(minute) > 0:
		return minute[0], "rollup_partitions", true
	}
	return "", "", false
}

// ErrSnapshotRunning means another snapshot is being taken.
var ErrSnapshotRunning = errors.New("a snapshot is already being taken")

// Snapshot is a consistent copy of the Flow Store: a DuckDB file that replaces
// flows.duckdb to restore. Close removes it.
type Snapshot struct {
	*os.File
	Size int64
	dir  string
}

// Close closes and removes the snapshot.
func (sn *Snapshot) Close() error {
	err := sn.File.Close()
	if rerr := os.RemoveAll(sn.dir); err == nil {
		err = rerr
	}
	return err
}

// snapshotPrefix names the directories snapshots are taken in, next to the Flow Store
// (which has room for one); Open removes any left by a crash.
const snapshotPrefix = ".flows-snapshot-"

// Snapshot copies the Flow Store into a new DuckDB file in a single transaction, so the
// copy holds exactly what was committed when it started. Collection continues
// meanwhile. One snapshot is taken at a time.
func (s *Store) Snapshot(ctx context.Context) (*Snapshot, error) {
	if !s.snapshotMu.TryLock() {
		return nil, ErrSnapshotRunning
	}
	defer s.snapshotMu.Unlock()
	dir, err := os.MkdirTemp(filepath.Dir(s.path), snapshotPrefix)
	if err != nil {
		return nil, err
	}
	sn, err := s.snapshotInto(ctx, filepath.Join(dir, "flows.duckdb"))
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	sn.dir = dir
	return sn, nil
}

func (s *Store) snapshotInto(ctx context.Context, path string) (*Snapshot, error) {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	var db string
	if err := conn.QueryRowContext(ctx, `SELECT current_database()`).Scan(&db); err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, `ATTACH '`+strings.ReplaceAll(path, "'", "''")+`' AS flowmon_snapshot`); err != nil {
		return nil, fmt.Errorf("creating the snapshot: %w", err)
	}
	_, err = conn.ExecContext(ctx, `COPY FROM DATABASE "`+strings.ReplaceAll(db, `"`, `""`)+`" TO flowmon_snapshot`)
	// Detaching checkpoints the copy into its file; it runs even when the copy failed.
	if _, derr := conn.ExecContext(context.WithoutCancel(ctx), `DETACH flowmon_snapshot`); err == nil && derr != nil {
		err = fmt.Errorf("closing the snapshot: %w", derr)
	}
	if err != nil {
		return nil, fmt.Errorf("copying into the snapshot: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	return &Snapshot{File: f, Size: fi.Size()}, nil
}

// removeStaleSnapshots removes snapshots left next to the Flow Store by a crash.
func removeStaleSnapshots(path string) {
	matches, _ := filepath.Glob(filepath.Join(filepath.Dir(path), snapshotPrefix+"*"))
	for _, m := range matches {
		os.RemoveAll(m)
	}
}
