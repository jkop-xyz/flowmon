package httpserver

import (
	"context"
	"crypto/subtle"
	"net/http"
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/ingest"
	"github.com/jkop-xyz/flowmon/internal/metrics"
)

// MetricsOptions wires the Prometheus /metrics endpoint.
type MetricsOptions struct {
	// Token, when set, must be sent as "Authorization: Bearer <token>".
	Token    string
	Pipeline interface{ Stats() ingest.Stats }
	// Sources lists the Data Sources with their health.
	Sources interface{ List() []datasource.Status }
}

// collectorStatus is the Exporters page's data: the collector, its Exporters with their
// approvals, and the write pipeline's counters.
func collectorStatus(opts Options) (collector.Status, *ingest.Stats) {
	st := collector.Status{Listeners: []collector.ListenerStatus{}, Exporters: []collector.ExporterStatus{}}
	if opts.Collector != nil {
		st = opts.Collector.Status()
	}
	mergeExporterStatus(&st, opts.Exporters)
	if opts.Timing != nil {
		for i := range st.Exporters {
			if addr, err := netip.ParseAddr(st.Exporters[i].Address); err == nil {
				st.Exporters[i].Timing = opts.Timing.Timing(addr)
				st.Exporters[i].Sampling = opts.Timing.Sampling(addr)
				st.Exporters[i].Counting = opts.Timing.Counting(addr)
			}
		}
	}
	var ps *ingest.Stats
	if opts.Metrics != nil && opts.Metrics.Pipeline != nil {
		s := opts.Metrics.Pipeline.Stats()
		ps = &s
	}
	return st, ps
}

// collectionJSON says whether Flows are arriving, for the "no Flows" banner.
type collectionJSON struct {
	ApprovedExporters int        `json:"approvedExporters"`
	LastFlow          *time.Time `json:"lastFlow,omitempty"`
	LastFlowFrom      string     `json:"lastFlowFrom,omitempty"`
	// Quiet is set when approved Exporters exist but no Flows arrived for WarnAfter
	// (counting from start-up when none arrived since).
	Quiet            bool    `json:"quiet"`
	WarnAfterSeconds float64 `json:"warnAfterSeconds"`
}

func collectionHealth(opts Options) collectionJSON {
	st, _ := collectorStatus(opts)
	out := collectionJSON{LastFlow: st.LastAccepted, LastFlowFrom: st.LastAcceptedFrom, WarnAfterSeconds: opts.NoFlowsWarning.Seconds()}
	for _, e := range st.Exporters {
		if e.Approval == "approved" {
			out.ApprovedExporters++
		}
	}
	if opts.NoFlowsWarning <= 0 || out.ApprovedExporters == 0 || opts.Collector == nil {
		return out
	}
	since := st.Started
	if st.LastAccepted != nil {
		since = *st.LastAccepted
	}
	out.Quiet = opts.Now().Sub(since) > opts.NoFlowsWarning
	return out
}

func registerMetrics(public *http.ServeMux, opts Options) {
	public.HandleFunc("GET /metrics", func(w http.ResponseWriter, r *http.Request) {
		mo := opts.Metrics
		if mo == nil {
			http.NotFound(w, r)
			return
		}
		if mo.Token != "" {
			got := r.Header.Get("Authorization")
			if subtle.ConstantTimeCompare([]byte(got), []byte("Bearer "+mo.Token)) != 1 {
				w.Header().Set("WWW-Authenticate", `Bearer realm="flowmon metrics"`)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
		}
		w.Header().Set("Content-Type", metrics.ContentType)
		w.Header().Set("Cache-Control", "no-store")
		writeMetrics(r.Context(), metrics.NewWriter(w), opts)
	})
}

func boolFloat(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func unixSeconds(t time.Time) float64 { return float64(t.UnixNano()) / 1e9 }

func writeMetrics(ctx context.Context, m *metrics.Writer, opts Options) {
	m.Gauge("flowmon_build_info", "Flowmon's version.", 1, "version", opts.Version)
	st, ps := collectorStatus(opts)

	for _, l := range st.Listeners {
		m.Counter("flowmon_collector_socket_datagrams_total", "Datagrams read from a collector socket.", float64(l.Datagrams), "listener", l.Address)
	}
	for _, l := range st.Listeners {
		if l.DropsCounted {
			m.Counter("flowmon_collector_socket_drops_total", "Datagrams the kernel dropped because the socket's receive buffer was full.", float64(l.KernelDrops), "listener", l.Address)
		}
	}
	for _, l := range st.Listeners {
		m.Gauge("flowmon_collector_socket_receive_buffer_bytes", "Receive buffer the kernel granted a collector socket (0 when unknown).", float64(l.GrantedBuffer), "listener", l.Address)
	}
	for _, d := range []struct {
		reason string
		n      uint64
	}{{"exporter_limit", st.DroppedOverExporterLimit}, {"pending_limit", st.DroppedOverPendingLimit}, {"new_exporter_rate", st.DroppedNewExporterRate}} {
		m.Counter("flowmon_collector_dropped_datagrams_total", "Datagrams from new sources the collector ignored, by reason.", float64(d.n), "reason", d.reason)
	}
	if st.LastAccepted != nil {
		m.Gauge("flowmon_collector_last_flow_timestamp_seconds", "When records from an approved Exporter last arrived.", unixSeconds(*st.LastAccepted))
	}

	exp := func(name, help string, kind string, value func(e collector.ExporterStatus) float64) {
		for _, e := range st.Exporters {
			if !e.Seen {
				continue
			}
			if kind == "counter" {
				m.Counter(name, help, value(e), "exporter", e.Address, "approval", e.Approval)
			} else {
				m.Gauge(name, help, value(e), "exporter", e.Address, "approval", e.Approval)
			}
		}
	}
	exp("flowmon_exporter_datagrams_total", "Datagrams received from an Exporter.", "counter", func(e collector.ExporterStatus) float64 { return float64(e.Datagrams) })
	exp("flowmon_exporter_received_bytes_total", "Bytes of datagrams received from an Exporter.", "counter", func(e collector.ExporterStatus) float64 { return float64(e.Bytes) })
	exp("flowmon_exporter_messages_total", "IPFIX messages decoded from an Exporter.", "counter", func(e collector.ExporterStatus) float64 { return float64(e.Messages) })
	exp("flowmon_exporter_records_total", "Data records decoded from an Exporter.", "counter", func(e collector.ExporterStatus) float64 { return float64(e.Records) })
	exp("flowmon_exporter_pending_dropped_records_total", "Records dropped because the Exporter awaits approval.", "counter", func(e collector.ExporterStatus) float64 { return float64(e.PendingDroppedRecords) })
	exp("flowmon_exporter_ignored_datagrams_total", "Datagrams dropped because the Exporter is ignored.", "counter", func(e collector.ExporterStatus) float64 { return float64(e.IgnoredDatagrams) })
	exp("flowmon_exporter_templates", "Templates an Exporter has sent.", "gauge", func(e collector.ExporterStatus) float64 { return float64(len(e.Templates)) })
	exp("flowmon_exporter_last_seen_timestamp_seconds", "When an Exporter last sent anything.", "gauge", func(e collector.ExporterStatus) float64 { return unixSeconds(e.LastSeen) })
	for _, e := range st.Exporters {
		for _, d := range e.Deviations {
			m.Counter("flowmon_exporter_deviations_total", "Ways an Exporter's data deviated from the standard, by kind.", float64(d.Count), "exporter", e.Address, "kind", d.Kind)
		}
	}
	for _, e := range st.Exporters {
		if e.Timing != nil {
			m.Gauge("flowmon_exporter_watermark_seconds", "How long after a time bucket ends an Exporter's Flows may still arrive.", e.Timing.WatermarkSeconds, "exporter", e.Address)
		}
	}
	for _, e := range st.Exporters {
		if e.Counting != nil {
			m.Counter("flowmon_exporter_flows_uncounted_total", "An Internal Exporter's Flows left to an Edge Exporter to count.", float64(e.Counting.Uncounted), "exporter", e.Address)
		}
	}
	for _, e := range st.Exporters {
		if e.Counting != nil {
			m.Counter("flowmon_exporter_duplicates_removed_total", "Flows an Exporter observing multiple points reported again.", float64(e.Counting.DuplicatesRemoved), "exporter", e.Address)
		}
	}
	for _, e := range st.Exporters {
		if e.Sampling != nil {
			m.Gauge("flowmon_exporter_sampling_rate", "The rate the newest Flow's counts were scaled up by: N for 1 in N packets, 0 for not sampled.", float64(e.Sampling.LastRate), "exporter", e.Address)
		}
	}
	for _, e := range st.Exporters {
		if e.Timing != nil {
			m.Gauge("flowmon_exporter_clock_offset_seconds", "Median of received time minus export time: positive when the Exporter's clock is behind.", e.Timing.ClockOffsetSeconds, "exporter", e.Address)
		}
	}

	if ps != nil {
		m.Counter("flowmon_ingest_flows_written_total", "Flows written to the Flow Store.", float64(ps.Written))
		for _, d := range []struct {
			reason string
			n      uint64
		}{{"queue_full", ps.Dropped}, {"late", ps.Late}, {"no_addresses", ps.NoAddresses}} {
			m.Counter("flowmon_ingest_flows_dropped_total", "Flows not stored, by reason.", float64(d.n), "reason", d.reason)
		}
		m.Counter("flowmon_ingest_options_records_total", "Options Template records (metadata, not Flows).", float64(ps.Options))
		m.Counter("flowmon_ingest_nat_events_total", "NAT session event records (not traffic, not Flows).", float64(ps.NATEvents))
		m.Counter("flowmon_ingest_write_errors_total", "Failed batch writes to the Flow Store.", float64(ps.WriteErrors))
		m.Gauge("flowmon_ingest_queue_length", "Flows waiting to be written.", float64(ps.Queued))
		m.Gauge("flowmon_ingest_queue_capacity", "Flows the write queue holds before dropping.", float64(ps.QueueCapacity))
		if ps.LastWrite != nil {
			m.Gauge("flowmon_ingest_last_write_timestamp_seconds", "When Flows were last written to the Flow Store.", unixSeconds(*ps.LastWrite))
		}
	}
	if fo := opts.FlowStore; fo != nil && fo.Store != nil {
		if d, err := fo.Store.Disk(ctx); err == nil {
			m.Gauge("flowmon_flowstore_disk_bytes", "Size of the Flow Store's files.", float64(d.File))
			m.Gauge("flowmon_flowstore_used_bytes", "Space the Flow Store's data takes; space of dropped data is reused before the files grow.", float64(d.Used))
		}
		if fo.MaxBytes > 0 {
			m.Gauge("flowmon_flowstore_max_bytes", "The Flow Store's size cap (FLOWMON_FLOW_STORE_MAX_GB).", float64(fo.MaxBytes))
			m.Gauge("flowmon_flowstore_over_cap", "1 when the Flow Store is over its cap with nothing left the cap may drop.", boolFloat(fo.Store.CapStatus().Over))
		}
		if mem, err := fo.Store.Memory(ctx); err == nil {
			m.Gauge("flowmon_flowstore_memory_bytes", "Memory DuckDB uses for the Flow Store.", float64(mem.Used))
		}
		days := fo.Store.RawDays(time.Time{})
		m.Gauge("flowmon_flowstore_raw_days", "Days of raw Flows kept.", float64(len(days)))
		if len(days) > 0 {
			m.Gauge("flowmon_flowstore_oldest_raw_timestamp_seconds", "Start of the oldest day of raw Flows kept.", unixSeconds(days[0]))
		}
	}
	if opts.AppStore != nil {
		m.Gauge("flowmon_appstore_configured", "Whether the App Store has been set up.", boolFloat(opts.AppStore.Get() != nil))
		m.Gauge("flowmon_appstore_up", "Whether the App Store answers.", boolFloat(opts.AppStore.Health().Available))
	}
	if opts.Metrics != nil && opts.Metrics.Sources != nil {
		writeSourceMetrics(m, opts.Metrics.Sources.List())
	}
	writeInterfaceMetrics(ctx, m, opts)
	q := collectionHealth(opts)
	m.Gauge("flowmon_collection_quiet", "1 when approved Exporters exist but no Flows arrived for FLOWMON_NO_FLOWS_WARNING.", boolFloat(q.Quiet))
}

func writeSourceMetrics(m *metrics.Writer, sources []datasource.Status) {
	labels := func(s datasource.Status) []string { return []string{"source", s.ID, "kind", s.Kind, "name", s.Name} }
	for _, s := range sources {
		m.Gauge("flowmon_source_enabled", "1 when a Data Source is enabled.", boolFloat(s.Enabled), labels(s)...)
	}
	for _, s := range sources {
		if s.Enabled {
			m.Gauge("flowmon_source_up", "1 when an enabled Data Source's last attempt worked.", boolFloat(s.Health.State == datasource.StateOK), labels(s)...)
		}
	}
	for _, s := range sources {
		if s.Health.LastSuccess != nil {
			m.Gauge("flowmon_source_last_success_timestamp_seconds", "When a Data Source last collected successfully.", unixSeconds(*s.Health.LastSuccess), labels(s)...)
		}
	}
	for _, s := range sources {
		if s.Enabled {
			m.Gauge("flowmon_source_warnings", "Warnings a Data Source has, such as a misconfigured network.", float64(len(s.Health.Warnings)), labels(s)...)
		}
	}
}
