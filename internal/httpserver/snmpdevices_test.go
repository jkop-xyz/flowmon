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

func TestSNMPDeviceEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	sources := fakeSourceList{{Source: datasource.Source{ID: "sw1", Kind: "snmp", Name: "Router", Enabled: true,
		Settings: datasource.Settings{"address": "192.168.10.1"}}}}
	h := New(Options{UI: builtUI, Now: func() time.Time { return now }, Sources: sources, InterfaceCounters: fakeInterfaces{now},
		Metrics: &MetricsOptions{}})
	rec := get(t, h, "/api/snmp-devices/sw1")
	for _, want := range []string{`"name":"Router"`, `"exporter":"192.168.10.1"`, `"alias":"WAN"`, `"inBps":250000000`, `"hourErrors":3`} {
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
	for _, path := range []string{"/api/snmp-devices/nope", "/api/snmp-devices/nope/interfaces/1?preset=1h"} {
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
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics lack %s", want)
		}
	}
}
