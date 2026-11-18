package snmp

import (
	"context"
	"log/slog"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// Interval is how often agents are polled.
const Interval = time.Minute

// Sink receives what polling learns. In production the interfaces registry gets names
// and the Flow Store gets counter deltas.
type Sink interface {
	// Name reports ifName for an interface number.
	Name(exporter netip.Addr, index uint32, name string, at time.Time)
	// WANInterfaces returns the Exporter's WAN Interfaces now: name → interface number.
	WANInterfaces(exporter netip.Addr) map[string]uint32
	AddSNMPDelta(ctx context.Context, exporter netip.Addr, ifName string, d flowstore.SNMPHour) error
}

// Status is an Exporter's polling state for the UI.
type Status struct {
	LastPoll  time.Time `json:"lastPoll,omitempty"`
	LastError string    `json:"lastError,omitempty"`
	// Interfaces is how many names the last walk returned.
	Interfaces int `json:"interfaces"`
}

type sample struct {
	at time.Time
	c  Counters
}

// Poller polls every Exporter with SNMP enabled. It is safe for concurrent use.
type Poller struct {
	dial   Dialer
	sink   Sink
	logger *slog.Logger
	now    func() time.Time

	mu      sync.Mutex
	configs map[netip.Addr]Config
	status  map[netip.Addr]Status
	last    map[netip.Addr]map[string]sample
}

// NewPoller returns a Poller with initial configs.
func NewPoller(dial Dialer, sink Sink, configs map[netip.Addr]Config, logger *slog.Logger, now func() time.Time) *Poller {
	if dial == nil {
		dial = Dial
	}
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if configs == nil {
		configs = map[netip.Addr]Config{}
	}
	return &Poller{dial: dial, sink: sink, logger: logger, now: now, configs: configs,
		status: map[netip.Addr]Status{}, last: map[netip.Addr]map[string]sample{}}
}

// Set replaces an Exporter's config; polling picks it up on the next round.
func (p *Poller) Set(exporter netip.Addr, c Config) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.configs[exporter] = c
	delete(p.last, exporter) // new credentials or target: start a fresh series
}

// Config returns an Exporter's config.
func (p *Poller) Config(exporter netip.Addr) (Config, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	c, ok := p.configs[exporter]
	return c, ok
}

// Status returns an Exporter's polling state.
func (p *Poller) Status(exporter netip.Addr) Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status[exporter]
}

// Run polls every Interval until ctx is done.
func (p *Poller) Run(ctx context.Context) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	for {
		p.PollAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// PollAll polls every enabled Exporter once.
func (p *Poller) PollAll(ctx context.Context) {
	p.mu.Lock()
	var targets []netip.Addr
	for exp, c := range p.configs {
		if c.Enabled {
			targets = append(targets, exp)
		}
	}
	p.mu.Unlock()
	sort.Slice(targets, func(i, j int) bool { return targets[i].Less(targets[j]) })
	for _, exp := range targets {
		if err := p.Poll(ctx, exp); err != nil {
			p.logger.Warn("snmp poll", "exporter", exp.String(), "err", err)
		}
	}
}

// Test connects with a config and walks ifName, for the "Test" button.
func (p *Poller) Test(ctx context.Context, exporter netip.Addr, c Config) (map[uint32]string, error) {
	a, err := p.dial(c, exporter)
	if err != nil {
		return nil, err
	}
	defer a.Close()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	return a.InterfaceNames(ctx)
}

// Poll polls one Exporter: names for every interface, counters for WAN Interfaces.
func (p *Poller) Poll(ctx context.Context, exporter netip.Addr) error {
	c, ok := p.Config(exporter)
	if !ok || !c.Enabled {
		return nil
	}
	now := p.now()
	err := p.poll(ctx, exporter, c, now)
	p.mu.Lock()
	st := p.status[exporter]
	st.LastPoll = now
	st.LastError = ""
	if err != nil {
		st.LastError = err.Error()
	}
	p.status[exporter] = st
	p.mu.Unlock()
	return err
}

func (p *Poller) poll(ctx context.Context, exporter netip.Addr, c Config, now time.Time) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	a, err := p.dial(c, exporter)
	if err != nil {
		return err
	}
	defer a.Close()

	names, err := a.InterfaceNames(ctx)
	if err != nil {
		return err
	}
	for idx, name := range names {
		p.sink.Name(exporter, idx, name, now)
	}
	p.mu.Lock()
	st := p.status[exporter]
	st.Interfaces = len(names)
	p.status[exporter] = st
	p.mu.Unlock()

	wan := p.sink.WANInterfaces(exporter)
	if len(wan) == 0 {
		return nil
	}
	var idxs []uint32
	for _, idx := range wan {
		idxs = append(idxs, idx)
	}
	counters, err := a.Counters(ctx, idxs)
	if err != nil {
		return err
	}
	for name, idx := range wan {
		cur, ok := counters[idx]
		if !ok {
			continue
		}
		if err := p.record(ctx, exporter, name, sample{at: now, c: cur}); err != nil {
			return err
		}
	}
	return nil
}

// record turns two consecutive samples into hourly deltas. A counter that went down
// (agent restart, PPPoE reconnect resetting counters) or a gap of more than three
// intervals makes the affected hours incomplete instead of producing bogus deltas.
func (p *Poller) record(ctx context.Context, exporter netip.Addr, name string, cur sample) error {
	p.mu.Lock()
	m := p.last[exporter]
	if m == nil {
		m = map[string]sample{}
		p.last[exporter] = m
	}
	prev, had := m[name]
	m[name] = cur
	p.mu.Unlock()

	incomplete := func(t time.Time) error {
		return p.sink.AddSNMPDelta(ctx, exporter, name, flowstore.SNMPHour{Hour: t.Truncate(time.Hour), Complete: false})
	}
	if !had {
		return incomplete(cur.at) // the series starts mid-hour
	}
	dt := cur.at.Sub(prev.at)
	reset := cur.c.InOctets < prev.c.InOctets || cur.c.OutOctets < prev.c.OutOctets ||
		cur.c.InPackets < prev.c.InPackets || cur.c.OutPackets < prev.c.OutPackets
	if dt <= 0 || dt > 3*Interval || reset {
		if err := incomplete(prev.at); err != nil {
			return err
		}
		return incomplete(cur.at)
	}
	d := Counters{
		InOctets: cur.c.InOctets - prev.c.InOctets, OutOctets: cur.c.OutOctets - prev.c.OutOctets,
		InPackets: cur.c.InPackets - prev.c.InPackets, OutPackets: cur.c.OutPackets - prev.c.OutPackets,
	}
	// Split the delta over the hours the interval covers, in proportion to time.
	for from := prev.at; from.Before(cur.at); {
		to := from.Truncate(time.Hour).Add(time.Hour)
		if to.After(cur.at) {
			to = cur.at
		}
		share := float64(to.Sub(from)) / float64(dt)
		part := flowstore.SNMPHour{Hour: from.Truncate(time.Hour), Complete: true,
			InOctets: uint64(float64(d.InOctets) * share), OutOctets: uint64(float64(d.OutOctets) * share),
			InPackets: uint64(float64(d.InPackets) * share), OutPackets: uint64(float64(d.OutPackets) * share)}
		if err := p.sink.AddSNMPDelta(ctx, exporter, name, part); err != nil {
			return err
		}
		from = to
	}
	return nil
}
