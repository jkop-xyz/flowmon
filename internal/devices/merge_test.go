package devices

import (
	"net/netip"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

func TestAutoMerges(t *testing.T) {
	oldPhone, newPhone := mustMAC("da:a1:19:00:00:01"), mustMAC("6e:00:00:00:00:02")
	otherOld, laptop := mustMAC("de:ad:be:ef:00:03"), mustMAC("b8:27:eb:00:00:45") // laptop: a vendor MAC
	a1, a2, a3, a4 := ip("192.168.10.60"), ip("192.168.10.61"), ip("192.168.10.62"), ip("192.168.10.63")
	names := map[netip.Addr]string{a1: "pixel.lan", a2: "pixel.lan", a3: "tv.lan", a4: "pixel.lan"}
	day := 24 * time.Hour
	base := []Seen{
		{MAC: oldPhone, FirstSeen: t0.Add(-7 * day), LastSeen: t0.Add(-time.Hour), Latest: a1},
		{MAC: newPhone, FirstSeen: t0, LastSeen: t0.Add(time.Hour), Latest: a2},
		{MAC: otherOld, FirstSeen: t0.Add(-7 * day), LastSeen: t0.Add(-time.Hour), Latest: a3},
		{MAC: laptop, FirstSeen: t0.Add(-7 * day), LastSeen: t0.Add(-time.Hour), Latest: a4},
	}
	got := AutoMerges(base, names, map[flow.MAC]flow.MAC{})
	if len(got) != 1 || got[0] != (Merge{MAC: newPhone, Into: oldPhone}) {
		t.Fatalf("merges %+v; want the new phone MAC into the old one, not into the TV or the vendor-MAC laptop", got)
	}

	// The old one still in use when the new one appeared: two devices, no merge.
	overlap := append([]Seen{}, base...)
	overlap[0].LastSeen = t0.Add(time.Minute)
	if got := AutoMerges(overlap, names, map[flow.MAC]flow.MAC{}); len(got) != 0 {
		t.Errorf("overlapping MACs merged: %+v", got)
	}
	// A different name: no merge.
	other := map[netip.Addr]string{a1: "pixel.lan", a2: "iphone.lan"}
	if got := AutoMerges(base, other, map[flow.MAC]flow.MAC{}); len(got) != 0 {
		t.Errorf("different names merged: %+v", got)
	}
	// Already merged, or two earlier candidates: no merge.
	if got := AutoMerges(base, names, map[flow.MAC]flow.MAC{newPhone: oldPhone}); len(got) != 0 {
		t.Errorf("merged twice: %+v", got)
	}
	twin := append(append([]Seen{}, base...), Seen{MAC: mustMAC("aa:00:00:00:00:09"), FirstSeen: t0.Add(-day), LastSeen: t0.Add(-2 * time.Hour), Latest: a1})
	if got := AutoMerges(twin, names, map[flow.MAC]flow.MAC{}); len(got) != 0 {
		t.Errorf("ambiguous earlier MACs merged: %+v", got)
	}
}
