package flowstore

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func domainFlow(dir flow.Direction, end time.Time, bytes uint64, local, domain string, mac flow.MAC) flow.Flow {
	f := mkFlow(dir, end.Add(-90*time.Second), end, bytes)
	f.Local, f.Src, f.Domain, f.LocalMAC = netip.MustParseAddr(local), netip.MustParseAddr(local), domain, mac
	return f
}

func totalsByDomain(t *testing.T, s *Store, from, to time.Time) map[string]DomainTotal {
	t.Helper()
	ts, err := s.DomainTotals(ctx, from, to)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]DomainTotal{}
	for _, d := range ts {
		out[d.Registrable] = d
	}
	return out
}

func TestDomainRollupsCountEachFlowOnceAfterTheDelay(t *testing.T) {
	s := open(t)
	// The first build only marks where counting starts.
	if n, err := s.BuildDomainRollups(ctx, t0); err != nil || n != 0 {
		t.Fatalf("first build: %d %v", n, err)
	}
	flows := []flow.Flow{
		domainFlow(flow.DirectionOutbound, t0.Add(time.Minute), 1000, "192.168.10.44", "www.bbc.co.uk", phoneMAC),
		domainFlow(flow.DirectionInbound, t0.Add(time.Minute), 9000, "192.168.10.44", "static.files.bbci.co.uk", phoneMAC),
		domainFlow(flow.DirectionInbound, t0.Add(2*time.Minute), 4000, "192.168.10.45", "news.bbc.co.uk", flow.MAC{}),
		domainFlow(flow.DirectionOutbound, t0.Add(2*time.Minute), 500, "192.168.10.45", "", flow.MAC{}),
		domainFlow(flow.DirectionInternal, t0.Add(2*time.Minute), 7777, "192.168.10.45", "", flow.MAC{}),
	}
	if err := s.Write(ctx, flows); err != nil {
		t.Fatal(err)
	}
	range_ := func() map[string]DomainTotal { return totalsByDomain(t, s, t0.Add(-time.Hour), t0.Add(time.Hour)) }

	// Builds count whole minutes of Flows received at least DomainRollupDelay ago: at
	// +7:00 the minute from +1:00, not yet those received at +2:00.
	if n, err := s.BuildDomainRollups(ctx, t0.Add(time.Minute+DomainRollupDelay+59*time.Second)); err != nil || n != 0 {
		t.Errorf("build at +6:59 counted %d (%v); want none", n, err)
	}
	if n, err := s.BuildDomainRollups(ctx, t0.Add(2*time.Minute+DomainRollupDelay)); err != nil || n != 2 {
		t.Errorf("build at +7:00 counted %d (%v); want the 2 Flows received at +1:00", n, err)
	}
	if n, err := s.BuildDomainRollups(ctx, t0.Add(10*time.Minute)); err != nil || n != 2 {
		t.Errorf("build at +10:00 counted %d (%v); want the 2 Inbound and Outbound Flows received at +2:00", n, err)
	}
	got := range_()
	if bbc := got["bbc.co.uk"]; bbc.BytesTx != 1000 || bbc.BytesRx != 4000 || bbc.Flows != 2 || bbc.LocalHosts != 2 || bbc.Devices != 1 {
		t.Errorf("bbc.co.uk: %+v", bbc)
	}
	if got["bbci.co.uk"].BytesRx != 9000 || got[""].BytesTx != 500 || len(got) != 3 {
		t.Errorf("totals %+v", got)
	}

	// Building again counts nothing twice, and compaction keeps the totals.
	if n, _ := s.BuildDomainRollups(ctx, t0.Add(11*time.Minute)); n != 0 {
		t.Errorf("a later build counted %d again", n)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if after := range_(); after["bbc.co.uk"] != got["bbc.co.uk"] || after[""] != got[""] {
		t.Errorf("after compaction %+v, before %+v", after, got)
	}
	// Hour tables hold the same totals.
	if day := totalsByDomain(t, s, t0.Truncate(24*time.Hour), t0.Truncate(24*time.Hour).Add(24*time.Hour)); day["bbc.co.uk"].BytesRx != 4000 {
		t.Errorf("hour totals %+v", day)
	}

	// Reprocessing recounts the Exporter's Flows without counting them twice.
	if _, _, err := s.Reprocess(ctx, exporter, t0, func(*flow.Flow) {}, nil); err != nil {
		t.Fatal(err)
	}
	if after := range_(); after["bbc.co.uk"].BytesRx != 4000 || after["bbci.co.uk"].BytesRx != 9000 || after[""].BytesTx != 500 {
		t.Errorf("after reprocessing %+v", after)
	}

	// Minute tables go with the minute retention, hour tables with the hour one.
	r := DefaultRetention()
	r.Minute = 24 * time.Hour
	dropped, err := s.ApplyRetention(ctx, r, t0.Add(3*24*time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	hasDomainMinute := false
	for _, d := range dropped {
		if d == "domain_minute_20261118" {
			hasDomainMinute = true
		}
		if d == "domain_hour_202611" {
			t.Error("dropped the hour table")
		}
	}
	if !hasDomainMinute {
		t.Errorf("dropped %v", dropped)
	}
}
