package flowstore

import (
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// writeDays writes n Flows on each of days (whole UTC days), enough that each day's
// table takes its own blocks.
func writeDays(t *testing.T, s *Store, days []time.Time, n int) {
	t.Helper()
	for _, day := range days {
		var fs []flow.Flow
		for i := range n {
			at := day.Add(time.Duration(i) * time.Second)
			f := mkFlow(flow.DirectionOutbound, at, at, uint64(1000+i))
			f.SrcPort, f.LocalPort = uint16(1024+i%60000), uint16(1024+i%60000)
			f.Peer = netip.AddrFrom4([4]byte{1, 1, byte(i >> 8), byte(i)})
			f.Dst = f.Peer
			f.Fields = []byte{byte(i), byte(i >> 8), byte(i >> 16), 7, 7, 7, 7, 7}
			fs = append(fs, f)
		}
		if err := s.Write(ctx, fs); err != nil {
			t.Fatal(err)
		}
	}
}

func hourBytes(t *testing.T, s *Store) float64 {
	t.Helper()
	res, err := s.Top(ctx, TopQuery{Dimension: TopLocalHosts, From: t0.Add(-30 * 24 * time.Hour), To: t0.Add(24 * time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	return res.TotalBytes
}

func TestDiskCapDropsTheOldestRawDaysFirst(t *testing.T) {
	s := open(t)
	today := t0.Truncate(24 * time.Hour)
	days := []time.Time{today.AddDate(0, 0, -3), today.AddDate(0, 0, -2), today.AddDate(0, 0, -1), today}
	writeDays(t, s, days, 20000)
	before := hourBytes(t, s)
	// As maintenance does before the cap: Top Remote IPs of closed days are built.
	if _, err := s.BuildTopRemote(ctx, t0, 37*time.Minute); err != nil {
		t.Fatal(err)
	}
	if err := s.checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	d, err := s.Disk(ctx)
	if err != nil || d.Used == 0 || d.File < d.Used {
		t.Fatalf("disk %+v %v", d, err)
	}

	// Just over the cap: the oldest raw day goes, nothing else.
	dropped, err := s.EnforceCap(ctx, d.Used-1, t0, 37*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dropped, []string{"flows_20261115"}) {
		t.Fatalf("dropped %v, want the oldest raw day only", dropped)
	}
	after, _ := s.Disk(ctx)
	if after.Used >= d.Used {
		t.Errorf("used %d after the drop, %d before", after.Used, d.Used)
	}
	if st := s.CapStatus(); st.Over || !st.LastDrop.Equal(t0) || st.Max != d.Used-1 {
		t.Errorf("status %+v", st)
	}
	// Its daily Top Remote IPs were built from its Flows before they went.
	top, err := s.Top(ctx, TopQuery{Dimension: TopRemoteIPs, From: days[0], To: days[1], Limit: 5})
	if err != nil || len(top.Items) == 0 {
		t.Errorf("Top Remote IPs of the dropped day: %+v %v", top, err)
	}

	// A cap that can't be met: every closed raw day and the older minute Rollups go;
	// today's raw Flows, the last day of minute Rollups and the hour Rollups stay.
	dropped, err = s.EnforceCap(ctx, 1, t0, 37*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"flows_20261116", "flows_20261117", "rollup_minute_20261115", "rollup_minute_20261116"}
	if !slices.Equal(dropped, want) {
		t.Errorf("dropped %v, want %v", dropped, want)
	}
	if got := s.RawDays(time.Time{}); len(got) != 1 || !got[0].Equal(today) {
		t.Errorf("raw days left %v", got)
	}
	if st := s.CapStatus(); !st.Over {
		t.Errorf("status %+v: the cap can't be met", st)
	}
	if got := hourBytes(t, s); got != before {
		t.Errorf("hour Rollups total %v after the cap, %v before", got, before)
	}

	// Under the cap again: nothing drops and the warning clears.
	if dropped, err := s.EnforceCap(ctx, 1<<40, t0, 37*time.Minute); err != nil || len(dropped) != 0 || s.CapStatus().Over {
		t.Errorf("dropped %v %v, status %+v", dropped, err, s.CapStatus())
	}
}

func TestDiskCapKeepsRawDaysThatMayStillChange(t *testing.T) {
	s := open(t)
	yesterday := t0.Truncate(24*time.Hour).AddDate(0, 0, -1)
	writeDays(t, s, []time.Time{yesterday}, 10)
	justAfterMidnight := t0.Truncate(24 * time.Hour).Add(10 * time.Minute)
	if dropped, _ := s.EnforceCap(ctx, 1, justAfterMidnight, 37*time.Minute); len(dropped) != 0 {
		t.Errorf("dropped %v while late Flows could still arrive", dropped)
	}
	if !s.CapStatus().Over {
		t.Error("over the cap with nothing to drop should say so")
	}
}

// restore puts a snapshot where a Flow Store lives, as the deploy guide says: stop
// Flowmon, replace flows.duckdb, start.
func restore(t *testing.T, sn *Snapshot) *Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "flows.duckdb")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(f, sn); err != nil {
		t.Fatal(err)
	}
	f.Close()
	r, err := Open(ctx, path, Options{MemoryLimit: "256MB", Threads: 2})
	if err != nil {
		t.Fatal("opening the restored snapshot:", err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestSnapshotRestores(t *testing.T) {
	s := open(t)
	today := t0.Truncate(24 * time.Hour)
	writeDays(t, s, []time.Time{today.AddDate(0, 0, -1), today}, 500)
	if err := s.SetSettings(ctx, map[string]string{"local_networks": "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BuildTopRemote(ctx, t0, 37*time.Minute); err != nil {
		t.Fatal(err)
	}

	sn, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if sn.Size == 0 {
		t.Fatal("empty snapshot")
	}
	// Written after the snapshot started: not in it.
	writeDays(t, s, []time.Time{today.Add(time.Hour)}, 1)

	r := restore(t, sn)
	dir := sn.dir
	if err := sn.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("snapshot left behind: %v", err)
	}

	want, _ := s.SchemaVersion(ctx)
	if got, _ := r.SchemaVersion(ctx); got != want {
		t.Errorf("schema %d, want %d", got, want)
	}
	if got := r.RawDays(time.Time{}); len(got) != 2 {
		t.Errorf("raw days %v", got)
	}
	page, err := r.Flows(ctx, FlowQuery{From: today.AddDate(0, 0, -1), To: today.AddDate(0, 0, 1), Limit: 10})
	if err != nil || page.Total != 1000 {
		t.Errorf("restored raw Flows: %d %v, want 1000", page.Total, err)
	}
	if got := hourBytes(t, r); got != 2*(500*1000+499*500/2) {
		t.Errorf("restored Rollups total %v", got)
	}
	if v, ok, _ := r.Setting(ctx, "local_networks"); !ok || v != "10.0.0.0/8" {
		t.Errorf("restored setting %q %v", v, ok)
	}
	top, err := r.Top(ctx, TopQuery{Dimension: TopRemoteIPs, From: today.AddDate(0, 0, -1), To: today, Limit: 5})
	if err != nil || len(top.Items) == 0 {
		t.Errorf("restored Top Remote IPs: %+v %v", top, err)
	}

	// The restored store keeps working: new Flows, settings and retention.
	writeDays(t, r, []time.Time{today.Add(2 * time.Hour)}, 3)
	if err := r.SetSettings(ctx, map[string]string{"local_networks": "10.1.0.0/16"}); err != nil {
		t.Error("changing a setting after restoring:", err)
	}
	if _, err := r.ApplyRetention(ctx, Retention{Raw: 24 * time.Hour, Minute: 24 * time.Hour, Hour: 48 * time.Hour}, t0.AddDate(0, 0, 3), time.Minute); err != nil {
		t.Error("retention after restoring:", err)
	}
}

func TestSnapshotWhileWriting(t *testing.T) {
	s := open(t)
	today := t0.Truncate(24 * time.Hour)
	writeDays(t, s, []time.Time{today}, 20000)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			f := mkFlow(flow.DirectionOutbound, today.Add(time.Duration(i)*time.Millisecond), today.Add(time.Duration(i)*time.Millisecond), 1)
			if err := s.Write(ctx, []flow.Flow{f}); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	sn, err := s.Snapshot(ctx)
	if err == nil {
		if _, err2 := s.Snapshot(ctx); err2 != nil && err2 != ErrSnapshotRunning {
			t.Error(err2)
		}
	}
	close(stop)
	wg.Wait()
	if err != nil {
		t.Fatal(err)
	}
	defer sn.Close()
	r := restore(t, sn)
	page, err := r.Flows(ctx, FlowQuery{From: today, To: today.AddDate(0, 0, 1), Limit: 1})
	if err != nil || page.Total < 20000 {
		t.Errorf("restored %d Flows %v", page.Total, err)
	}
}

func TestOpenRemovesStaleSnapshots(t *testing.T) {
	dir := t.TempDir()
	stale := filepath.Join(dir, snapshotPrefix+"123")
	if err := os.MkdirAll(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	s, err := Open(ctx, filepath.Join(dir, "flows.duckdb"), Options{})
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale snapshot kept: %v", err)
	}
}

func TestMemory(t *testing.T) {
	s := open(t)
	m, err := s.Memory(ctx)
	if err != nil || m.Threads != 2 || m.Limit == "" {
		t.Errorf("%+v %v", m, err)
	}
}
