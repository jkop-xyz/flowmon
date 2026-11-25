package httpserver

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/metrics"
	"github.com/jkop-xyz/flowmon/internal/snmp"
)

// InterfaceStore reads Interface Counters. *flowstore.Store implements it.
type InterfaceStore interface {
	SNMPInterfaces(ctx context.Context, source string) ([]flowstore.SNMPInterface, error)
	InterfacesNow(ctx context.Context, source string, now time.Time) (map[uint32]flowstore.InterfaceNow, error)
	InterfaceSeries(ctx context.Context, source string, ifIndex uint32, from, to time.Time) ([]flowstore.InterfacePoint, error)
	LatestHealth(ctx context.Context, source string) (map[string]flowstore.HealthNow, error)
	HealthSeries(ctx context.Context, source string, from, to time.Time) ([]flowstore.HealthPoint, error)
	Restarts(ctx context.Context, source string, from, to time.Time) ([]time.Time, error)
	WifiClients(ctx context.Context, source string) ([]flowstore.WifiClient, error)
}

type snmpInterfaceJSON struct {
	flowstore.SNMPInterface
	// Now is its latest minute and last hour; absent without recent counters.
	Now *flowstore.InterfaceNow `json:"now,omitempty"`
}

// snmpSource finds an SNMP Data Source by id.
func snmpSource(opts Options, id string) (datasource.Status, bool) {
	if opts.Sources == nil {
		return datasource.Status{}, false
	}
	for _, s := range opts.Sources.List() {
		if s.ID == id && s.Kind == "snmp" {
			return s, true
		}
	}
	return datasource.Status{}, false
}

func registerSNMPDevices(mux *http.ServeMux, opts Options) {
	is := opts.InterfaceCounters
	available := func(w http.ResponseWriter) bool {
		if is == nil {
			errorJSON(w, http.StatusServiceUnavailable, "flow store not available")
			return false
		}
		return true
	}
	// An SNMP device: its source and every interface with its current load.
	mux.HandleFunc("GET /api/snmp-devices/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		src, ok := snmpSource(opts, r.PathValue("id"))
		if !ok {
			errorJSON(w, http.StatusNotFound, "no such SNMP device")
			return
		}
		ifs, err := is.SNMPInterfaces(r.Context(), src.ID)
		if queryError(w, opts.Logger, "snmp interfaces", err) {
			return
		}
		now, err := is.InterfacesNow(r.Context(), src.ID, opts.Now())
		if queryError(w, opts.Logger, "snmp interfaces", err) {
			return
		}
		out := make([]snmpInterfaceJSON, 0, len(ifs))
		for _, i := range ifs {
			j := snmpInterfaceJSON{SNMPInterface: i}
			if n, ok := now[i.IfIndex]; ok {
				j.Now = &n
			}
			out = append(out, j)
		}
		health, err := is.LatestHealth(r.Context(), src.ID)
		if queryError(w, opts.Logger, "device health", err) {
			return
		}
		wifi, err := is.WifiClients(r.Context(), src.ID)
		if queryError(w, opts.Logger, "wifi clients", err) {
			return
		}
		res := map[string]any{"source": snmpSourceView{src.ID, src.Name, src.Enabled, src.Health}, "address": src.Settings["address"],
			"interfaces": out, "wifi": wifi}
		if h, ok := health[src.ID]; ok {
			res["health"] = h
		}
		if e, ok := snmp.ExporterOf(src.Settings); ok {
			res["exporter"] = e.String()
		}
		writeJSON(w, http.StatusOK, res)
	})
	// One interface's counters over a range: bits per second, errors and discards.
	mux.HandleFunc("GET /api/snmp-devices/{id}/interfaces/{index}", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		src, ok := snmpSource(opts, r.PathValue("id"))
		if !ok {
			errorJSON(w, http.StatusNotFound, "no such SNMP device")
			return
		}
		idx, err := strconv.ParseUint(r.PathValue("index"), 10, 32)
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "index must be an interface number")
			return
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		pts, err := is.InterfaceSeries(r.Context(), src.ID, uint32(idx), bq.From, bq.To)
		if queryError(w, opts.Logger, "interface series", err) {
			return
		}
		restarts, err := is.Restarts(r.Context(), src.ID, bq.From, bq.To)
		if queryError(w, opts.Logger, "restarts", err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"points": pts, "stepSeconds": seriesStep(bq.From, bq.To), "restarts": restarts})
	})
	// The device's health over a range: each reading's mean per bucket, and its restarts.
	mux.HandleFunc("GET /api/snmp-devices/{id}/health", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		src, ok := snmpSource(opts, r.PathValue("id"))
		if !ok {
			errorJSON(w, http.StatusNotFound, "no such SNMP device")
			return
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return
		}
		pts, err := is.HealthSeries(r.Context(), src.ID, bq.From, bq.To)
		if queryError(w, opts.Logger, "health series", err) {
			return
		}
		restarts, err := is.Restarts(r.Context(), src.ID, bq.From, bq.To)
		if queryError(w, opts.Logger, "restarts", err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"points": pts, "stepSeconds": seriesStep(bq.From, bq.To), "restarts": restarts})
	})
}

// seriesStep is the bucket Interface Counters and Device Health are read in: minutes
// under a day, hours from a day.
func seriesStep(from, to time.Time) int {
	if to.Sub(from) >= 24*time.Hour {
		return 3600
	}
	return 60
}

// sensorMetrics name each kind of sensor reading on /metrics.
var sensorMetrics = []struct{ kind, name, help string }{
	{"temperature", "flowmon_device_temperature_celsius", "An SNMP device's temperature sensor, in °C."},
	{"voltage", "flowmon_device_voltage_volts", "An SNMP device's voltage sensor, in volts."},
	{"current", "flowmon_device_current_amperes", "An SNMP device's current sensor, in amperes."},
	{"power", "flowmon_device_power_watts", "An SNMP device's power sensor, in watts."},
	{"fan", "flowmon_device_fan_rpm", "An SNMP device's fan speed, in revolutions per minute."},
}

// writeHealthMetrics writes each SNMP device's latest Device Health, when it is recent.
func writeHealthMetrics(ctx context.Context, m *metrics.Writer, opts Options) {
	if opts.InterfaceCounters == nil || opts.Sources == nil {
		return
	}
	latest, err := opts.InterfaceCounters.LatestHealth(ctx, "")
	if err != nil {
		return
	}
	type device struct {
		name string
		h    flowstore.HealthNow
	}
	var devices []device
	for _, src := range opts.Sources.List() {
		if h, ok := latest[src.ID]; ok && src.Kind == "snmp" && src.Enabled && opts.Now().Sub(h.At) < 5*time.Minute {
			devices = append(devices, device{src.Name, h})
		}
	}
	for _, g := range []struct{ metric, name, help string }{
		{"uptime", "flowmon_device_uptime_seconds", "How long an SNMP device has been running."},
		{"cpu", "flowmon_device_cpu_percent", "An SNMP device's CPU load, averaged over its processors."},
		{"memory_used", "flowmon_device_memory_used_bytes", "An SNMP device's memory in use."},
		{"memory_total", "flowmon_device_memory_total_bytes", "An SNMP device's memory."},
		{"wifi_clients", "flowmon_device_wifi_clients", "Stations registered to an SNMP device's Wi-Fi interfaces."},
	} {
		for _, d := range devices {
			if v, ok := d.h.Metrics[g.metric]; ok {
				m.Gauge(g.name, g.help, v, "source", d.name)
			}
		}
	}
	for _, s := range sensorMetrics {
		for _, d := range devices {
			names := make([]string, 0)
			for k := range d.h.Metrics {
				if sensor, ok := strings.CutPrefix(k, s.kind+":"); ok {
					names = append(names, sensor)
				}
			}
			sort.Strings(names)
			for _, n := range names {
				m.Gauge(s.name, s.help, d.h.Metrics[s.kind+":"+n], "source", d.name, "sensor", n)
			}
		}
	}
}

// writeInterfaceMetrics writes each SNMP device's interfaces: status, speed, and the
// latest minute's rates.
func writeInterfaceMetrics(ctx context.Context, m *metrics.Writer, opts Options) {
	if opts.InterfaceCounters == nil || opts.Sources == nil {
		return
	}
	type row struct {
		labels []string
		i      flowstore.SNMPInterface
		now    *flowstore.InterfaceNow
	}
	var rows []row
	for _, src := range opts.Sources.List() {
		if src.Kind != "snmp" || !src.Enabled {
			continue
		}
		ifs, err := opts.InterfaceCounters.SNMPInterfaces(ctx, src.ID)
		if err != nil {
			continue
		}
		now, _ := opts.InterfaceCounters.InterfacesNow(ctx, src.ID, opts.Now())
		for _, i := range ifs {
			r := row{labels: []string{"source", src.Name, "interface", i.Name}, i: i}
			if n, ok := now[i.IfIndex]; ok && opts.Now().Sub(n.At) < 5*time.Minute {
				r.now = &n
			}
			rows = append(rows, r)
		}
	}
	for _, r := range rows {
		m.Gauge("flowmon_interface_up", "1 when an SNMP device's interface is up.", boolFloat(r.i.Up), r.labels...)
	}
	for _, r := range rows {
		m.Gauge("flowmon_interface_speed_bits", "An SNMP device's interface speed in bits per second (0 when unknown).", float64(r.i.Speed), r.labels...)
	}
	for _, r := range rows {
		if r.now != nil {
			m.Gauge("flowmon_interface_in_bits_per_second", "Traffic into an SNMP device's interface over its latest minute.", r.now.InBps, r.labels...)
		}
	}
	for _, r := range rows {
		if r.now != nil {
			m.Gauge("flowmon_interface_out_bits_per_second", "Traffic out of an SNMP device's interface over its latest minute.", r.now.OutBps, r.labels...)
		}
	}
	for _, r := range rows {
		if r.now != nil {
			m.Gauge("flowmon_interface_errors_last_hour", "Errors on an SNMP device's interface over the last hour, both directions.", float64(r.now.HourErrors), r.labels...)
		}
	}
}
