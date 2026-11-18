// Package ingest turns decoded records into stored Flows: normalise, classify, then
// write to the Flow Store in batches. It also keeps the per-Exporter timing shown in
// the Exporters page truth panel.
package ingest

import (
	"context"
	"errors"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// Writer stores Flows. *flowstore.Store implements it.
type Writer interface {
	Write(ctx context.Context, flows []flow.Flow) error
}

// Options configure a Pipeline.
type Options struct {
	Writer     Writer
	Classifier *classify.Classifier
	// Enrich, if set, adds GeoIP data to each classified Flow just before it is stored.
	Enrich func(*flow.Flow)
	Logger *slog.Logger
	// BatchSize and FlushInterval bound how long Flows wait before being written.
	BatchSize     int
	FlushInterval time.Duration
	// QueueSize bounds Flows waiting to be written; beyond it new Flows are dropped and counted.
	QueueSize int
	// WatermarkOverride, if set, replaces the Watermark learned from Flow durations.
	WatermarkOverride time.Duration
	// OnInterfaceName receives interface names an Exporter reports in Options records
	// (interfaceName scoped by an interface number).
	OnInterfaceName func(exporter netip.Addr, index uint32, name string, at time.Time)
	Now             func() time.Time
}

// Watermark parameters (ADR 0004): the 99th percentile of Flow durations over the last
// 24 hours, capped, plus a margin. Flows arriving more than LateLimit past it are dropped.
const (
	WatermarkWindow = 24 * time.Hour
	WatermarkCap    = 35 * time.Minute
	WatermarkMargin = 2 * time.Minute
	LateLimit       = 2 * time.Hour
	durationSamples = 4096
)

// Pipeline implements collector.RecordSink.
type Pipeline struct {
	opts  Options
	norm  *flow.Normalizer
	queue chan flow.Flow

	mu     sync.Mutex
	timing map[netip.Addr]*timing
	ifs    map[netip.Addr]map[uint32]*InterfaceStats

	written      atomic.Uint64
	dropped      atomic.Uint64
	lastWrite    atomic.Int64 // Unix nanoseconds of the last successful write
	batched      atomic.Int64 // Flows in the batch being filled
	late         atomic.Uint64
	writeErrors  atomic.Uint64
	noAddresses  atomic.Uint64
	optionsCount atomic.Uint64
}

// New returns a Pipeline. Call Run to start writing.
func New(opts Options) *Pipeline {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 5000
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	if opts.QueueSize <= 0 {
		opts.QueueSize = 100_000
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Classifier == nil {
		opts.Classifier = classify.New(classify.DefaultLocalNetworks())
	}
	return &Pipeline{
		opts:   opts,
		norm:   flow.NewNormalizer(),
		queue:  make(chan flow.Flow, opts.QueueSize),
		timing: map[netip.Addr]*timing{},
		ifs:    map[netip.Addr]map[uint32]*InterfaceStats{},
	}
}

// Records implements collector.RecordSink. It never blocks the collector: when the
// write queue is full, Flows are dropped and counted.
func (p *Pipeline) Records(exporter netip.Addr, records []ipfix.Record) {
	now := p.opts.Now()
	for _, rec := range records {
		f, err := p.norm.Normalize(exporter, rec, now)
		switch {
		case errors.Is(err, flow.ErrOptionsRecord):
			p.optionsCount.Add(1)
			p.interfaceNames(exporter, rec, now)
			continue
		case errors.Is(err, flow.ErrNoAddresses):
			p.noAddresses.Add(1)
			continue
		case err != nil:
			continue
		}
		p.opts.Classifier.Classify(&f, now)
		p.countInterfaces(exporter, &f)
		if p.observe(exporter, &f, now) {
			p.late.Add(1)
			continue
		}
		select {
		case p.queue <- f:
		default:
			p.dropped.Add(1)
		}
	}
}

// Run writes queued Flows until ctx is done, then writes what is left.
func (p *Pipeline) Run(ctx context.Context) {
	ticker := time.NewTicker(p.opts.FlushInterval)
	defer ticker.Stop()
	batch := make([]flow.Flow, 0, p.opts.BatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}
		if p.opts.Enrich != nil {
			for i := range batch {
				p.opts.Enrich(&batch[i])
			}
		}
		// A shutdown must not lose the last batch, so writes get their own context.
		wctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := p.opts.Writer.Write(wctx, batch)
		cancel()
		if err != nil {
			p.writeErrors.Add(1)
			p.opts.Logger.Error("writing flows", "flows", len(batch), "err", err)
		} else {
			p.written.Add(uint64(len(batch)))
			p.lastWrite.Store(time.Now().UnixNano())
		}
		batch = batch[:0]
		p.batched.Store(0)
	}

	for {
		select {
		case f := <-p.queue:
			batch = append(batch, f)
			p.batched.Store(int64(len(batch)))
			if len(batch) >= p.opts.BatchSize {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-ctx.Done():
			for {
				select {
				case f := <-p.queue:
					batch = append(batch, f)
					if len(batch) >= p.opts.BatchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// Stats are pipeline counters.
type Stats struct {
	Written uint64 `json:"written"`
	// Dropped Flows didn't fit in the write queue.
	Dropped     uint64 `json:"dropped"`
	Late        uint64 `json:"late"`
	WriteErrors uint64 `json:"writeErrors"`
	NoAddresses uint64 `json:"noAddresses"`
	Options     uint64 `json:"optionsRecords"`
	// Queued Flows wait to be written: in the queue (of QueueCapacity) or the next batch.
	Queued        int `json:"queued"`
	QueueCapacity int `json:"queueCapacity"`
	// LastWrite is when Flows were last written to the Flow Store.
	LastWrite *time.Time `json:"lastWrite,omitempty"`
}

// Stats returns the pipeline counters.
func (p *Pipeline) Stats() Stats {
	st := Stats{
		Written:       p.written.Load(),
		Dropped:       p.dropped.Load(),
		Late:          p.late.Load(),
		WriteErrors:   p.writeErrors.Load(),
		NoAddresses:   p.noAddresses.Load(),
		Options:       p.optionsCount.Load(),
		Queued:        len(p.queue) + int(p.batched.Load()),
		QueueCapacity: cap(p.queue),
	}
	if ns := p.lastWrite.Load(); ns != 0 {
		t := time.Unix(0, ns).UTC()
		st.LastWrite = &t
	}
	return st
}

// timingSamples is how many recent Flows the timing medians are taken over.
const timingSamples = 512

type timing struct {
	exportDelay []time.Duration // received - export time: the exporter's clock against ours
	endLag      []time.Duration // export time - flow end: how long after ending a Flow is exported
	next        int
	flows       uint64
	estimated   uint64
	late        uint64

	durations []sample // recent Flow durations, for the Watermark
	nextDur   int
}

type sample struct {
	at  time.Time
	dur time.Duration
}

// observe records timing for f and reports whether f is too late to keep.
func (p *Pipeline) observe(exporter netip.Addr, f *flow.Flow, now time.Time) (late bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.timing[exporter]
	if t == nil {
		t = &timing{}
		p.timing[exporter] = t
	}
	if f.End.Before(now.Add(-p.watermarkLocked(t, now) - LateLimit)) {
		t.late++
		return true
	}
	d := sample{at: now, dur: f.End.Sub(f.Start)}
	if len(t.durations) < durationSamples {
		t.durations = append(t.durations, d)
	} else {
		t.durations[t.nextDur] = d
		t.nextDur = (t.nextDur + 1) % durationSamples
	}
	t.flows++
	if f.TimeEstimated {
		t.estimated++
	}
	delay, lag := f.Received.Sub(f.ExportTime), f.ExportTime.Sub(f.End)
	if len(t.exportDelay) < timingSamples {
		t.exportDelay = append(t.exportDelay, delay)
		t.endLag = append(t.endLag, lag)
	} else {
		t.exportDelay[t.next] = delay
		t.endLag[t.next] = lag
		t.next = (t.next + 1) % timingSamples
	}
	return false
}

func (p *Pipeline) watermarkLocked(t *timing, now time.Time) time.Duration {
	if p.opts.WatermarkOverride > 0 {
		return p.opts.WatermarkOverride
	}
	var ds []time.Duration
	for _, s := range t.durations {
		if now.Sub(s.at) <= WatermarkWindow {
			ds = append(ds, s.dur)
		}
	}
	var p99 time.Duration
	if len(ds) > 0 {
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		p99 = ds[(len(ds)*99)/100]
	}
	return min(max(p99, 0), WatermarkCap) + WatermarkMargin
}

// Watermark returns an Exporter's Watermark: how long after a bucket ends its data may
// still change.
func (p *Pipeline) Watermark(exporter netip.Addr) time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.timing[exporter]
	if t == nil {
		t = &timing{}
	}
	return p.watermarkLocked(t, p.opts.Now())
}

// MaxWatermark is the largest Watermark of any Exporter: data newer than now minus this
// may still change, so the UI shades it as incomplete.
func (p *Pipeline) MaxWatermark() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.opts.Now()
	w := p.watermarkLocked(&timing{}, now)
	for _, t := range p.timing {
		w = max(w, p.watermarkLocked(t, now))
	}
	return w
}

func median(ds []time.Duration) time.Duration {
	s := append([]time.Duration(nil), ds...)
	sort.Slice(s, func(i, j int) bool { return s[i] < s[j] })
	return s[len(s)/2]
}

// Clock skew beyond these limits gets a finding. Export time has whole-second
// resolution, so small values are noise.
const (
	clockAheadLimit  = 5 * time.Second
	clockBehindLimit = 60 * time.Second
)

// Timing returns the timing panel for an Exporter, or nil before any Flow arrived.
func (p *Pipeline) Timing(exporter netip.Addr) *collector.TimingStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.timing[exporter]
	if t == nil || len(t.exportDelay) == 0 {
		return nil
	}
	delay, lag := median(t.exportDelay), median(t.endLag)
	st := &collector.TimingStatus{
		WatermarkSeconds:   p.watermarkLocked(t, p.opts.Now()).Seconds(),
		LateFlows:          t.late,
		Samples:            len(t.exportDelay),
		ClockOffsetSeconds: delay.Seconds(),
		ExportLagSeconds:   lag.Seconds(),
		Flows:              t.flows,
		EstimatedTimeFlows: t.estimated,
		Findings:           []collector.Finding{},
	}
	switch {
	case delay < -clockAheadLimit:
		st.Findings = append(st.Findings, collector.Finding{Severity: collector.SeverityWarning, Code: "clock_ahead",
			Message: "The Exporter's clock is ahead of Flowmon's by about " + (-delay).Round(time.Second).String() + ". Check NTP on the router."})
	case delay > clockBehindLimit:
		st.Findings = append(st.Findings, collector.Finding{Severity: collector.SeverityWarning, Code: "clock_behind",
			Message: "Export times are about " + delay.Round(time.Second).String() + " behind Flowmon's clock. Check NTP on the router."})
	}
	if t.late > 0 {
		st.Findings = append(st.Findings, collector.Finding{Severity: collector.SeverityWarning, Code: "late_flows",
			Message: "Some Flows arrived more than 2 hours after their Watermark and were dropped. Check the exporter's clock and timeouts."})
	}
	if t.estimated > 0 {
		st.Findings = append(st.Findings, collector.Finding{Severity: collector.SeverityInfo, Code: "estimated_times",
			Message: "Some Flows carried no usable absolute times, so their times were derived from the export time."})
	}
	return st
}

// Interface element IDs that scope an interfaceName in Options records.
var interfaceScopes = []uint16{10, 14, 252, 253}

const ieInterfaceName = 82

// interfaceNames passes on interface names from an Options record.
func (p *Pipeline) interfaceNames(exporter netip.Addr, rec ipfix.Record, now time.Time) {
	if p.opts.OnInterfaceName == nil {
		return
	}
	nameField, ok := rec.Get(ieInterfaceName)
	if !ok {
		return
	}
	v, err := nameField.Value()
	if err != nil || v.Kind != ipfix.KindString || v.String == "" {
		return
	}
	for i := 0; i < int(rec.ScopeFieldCount) && i < len(rec.Fields); i++ {
		f := rec.Fields[i]
		for _, id := range interfaceScopes {
			if f.Spec.Enterprise == 0 && f.Spec.ID == id {
				if idx, err := f.Value(); err == nil && idx.Kind == ipfix.KindUnsigned && idx.Uint > 0 && idx.Uint <= 0xFFFFFFFF {
					p.opts.OnInterfaceName(exporter, uint32(idx.Uint), v.String, now)
					return
				}
			}
		}
	}
}

// InterfaceStats are live counts for one interface number of an Exporter, since start.
type InterfaceStats struct {
	Index uint32 `json:"index"`
	// FlowsIn and BytesIn count Flows that entered through the interface; Out, that left.
	FlowsIn  uint64 `json:"flowsIn"`
	FlowsOut uint64 `json:"flowsOut"`
	BytesIn  uint64 `json:"bytesIn"`
	BytesOut uint64 `json:"bytesOut"`
	// RemoteIn counts Flows entering here whose source is outside the Local Networks:
	// the WAN suggestion.
	RemoteIn uint64 `json:"remoteIn"`
}

const maxInterfacesPerExporter = 256

func (p *Pipeline) countInterfaces(exporter netip.Addr, f *flow.Flow) {
	if f.IngressInterface == 0 && f.EgressInterface == 0 {
		return
	}
	remote := !p.opts.Classifier.InLocalNetwork(f.Src)
	p.mu.Lock()
	defer p.mu.Unlock()
	m := p.ifs[exporter]
	if m == nil {
		m = map[uint32]*InterfaceStats{}
		p.ifs[exporter] = m
	}
	get := func(idx uint32) *InterfaceStats {
		st := m[idx]
		if st == nil && len(m) < maxInterfacesPerExporter {
			st = &InterfaceStats{Index: idx}
			m[idx] = st
		}
		return st
	}
	if f.IngressInterface != 0 {
		if st := get(f.IngressInterface); st != nil {
			st.FlowsIn++
			st.BytesIn += f.Bytes
			if remote {
				st.RemoteIn++
			}
		}
	}
	if f.EgressInterface != 0 {
		if st := get(f.EgressInterface); st != nil {
			st.FlowsOut++
			st.BytesOut += f.Bytes
		}
	}
}

// Interfaces returns an Exporter's live interface counts, and the interface number most
// likely to be its WAN (0 if none stands out): the one where most Flows from Remote IPs
// come in, if it carries more than half of them.
func (p *Pipeline) Interfaces(exporter netip.Addr) ([]InterfaceStats, uint32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []InterfaceStats
	var total, best uint64
	var suggested uint32
	for _, st := range p.ifs[exporter] {
		out = append(out, *st)
		total += st.RemoteIn
		if st.RemoteIn > best {
			best, suggested = st.RemoteIn, st.Index
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	if total == 0 || best*2 <= total {
		suggested = 0
	}
	return out, suggested
}
