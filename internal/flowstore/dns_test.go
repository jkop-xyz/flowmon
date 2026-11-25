package flowstore

import (
	"math"
	"net/netip"
	"slices"
	"testing"
	"time"
)

func dnsQuery(at time.Time, client, name string, answers ...string) DNSQuery {
	q := DNSQuery{Time: at, Source: "a1", Client: netip.MustParseAddr(client), Name: name, Registrable: name,
		Type: "A", Status: "NOERROR", Upstream: "https://dns.quad9.net/dns-query", Elapsed: 12500 * time.Microsecond}
	for _, a := range answers {
		q.Answers = append(q.Answers, netip.MustParseAddr(a))
	}
	return q
}

func TestDNSQueriesRoundTrip(t *testing.T) {
	s := open(t)
	late := t0.Add(-13 * time.Hour) // the day before t0
	in := []DNSQuery{
		dnsQuery(late, "192.168.10.44", "example.com", "93.184.215.14", "2606:2800:21f:cb07:6820:80da:af6b:8b2c"),
		dnsQuery(t0, "192.168.10.45", "www.example.org", "1.2.3.4"),
	}
	in[1].CNAMEs = []string{"cdn.example.net"}
	in[1].Blocked, in[1].Reason, in[1].Rule = true, "FilteredBlackList", "||example.org^"
	in[1].ClientName, in[1].Cached, in[1].Protocol, in[1].Status = "phone", true, "doh", "NXDOMAIN"
	if err := s.WriteDNS(ctx, in); err != nil {
		t.Fatal(err)
	}
	if got := s.DNSDays(); len(got) != 2 || !got[0].Equal(time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("DNS days %v", got)
	}
	out, err := s.DNSQueries(ctx, late.Add(-time.Hour), t0.Add(time.Second), 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("read %d queries: %+v", len(out), out)
	}
	for i := range in {
		got, want := out[i], in[i]
		if !got.Time.Equal(want.Time) || got.Client != want.Client || got.Name != want.Name || !slices.Equal(got.Answers, want.Answers) ||
			got.Blocked != want.Blocked || got.Rule != want.Rule || got.Elapsed != want.Elapsed || got.ClientName != want.ClientName ||
			got.Cached != want.Cached || got.Protocol != want.Protocol || got.Status != want.Status || len(got.CNAMEs) != len(want.CNAMEs) {
			t.Errorf("query %d: got %+v, want %+v", i, got, want)
		}
	}
	if out, _ := s.DNSQueries(ctx, t0, t0.Add(time.Second), 10); len(out) != 1 || out[0].Name != "www.example.org" {
		t.Errorf("range to one query: %+v", out)
	}
}

func TestLatestDNSIsPerSourceWithTies(t *testing.T) {
	s := open(t)
	if at, qs, err := s.LatestDNS(ctx, "a1"); err != nil || !at.IsZero() || qs != nil {
		t.Fatalf("empty store: %v %v %v", at, qs, err)
	}
	other := dnsQuery(t0.Add(time.Minute), "192.168.10.9", "other.example")
	other.Source = "b2"
	if err := s.WriteDNS(ctx, []DNSQuery{
		dnsQuery(t0.Add(-time.Hour), "192.168.10.44", "old.example"),
		dnsQuery(t0, "192.168.10.44", "a.example"),
		dnsQuery(t0, "192.168.10.45", "b.example"),
		other,
	}); err != nil {
		t.Fatal(err)
	}
	at, qs, err := s.LatestDNS(ctx, "a1")
	if err != nil || !at.Equal(t0) || len(qs) != 2 {
		t.Errorf("latest of a1: %v %+v %v; want both queries at t0, not b2's later one", at, qs, err)
	}
}

func TestDNSRetentionAndCapGoFirst(t *testing.T) {
	s := open(t)
	today := t0.Truncate(24 * time.Hour)
	var qs []DNSQuery
	for d := 4; d >= 0; d-- {
		qs = append(qs, dnsQuery(today.AddDate(0, 0, -d).Add(time.Hour), "192.168.10.44", "example.com"))
	}
	if err := s.WriteDNS(ctx, qs); err != nil {
		t.Fatal(err)
	}
	writeDays(t, s, []time.Time{today.AddDate(0, 0, -2), today}, 2000)

	r := DefaultRetention()
	r.DNS = 2 * 24 * time.Hour
	dropped, err := s.ApplyRetention(ctx, r, t0, 37*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(dropped, []string{"dns_20261114", "dns_20261115"}) {
		t.Errorf("retention dropped %v; want the DNS days that ended over 2 days ago", dropped)
	}

	// Over the cap, DNS days go before any raw Flow day, but never today's.
	dropped, err = s.EnforceCap(ctx, 1, t0, 37*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if len(dropped) < 3 || !slices.Equal(dropped[:3], []string{"dns_20261116", "dns_20261117", "flows_20261116"}) {
		t.Errorf("cap dropped %v; want DNS days first", dropped)
	}
	if got := s.DNSDays(); len(got) != 1 || !got[0].Equal(today) {
		t.Errorf("DNS days left %v; want today's", got)
	}
}

func TestDNSSummary(t *testing.T) {
	s := open(t)
	blocked := dnsQuery(t0.Add(2*time.Minute), "192.168.10.45", "ads.tracker.example")
	blocked.Registrable, blocked.Blocked, blocked.Upstream = "tracker.example", true, ""
	cached := dnsQuery(t0.Add(3*time.Minute), "192.168.10.44", "www.bbc.co.uk", "151.101.0.81")
	cached.Registrable, cached.Cached = "bbc.co.uk", true
	www := dnsQuery(t0, "192.168.10.44", "www.bbc.co.uk", "151.101.0.81")
	www.Registrable = "bbc.co.uk"
	news := dnsQuery(t0.Add(time.Minute), "192.168.10.45", "news.bbc.co.uk", "151.101.64.81")
	news.Registrable, news.Elapsed = "bbc.co.uk", 40*time.Millisecond
	if err := s.WriteDNS(ctx, []DNSQuery{www, news, blocked, cached}); err != nil {
		t.Fatal(err)
	}
	all, err := s.DNSSummary(ctx, DNSFilter{From: t0.Add(-time.Hour), To: t0.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if all.Queries != 4 || all.Blocked != 1 || all.Clients != 2 || all.StepSeconds != 60 || len(all.Points) != 120 {
		t.Errorf("totals %+v", all)
	}
	if len(all.TopNames) != 2 || all.TopNames[0].Name != "bbc.co.uk" || all.TopNames[0].Queries != 3 || all.TopNames[0].Clients != 2 {
		t.Errorf("top names %+v", all.TopNames)
	}
	if len(all.TopBlocked) != 1 || all.TopBlocked[0].Name != "tracker.example" {
		t.Errorf("top blocked %+v", all.TopBlocked)
	}
	if len(all.Upstreams) != 1 || all.Upstreams[0].Queries != 2 || all.Upstreams[0].AvgMs < 26 || all.Upstreams[0].AvgMs > 27 {
		t.Errorf("upstreams %+v; want the 2 uncached, unblocked lookups averaging 26.25 ms", all.Upstreams)
	}
	if all.TopClients[0].Client != "192.168.10.44" || all.TopClients[0].Queries != 2 {
		t.Errorf("top clients %+v", all.TopClients)
	}
	if all.Since == nil {
		t.Error("no Since")
	}

	host, err := s.DNSSummary(ctx, DNSFilter{From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Clients: []string{"192.168.10.45"}})
	if err != nil || host.Queries != 2 || host.Blocked != 1 {
		t.Errorf("one host: %+v %v", host, err)
	}
	dom, err := s.DNSSummary(ctx, DNSFilter{From: t0.Add(-time.Hour), To: t0.Add(time.Hour), Domain: "bbc.co.uk"})
	if err != nil || dom.Queries != 3 || len(dom.TopNames) != 2 || dom.TopNames[0].Name != "www.bbc.co.uk" || len(dom.Answers) != 2 ||
		dom.Answers[0].Address != "151.101.0.81" || dom.Answers[0].Queries != 2 {
		t.Errorf("one domain: %+v %v", dom, err)
	}
}

// Every range's chart holds every lookup in it, whatever its step.
func TestDNSSummaryChartCountsEveryQuery(t *testing.T) {
	s := open(t)
	var qs []DNSQuery
	for i := range 5 {
		qs = append(qs, dnsQuery(t0.Add(-time.Duration(i)*7*time.Minute), "192.168.10.44", "www.bbc.co.uk"))
	}
	if err := s.WriteDNS(ctx, qs); err != nil {
		t.Fatal(err)
	}
	for _, r := range []time.Duration{time.Hour, 24 * time.Hour, 7 * 24 * time.Hour, 30 * 24 * time.Hour, 365 * 24 * time.Hour, 730 * 24 * time.Hour} {
		res, err := s.DNSSummary(ctx, DNSFilter{From: t0.Add(time.Minute - r), To: t0.Add(time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		var charted float64
		for _, p := range res.Points {
			charted += p.Queries * float64(res.StepSeconds) / 60
		}
		if res.Queries != 5 || math.Round(charted) != 5 {
			t.Errorf("range %v (step %ds): %d lookups, %v on the chart; want 5 on both", r, res.StepSeconds, res.Queries, charted)
		}
	}
}

// A lookup late in a point's step counts in that point, not the next.
func TestDNSSummaryChartKeepsLateLookupsInTheirPoint(t *testing.T) {
	s := open(t)
	if err := s.WriteDNS(ctx, []DNSQuery{dnsQuery(t0.Add(-300*time.Millisecond), "192.168.10.44", "www.bbc.co.uk")}); err != nil {
		t.Fatal(err)
	}
	res, err := s.DNSSummary(ctx, DNSFilter{From: t0.Add(-time.Hour), To: t0})
	if err != nil {
		t.Fatal(err)
	}
	if last := res.Points[len(res.Points)-1]; res.StepSeconds != 60 || last.Queries != 1 {
		t.Errorf("last point %+v (step %ds); want the lookup at 11:59:59.7 in the 11:59 point", last, res.StepSeconds)
	}
}
