package flowstore

import (
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func TestUsageNow(t *testing.T) {
	s := open(t)
	at := t0.Add(2 * time.Minute)
	tv := func(bytes uint64, domain string) flow.Flow {
		f := withDevice(mkFlow(flow.DirectionInbound, at, at, bytes), "192.168.10.44", phoneMAC)
		f.Domain = domain
		return f
	}
	laptop := withDevice(mkFlow(flow.DirectionOutbound, at, at, 15_000_000), "192.168.10.45", laptopMAC)
	laptop.Domain = "teams.microsoft.com"
	noMAC := withDevice(mkFlow(flow.DirectionInbound, at, at, 1_500_000), "192.168.10.99", flow.MAC{})
	internal := withDevice(mkFlow(flow.DirectionInternal, at, at, 900_000_000), "192.168.10.44", phoneMAC)
	old := tv(900_000_000, "nflxvideo.net")
	old.End = t0.Add(-time.Hour)
	if err := s.Write(ctx, []flow.Flow{tv(60_000_000, "ipv4-c001.1.oca.nflxvideo.net"), tv(15_000_000, "occ-0.nflxvideo.net"),
		laptop, noMAC, internal, old}); err != nil {
		t.Fatal(err)
	}
	res, err := s.UsageNow(ctx, t0, t0.Add(5*time.Minute), 10)
	if err != nil {
		t.Fatal(err)
	}
	// 75 MB in 300 s is 2 Mbit/s; Internal traffic and the old Flow don't count.
	if len(res.Devices) != 3 || res.Devices[0].MAC != phoneMAC.String() || res.Devices[0].InBps != 2_000_000 ||
		res.Devices[1].OutBps != 400_000 || res.Devices[2].MAC != "" || res.Devices[2].Host != "192.168.10.99" {
		t.Errorf("devices %+v", res.Devices)
	}
	if len(res.Domains) != 2 || res.Domains[0].Domain != "nflxvideo.net" || res.Domains[0].InBps != 2_000_000 ||
		res.Domains[0].TopMAC != phoneMAC.String() || res.Domains[1].Domain != "microsoft.com" {
		t.Errorf("domains %+v", res.Domains)
	}
	if _, err := s.UsageNow(ctx, t0, t0.Add(48*time.Hour), 10); err == nil {
		t.Error("a two-day window accepted")
	}
}
