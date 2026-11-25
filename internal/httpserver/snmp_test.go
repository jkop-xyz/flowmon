package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type fakeReconStore struct{ source string }

func (f *fakeReconStore) ReconciliationHours(_ context.Context, source string, _ netip.Addr, _ string, _, _ time.Time) ([]flowstore.SNMPHour, error) {
	f.source = source
	return nil, nil
}

func (f *fakeReconStore) FlowBytesByInterface(context.Context, netip.Addr, uint32, time.Time, time.Time) (map[time.Time]flowstore.InterfaceHour, error) {
	return map[time.Time]flowstore.InterfaceHour{}, nil
}

func TestSNMPEndpoints(t *testing.T) {
	router := netip.MustParseAddr("192.168.10.1")
	src := datasource.Status{Source: datasource.Source{ID: "sw1", Kind: "snmp", Name: "Router", Enabled: true,
		Settings: datasource.Settings{"address": "192.168.10.1", "overhead": "pppoe"}}, Health: datasource.Health{State: datasource.StateOK}}
	store := &fakeReconStore{}
	h := New(Options{UI: builtUI, SNMP: &SNMPOptions{
		SourceFor: func(a netip.Addr) (datasource.Status, bool) { return src, a == router },
		Store:     store,
		WAN:       func(netip.Addr) map[string]uint32 { return map[string]uint32{"ether1": 1} },
	}})
	rec := get(t, h, "/api/exporters/192.168.10.1/snmp")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"id":"sw1"`) || !strings.Contains(rec.Body.String(), `"state":"ok"`) {
		t.Errorf("the router's source: %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/exporters/192.168.10.2/snmp"); !strings.Contains(rec.Body.String(), `"source":null`) {
		t.Errorf("an Exporter without a source: %s", rec.Body)
	}
	rec = get(t, h, "/api/exporters/192.168.10.1/reconciliation")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"overheadBytes":22`) || !strings.Contains(rec.Body.String(), `"enabled":true`) || store.source != "sw1" {
		t.Errorf("reconciliation: %d %s, read source %q", rec.Code, rec.Body, store.source)
	}
	if rec := get(t, h, "/api/exporters/192.168.10.2/reconciliation"); !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Errorf("reconciliation without a source: %s", rec.Body)
	}
	if rec := get(t, h, "/api/exporters/nope/snmp"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad address: %d", rec.Code)
	}
}
