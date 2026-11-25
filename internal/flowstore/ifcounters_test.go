package flowstore

import (
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/series"
)

func TestInterfaceCounters(t *testing.T) {
	s := open(t)
	wan := series.Interface{Source: "sw1", IfIndex: 1, Name: "ether1", Alias: "WAN", Speed: 1_000_000_000, Up: true}
	sample := func(at time.Time, in uint64) series.InterfaceSample {
		return series.InterfaceSample{Source: "sw1", IfIndex: 1, At: at, Seconds: 60, InOctets: in, OutOctets: in / 10, InErrors: 1}
	}
	// Two polls in one minute's bucket would be odd; one per minute, then a gap.
	if err := s.WriteInterfaces(ctx, t0, []series.Interface{wan}, []series.InterfaceSample{
		sample(t0.Add(30*time.Second), 7_500_000), sample(t0.Add(90*time.Second), 15_000_000),
	}); err != nil {
		t.Fatal(err)
	}
	wan.Up = false
	if err := s.WriteInterfaces(ctx, t0.Add(time.Hour), []series.Interface{wan}, nil); err != nil {
		t.Fatal(err)
	}
	ifs, err := s.SNMPInterfaces(ctx, "sw1")
	if err != nil || len(ifs) != 1 || ifs[0].Up || ifs[0].Alias != "WAN" || !ifs[0].LastSeen.Equal(t0.Add(time.Hour)) {
		t.Errorf("interfaces %+v %v; want the latest state", ifs, err)
	}

	pts, err := s.InterfaceSeries(ctx, "sw1", 1, t0, t0.Add(time.Hour))
	if err != nil || len(pts) != 2 {
		t.Fatalf("points %+v %v", pts, err)
	}
	if pts[0].InBps != 1_000_000 || pts[1].InBps != 2_000_000 || pts[0].OutBps != 100_000 || pts[0].Coverage != 1 || pts[0].Errors != 1 {
		t.Errorf("points %+v; want 1 and 2 Mbit/s in, 100 kbit/s out, fully covered", pts)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}
	if after, _ := s.InterfaceSeries(ctx, "sw1", 1, t0, t0.Add(time.Hour)); len(after) != 2 || after[1].InBps != 2_000_000 {
		t.Errorf("after compaction %+v", after)
	}
	day, err := s.InterfaceSeries(ctx, "sw1", 1, t0.Truncate(24*time.Hour), t0.Truncate(24*time.Hour).Add(24*time.Hour))
	if err != nil || len(day) != 1 || day[0].Coverage != 120.0/3600 || day[0].InBps != 22_500_000*8/120.0 {
		t.Errorf("hour points %+v %v", day, err)
	}

	r := DefaultRetention()
	r.Minute = 24 * time.Hour
	dropped, err := s.ApplyRetention(ctx, r, t0.Add(3*24*time.Hour), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, d := range dropped {
		found = found || d == "iface_minute_20261118"
		if d == "iface_hour_202611" {
			t.Error("dropped the hour table")
		}
	}
	if !found {
		t.Errorf("dropped %v", dropped)
	}
}

func TestReconciliationHours(t *testing.T) {
	s := open(t)
	h := t0.Truncate(time.Hour)
	// An hour from before the move, in snmp_hours.
	if err := s.AddSNMPDelta(ctx, exporter, "ether1", SNMPHour{Hour: h.Add(-time.Hour), InOctets: 5, Complete: true}); err != nil {
		t.Fatal(err)
	}
	var samples []series.InterfaceSample
	for m := 0; m < 60; m++ {
		samples = append(samples, series.InterfaceSample{Source: "sw1", IfIndex: 1, At: h.Add(time.Duration(m) * time.Minute), Seconds: 60, InOctets: 100})
	}
	samples = append(samples, series.InterfaceSample{Source: "sw1", IfIndex: 1, At: h.Add(time.Hour), Seconds: 60, InOctets: 7})
	if err := s.WriteInterfaces(ctx, t0, []series.Interface{{Source: "sw1", IfIndex: 1, Name: "ether1"}}, samples); err != nil {
		t.Fatal(err)
	}
	got, err := s.ReconciliationHours(ctx, "sw1", exporter, "ether1", h.Add(-2*time.Hour), h.Add(2*time.Hour))
	if err != nil || len(got) != 3 {
		t.Fatalf("hours %+v %v", got, err)
	}
	if got[0].InOctets != 5 || !got[0].Complete || got[1].InOctets != 6000 || !got[1].Complete || got[2].InOctets != 7 || got[2].Complete {
		t.Errorf("hours %+v; want the old hour, a complete hour of 6000 bytes, and a barely begun one", got)
	}
}

func TestInterfacesNow(t *testing.T) {
	s := open(t)
	if err := s.WriteInterfaces(ctx, t0, nil, []series.InterfaceSample{
		{Source: "sw1", IfIndex: 1, At: t0.Add(-2 * time.Minute), Seconds: 60, InOctets: 7_500_000, InErrors: 2},
		{Source: "sw1", IfIndex: 1, At: t0.Add(-time.Minute), Seconds: 60, InOctets: 15_000_000, OutDiscards: 3},
		{Source: "sw1", IfIndex: 1, At: t0.Add(-2 * time.Hour), Seconds: 60, InErrors: 100}, // outside the hour
		{Source: "sw2", IfIndex: 1, At: t0.Add(-time.Minute), Seconds: 60, InOctets: 1},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := s.InterfacesNow(ctx, "sw1", t0)
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	n := got[1]
	if !n.At.Equal(t0.Add(-time.Minute)) || n.InBps != 2_000_000 || n.HourErrors != 2 || n.HourDiscards != 3 {
		t.Errorf("now %+v; want the latest minute's 2 Mbit/s and the hour's 2 errors and 3 discards", n)
	}
}
