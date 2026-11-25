package snmp

import (
	"time"

	"github.com/jkop-xyz/flowmon/internal/series"
)

// Poll is one read of a device's interface table, and when it was taken.
type Poll struct {
	At    time.Time
	Table Table
	// Health is the device's Device Health, when read; HealthErr why it wasn't.
	Health    *Health
	HealthErr error
	// Neighbors are its neighbor tables, when read; NeighborsErr why they weren't.
	Neighbors    []Neighbor
	NeighborsErr error
}

// MaxGap is the longest time between two polls whose difference still counts: longer,
// and the counters may have wrapped or reset unseen.
const MaxGap = 3 * time.Minute

// Deltas turns two polls of source into Interface Counter samples. There are none when
// the device restarted in between (sysUpTime went back) or the polls are too far apart.
// An interface new in cur has none yet. A 64-bit counter that went down (a reset)
// leaves that interface out; a 32-bit one wrapped.
func Deltas(source string, prev, cur Poll) []series.InterfaceSample {
	dt := cur.At.Sub(prev.At)
	if dt <= 0 || dt > MaxGap || cur.Table.Uptime < prev.Table.Uptime {
		return nil
	}
	var out []series.InterfaceSample
	for idx, c := range cur.Table.Rows {
		p, ok := prev.Table.Rows[idx]
		if !ok {
			continue
		}
		if c.InOctets < p.InOctets || c.OutOctets < p.OutOctets || c.InPackets < p.InPackets || c.OutPackets < p.OutPackets {
			continue
		}
		out = append(out, series.InterfaceSample{
			Source: source, IfIndex: idx, At: cur.At, Seconds: dt.Seconds(),
			InOctets: c.InOctets - p.InOctets, OutOctets: c.OutOctets - p.OutOctets,
			InPackets: c.InPackets - p.InPackets, OutPackets: c.OutPackets - p.OutPackets,
			// Unsigned subtraction wraps at 2^32, as the counters do.
			InErrors: uint64(c.InErrors - p.InErrors), OutErrors: uint64(c.OutErrors - p.OutErrors),
			InDiscards: uint64(c.InDiscards - p.InDiscards), OutDiscards: uint64(c.OutDiscards - p.OutDiscards),
		})
	}
	return out
}

// Interfaces describes cur's interfaces for series.Writer.
func Interfaces(source string, cur Table) []series.Interface {
	out := make([]series.Interface, 0, len(cur.Rows))
	for idx, r := range cur.Rows {
		out = append(out, series.Interface{Source: source, IfIndex: idx, Name: r.Name, Alias: r.Alias, Speed: r.SpeedMbps * 1_000_000, Up: r.Up})
	}
	return out
}
