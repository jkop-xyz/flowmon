package collector

import (
	"context"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

type sinkRecorder struct {
	mu      sync.Mutex
	records map[netip.Addr]int
}

func (s *sinkRecorder) Records(exp netip.Addr, recs []ipfix.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = map[netip.Addr]int{}
	}
	s.records[exp] += len(recs)
}

func (s *sinkRecorder) count(a netip.Addr) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.records[a]
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

var (
	router = netip.MustParseAddrPort("192.168.10.1:38000")
	local  = netip.MustParseAddrPort("0.0.0.0:4739")
)

func mikrotikMessage(seq uint32, withTemplate bool) []byte {
	tmpl := ipfixtest.MikroTikLikeTemplate(256)
	m := ipfixtest.NewMessage(0, seq, t0)
	if withTemplate {
		m.TemplateSet(tmpl)
	}
	rec := [][]byte{
		{192, 168, 10, 44}, {1, 1, 1, 1}, ipfixtest.U16(50000), ipfixtest.U16(443), ipfixtest.U8(6),
		ipfixtest.U32(3), ipfixtest.U32(1), ipfixtest.U32(1500), ipfixtest.U32(3),
		ipfixtest.U32(1000), ipfixtest.U32(2000), {203, 0, 113, 7}, {1, 1, 1, 1},
	}
	return m.DataSet(tmpl, rec).Bytes()
}

func TestExporterAppearsWithTemplatesAndFindings(t *testing.T) {
	sink := &sinkRecorder{}
	clk := &clock{now: t0}
	c := New(Options{Sink: sink, Now: clk.Now})

	c.HandleDatagram(router, local, mikrotikMessage(0, true))

	if got := sink.count(router.Addr()); got != 1 {
		t.Fatalf("sink got %d records", got)
	}
	st := c.Status()
	if len(st.Exporters) != 1 {
		t.Fatalf("exporters %+v", st.Exporters)
	}
	exp := st.Exporters[0]
	if exp.Address != "192.168.10.1" || exp.Datagrams != 1 || exp.Messages != 1 || exp.Records != 1 {
		t.Errorf("status %+v", exp)
	}
	if len(exp.Templates) != 1 || exp.Templates[0].ID != 256 || len(exp.Templates[0].Fields) != 13 {
		t.Fatalf("templates %+v", exp.Templates)
	}
	f := exp.Templates[0].Fields[0]
	if f.Name != "sourceIPv4Address" || f.Type != "ipv4Address" || f.Length != 4 {
		t.Errorf("field %+v", f)
	}
	codes := map[string]bool{}
	for _, fd := range exp.Templates[0].Findings {
		codes[fd.Code] = true
	}
	// The MikroTik-like Template sends a 4-byte octet counter and uptime-relative times.
	if !codes["short_byte_counter"] || !codes["uptime_relative_times"] || len(codes) != 2 {
		t.Errorf("findings %+v", exp.Templates[0].Findings)
	}
}

func TestDataBeforeTemplateIsNotLost(t *testing.T) {
	sink := &sinkRecorder{}
	c := New(Options{Sink: sink, Now: (&clock{now: t0}).Now})
	c.HandleDatagram(router, local, mikrotikMessage(0, false))
	c.HandleDatagram(router, local, mikrotikMessage(1, true))
	if got := sink.count(router.Addr()); got != 2 {
		t.Errorf("sink got %d records, want both", got)
	}
}

func TestNoneDecodedFindingAfterGrace(t *testing.T) {
	clk := &clock{now: t0}
	c := New(Options{Now: clk.Now})
	c.HandleDatagram(router, local, mikrotikMessage(0, false)) // data, Template never arrives

	if fs := c.Status().Exporters[0].Findings; len(fs) != 0 {
		t.Errorf("no finding expected during the grace period: %+v", fs)
	}
	clk.now = t0.Add(2 * time.Minute)
	c.HandleDatagram(router, local, mikrotikMessage(1, false))
	fs := c.Status().Exporters[0].Findings
	if len(fs) != 1 || fs[0].Code != "none_decoded" {
		t.Errorf("findings %+v", fs)
	}
}

func TestNetFlowV5IsCountedNotDecoded(t *testing.T) {
	c := New(Options{Now: (&clock{now: t0}).Now})
	v5 := []byte{0, 5, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	c.HandleDatagram(router, local, v5)
	exp := c.Status().Exporters[0]
	if len(exp.Deviations) != 1 || exp.Deviations[0].Kind != string(DevNetFlowV5Unsupported) {
		t.Errorf("deviations %+v", exp.Deviations)
	}
	if len(exp.Findings) != 1 || exp.Findings[0].Code != "not_ipfix" {
		t.Errorf("findings %+v", exp.Findings)
	}
}

func TestExporterLimit(t *testing.T) {
	c := New(Options{MaxExporters: 2, Now: (&clock{now: t0}).Now})
	for i := 1; i <= 5; i++ {
		src := netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), 4000)
		c.HandleDatagram(src, local, mikrotikMessage(0, true))
	}
	st := c.Status()
	if len(st.Exporters) != 2 || st.DroppedOverExporterLimit != 3 {
		t.Errorf("exporters %d, dropped %d", len(st.Exporters), st.DroppedOverExporterLimit)
	}
}

func TestSessionsAreScopedBySourcePort(t *testing.T) {
	sink := &sinkRecorder{}
	c := New(Options{Sink: sink, Now: (&clock{now: t0}).Now})
	c.HandleDatagram(router, local, mikrotikMessage(0, true))

	// Same router, new source port (e.g. after a reboot): a new transport session that
	// hasn't received the Template yet.
	restarted := netip.AddrPortFrom(router.Addr(), 38001)
	c.HandleDatagram(restarted, local, mikrotikMessage(0, false))
	if got := sink.count(router.Addr()); got != 1 {
		t.Errorf("the new session must not reuse the old session's Template: %d records", got)
	}
	if n := len(c.Status().Exporters); n != 1 {
		t.Errorf("still one Exporter, got %d", n)
	}
}

func TestIPv4MappedSourceIsUnmapped(t *testing.T) {
	c := New(Options{Now: (&clock{now: t0}).Now})
	mapped := netip.AddrPortFrom(netip.AddrFrom16(router.Addr().As16()), router.Port())
	c.HandleDatagram(mapped, local, mikrotikMessage(0, true))
	if got := c.Status().Exporters[0].Address; got != "192.168.10.1" {
		t.Errorf("address %q", got)
	}
}

func TestListenUDPEndToEnd(t *testing.T) {
	sink := &sinkRecorder{}
	c := New(Options{Sink: sink})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.ListenUDP(ctx, "127.0.0.1:0"); err != nil {
		t.Fatal(err)
	}
	addr := c.Listeners()[0].Address
	addr = addr[:len(addr)-len("/udp")]

	conn, err := net.Dial("udp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write(mikrotikMessage(0, true)); err != nil {
		t.Fatal(err)
	}
	loopback := netip.MustParseAddr("127.0.0.1")
	deadline := time.Now().Add(5 * time.Second)
	for sink.count(loopback) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("record never arrived over UDP")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

type gateMap map[netip.Addr]Admission

func (g gateMap) Admission(a netip.Addr) Admission {
	if ad, ok := g[a]; ok {
		return ad
	}
	return AdmitPending
}

func TestPendingExportersKeepTemplatesButDropData(t *testing.T) {
	sink := &sinkRecorder{}
	gate := gateMap{}
	c := New(Options{Sink: sink, Gate: gate, Now: (&clock{now: t0}).Now})
	c.HandleDatagram(router, local, mikrotikMessage(0, true))
	exp := c.Status().Exporters[0]
	if sink.count(router.Addr()) != 0 || exp.Approval != "pending" || exp.PendingDroppedRecords != 1 || len(exp.Templates) != 1 {
		t.Fatalf("pending: sink %d, %+v", sink.count(router.Addr()), exp)
	}
	// Approval applies to the next datagram, and the Template is already known.
	gate[router.Addr()] = AdmitAccept
	c.HandleDatagram(router, local, mikrotikMessage(1, false))
	if sink.count(router.Addr()) != 1 || c.Status().Exporters[0].Approval != "approved" {
		t.Errorf("after approval: sink %d", sink.count(router.Addr()))
	}
	gate[router.Addr()] = AdmitIgnore
	c.HandleDatagram(router, local, mikrotikMessage(2, false))
	exp = c.Status().Exporters[0]
	if sink.count(router.Addr()) != 1 || exp.IgnoredDatagrams != 1 || exp.Approval != "ignored" {
		t.Errorf("ignored: sink %d %+v", sink.count(router.Addr()), exp)
	}
}

func TestPendingLimitAndNewExporterRate(t *testing.T) {
	c := New(Options{Gate: gateMap{}, MaxPending: 2, NewExportersPerMinute: 100, Now: (&clock{now: t0}).Now})
	for i := 1; i <= 5; i++ {
		c.HandleDatagram(netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 0, byte(i)}), 4000), local, mikrotikMessage(0, true))
	}
	st := c.Status()
	if len(st.Exporters) != 2 || st.DroppedOverPendingLimit != 3 {
		t.Errorf("pending limit: %d exporters, %d dropped", len(st.Exporters), st.DroppedOverPendingLimit)
	}

	clk := &clock{now: t0}
	r := New(Options{NewExportersPerMinute: 3, Now: clk.Now})
	for i := 1; i <= 5; i++ {
		r.HandleDatagram(netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 1, byte(i)}), 4000), local, mikrotikMessage(0, true))
	}
	if st := r.Status(); len(st.Exporters) != 3 || st.DroppedNewExporterRate != 2 {
		t.Errorf("rate limit: %d exporters, %d dropped", len(st.Exporters), st.DroppedNewExporterRate)
	}
	clk.now = t0.Add(time.Minute)
	r.HandleDatagram(netip.AddrPortFrom(netip.AddrFrom4([4]byte{10, 0, 1, 9}), 4000), local, mikrotikMessage(0, true))
	if n := len(r.Status().Exporters); n != 4 {
		t.Errorf("the bucket refills: %d exporters", n)
	}
}
