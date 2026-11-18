// Package collector receives flow export datagrams, decodes them per transport
// session, and keeps the per-Exporter state shown on the Exporters page.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jkop-xyz/flowmon/internal/capture"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// Collector-level deviations, reported alongside ipfix.Deviation values.
const (
	DevNetFlowV9Unsupported ipfix.Deviation = "netflow_v9_not_supported_yet"
	DevNetFlowV5Unsupported ipfix.Deviation = "netflow_v5_not_supported_yet"
	DevUnknownVersion       ipfix.Deviation = "unknown_version"
	DevDecoderPanic         ipfix.Deviation = "decoder_panic"
)

// RecordSink receives decoded records. It is called outside the collector's lock and
// must not keep the slice beyond the call (the records themselves may be kept).
type RecordSink interface {
	Records(exporter netip.Addr, records []ipfix.Record)
}

// DiscardSink drops records. It is used until the Flow Store exists.
type DiscardSink struct{}

// Records implements RecordSink.
func (DiscardSink) Records(netip.Addr, []ipfix.Record) {}

// Admission is what the collector does with an Exporter's datagrams.
type Admission uint8

// Admissions.
const (
	// AdmitPending decodes (so Templates are kept) but drops and counts the data.
	AdmitPending Admission = iota
	AdmitAccept
	// AdmitIgnore drops datagrams without decoding.
	AdmitIgnore
)

func (a Admission) String() string {
	switch a {
	case AdmitAccept:
		return "approved"
	case AdmitIgnore:
		return "ignored"
	}
	return "pending"
}

// Gate decides admission per Exporter. It is asked for every datagram, so decisions
// apply at once; it must be fast and must not depend on the App Store.
type Gate interface {
	Admission(exporter netip.Addr) Admission
}

// GateFunc adapts a function to Gate.
type GateFunc func(netip.Addr) Admission

// Admission implements Gate.
func (f GateFunc) Admission(a netip.Addr) Admission { return f(a) }

// Options configure a Collector.
type Options struct {
	Limits ipfix.Limits
	// MaxExporters caps how many Exporters are tracked, so spoofed sources can't exhaust memory.
	MaxExporters int
	// MaxSessionsPerExporter caps transport sessions (source ports) kept per Exporter.
	MaxSessionsPerExporter int
	// MaxPending caps Pending Exporters, so spoofed sources can't fill the list.
	MaxPending int
	// NewExportersPerMinute rate-limits how fast unknown sources become Exporters.
	NewExportersPerMinute int
	// ReceiveBuffer is the socket receive buffer to ask for; 0 means 8 MiB.
	ReceiveBuffer int
	// Gate decides admission; nil accepts every Exporter.
	Gate    Gate
	Sink    RecordSink
	Capture *capture.Writer
	Logger  *slog.Logger
	// Now returns the current time; tests replace it.
	Now func() time.Time
}

// Collector is safe for concurrent use.
type Collector struct {
	opts      Options
	mu        sync.Mutex
	exporters map[netip.Addr]*exporter
	listeners []*listener
	// lastAccepted is when records from an approved Exporter last went to the sink.
	lastAccepted     time.Time
	lastAcceptedFrom netip.Addr
	overLimit        uint64 // datagrams dropped because MaxExporters was reached
	// overPending counts datagrams dropped because MaxPending was reached.
	overPending uint64
	// rateLimited counts datagrams from new sources dropped by NewExportersPerMinute.
	rateLimited uint64
	newTokens   float64
	newRefill   time.Time
	started     time.Time
}

// New returns a Collector. Zero option values get defaults.
func New(opts Options) *Collector {
	if opts.Limits == (ipfix.Limits{}) {
		opts.Limits = ipfix.DefaultLimits()
	}
	if opts.MaxExporters <= 0 {
		opts.MaxExporters = 64
	}
	if opts.MaxSessionsPerExporter <= 0 {
		opts.MaxSessionsPerExporter = 8
	}
	if opts.MaxPending <= 0 {
		opts.MaxPending = 16
	}
	if opts.NewExportersPerMinute <= 0 {
		opts.NewExportersPerMinute = 10
	}
	if opts.ReceiveBuffer <= 0 {
		opts.ReceiveBuffer = 8 << 20
	}
	if opts.Gate == nil {
		opts.Gate = GateFunc(func(netip.Addr) Admission { return AdmitAccept })
	}
	if opts.Sink == nil {
		opts.Sink = DiscardSink{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	now := opts.Now()
	return &Collector{opts: opts, exporters: map[netip.Addr]*exporter{}, started: now,
		newTokens: float64(opts.NewExportersPerMinute), newRefill: now}
}

// listener is one socket the collector receives on.
type listener struct {
	address      string
	requested    int
	granted      int
	dropsCounted bool
	datagrams    atomic.Uint64
	kernelDrops  atomic.Uint64
}

// ListenerStatus describes one collector socket.
type ListenerStatus struct {
	Address string `json:"address"`
	// RequestedBuffer and GrantedBuffer are the receive buffer asked for and what the
	// kernel gave (0 when unknown).
	RequestedBuffer int `json:"requestedBuffer"`
	GrantedBuffer   int `json:"grantedBuffer"`
	// KernelDrops counts datagrams the kernel dropped because the buffer was full;
	// DropsCounted is false where the kernel can't report them.
	KernelDrops  uint64 `json:"kernelDrops"`
	DropsCounted bool   `json:"dropsCounted"`
	Datagrams    uint64 `json:"datagrams"`
}

func (l *listener) status() ListenerStatus {
	return ListenerStatus{Address: l.address, RequestedBuffer: l.requested, GrantedBuffer: l.granted,
		KernelDrops: l.kernelDrops.Load(), DropsCounted: l.dropsCounted, Datagrams: l.datagrams.Load()}
}

// ListenUDP receives datagrams on addr until ctx is done. It returns once the socket is
// bound (or binding failed); receiving continues in a goroutine.
func (c *Collector) ListenUDP(ctx context.Context, addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s/udp: %w", addr, err)
	}
	udp := conn.(*net.UDPConn)
	local := udp.LocalAddr().(*net.UDPAddr).AddrPort()
	l := &listener{address: local.String() + "/udp", requested: c.opts.ReceiveBuffer}
	l.granted, l.dropsCounted = tuneSocket(udp, c.opts.ReceiveBuffer)

	c.mu.Lock()
	c.listeners = append(c.listeners, l)
	c.mu.Unlock()
	c.opts.Logger.Info("collector listening", "addr", local.String(), "transport", "udp", "receive_buffer", l.granted)
	if l.granted > 0 && l.granted < l.requested {
		c.opts.Logger.Warn("the kernel granted a smaller UDP receive buffer than asked for; bursts may be dropped. Raise net.core.rmem_max on the host",
			"addr", local.String(), "asked", l.requested, "granted", l.granted)
	}

	go func() {
		<-ctx.Done()
		_ = udp.Close()
	}()
	go func() {
		buf := make([]byte, 65535)
		oob := make([]byte, oobSize)
		for {
			n, oobn, _, src, err := udp.ReadMsgUDPAddrPort(buf, oob)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				c.opts.Logger.Warn("udp read", "addr", local.String(), "err", err)
				continue
			}
			l.datagrams.Add(1)
			if drops, ok := socketDrops(oob[:oobn]); ok {
				// The kernel reports its running total for the socket.
				l.kernelDrops.Store(uint64(drops))
			}
			c.HandleDatagram(src, local, buf[:n])
		}
	}()
	return nil
}

// HandleDatagram processes one datagram from src received on the collector socket local.
// The buffer may be reused as soon as it returns.
func (c *Collector) HandleDatagram(src, local netip.AddrPort, data []byte) {
	now := c.opts.Now()
	src = netip.AddrPortFrom(src.Addr().Unmap(), src.Port())

	if c.opts.Capture != nil {
		if err := c.opts.Capture.Write(now, src, local, data); err != nil && !errors.Is(err, capture.ErrStopped) {
			c.opts.Logger.Warn("debug capture stopped", "err", err)
		}
	}

	admission := c.opts.Gate.Admission(src.Addr())

	c.mu.Lock()
	exp, ok := c.exporterLocked(src.Addr(), admission, now)
	if !ok {
		c.mu.Unlock()
		return
	}
	exp.admission = admission
	exp.lastSeen = now
	exp.datagrams++
	exp.bytes += uint64(len(data))
	exp.lastSource = src
	if admission == AdmitIgnore {
		exp.ignoredDatagrams++
		c.mu.Unlock()
		return
	}

	var records []ipfix.Record
	switch ipfix.PeekVersion(data) {
	case ipfix.Version:
		records = c.decodeLocked(exp, src, local, data, now)
		if admission == AdmitPending {
			// Templates are kept so approval decodes at once; the data isn't held.
			exp.pendingDropped += uint64(len(records))
			records = nil
		}
	case 9:
		exp.deviation(DevNetFlowV9Unsupported, "NetFlow v9 support comes in v1.1; send IPFIX instead", now)
	case 5:
		exp.deviation(DevNetFlowV5Unsupported, "NetFlow v5 support comes in v1.1; send IPFIX instead", now)
	default:
		exp.deviation(DevUnknownVersion, fmt.Sprintf("version %d", ipfix.PeekVersion(data)), now)
	}
	if len(records) > 0 {
		c.lastAccepted, c.lastAcceptedFrom = now, src.Addr()
	}
	c.mu.Unlock()

	if len(records) > 0 {
		c.opts.Sink.Records(src.Addr(), records)
	}
}

func (c *Collector) exporterLocked(addr netip.Addr, admission Admission, now time.Time) (*exporter, bool) {
	if exp, ok := c.exporters[addr]; ok {
		return exp, true
	}
	if len(c.exporters) >= c.opts.MaxExporters {
		c.overLimit++
		return nil, false
	}
	if admission == AdmitPending {
		pending := 0
		for _, e := range c.exporters {
			if e.admission == AdmitPending {
				pending++
			}
		}
		if pending >= c.opts.MaxPending {
			c.overPending++
			return nil, false
		}
	}
	// Token bucket for new sources: a flood of spoofed addresses can't create Exporters fast.
	rate := float64(c.opts.NewExportersPerMinute)
	c.newTokens = min(rate, c.newTokens+now.Sub(c.newRefill).Minutes()*rate)
	c.newRefill = now
	if c.newTokens < 1 {
		c.rateLimited++
		return nil, false
	}
	c.newTokens--
	exp := newExporter(addr, now)
	c.exporters[addr] = exp
	c.opts.Logger.Info("new exporter", "addr", addr.String())
	return exp, true
}

// decodeLocked decodes an IPFIX message, recovering from any panic so one bad packet
// can't stop collection.
func (c *Collector) decodeLocked(exp *exporter, src, local netip.AddrPort, data []byte, now time.Time) (records []ipfix.Record) {
	sess := exp.session(sessionKey{remote: src, local: local}, now, c.opts.Limits, c.opts.MaxSessionsPerExporter)
	h := &decodeHandler{exp: exp, now: now}

	defer func() {
		if r := recover(); r != nil {
			exp.deviation(DevDecoderPanic, fmt.Sprint(r), now)
			c.opts.Logger.Error("decoder panic", "exporter", exp.addr.String(), "panic", r, "stack", string(debug.Stack()))
			records = nil
		}
	}()

	if _, err := sess.s.Decode(data, now, h); err == nil {
		exp.messages++
	}
	exp.records += uint64(len(h.records))
	return h.records
}

type decodeHandler struct {
	exp     *exporter
	now     time.Time
	records []ipfix.Record
}

func (h *decodeHandler) Template(od uint32, t *ipfix.Template, ev ipfix.TemplateEvent) {
	h.exp.observationDomains[od] = struct{}{}
	if ev == ipfix.TemplateRedefined {
		h.exp.templateRedefinitions++
	}
}

func (h *decodeHandler) Records(recs []ipfix.Record) { h.records = append(h.records, recs...) }

func (h *decodeHandler) Deviation(d ipfix.Deviation, detail string) {
	h.exp.deviation(d, detail, h.now)
}

// Listeners returns the sockets the collector receives on.
func (c *Collector) Listeners() []ListenerStatus {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]ListenerStatus, len(c.listeners))
	for i, l := range c.listeners {
		out[i] = l.status()
	}
	return out
}

// Status is a snapshot for the Exporters page.
type Status struct {
	Listeners []ListenerStatus `json:"listeners"`
	// Started is when the collector started; LastAccepted when records from an
	// approved Exporter last arrived (zero if none yet), and from which.
	Started          time.Time  `json:"started"`
	LastAccepted     *time.Time `json:"lastAccepted,omitempty"`
	LastAcceptedFrom string     `json:"lastAcceptedFrom,omitempty"`
	// DroppedOverExporterLimit counts datagrams from new sources ignored because the
	// Exporter limit was reached.
	DroppedOverExporterLimit uint64 `json:"droppedOverExporterLimit"`
	MaxExporters             int    `json:"maxExporters"`
	// DroppedOverPendingLimit counts datagrams from new sources ignored because the
	// Pending Exporter limit was reached.
	DroppedOverPendingLimit uint64 `json:"droppedOverPendingLimit"`
	MaxPending              int    `json:"maxPending"`
	// DroppedNewExporterRate counts datagrams from new sources ignored by the rate limit.
	DroppedNewExporterRate uint64           `json:"droppedNewExporterRate"`
	Exporters              []ExporterStatus `json:"exporters"`
}

// Status returns the current state of every Exporter, ordered by address.
func (c *Collector) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Now()
	st := Status{
		Listeners:                make([]ListenerStatus, len(c.listeners)),
		Started:                  c.started,
		DroppedOverExporterLimit: c.overLimit,
		MaxExporters:             c.opts.MaxExporters,
		DroppedOverPendingLimit:  c.overPending,
		MaxPending:               c.opts.MaxPending,
		DroppedNewExporterRate:   c.rateLimited,
		Exporters:                make([]ExporterStatus, 0, len(c.exporters)),
	}
	for i, l := range c.listeners {
		st.Listeners[i] = l.status()
	}
	if !c.lastAccepted.IsZero() {
		t := c.lastAccepted
		st.LastAccepted, st.LastAcceptedFrom = &t, c.lastAcceptedFrom.String()
	}
	for _, exp := range c.exporters {
		st.Exporters = append(st.Exporters, exp.status(now))
	}
	sort.Slice(st.Exporters, func(i, j int) bool { return st.Exporters[i].Address < st.Exporters[j].Address })
	return st
}
