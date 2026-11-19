package ingest

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
	"github.com/jkop-xyz/flowmon/internal/ipfix/ipfixtest"
)

type memWriter struct {
	mu    sync.Mutex
	flows []flow.Flow
}

func (w *memWriter) Write(_ context.Context, fs []flow.Flow) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.flows = append(w.flows, fs...)
	return nil
}

func (w *memWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.flows)
}

var (
	exporter = netip.MustParseAddr("192.168.10.1")
	now      = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
)

func outboundRecord(exportTime time.Time) ipfix.Record {
	spec := func(id, length uint16) ipfix.FieldSpec { return ipfix.FieldSpec{ID: id, Length: length} }
	return ipfix.Record{ExportTime: exportTime, Fields: []ipfix.Field{
		{Spec: spec(8, 4), Raw: []byte{192, 168, 10, 44}},
		{Spec: spec(12, 4), Raw: []byte{1, 1, 1, 1}},
		{Spec: spec(7, 2), Raw: ipfixtest.U16(50000)},
		{Spec: spec(11, 2), Raw: ipfixtest.U16(443)},
		{Spec: spec(4, 1), Raw: ipfixtest.U8(6)},
		{Spec: spec(1, 4), Raw: ipfixtest.U32(1500)},
		{Spec: spec(152, 8), Raw: ipfixtest.U64(uint64(exportTime.Add(-5 * time.Second).UnixMilli()))},
		{Spec: spec(153, 8), Raw: ipfixtest.U64(uint64(exportTime.Add(-time.Second).UnixMilli()))},
	}}
}

func TestPipelineNormalisesClassifiesAndWrites(t *testing.T) {
	w := &memWriter{}
	enrich := func(f *flow.Flow) { f.PeerCountry = "classified as " + f.Direction.String() }
	p := New(Options{Writer: w, FlushInterval: 10 * time.Millisecond, Enrich: enrich, Now: func() time.Time { return now.Add(2 * time.Second) }})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()

	p.Records(exporter, []ipfix.Record{
		outboundRecord(now),
		{ScopeFieldCount: 1, ExportTime: now, Fields: []ipfix.Field{{Spec: ipfix.FieldSpec{ID: 144, Length: 4}, Raw: ipfixtest.U32(1)}}},
		{ExportTime: now, Fields: []ipfix.Field{{Spec: ipfix.FieldSpec{ID: 1, Length: 4}, Raw: ipfixtest.U32(1)}}},
	})

	deadline := time.Now().Add(5 * time.Second)
	for w.count() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("flow never written")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done

	f := w.flows[0]
	if f.Direction != flow.DirectionOutbound || f.Local != netip.MustParseAddr("192.168.10.44") || f.Bytes != 1500 {
		t.Errorf("flow %+v", f)
	}
	// Enrichment runs after classification.
	if f.PeerCountry != "classified as outbound" {
		t.Errorf("enrichment saw %q", f.PeerCountry)
	}
	st := p.Stats()
	if st.Written != 1 || st.Options != 1 || st.NoAddresses != 1 {
		t.Errorf("stats %+v", st)
	}

	tm := p.Timing(exporter)
	if tm == nil || tm.ClockOffsetSeconds != 2 || tm.ExportLagSeconds != 1 || len(tm.Findings) != 0 {
		t.Errorf("timing %+v", tm)
	}
}

func TestShutdownFlushesQueuedFlows(t *testing.T) {
	w := &memWriter{}
	p := New(Options{Writer: w, FlushInterval: time.Hour, Now: func() time.Time { return now }})
	for i := 0; i < 10; i++ {
		p.Records(exporter, []ipfix.Record{outboundRecord(now)})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	if w.count() != 10 {
		t.Errorf("wrote %d of 10 queued Flows on shutdown", w.count())
	}
}

func TestQueueFullDropsAndCounts(t *testing.T) {
	p := New(Options{Writer: &memWriter{}, QueueSize: 2, Now: func() time.Time { return now }})
	for i := 0; i < 5; i++ {
		p.Records(exporter, []ipfix.Record{outboundRecord(now)})
	}
	if p.Stats().Dropped != 3 {
		t.Errorf("dropped %d", p.Stats().Dropped)
	}
}

func TestClockAheadFinding(t *testing.T) {
	p := New(Options{Writer: &memWriter{}, Now: func() time.Time { return now }})
	// The exporter's clock says it's 30 s later than Flowmon thinks.
	p.Records(exporter, []ipfix.Record{outboundRecord(now.Add(30 * time.Second))})
	tm := p.Timing(exporter)
	if tm == nil || len(tm.Findings) != 1 || tm.Findings[0].Code != "clock_ahead" {
		t.Errorf("timing %+v", tm)
	}
}

func TestWatermarkFollowsFlowDurations(t *testing.T) {
	p := New(Options{Writer: &memWriter{}, Now: func() time.Time { return now }})
	if w := p.Watermark(exporter); w != WatermarkMargin {
		t.Errorf("no Flows yet: %v", w)
	}
	// outboundRecord Flows last 4 s.
	for i := 0; i < 100; i++ {
		p.Records(exporter, []ipfix.Record{outboundRecord(now)})
	}
	if w := p.Watermark(exporter); w != 4*time.Second+WatermarkMargin {
		t.Errorf("watermark %v", w)
	}
	if w := p.MaxWatermark(); w != 4*time.Second+WatermarkMargin {
		t.Errorf("max watermark %v", w)
	}
}

func TestWatermarkIsCappedAndOverridable(t *testing.T) {
	p := New(Options{Writer: &memWriter{}, Now: func() time.Time { return now }})
	rec := outboundRecord(now)
	rec.Fields[6].Raw = ipfixtest.U64(uint64(now.Add(-3 * time.Hour).UnixMilli())) // a 3-hour Flow
	p.Records(exporter, []ipfix.Record{rec})
	if w := p.Watermark(exporter); w != WatermarkCap+WatermarkMargin {
		t.Errorf("capped watermark %v", w)
	}
	o := New(Options{Writer: &memWriter{}, WatermarkOverride: 10 * time.Minute, Now: func() time.Time { return now }})
	if w := o.Watermark(exporter); w != 10*time.Minute {
		t.Errorf("override %v", w)
	}
}

func TestLateFlowsAreDroppedAndCounted(t *testing.T) {
	w := &memWriter{}
	p := New(Options{Writer: w, Now: func() time.Time { return now.Add(3 * time.Hour) }})
	p.Records(exporter, []ipfix.Record{outboundRecord(now)}) // ended 3 h ago: past Watermark + 2 h
	if p.Stats().Late != 1 || p.Stats().Dropped != 0 {
		t.Errorf("stats %+v", p.Stats())
	}
	tm := p.Timing(exporter)
	if tm != nil {
		t.Errorf("late Flows don't produce timing samples: %+v", tm)
	}
}

func TestInterfaceCountsAndWANSuggestion(t *testing.T) {
	p := New(Options{Writer: &memWriter{}, Now: func() time.Time { return now }})
	withIfs := func(rec ipfix.Record, in, out uint32) ipfix.Record {
		rec.Fields = append(rec.Fields,
			ipfix.Field{Spec: ipfix.FieldSpec{ID: 10, Length: 4}, Raw: ipfixtest.U32(in)},
			ipfix.Field{Spec: ipfix.FieldSpec{ID: 14, Length: 4}, Raw: ipfixtest.U32(out)})
		return rec
	}
	reply := outboundRecord(now)
	reply.Fields[0].Raw, reply.Fields[1].Raw = []byte{1, 1, 1, 1}, []byte{192, 168, 10, 44} // remote → LAN
	for i := 0; i < 10; i++ {
		p.Records(exporter, []ipfix.Record{withIfs(reply, 1, 2)})
	}
	for i := 0; i < 4; i++ {
		p.Records(exporter, []ipfix.Record{withIfs(outboundRecord(now), 2, 1)})
	}
	stats, wan := p.Interfaces(exporter)
	if wan != 1 || len(stats) != 2 || stats[0].RemoteIn != 10 || stats[0].FlowsIn != 10 || stats[0].FlowsOut != 4 {
		t.Errorf("wan %d, stats %+v", wan, stats)
	}
}

func TestInterfaceNamesFromOptionsRecords(t *testing.T) {
	type got struct {
		idx  uint32
		name string
	}
	var names []got
	p := New(Options{Writer: &memWriter{}, Now: func() time.Time { return now },
		OnInterfaceName: func(_ netip.Addr, idx uint32, name string, _ time.Time) { names = append(names, got{idx, name}) }})
	p.Records(exporter, []ipfix.Record{{ScopeFieldCount: 1, ExportTime: now, Fields: []ipfix.Field{
		{Spec: ipfix.FieldSpec{ID: 10, Length: 4}, Raw: ipfixtest.U32(3)},
		{Spec: ipfix.FieldSpec{ID: 82, Length: ipfix.VariableLength}, Raw: []byte("ether1-WAN")},
	}}})
	if len(names) != 1 || names[0] != (got{3, "ether1-WAN"}) {
		t.Errorf("names %+v", names)
	}
}

func TestSampledFlowsAreScaledUp(t *testing.T) {
	w := &memWriter{}
	manual := map[netip.Addr]uint32{exporter: 50}
	p := New(Options{Writer: w, SamplingRate: func(a netip.Addr) uint32 { return manual[a] }, Now: func() time.Time { return now }})

	announced := outboundRecord(now)
	announced.Fields = append(announced.Fields, ipfix.Field{Spec: ipfix.FieldSpec{ID: 34, Length: 4}, Raw: ipfixtest.U32(100)})
	other := netip.MustParseAddr("192.168.20.1")
	p.Records(exporter, []ipfix.Record{announced, outboundRecord(now)})
	p.Records(other, []ipfix.Record{outboundRecord(now)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx) // writes what is queued

	if w.count() != 3 {
		t.Fatalf("%d Flows", w.count())
	}
	want := []struct {
		rate  uint32
		bytes uint64
	}{{100, 150_000}, {50, 75_000}, {0, 1500}} // announced, set by hand, neither
	for i, f := range w.flows {
		if f.SamplingRate != want[i].rate || f.Bytes != want[i].bytes {
			t.Errorf("Flow %d: rate %d bytes %d, want %+v", i, f.SamplingRate, f.Bytes, want[i])
		}
	}
	st := p.Sampling(exporter)
	if st == nil || st.Flows != 2 || st.SampledFlows != 2 || st.AnnouncedRate != 0 || st.LastRate != 50 {
		t.Errorf("status %+v", st)
	}
	if st := p.Sampling(other); st == nil || st.SampledFlows != 0 {
		t.Errorf("unsampled Exporter %+v", st)
	}
}

func TestBiflowRecordsBecomeTwoFlows(t *testing.T) {
	w := &memWriter{}
	p := New(Options{Writer: w, Now: func() time.Time { return now }})
	rec := outboundRecord(now)
	rec.Fields = append(rec.Fields,
		ipfix.Field{Spec: ipfix.FieldSpec{Enterprise: ipfix.EnterpriseReverse, ID: 1, Length: 4}, Raw: ipfixtest.U32(90_000)})
	p.Records(exporter, []ipfix.Record{rec})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	if w.count() != 2 {
		t.Fatalf("%d Flows, want the forward and the reverse direction", w.count())
	}
	fw, rv := w.flows[0], w.flows[1]
	if fw.Direction != flow.DirectionOutbound || fw.Bytes != 1500 || rv.Direction != flow.DirectionInbound || rv.Bytes != 90_000 ||
		rv.Local != fw.Local || rv.Peer != fw.Peer {
		t.Errorf("forward %v %d %v, reverse %v %d %v", fw.Direction, fw.Bytes, fw.Local, rv.Direction, rv.Bytes, rv.Local)
	}
}

func TestNATEventsAreCountedAndTeachTheWANAddress(t *testing.T) {
	w := &memWriter{}
	c := classify.New(classify.DefaultLocalNetworks())
	p := New(Options{Writer: w, Classifier: c, Now: func() time.Time { return now }})
	spec := func(id, length uint16) ipfix.FieldSpec { return ipfix.FieldSpec{ID: id, Length: length} }
	event := ipfix.Record{ExportTime: now, Fields: []ipfix.Field{
		{Spec: spec(4, 1), Raw: ipfixtest.U8(17)},
		{Spec: spec(8, 4), Raw: []byte{192, 168, 10, 44}},
		{Spec: spec(12, 4), Raw: []byte{9, 9, 9, 9}},
		{Spec: spec(225, 4), Raw: []byte{203, 0, 113, 7}},
		{Spec: spec(226, 4), Raw: []byte{9, 9, 9, 9}},
		{Spec: spec(230, 1), Raw: ipfixtest.U8(4)},
	}}
	p.Records(exporter, []ipfix.Record{event, outboundRecord(now)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Run(ctx)
	if w.count() != 1 || p.Stats().NATEvents != 1 {
		t.Errorf("%d Flows written, %d NAT events; want the one Flow and one event", w.count(), p.Stats().NATEvents)
	}
	if hosts := c.ExporterHosts(exporter, now); len(hosts) != 1 || hosts[0] != netip.MustParseAddr("203.0.113.7") {
		t.Errorf("learned %v", hosts)
	}
	if st := p.Timing(exporter); st != nil && st.EstimatedTimeFlows != 0 {
		t.Errorf("NAT events counted as Flows with estimated times: %+v", st)
	}
}
