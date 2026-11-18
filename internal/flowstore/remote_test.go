package flowstore

import (
	"fmt"
	"math"
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func remoteFlow(peer string, start, end time.Time, bytes uint64) flow.Flow {
	f := mkFlow(flow.DirectionOutbound, start, end, bytes)
	f.Peer, f.Dst = netip.MustParseAddr(peer), netip.MustParseAddr(peer)
	return f
}

func (s *Store) topRemoteRows(t *testing.T, gran string, p time.Time) (kept int, other, total float64) {
	t.Helper()
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FILTER (WHERE remote_ip <> ''), coalesce(sum(bytes_tx) FILTER (WHERE remote_ip = ''), 0), coalesce(sum(bytes_tx), 0)
		FROM top_remote WHERE granularity = ? AND period = ?`, gran, p).Scan(&kept, &other, &total)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestTopRemoteKeepsTopByBytesAndByFlowsPlusOther(t *testing.T) {
	s := open(t)
	var flows []flow.Flow
	var all float64
	for i := 1; i <= 60; i++ {
		peer := fmt.Sprintf("203.0.113.%d", i)
		flows = append(flows, remoteFlow(peer, t0.Add(10*time.Minute), t0.Add(10*time.Minute), uint64(i*1000)))
		all += float64(i * 1000)
		if i <= 5 { // chatty but small: in the top by Flow count only
			for j := 0; j < 99; j++ {
				flows = append(flows, remoteFlow(peer, t0.Add(10*time.Minute), t0.Add(10*time.Minute), 1))
				all++
			}
		}
	}
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	n, err := s.BuildTopRemote(ctx, t0.Add(2*time.Hour), 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// Every hour of the raw day up to 12:00 is complete (13:00 isn't, with the
	// Watermark); the day isn't.
	if n != 13 {
		t.Errorf("built %d periods, want 13", n)
	}
	kept, other, total := s.topRemoteRows(t, "hour", t0)
	// Kept: the top 50 by bytes (11..60) and the five chatty ones (1..5); other: 6..10.
	if kept != 55 || other != 6000+7000+8000+9000+10000 || total != all {
		t.Errorf("kept %d, other %v, total %v (want 55, 40000, %v)", kept, other, total, all)
	}
	// Building again finds nothing to do.
	if n, _ := s.BuildTopRemote(ctx, t0.Add(2*time.Hour), 5*time.Minute); n != 0 {
		t.Errorf("rebuilt %d periods", n)
	}
}

func TestTopRemoteSpreadsAndRebuildsAfterLateFlows(t *testing.T) {
	s := open(t)
	// 11:30 → 12:30: half in each hour.
	if err := s.Write(ctx, []flow.Flow{remoteFlow("8.8.8.8", t0.Add(-30*time.Minute), t0.Add(30*time.Minute), 1000)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BuildTopRemote(ctx, t0.Add(3*time.Hour), 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	_, _, before := s.topRemoteRows(t, "hour", t0.Add(-time.Hour))
	_, _, after := s.topRemoteRows(t, "hour", t0)
	if math.Abs(before-500) > 1e-6 || math.Abs(after-500) > 1e-6 {
		t.Errorf("11:00 %v, 12:00 %v; want 500 each", before, after)
	}

	// A late Flow for 12:00 marks that hour, and only that hour, to be built again.
	if err := s.Write(ctx, []flow.Flow{remoteFlow("8.8.8.8", t0.Add(5*time.Minute), t0.Add(5*time.Minute), 70)}); err != nil {
		t.Fatal(err)
	}
	n, err := s.BuildTopRemote(ctx, t0.Add(3*time.Hour), 5*time.Minute)
	if err != nil || n != 1 {
		t.Fatalf("rebuilt %d periods, %v; want 1", n, err)
	}
	if _, _, got := s.topRemoteRows(t, "hour", t0); math.Abs(got-570) > 1e-6 {
		t.Errorf("12:00 after the late Flow: %v", got)
	}

	// Reprocessing marks everything from its window on.
	if _, _, err := s.Reprocess(ctx, exporter, t0, func(*flow.Flow) {}, nil); err != nil {
		t.Fatal(err)
	}
	// The window starts at the start of t0's UTC day: 00:00 to 13:00 are built again.
	if n, _ := s.BuildTopRemote(ctx, t0.Add(3*time.Hour), 5*time.Minute); n != 14 {
		t.Errorf("after reprocessing, rebuilt %d periods; want 14", n)
	}
}

func TestTopRemoteIPsQueries(t *testing.T) {
	s := open(t)
	now := t0.Add(48 * time.Hour)
	var flows []flow.Flow
	// Yesterday and the day before: an hour each. Today: the last few minutes.
	for _, at := range []time.Time{t0.Add(10 * time.Minute), t0.Add(24*time.Hour + 10*time.Minute), now.Add(-3 * time.Minute)} {
		flows = append(flows,
			remoteFlow("8.8.8.8", at, at, 3000),
			remoteFlow("1.1.1.1", at, at, 1000),
			remoteFlow("1.1.1.1", at, at, 1000),
		)
	}
	flows = append(flows, remoteFlow("1.1.1.1", now.Add(-2*time.Minute), now.Add(-2*time.Minute), 10))
	other := mkFlow(flow.DirectionInbound, now.Add(-3*time.Minute), now.Add(-3*time.Minute), 400)
	other.Local, other.Peer = netip.MustParseAddr("192.168.10.50"), netip.MustParseAddr("8.8.8.8")
	flows = append(flows, other, internalFlow(now.Add(-time.Minute), now.Add(-time.Minute), 99999))
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BuildTopRemote(ctx, now, 5*time.Minute); err != nil {
		t.Fatal(err)
	}

	// The last hour is answered exactly, from raw Flows.
	short, err := s.TopRemoteIPs(ctx, RemoteQuery{From: now.Add(-time.Hour), To: now, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if short.Source != SourceRaw || short.Approximate || len(short.Items) != 2 || short.Items[0].RemoteIP != "8.8.8.8" ||
		short.Items[0].TotalBytes != 3400 || short.Items[0].LocalHosts != 2 || short.Items[1].Flows != 3 || short.TotalBytes != 5410 {
		t.Errorf("last hour: %+v", short)
	}
	byFlows, _ := s.TopRemoteIPs(ctx, RemoteQuery{From: now.Add(-time.Hour), To: now, Now: now, ByFlows: true})
	if byFlows.Items[0].RemoteIP != "1.1.1.1" {
		t.Errorf("by Flows: %+v", byFlows.Items)
	}

	// Three days: hourly lists, plus raw Flows for the hours not built yet.
	long, err := s.TopRemoteIPs(ctx, RemoteQuery{From: t0.Add(-time.Hour), To: now, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if long.Source != SourceHour || !long.Approximate || long.TotalBytes != 3*5000+410 || long.Items[0].Flows != 4 || long.ListsFrom == nil {
		t.Errorf("three days: %+v", long)
	}

	// One Remote IP: totals and Local Hosts.
	d, err := s.RemoteIP(ctx, netip.MustParseAddr("8.8.8.8"), RemoteQuery{From: t0.Add(-time.Hour), To: now, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if d.SentBytes != 9000 || d.ReceivedBytes != 400 || d.Flows != 4 || len(d.LocalHosts) != 2 || d.LocalHosts[0].Host != "192.168.10.44" {
		t.Errorf("detail %+v", d)
	}

	// Its raw Flows, newest first, with a limit.
	list, err := s.Flows(ctx, FlowQuery{Remote: netip.MustParseAddr("1.1.1.1"), From: t0.Add(-time.Hour), To: now, Limit: 5})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 5 || list.Total <= 5 || !list.Items[0].End.Equal(now.Add(-2*time.Minute)) || list.Items[0].Direction != "outbound" || list.RawFrom == nil {
		t.Errorf("flows %+v", list)
	}

	// The Overview widget goes through Top.
	top, err := s.Top(ctx, TopQuery{Dimension: TopRemoteIPs, From: now.Add(-time.Hour), To: now})
	if err != nil || len(top.Items) != 2 || top.Items[0].RemoteIP != "8.8.8.8" {
		t.Errorf("top %+v %v", top, err)
	}
}

func TestTopRemoteRetention(t *testing.T) {
	s := open(t)
	old := t0.Add(-10 * 24 * time.Hour)
	if err := s.Write(ctx, []flow.Flow{remoteFlow("8.8.8.8", old, old, 100)}); err != nil {
		t.Fatal(err)
	}
	// Nothing was built yet: retention builds the day before dropping its raw Flows.
	if _, err := s.ApplyRetention(ctx, DefaultRetention(), t0, time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := s.sortedPeriods("day"); len(got) != 1 || !got[0].Equal(old.Truncate(24*time.Hour)) {
		t.Errorf("daily lists %v", got)
	}
	if _, _, total := s.topRemoteRows(t, "day", old.Truncate(24*time.Hour)); total != 100 {
		t.Errorf("daily total %v", total)
	}
	// Long ago, only daily lists answer.
	res, err := s.TopRemoteIPs(ctx, RemoteQuery{From: old.Add(-24 * time.Hour), To: old.Add(24 * time.Hour), Now: t0.Add(100 * 24 * time.Hour)})
	if err != nil || res.Source != SourceDay || len(res.Items) != 1 || res.RawFrom != nil {
		t.Errorf("long ago: %+v %v", res, err)
	}
	// Hourly lists go after 90 days.
	if err := s.Write(ctx, []flow.Flow{remoteFlow("8.8.8.8", t0, t0, 100)}); err != nil {
		t.Fatal(err)
	}
	s.BuildTopRemote(ctx, t0.Add(2*time.Hour), time.Minute)
	if len(s.sortedPeriods("hour")) == 0 {
		t.Fatal("no hourly lists")
	}
	if _, err := s.ApplyRetention(ctx, DefaultRetention(), t0.Add(91*24*time.Hour), time.Hour); err != nil {
		t.Fatal(err)
	}
	if got := s.sortedPeriods("hour"); len(got) != 0 {
		t.Errorf("hourly lists left: %v", got)
	}
}
