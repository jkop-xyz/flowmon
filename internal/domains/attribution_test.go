package domains

import (
	"net/netip"
	"testing"
	"time"
)

var (
	t0     = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	phone  = netip.MustParseAddr("192.168.10.44")
	laptop = netip.MustParseAddr("192.168.10.45")
	cdn    = netip.MustParseAddr("151.101.0.81")
)

func lookup(at time.Time, client netip.Addr, name string, answers ...netip.Addr) Lookup {
	return Lookup{Time: at, Client: client, Name: name, Answers: answers}
}

func TestLatestLookupBeforeTheFlowWins(t *testing.T) {
	a := NewAttributor()
	a.Add([]Lookup{
		lookup(t0.Add(-2*time.Hour), phone, "www.bbc.co.uk", cdn),
		lookup(t0.Add(-time.Minute), phone, "www.theguardian.com", cdn),
		lookup(t0.Add(time.Hour), phone, "later.example", cdn), // after the Flow
	})
	if got := a.Name(phone, cdn, t0); got != "www.theguardian.com" {
		t.Errorf("Name = %q, want the latest lookup before the Flow", got)
	}
	if got := a.Name(phone, cdn, t0.Add(2*time.Hour)); got != "later.example" {
		t.Errorf("a later Flow: %q", got)
	}
	// A lookup logged a few seconds after the Flow started (clocks differ) counts.
	a.Add([]Lookup{lookup(t0.Add(3*time.Hour+5*time.Second), phone, "skewed.example", cdn)})
	if got := a.Name(phone, cdn, t0.Add(3*time.Hour)); got != "skewed.example" {
		t.Errorf("a lookup 5 s after the start: %q", got)
	}
}

func TestOnlyTheSameHostWithinADay(t *testing.T) {
	a := NewAttributor()
	a.Add([]Lookup{lookup(t0.Add(-time.Hour), laptop, "www.example.com", cdn)})
	if got := a.Name(phone, cdn, t0); got != "" {
		t.Errorf("another host's lookup counted: %q", got)
	}
	if got := a.Name(laptop, cdn, t0.Add(Window)); got != "" {
		t.Errorf("a lookup over a day before: %q", got)
	}
	if got := a.Name(laptop, cdn, t0.Add(Window-2*time.Hour)); got != "www.example.com" {
		t.Errorf("within the day: %q", got)
	}
}

func TestBlockedAndIPv6(t *testing.T) {
	a := NewAttributor()
	v6 := netip.MustParseAddr("2a04:4e42::81")
	client6 := netip.MustParseAddr("fd00::44")
	a.Add([]Lookup{
		lookup(t0.Add(-time.Minute), phone, "ads.example", netip.MustParseAddr("0.0.0.0")),
		lookup(t0.Add(-time.Minute), client6, "www.example.org", v6),
		lookup(t0.Add(-time.Minute), netip.MustParseAddr("::ffff:192.168.10.44"), "mapped.example", cdn),
	})
	if a.Size() != 2 {
		t.Errorf("kept %d pairs; a blocked lookup's 0.0.0.0 must not be kept", a.Size())
	}
	if got := a.Name(client6, v6, t0); got != "www.example.org" {
		t.Errorf("IPv6: %q", got)
	}
	if got := a.Name(phone, cdn, t0); got != "mapped.example" {
		t.Errorf("an IPv4-mapped client: %q", got)
	}
}

func TestPruneAndDuplicates(t *testing.T) {
	a := NewAttributor()
	l := lookup(t0, phone, "www.example.com", cdn, netip.MustParseAddr("151.101.64.81"))
	a.Add([]Lookup{l, l})
	a.Add([]Lookup{l})
	if a.Size() != 2 || len(a.byPair[answerKey{phone, cdn}]) != 1 {
		t.Fatalf("duplicates kept: %v", a.byPair)
	}
	a.Prune(t0.Add(Window))
	if a.Size() != 2 {
		t.Error("pruned a lookup still in the window")
	}
	a.Prune(t0.Add(Window + Skew + time.Second))
	if a.Size() != 0 {
		t.Error("kept a lookup past the window")
	}
}
