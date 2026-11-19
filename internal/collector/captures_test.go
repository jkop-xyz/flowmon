package collector

import (
	"errors"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/capture"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// Captures of real exporters live in testdata/captures as pcap files: tcpdump on the
// collector's port, or FLOWMON_DEBUG_CAPTURE. Each must decode completely into Flows
// with times the exporter actually gave (ADR 0002: real captures become test data).
// testdata/captures/README.md says where each came from.

type keepingSink struct {
	mu      sync.Mutex
	records map[netip.Addr][]ipfix.Record
}

func (s *keepingSink) Records(exp netip.Addr, recs []ipfix.Record) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[exp] = append(s.records[exp], recs...)
}

// expectedAtStart are deviations a capture can show only because it starts mid-stream:
// data before its Template, a sequence number that doesn't follow one never seen.
var expectedAtStart = map[string]bool{string(ipfix.DevUnknownTemplate): true, string(ipfix.DevSequenceGap): true}

func TestCaptures(t *testing.T) {
	files, _ := filepath.Glob("testdata/captures/*.pcap")
	if len(files) == 0 {
		t.Skip("no captures in testdata/captures")
	}
	for _, file := range files {
		t.Run(filepath.Base(file), func(t *testing.T) {
			f, err := os.Open(file)
			if err != nil {
				t.Fatal(err)
			}
			datagrams, err := capture.Read(f)
			f.Close()
			if err != nil || len(datagrams) == 0 {
				t.Fatalf("%d datagrams: %v", len(datagrams), err)
			}
			clk := &clock{now: datagrams[0].Time}
			sink := &keepingSink{records: map[netip.Addr][]ipfix.Record{}}
			c := New(Options{Now: clk.Now, Sink: sink})
			for _, d := range datagrams {
				clk.now = d.Time
				c.HandleDatagram(d.Src, d.Dst, d.Payload)
			}

			for _, exp := range c.Status().Exporters {
				for _, d := range exp.Deviations {
					if !expectedAtStart[d.Kind] || d.Count > 2 {
						t.Errorf("%s: %d × %s: %s", exp.Address, d.Count, d.Kind, d.LastDetail)
					}
				}
				for _, tmpl := range exp.Templates {
					for _, f := range tmpl.Findings {
						t.Logf("%s Template %d (%s): %s", exp.Address, tmpl.ID, tmpl.Protocol, f.Message)
					}
				}
				addr := netip.MustParseAddr(exp.Address)
				n := flow.NewNormalizer()
				flows := 0
				for _, rec := range sink.records[addr] {
					fl, err := n.Normalize(addr, rec, rec.ExportTime)
					if errors.Is(err, flow.ErrOptionsRecord) {
						continue
					}
					if err != nil {
						t.Errorf("record of Template %d: %v", rec.TemplateID, err)
						continue
					}
					flows++
					if r, ok := flow.Reverse(fl, rec); ok {
						// A biflow record's reverse direction is a Flow of its own.
						flows++
						if r.Bytes == 0 && r.Packets == 0 || r.Src != fl.Dst && r.Src != fl.PostNATDst {
							t.Errorf("reverse of %v → %v: %+v", fl.Src, fl.Dst, r)
						}
					}
					if fl.TimeEstimated || fl.End.After(rec.ExportTime.Add(time.Second)) || rec.ExportTime.Sub(fl.End) > 30*time.Minute {
						t.Errorf("Flow end %v for export time %v (estimated=%v)", fl.End, rec.ExportTime, fl.TimeEstimated)
					}
				}
				if flows == 0 {
					t.Errorf("%s: no Flows decoded from %d datagrams", exp.Address, exp.Datagrams)
				}
				t.Logf("%s: %d datagrams, %d Flows", exp.Address, exp.Datagrams, flows)
			}
		})
	}
}
