package flowstore

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func TestNeighbors(t *testing.T) {
	s := open(t)
	printer := flow.MAC{0x00, 0x1b, 0xa9, 0, 0, 0x60}
	a := func(s string) netip.Addr { return netip.MustParseAddr(s) }
	poll := func(at time.Time, ns ...NeighborEntry) {
		t.Helper()
		if err := s.SaveNeighbors(ctx, "core", at, ns); err != nil {
			t.Fatal(err)
		}
	}
	// The phone is seen in the ARP table and has traffic; the printer only in the table.
	poll(t0, NeighborEntry{phoneMAC, a("192.168.10.44"), 2}, NeighborEntry{printer, a("192.168.10.60"), 2})
	poll(t0.Add(time.Minute), NeighborEntry{phoneMAC, a("192.168.10.44"), 2}, NeighborEntry{printer, a("192.168.10.60"), 2})
	if err := s.Write(ctx, []flow.Flow{withDevice(mkFlow(flow.DirectionOutbound, t0, t0, 100), "192.168.10.44", phoneMAC)}); err != nil {
		t.Fatal(err)
	}
	if err := s.Compact(ctx); err != nil {
		t.Fatal(err)
	}

	quiet, err := s.QuietDevices(ctx, t0, t0.Add(time.Hour))
	if err != nil || len(quiet) != 1 || quiet[0].MAC != printer.String() || quiet[0].Address != "192.168.10.60" || !quiet[0].LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("quiet devices %+v %v: want only the printer", quiet, err)
	}

	// The printer gets a new lease: a new mapping; the old one stays with its last sighting.
	poll(t0.Add(2*time.Minute), NeighborEntry{printer, a("192.168.10.61"), 2})
	addrs, err := s.DeviceAddressesOf(ctx, printer)
	if err != nil || len(addrs) != 2 || addrs[0].Address != a("192.168.10.61") || !addrs[1].LastSeen.Equal(t0.Add(time.Minute)) {
		t.Fatalf("addresses %+v %v", addrs, err)
	}
	if quiet, _ := s.QuietDevices(ctx, t0, t0.Add(time.Hour)); len(quiet) != 1 || quiet[0].Address != "192.168.10.61" {
		t.Errorf("quiet devices go by the address used last: %+v", quiet)
	}
	seen, err := s.NeighborsOf(ctx, printer)
	if err != nil || len(seen) != 2 || seen[0].Source != "core" || seen[0].IfIndex != 2 {
		t.Errorf("neighbors of the printer %+v %v", seen, err)
	}

	// Outside the range it was seen in, it isn't listed; a router hop never is.
	if quiet, _ := s.QuietDevices(ctx, t0.Add(2*time.Hour), t0.Add(3*time.Hour)); len(quiet) != 0 {
		t.Errorf("a mapping not seen in the range listed: %+v", quiet)
	}
	if err := s.AddRouterMAC(ctx, RouterMAC{MAC: printer, Exporter: a("192.168.10.1"), Reason: "many addresses", Since: t0}); err != nil {
		t.Fatal(err)
	}
	if quiet, _ := s.QuietDevices(ctx, t0, t0.Add(time.Hour)); len(quiet) != 0 {
		t.Errorf("a router hop listed as a quiet Device: %+v", quiet)
	}

	// Mappings age out with the hour retention.
	if err := s.applyHealthRetention(ctx, t0.Add(48*time.Hour), Retention{Minute: time.Hour, Hour: 24 * time.Hour}); err != nil {
		t.Fatal(err)
	}
	if seen, _ := s.NeighborsOf(ctx, printer); len(seen) != 0 {
		t.Errorf("aged-out mappings kept: %+v", seen)
	}
}
