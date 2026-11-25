package httpserver

import (
	"context"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type fakeUsage struct{ from, to time.Time }

func (f *fakeUsage) UsageNow(_ context.Context, from, to time.Time, _ int) (flowstore.NowResult, error) {
	f.from, f.to = from, to
	return flowstore.NowResult{Devices: []flowstore.NowDevice{{MAC: "3c:5a:b4:00:00:44", Host: "192.168.10.44", InBps: 2e6}}, Domains: []flowstore.NowDomain{}}, nil
}

func TestOverviewEndpoints(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	sources := fakeSourceList{
		{Source: datasource.Source{ID: "sw1", Kind: "snmp", Name: "Router", Enabled: true, Settings: datasource.Settings{"address": "192.168.10.1"}},
			Health: datasource.Health{State: datasource.StateOK}},
		{Source: datasource.Source{ID: "ag", Kind: "adguard", Name: "AdGuard", Enabled: true}},
	}
	usage := &fakeUsage{}
	h := New(Options{UI: builtUI, Now: func() time.Time { return now }, Sources: sources, InterfaceCounters: fakeInterfaces{now}, Usage: usage,
		SNMP: &SNMPOptions{WAN: func(a netip.Addr) map[string]uint32 {
			if a == netip.MustParseAddr("192.168.10.1") {
				return map[string]uint32{"ether1": 1}
			}
			return nil
		}}})
	body := get(t, h, "/api/overview/network?from=2026-11-25T11:00:00Z&to=2026-11-25T12:00:00Z").Body.String()
	for _, want := range []string{
		`"interfaces":[{"source":"sw1","sourceName":"Router","ifIndex":1,"name":"ether1","speed":1000000000}]`,
		`"now":{"t":"2026-11-25T11:59:00Z","inBps":250000000,"outBps":20000000}`,
		`"line":{"downBps":1000000000,"upBps":1000000000,"fromSetting":false}`,
		`"restarts":["2026-11-25T11:30:00Z"]`,
		`"cpu":12.5`,
		`"problems":[{"source":"sw1","sourceName":"Router","ifIndex":1,"name":"ether1","alias":"WAN","down":false,"errors":3,"discards":0}]`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("network lacks %s: %s", want, body)
		}
	}
	if strings.Contains(body, `"name":"AdGuard"`) {
		t.Error("a DNS source listed as a device")
	}
	rec := get(t, h, "/api/overview/now")
	if !strings.Contains(rec.Body.String(), `"inBps":2000000`) || usage.to.Sub(usage.from) != NowWindow || !usage.to.Equal(now) {
		t.Errorf("now: %s, window %v–%v", rec.Body, usage.from, usage.to)
	}
	// Without SNMP, there is no WAN load and no devices: the Overview shows Flows.
	body = get(t, New(Options{UI: builtUI}), "/api/overview/network").Body.String()
	if !strings.Contains(body, `"wan":null`) || !strings.Contains(body, `"devices":[]`) {
		t.Errorf("without SNMP: %s", body)
	}
}
