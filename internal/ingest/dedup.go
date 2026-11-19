package ingest

import (
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// An Exporter that "observes multiple points" (a router exporting on several
// interfaces, softflowd on every tap of a hypervisor) reports the same packets more than
// once. Duplicates are removed within one Exporter, before Rollups (ADR 0003): a Flow is
// a duplicate when it shares a key with one already seen and their times overlap. The
// key is NAT-aware: the same packets seen before and after NAT carry different
// addresses, so a Flow is known by its pre-NAT and its post-NAT ends alike.

// dedupKey is one direction of a connection: who sent to whom, on which ports.
type dedupKey struct {
	src, dst         netip.Addr
	srcPort, dstPort uint16
	protocol         uint8
}

type span struct{ start, end time.Time }

func (a span) overlaps(b span) bool {
	return a.start.Before(b.end) && b.start.Before(a.end) || a == b
}

// dedupWindow is how long a Flow is remembered: past the longest Watermark, a duplicate
// can't arrive anymore.
const dedupWindow = WatermarkCap + WatermarkMargin

// maxDedupKeys bounds what one Exporter's duplicate removal remembers.
const maxDedupKeys = 200_000

type dedup struct {
	seen    map[dedupKey][]span
	newest  time.Time
	swept   time.Time
	removed uint64
}

// keys are a Flow's ends before and after NAT.
func dedupKeys(f *flow.Flow) []dedupKey {
	pre := dedupKey{f.Src.Unmap(), f.Dst.Unmap(), f.SrcPort, f.DstPort, f.Protocol}
	post := pre
	if f.PostNATSrc.IsValid() {
		post.src = f.PostNATSrc.Unmap()
	}
	if f.PostNATDst.IsValid() {
		post.dst = f.PostNATDst.Unmap()
	}
	if f.PostNATSrcPort != 0 {
		post.srcPort = f.PostNATSrcPort
	}
	if f.PostNATDstPort != 0 {
		post.dstPort = f.PostNATDstPort
	}
	if post == pre {
		return []dedupKey{pre}
	}
	return []dedupKey{pre, post}
}

// duplicate reports whether f repeats a Flow seen before, and remembers it if not.
func (d *dedup) duplicate(f *flow.Flow) bool {
	if d.seen == nil {
		d.seen = map[dedupKey][]span{}
	}
	s := span{f.Start, f.End}
	keys := dedupKeys(f)
	for _, k := range keys {
		for _, o := range d.seen[k] {
			if o.overlaps(s) {
				d.removed++
				return true
			}
		}
	}
	if f.End.After(d.newest) {
		d.newest = f.End
	}
	d.sweep()
	if len(d.seen) >= maxDedupKeys {
		// More distinct connections than a home network has in a Watermark: start over
		// rather than grow; a few duplicates may get through.
		d.seen = map[dedupKey][]span{}
	}
	for _, k := range keys {
		d.seen[k] = append(d.seen[k], s)
	}
	return false
}

// sweep forgets Flows that ended before the window, once a minute of Flow time.
func (d *dedup) sweep() {
	if d.newest.Sub(d.swept) < time.Minute {
		return
	}
	d.swept = d.newest
	cutoff := d.newest.Add(-dedupWindow)
	for k, spans := range d.seen {
		kept := spans[:0]
		for _, s := range spans {
			if s.end.After(cutoff) {
				kept = append(kept, s)
			}
		}
		if len(kept) == 0 {
			delete(d.seen, k)
		} else {
			d.seen[k] = kept
		}
	}
}
