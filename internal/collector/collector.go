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

// Options configure a Collector.
type Options struct {
	Limits ipfix.Limits
	// MaxExporters caps how many Exporters are tracked, so spoofed sources can't exhaust memory.
	MaxExporters int
	// MaxSessionsPerExporter caps transport sessions (source ports) kept per Exporter.
	MaxSessionsPerExporter int
	Sink                   RecordSink
	Capture                *capture.Writer
	Logger                 *slog.Logger
	// Now returns the current time; tests replace it.
	Now func() time.Time
}

// Collector is safe for concurrent use.
type Collector struct {
	opts      Options
	mu        sync.Mutex
	exporters map[netip.Addr]*exporter
	listeners []string
	overLimit uint64 // datagrams dropped because MaxExporters was reached
	started   time.Time
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
	if opts.Sink == nil {
		opts.Sink = DiscardSink{}
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Collector{opts: opts, exporters: map[netip.Addr]*exporter{}, started: opts.Now()}
}

// ListenUDP receives datagrams on addr until ctx is done. It returns once the socket is
// bound (or binding failed); receiving continues in a goroutine.
func (c *Collector) ListenUDP(ctx context.Context, addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("listening on %s/udp: %w", addr, err)
	}
	udp := conn.(*net.UDPConn)
	_ = udp.SetReadBuffer(4 << 20) // best effort; the kernel caps it at net.core.rmem_max
	local := udp.LocalAddr().(*net.UDPAddr).AddrPort()

	c.mu.Lock()
	c.listeners = append(c.listeners, local.String()+"/udp")
	c.mu.Unlock()
	c.opts.Logger.Info("collector listening", "addr", local.String(), "transport", "udp")

	go func() {
		<-ctx.Done()
		_ = udp.Close()
	}()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, src, err := udp.ReadFromUDPAddrPort(buf)
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				c.opts.Logger.Warn("udp read", "addr", local.String(), "err", err)
				continue
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

	c.mu.Lock()
	exp, ok := c.exporterLocked(src.Addr(), now)
	if !ok {
		c.overLimit++
		c.mu.Unlock()
		return
	}
	exp.lastSeen = now
	exp.datagrams++
	exp.bytes += uint64(len(data))
	exp.lastSource = src

	var records []ipfix.Record
	switch ipfix.PeekVersion(data) {
	case ipfix.Version:
		records = c.decodeLocked(exp, src, local, data, now)
	case 9:
		exp.deviation(DevNetFlowV9Unsupported, "NetFlow v9 support comes in v1.1; send IPFIX instead", now)
	case 5:
		exp.deviation(DevNetFlowV5Unsupported, "NetFlow v5 support comes in v1.1; send IPFIX instead", now)
	default:
		exp.deviation(DevUnknownVersion, fmt.Sprintf("version %d", ipfix.PeekVersion(data)), now)
	}
	c.mu.Unlock()

	if len(records) > 0 {
		c.opts.Sink.Records(src.Addr(), records)
	}
}

func (c *Collector) exporterLocked(addr netip.Addr, now time.Time) (*exporter, bool) {
	if exp, ok := c.exporters[addr]; ok {
		return exp, true
	}
	if len(c.exporters) >= c.opts.MaxExporters {
		return nil, false
	}
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

// Listeners returns the addresses the collector receives on.
func (c *Collector) Listeners() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.listeners...)
}

// Status is a snapshot for the Exporters page.
type Status struct {
	Listeners []string `json:"listeners"`
	// DroppedOverExporterLimit counts datagrams from new sources ignored because the
	// Exporter limit was reached.
	DroppedOverExporterLimit uint64           `json:"droppedOverExporterLimit"`
	MaxExporters             int              `json:"maxExporters"`
	Exporters                []ExporterStatus `json:"exporters"`
}

// Status returns the current state of every Exporter, ordered by address.
func (c *Collector) Status() Status {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.opts.Now()
	st := Status{
		Listeners:                append([]string{}, c.listeners...),
		DroppedOverExporterLimit: c.overLimit,
		MaxExporters:             c.opts.MaxExporters,
		Exporters:                make([]ExporterStatus, 0, len(c.exporters)),
	}
	for _, exp := range c.exporters {
		st.Exporters = append(st.Exporters, exp.status(now))
	}
	sort.Slice(st.Exporters, func(i, j int) bool { return st.Exporters[i].Address < st.Exporters[j].Address })
	return st
}
