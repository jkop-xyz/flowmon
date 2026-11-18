package ingest

import (
	"context"
	"net/netip"
	"sync"
	"testing"
	"time"

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
	p := New(Options{Writer: w, FlushInterval: 10 * time.Millisecond, Now: func() time.Time { return now.Add(2 * time.Second) }})
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
