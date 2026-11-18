package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/snmp"
)

type savedSNMP struct{ last snmp.Config }

func (s *savedSNMP) Save(_ context.Context, _ netip.Addr, c snmp.Config) error {
	s.last = c
	return nil
}

type testAgent struct{}

func (testAgent) InterfaceNames(context.Context) (map[uint32]string, error) {
	return map[uint32]string{1: "ether1", 2: "bridge"}, nil
}
func (testAgent) Counters(context.Context, []uint32) (map[uint32]snmp.Counters, error) {
	return map[uint32]snmp.Counters{}, nil
}
func (testAgent) Close() error { return nil }

type nopSink struct{}

func (nopSink) Name(netip.Addr, uint32, string, time.Time) {}
func (nopSink) WANInterfaces(netip.Addr) map[string]uint32 { return nil }
func (nopSink) AddSNMPDelta(context.Context, netip.Addr, string, flowstore.SNMPHour) error {
	return nil
}

func TestSNMPEndpoints(t *testing.T) {
	saved := &savedSNMP{}
	poller := snmp.NewPoller(func(snmp.Config, netip.Addr) (snmp.Agent, error) { return testAgent{}, nil }, nopSink{}, nil, nil, nil)
	c := &client{t: t, h: New(Options{UI: builtUI, SNMP: &SNMPOptions{Settings: saved, Poller: poller}})}

	if w := c.do("GET", "/api/exporters/192.168.10.1/snmp", ""); !strings.Contains(w.Body.String(), `"version":"v3"`) {
		t.Errorf("defaults %s", w.Body.String())
	}
	if w := c.do("PUT", "/api/exporters/192.168.10.1/snmp", `{"config":{"enabled":true,"version":"v3"}}`); w.Code != http.StatusBadRequest {
		t.Errorf("v3 without a user: %d", w.Code)
	}
	cfg := `{"config":{"enabled":true,"version":"v3","user":"flowmon","authProtocol":"SHA256","authPassword":"authpass123","privProtocol":"AES","privPassword":"privpass123","overhead":"ethernet"}}`
	w := c.do("PUT", "/api/exporters/192.168.10.1/snmp", cfg)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "authpass123") || !strings.Contains(w.Body.String(), `"hasAuthPassword":true`) {
		t.Fatalf("save: %d %s", w.Code, w.Body.String())
	}
	if saved.last.AuthPassword != "authpass123" {
		t.Error("the password reaches the settings store")
	}
	// Saving again without passwords keeps them.
	c.do("PUT", "/api/exporters/192.168.10.1/snmp", `{"config":{"enabled":true,"version":"v3","user":"flowmon","authProtocol":"SHA256","privProtocol":"AES","overhead":"pppoe"}}`)
	if saved.last.AuthPassword != "authpass123" || saved.last.Overhead != "pppoe" {
		t.Errorf("merge: %+v", saved.last)
	}
	if w := c.do("POST", "/api/exporters/192.168.10.1/snmp/test", cfg); !strings.Contains(w.Body.String(), `"interfaces":["bridge","ether1"]`) {
		t.Errorf("test: %s", w.Body.String())
	}
	if w := c.do("GET", "/api/exporters/192.168.10.1/reconciliation", ""); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"overheadBytes":22`) {
		t.Errorf("reconciliation: %d %s", w.Code, w.Body.String())
	}
}
