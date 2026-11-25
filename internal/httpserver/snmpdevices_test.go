package httpserver

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type fakeSourceList []datasource.Status

func (f fakeSourceList) Kinds() []datasource.KindInfo { return nil }
func (f fakeSourceList) List() []datasource.Status    { return f }
func (f fakeSourceList) Create(context.Context, datasource.Source) (datasource.Status, error) {
	return datasource.Status{}, nil
}
func (f fakeSourceList) Update(context.Context, string, datasource.Source) (datasource.Status, error) {
	return datasource.Status{}, nil
}
func (f fakeSourceList) Delete(context.Context, string) error { return nil }
func (f fakeSourceList) Test(context.Context, string, datasource.Source) (string, error) {
	return "", nil
}

type fakeInterfaces struct{ now time.Time }

func (f fakeInterfaces) SNMPInterfaces(_ context.Context, source string) ([]flowstore.SNMPInterface, error) {
	return []flowstore.SNMPInterface{
		{Source: source, IfIndex: 1, Name: "ether1", Alias: "WAN", Speed: 1_000_000_000, Up: true},
		{Source: source, IfIndex: 2, Name: "ether2", Up: false},
	}, nil
}

func (f fakeInterfaces) InterfacesNow(context.Context, string, time.Time) (map[uint32]flowstore.InterfaceNow, error) {
	return map[uint32]flowstore.InterfaceNow{1: {IfIndex: 1, At: f.now.Add(-time.Minute), InBps: 250_000_000, OutBps: 20_000_000, HourErrors: 3}}, nil
}

func (f fakeInterfaces) InterfaceSeries(_ context.Context, _ string, idx uint32, _, _ time.Time) ([]flowstore.InterfacePoint, error) {
	return []flowstore.InterfacePoint{{T: f.now, InBps: float64(idx)}}, nil
}

func (f fakeInterfaces) LatestHealth(context.Context, string) (map[string]flowstore.HealthNow, error) {
	return map[string]flowstore.HealthNow{"sw1": {At: f.now.Add(-time.Minute), Metrics: map[string]float64{
		"cpu": 12.5, "uptime": 86400, "temperature:cpu-temperature": 51, "temperature:board-temperature1": 40, "voltage:voltage": 24.1}}}, nil
}

func (f fakeInterfaces) HealthSeries(context.Context, string, time.Time, time.Time) ([]flowstore.HealthPoint, error) {
	return []flowstore.HealthPoint{{T: f.now, Metrics: map[string]float64{"cpu": 7}}}, nil
}

func (f fakeInterfaces) Restarts(context.Context, string, time.Time, time.Time) ([]time.Time, error) {
	return []time.Time{f.now.Add(-time.Hour / 2)}, nil
}

func (f fakeInterfaces) WifiClients(context.Context, string) ([]flowstore.WifiClient, error) {
	return []flowstore.WifiClient{{MAC: "00:11:22:33:44:55", IfIndex: 5, Signal: -58}}, nil
}

func TestSNMPDeviceEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	sources := fakeSourceList{{Source: datasource.Source{ID: "sw1", Kind: "snmp", Name: "Router", Enabled: true,
		Settings: datasource.Settings{"address": "192.168.10.1"}}}}
	h := New(Options{UI: builtUI, Now: func() time.Time { return now }, Sources: sources, InterfaceCounters: fakeInterfaces{now},
		Metrics: &MetricsOptions{}})
	rec := get(t, h, "/api/snmp-devices/sw1")
	for _, want := range []string{`"name":"Router"`, `"exporter":"192.168.10.1"`, `"alias":"WAN"`, `"inBps":250000000`, `"hourErrors":3`,
		`"cpu":12.5`, `"signal":-58`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("device lacks %s: %s", want, rec.Body)
		}
	}
	if strings.Count(rec.Body.String(), `"now":`) != 1 {
		t.Errorf("only ether1 has recent counters: %s", rec.Body)
	}
	if rec := get(t, h, "/api/snmp-devices/sw1/interfaces/2?preset=1h"); !strings.Contains(rec.Body.String(), `"inBps":2`) || !strings.Contains(rec.Body.String(), `"stepSeconds":60`) {
		t.Errorf("series: %s", rec.Body)
	}
	if rec := get(t, h, "/api/snmp-devices/sw1/health?from=2026-11-18T12:00:00Z&to=2026-11-25T12:00:00Z"); !strings.Contains(rec.Body.String(), `"cpu":7`) ||
		!strings.Contains(rec.Body.String(), `"stepSeconds":3600`) || !strings.Contains(rec.Body.String(), `"restarts":["2026-11-25T11:30:00Z"]`) {
		t.Errorf("health: %s", rec.Body)
	}
	for _, path := range []string{"/api/snmp-devices/nope", "/api/snmp-devices/nope/interfaces/1?preset=1h", "/api/snmp-devices/nope/health?preset=1h"} {
		if rec := get(t, h, path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
	body := get(t, h, "/metrics").Body.String()
	for _, want := range []string{
		`flowmon_interface_up{source="Router",interface="ether1"} 1`,
		`flowmon_interface_up{source="Router",interface="ether2"} 0`,
		`flowmon_interface_speed_bits{source="Router",interface="ether1"} 1000000000`,
		`flowmon_interface_in_bits_per_second{source="Router",interface="ether1"} 250000000`,
		`flowmon_interface_errors_last_hour{source="Router",interface="ether1"} 3`,
		`flowmon_device_cpu_percent{source="Router"} 12.5`,
		`flowmon_device_uptime_seconds{source="Router"} 86400`,
		`flowmon_device_temperature_celsius{source="Router",sensor="board-temperature1"} 40`,
		`flowmon_device_temperature_celsius{source="Router",sensor="cpu-temperature"} 51`,
		`flowmon_device_voltage_volts{source="Router",sensor="voltage"} 24.1`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}
