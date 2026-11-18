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
	Logger     *slog.Logger
	// BatchSize and FlushInterval bound how long Flows wait before being written.
	BatchSize     int
	FlushInterval time.Duration
	// QueueSize bounds Flows waiting to be written; beyond it new Flows are dropped and counted.
	QueueSize int
	Now       func() time.Time
}

// Pipeline implements collector.RecordSink.
type Pipeline struct {
	opts  Options
	norm  *flow.Normalizer
	queue chan flow.Flow

	mu     sync.Mutex
	timing map[netip.Addr]*timing

	written      atomic.Uint64
	dropped      atomic.Uint64
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
			continue
		case errors.Is(err, flow.ErrNoAddresses):
			p.noAddresses.Add(1)
			continue
		case err != nil:
			continue
		}
		p.opts.Classifier.Classify(&f, now)
		p.observe(exporter, &f)
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
		// A shutdown must not lose the last batch, so writes get their own context.
		wctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		err := p.opts.Writer.Write(wctx, batch)
		cancel()
		if err != nil {
			p.writeErrors.Add(1)
			p.opts.Logger.Error("writing flows", "flows", len(batch), "err", err)
		} else {
			p.written.Add(uint64(len(batch)))
		}
		batch = batch[:0]
	}

	for {
		select {
		case f := <-p.queue:
			batch = append(batch, f)
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
	Written     uint64 `json:"written"`
	Dropped     uint64 `json:"dropped"`
	WriteErrors uint64 `json:"writeErrors"`
	NoAddresses uint64 `json:"noAddresses"`
	Options     uint64 `json:"optionsRecords"`
}

// Stats returns the pipeline counters.
func (p *Pipeline) Stats() Stats {
	return Stats{
		Written:     p.written.Load(),
		Dropped:     p.dropped.Load(),
		WriteErrors: p.writeErrors.Load(),
		NoAddresses: p.noAddresses.Load(),
		Options:     p.optionsCount.Load(),
	}
}

// timingSamples is how many recent Flows the timing medians are taken over.
const timingSamples = 512

type timing struct {
	exportDelay []time.Duration // received - export time: the exporter's clock against ours
	endLag      []time.Duration // export time - flow end: how long after ending a Flow is exported
	next        int
	flows       uint64
	estimated   uint64
}

func (p *Pipeline) observe(exporter netip.Addr, f *flow.Flow) {
	p.mu.Lock()
	defer p.mu.Unlock()
	t := p.timing[exporter]
	if t == nil {
		t = &timing{}
		p.timing[exporter] = t
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
	if t.estimated > 0 {
		st.Findings = append(st.Findings, collector.Finding{Severity: collector.SeverityInfo, Code: "estimated_times",
			Message: "Some Flows carried no usable absolute times, so their times were derived from the export time."})
	}
	return st
}
