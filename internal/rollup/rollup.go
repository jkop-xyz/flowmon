// Package rollup turns Flows into Rollup rows (ADR 0004): traffic totals per time
// bucket, always seen from the Local Host, with bytes and packets received and sent
// as separate measures.
package rollup

import (
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// PeerKind says whether a Rollup row's Peer is a Remote IP or another Local Host.
type PeerKind uint8

// Peer kinds. The values are stored.
const (
	PeerRemote PeerKind = 1
	PeerLocal  PeerKind = 2
)

// Key is what a Rollup row is grouped by. Remote IPs are deliberately absent: they
// would make Rollups nearly as large as raw Flows (see Top Remote IPs instead).
type Key struct {
	Bucket              time.Time
	Exporter            string
	LocalHost           string
	LocalIsExporterHost bool
	PeerKind            PeerKind
	// PeerLocalHost is the other Local Host for Internal traffic, empty otherwise.
	PeerLocalHost string
	// PeerASN and PeerCountry are the Remote IP's GeoIP enrichment; 0 and "" when
	// unknown and for Internal traffic.
	PeerASN     uint32
	PeerCountry string
	Protocol    uint8
	ServicePort uint16
}

// Measures are additive. Bytes and packets are float because a Flow's bytes are
// spread over the buckets it overlaps.
type Measures struct {
	BytesRx, BytesTx     float64
	PacketsRx, PacketsTx float64
	// Flows counts Flows on the row of the Local Host that sent them, in their end bucket.
	Flows uint64
	// EstimatedRx and EstimatedTx are the parts of BytesRx and BytesTx that come from
	// sampled Flows, whose counts were scaled up to estimates.
	EstimatedRx, EstimatedTx float64
}

// Add adds o to m.
func (m *Measures) Add(o Measures) {
	m.BytesRx += o.BytesRx
	m.BytesTx += o.BytesTx
	m.PacketsRx += o.PacketsRx
	m.PacketsTx += o.PacketsTx
	m.Flows += o.Flows
	m.EstimatedRx += o.EstimatedRx
	m.EstimatedTx += o.EstimatedTx
}

// Row is one contribution of a Flow to a bucket.
type Row struct {
	Key Key
	Measures
}

// MaxSpread bounds how far a Flow's bytes are spread. A Flow longer than this has
// implausible times, so all its bytes go to its end bucket instead.
const MaxSpread = 6 * time.Hour

// Contributions returns the Rollup rows a Flow adds at the given bucket size.
//
// Outbound Flows are bytes the Local Host sent; Inbound Flows are bytes it received.
// An Internal Flow produces a sent row for its Local Host and a received row for its
// Peer, so per-host views see both sides, while Network Totals count only the sent
// side of Internal traffic. Transit Flows have no Local Host and contribute nothing.
func Contributions(f *flow.Flow, bucket time.Duration) []Row {
	if f.Direction == flow.DirectionTransit || !f.Local.IsValid() {
		return nil
	}
	base := Key{
		Exporter:            f.Exporter.String(),
		LocalHost:           f.Local.String(),
		LocalIsExporterHost: f.LocalIsExporterHost,
		PeerKind:            PeerRemote,
		PeerASN:             f.PeerASN,
		PeerCountry:         f.PeerCountry,
		Protocol:            f.Protocol,
		ServicePort:         flow.ServicePort(f.Protocol, f.LocalPort, f.PeerPort),
	}
	total := Measures{Flows: 1}
	switch f.Direction {
	case flow.DirectionInbound:
		total.BytesRx, total.PacketsRx = float64(f.Bytes), float64(f.Packets)
	default: // Outbound, Internal: the Local Host sent it
		total.BytesTx, total.PacketsTx = float64(f.Bytes), float64(f.Packets)
	}
	if f.SamplingRate > 1 {
		total.EstimatedRx, total.EstimatedTx = total.BytesRx, total.BytesTx
	}

	var keys []Key
	var measures []Measures
	if f.Direction == flow.DirectionInternal {
		sender := base
		sender.PeerKind, sender.PeerLocalHost = PeerLocal, f.Peer.String()
		sender.PeerASN, sender.PeerCountry = 0, ""
		receiver := sender
		receiver.LocalHost, receiver.PeerLocalHost = f.Peer.String(), f.Local.String()
		receiver.LocalIsExporterHost = false
		keys = []Key{sender, receiver}
		measures = []Measures{total, {BytesRx: total.BytesTx, PacketsRx: total.PacketsTx, EstimatedRx: total.EstimatedTx}}
	} else {
		keys = []Key{base}
		measures = []Measures{total}
	}

	var rows []Row
	for _, sh := range spread(f.Start, f.End, bucket) {
		for i, k := range keys {
			k.Bucket = sh.bucket
			m := measures[i]
			var flows uint64
			if sh.last {
				flows = m.Flows
			}
			rows = append(rows, Row{Key: k, Measures: Measures{
				BytesRx: m.BytesRx * sh.share, BytesTx: m.BytesTx * sh.share,
				PacketsRx: m.PacketsRx * sh.share, PacketsTx: m.PacketsTx * sh.share,
				Flows: flows, EstimatedRx: m.EstimatedRx * sh.share, EstimatedTx: m.EstimatedTx * sh.share,
			}})
		}
	}
	return rows
}

type share struct {
	bucket time.Time
	share  float64
	last   bool
}

// spread splits [start, end] over buckets in proportion to overlap.
func spread(start, end time.Time, bucket time.Duration) []share {
	start, end = start.UTC(), end.UTC()
	dur := end.Sub(start)
	if dur <= 0 || dur > MaxSpread {
		return []share{{bucket: end.Truncate(bucket), share: 1, last: true}}
	}
	var out []share
	for b := start.Truncate(bucket); b.Before(end); b = b.Add(bucket) {
		lo, hi := b, b.Add(bucket)
		if start.After(lo) {
			lo = start
		}
		if end.Before(hi) {
			hi = end
		}
		out = append(out, share{bucket: b, share: float64(hi.Sub(lo)) / float64(dur)})
	}
	out[len(out)-1].last = true
	return out
}

// Aggregate sums rows with the same key.
func Aggregate(rows []Row) map[Key]*Measures {
	out := make(map[Key]*Measures, len(rows))
	for _, r := range rows {
		m := out[r.Key]
		if m == nil {
			m = &Measures{}
			out[r.Key] = m
		}
		m.Add(r.Measures)
	}
	return out
}
