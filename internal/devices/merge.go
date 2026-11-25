package devices

import (
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// Seen is one MAC's span: when it was first and last seen, and the address it used last.
type Seen struct {
	MAC                 flow.MAC
	FirstSeen, LastSeen time.Time
	Latest              netip.Addr
}

// Merge folds one MAC into a Device, so both count as one.
type Merge struct {
	MAC, Into flow.MAC
}

// AutoMerges finds Private Addresses that are an earlier Device under a new randomized
// MAC (ADR 0005): a private MAC first seen after another private MAC was last seen,
// whose current address has the same reverse DNS name (the host name the device gives
// DHCP). A merge needs exactly one such earlier MAC. merged maps the MACs merged
// already to the Device they belong to; names maps addresses to their names.
func AutoMerges(seen []Seen, names map[netip.Addr]string, merged map[flow.MAC]flow.MAC) []Merge {
	private := func(m flow.MAC) bool { return m[0]&0x02 != 0 }
	var out []Merge
	for _, n := range seen {
		if !private(n.MAC) || merged[n.MAC] != (flow.MAC{}) {
			continue
		}
		name := names[n.Latest]
		if name == "" {
			continue
		}
		var match flow.MAC
		matches := 0
		for _, o := range seen {
			if o.MAC == n.MAC || !private(o.MAC) || merged[o.MAC] != (flow.MAC{}) || names[o.Latest] != name || !o.LastSeen.Before(n.FirstSeen) {
				continue
			}
			match = o.MAC
			matches++
		}
		if matches == 1 {
			out = append(out, Merge{MAC: n.MAC, Into: match})
		}
	}
	return out
}
