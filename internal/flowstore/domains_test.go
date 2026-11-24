package flowstore

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func domainsByPeer(t *testing.T, s *Store) map[string]string {
	t.Helper()
	rows, err := s.db.QueryContext(ctx, `SELECT peer || '@' || strftime(end_ts, '%H:%M'), domain FROM flows_20261118`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, d string
		if err := rows.Scan(&k, &d); err != nil {
			t.Fatal(err)
		}
		out[k] = d
	}
	return out
}

func TestFillDomainsNamesOnlyRecentUnnamedFlows(t *testing.T) {
	s := open(t)
	at := func(f flow.Flow, peer string) flow.Flow {
		f.Peer, f.Dst = netip.MustParseAddr(peer), netip.MustParseAddr(peer)
		return f
	}
	named := at(mkFlow(flow.DirectionOutbound, t0, t0, 10), "1.1.1.1")
	named.Domain = "one.one.one.one"
	internal := at(mkFlow(flow.DirectionInternal, t0, t0, 10), "192.168.10.45")
	old := at(mkFlow(flow.DirectionOutbound, t0.Add(-time.Hour), t0.Add(-time.Hour), 10), "8.8.8.8")
	if err := s.Write(ctx, []flow.Flow{
		at(mkFlow(flow.DirectionOutbound, t0, t0, 10), "8.8.8.8"),
		at(mkFlow(flow.DirectionInbound, t0, t0, 10), "9.9.9.9"),
		at(mkFlow(flow.DirectionOutbound, t0, t0, 10), "4.4.4.4"), // looked up by nobody
		named, internal, old,
	}); err != nil {
		t.Fatal(err)
	}
	names := map[string]string{"8.8.8.8": "dns.google", "9.9.9.9": "dns.quad9.net", "1.1.1.1": "cloudflare-dns.com", "192.168.10.45": "laptop.lan"}
	var asked []string
	n, err := s.FillDomains(ctx, t0.Add(-10*time.Minute), func(local, remote netip.Addr, start time.Time) string {
		asked = append(asked, remote.String())
		if local != netip.MustParseAddr("192.168.10.44") || !start.Equal(t0) {
			t.Errorf("asked for %s → %s at %s", local, remote, start)
		}
		return names[remote.String()]
	})
	if err != nil || n != 2 {
		t.Errorf("filled %d, %v; want 2", n, err)
	}
	if len(asked) != 3 {
		t.Errorf("asked about %v; want only the 3 recent unnamed Inbound and Outbound Flows", asked)
	}
	got := domainsByPeer(t, s)
	for k, want := range map[string]string{
		"8.8.8.8@12:00": "dns.google", "9.9.9.9@12:00": "dns.quad9.net", "4.4.4.4@12:00": "",
		"1.1.1.1@12:00": "one.one.one.one", "192.168.10.45@12:00": "", "8.8.8.8@11:00": "",
	} {
		if got[k] != want {
			t.Errorf("%s: domain %q, want %q", k, got[k], want)
		}
	}
	// Domains are kept through reprocessing.
	if _, _, err := s.Reprocess(ctx, exporter, t0, func(*flow.Flow) {}, nil); err != nil {
		t.Fatal(err)
	}
	if d := domainsByPeer(t, s)["8.8.8.8@12:00"]; d != "dns.google" {
		t.Errorf("after reprocessing: %q", d)
	}
}
