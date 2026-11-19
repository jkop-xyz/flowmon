package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/ingest"
)

type fakeCollector struct{ st collector.Status }

func (f fakeCollector) Status() collector.Status { return f.st }

type fakePipeline struct{}

func (fakePipeline) Stats() ingest.Stats {
	return ingest.Stats{Written: 1200, Dropped: 3, Late: 1, Queued: 5, QueueCapacity: 50000}
}

type fakeDisk struct{ over bool }

func (fakeDisk) Disk(context.Context) (flowstore.Disk, error) {
	return flowstore.Disk{Used: 3072, File: 4096}, nil
}
func (fakeDisk) Memory(context.Context) (flowstore.Memory, error) {
	return flowstore.Memory{Used: 1 << 20, Limit: "953.6 MiB", Threads: 4}, nil
}
func (f fakeDisk) CapStatus() flowstore.CapStatus { return flowstore.CapStatus{Over: f.over} }
func (fakeDisk) RawDays(time.Time) []time.Time {
	return []time.Time{time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)}
}
func (fakeDisk) Snapshot(context.Context) (*flowstore.Snapshot, error) {
	return nil, errors.New("no snapshots")
}

func collectorWith(started time.Time, last *time.Time) fakeCollector {
	return fakeCollector{collector.Status{
		Started: started, LastAccepted: last, LastAcceptedFrom: "192.168.88.1",
		Listeners: []collector.ListenerStatus{{Address: "0.0.0.0:4739/udp", GrantedBuffer: 212992, KernelDrops: 17, DropsCounted: true, Datagrams: 900}},
		Exporters: []collector.ExporterStatus{{Address: "192.168.88.1", Approval: "approved", Seen: true, Datagrams: 900, Records: 27000,
			Deviations: []collector.DeviationStatus{{Kind: "unknown_set", Count: 2}}}},
	}}
}

func TestMetrics(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	last := now.Add(-time.Minute)
	opts := Options{UI: builtUI, Version: "1.2.3", Collector: collectorWith(now.Add(-time.Hour), &last), Now: func() time.Time { return now },
		NoFlowsWarning: 5 * time.Minute, Metrics: &MetricsOptions{Pipeline: fakePipeline{}},
		FlowStore: &FlowStoreOptions{Store: fakeDisk{over: true}, MaxBytes: 2048}}
	rec := get(t, New(opts), "/metrics")
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain; version=0.0.4") {
		t.Fatalf("%d %s", rec.Code, rec.Header().Get("Content-Type"))
	}
	for _, want := range []string{
		`flowmon_build_info{version="1.2.3"} 1`,
		`flowmon_collector_socket_drops_total{listener="0.0.0.0:4739/udp"} 17`,
		`flowmon_collector_socket_receive_buffer_bytes{listener="0.0.0.0:4739/udp"} 212992`,
		`flowmon_exporter_records_total{exporter="192.168.88.1",approval="approved"} 27000`,
		`flowmon_exporter_deviations_total{exporter="192.168.88.1",kind="unknown_set"} 2`,
		`flowmon_ingest_flows_dropped_total{reason="queue_full"} 3`,
		`flowmon_ingest_queue_length 5`,
		`flowmon_flowstore_disk_bytes 4096`,
		`flowmon_flowstore_used_bytes 3072`,
		`flowmon_flowstore_max_bytes 2048`,
		`flowmon_flowstore_over_cap 1`,
		`flowmon_flowstore_raw_days 1`,
		`flowmon_collection_quiet 0`,
		"# TYPE flowmon_ingest_flows_written_total counter",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
	// /metrics doesn't need a session, but takes a token when one is set.
	opts.Metrics.Token = "s3cret"
	h := New(opts)
	if rec := get(t, h, "/metrics"); rec.Code != http.StatusUnauthorized {
		t.Errorf("without the token: %d", rec.Code)
	}
	r := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	r.Header.Set("Authorization", "Bearer s3cret")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK {
		t.Errorf("with the token: %d", rec.Code)
	}
}

func TestCollectionQuiet(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		started time.Time
		last    *time.Time
		warn    time.Duration
		quiet   bool
	}{
		"recent flows":              {now.Add(-time.Hour), ptr(now.Add(-time.Minute)), 5 * time.Minute, false},
		"stopped":                   {now.Add(-time.Hour), ptr(now.Add(-10 * time.Minute)), 5 * time.Minute, true},
		"none since a recent start": {now.Add(-2 * time.Minute), nil, 5 * time.Minute, false},
		"none since start":          {now.Add(-20 * time.Minute), nil, 5 * time.Minute, true},
		"warning off":               {now.Add(-time.Hour), ptr(now.Add(-time.Hour)), 0, false},
	} {
		h := New(Options{UI: builtUI, Collector: collectorWith(c.started, c.last), NoFlowsWarning: c.warn, Now: func() time.Time { return now }})
		rec := get(t, h, "/api/status")
		if got := strings.Contains(rec.Body.String(), `"quiet":true`); got != c.quiet || !strings.Contains(rec.Body.String(), `"approvedExporters":1`) {
			t.Errorf("%s: %s", name, rec.Body.String())
		}
	}
}

func ptr[T any](v T) *T { return &v }
