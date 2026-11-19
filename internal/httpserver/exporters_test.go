package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/exporters"
)

func TestExporterApprovalEndpoints(t *testing.T) {
	reg, err := exporters.Load(context.Background(), exporters.Options{})
	if err != nil {
		t.Fatal(err)
	}
	coll := collector.New(collector.Options{Gate: collector.GateFunc(func(a netip.Addr) collector.Admission {
		if reg.Status(a) == exporters.StatusApproved {
			return collector.AdmitAccept
		}
		return collector.AdmitPending
	})})
	coll.HandleDatagram(netip.MustParseAddrPort("192.168.10.1:5000"), netip.MustParseAddrPort("0.0.0.0:4739"), []byte{0, 10, 0, 16, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0})
	c := &client{t: t, h: New(Options{UI: builtUI, Collector: coll, Exporters: reg})}

	status := func() collector.ExporterStatus {
		var st collector.Status
		_ = json.Unmarshal(c.do("GET", "/api/exporters", "").Body.Bytes(), &st)
		return st.Exporters[0]
	}
	if e := status(); e.Approval != "pending" {
		t.Fatalf("%+v", e)
	}
	if w := c.do("POST", "/api/exporters/192.168.10.1/approve", `{"role":"internal"}`); w.Code != http.StatusBadRequest {
		t.Errorf("internal role: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/exporters/not-an-ip/approve", `{}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad address: %d", w.Code)
	}
	if w := c.do("POST", "/api/exporters/192.168.10.1/approve", `{"name":"MikroTik","role":"edge","classificationMode":"address"}`); w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/exporters/options", ""); !strings.Contains(w.Body.String(), `"name":"MikroTik"`) {
		t.Errorf("options %s", w.Body.String())
	}
	// The registry decision is what the API shows; the collector picks it up on the next datagram.
	if e := status(); e.Name != "MikroTik" || e.Role != "edge" {
		t.Errorf("%+v", e)
	}

	// An approved Exporter that hasn't been seen still shows up.
	_, _ = reg.Approve(context.Background(), netip.MustParseAddr("10.0.0.9"), exporters.Counting{}, "", "Proxmox", "admin")
	var st collector.Status
	_ = json.Unmarshal(c.do("GET", "/api/exporters", "").Body.Bytes(), &st)
	if len(st.Exporters) != 2 || st.Exporters[1].Seen || st.Exporters[1].Name != "Proxmox" {
		t.Errorf("unseen: %+v", st.Exporters)
	}

	if w := c.do("POST", "/api/exporters/192.168.10.1/ignore", ""); w.Code != http.StatusOK {
		t.Errorf("ignore: %d", w.Code)
	}
	if w := c.do("DELETE", "/api/exporters/192.168.10.1", ""); w.Code != http.StatusOK {
		t.Errorf("forget: %d", w.Code)
	}
	if s := reg.Status(netip.MustParseAddr("192.168.10.1")); s != exporters.StatusPending {
		t.Errorf("after forget: %s", s)
	}
}
