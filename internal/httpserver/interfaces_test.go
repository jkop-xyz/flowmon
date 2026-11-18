package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/netip"
	"path/filepath"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/ingest"
	"github.com/jkop-xyz/flowmon/internal/interfaces"
)

type fakeStats struct{}

func (fakeStats) Interfaces(netip.Addr) ([]ingest.InterfaceStats, uint32) {
	return []ingest.InterfaceStats{{Index: 1, FlowsIn: 10, RemoteIn: 10}, {Index: 2, FlowsOut: 10}}, 1
}

func TestInterfacesEndpoints(t *testing.T) {
	ctx := context.Background()
	store, err := flowstore.Open(ctx, filepath.Join(t.TempDir(), "f.duckdb"), flowstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg, _ := exporters.Load(ctx, exporters.Options{Persister: store})
	ifs, _ := interfaces.Load(ctx, store)
	router := netip.MustParseAddr("192.168.10.1")
	_, _ = reg.Approve(ctx, router, "", exporters.ByAddress, "MikroTik", "admin")
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	c := &client{t: t, h: New(Options{UI: builtUI, Exporters: reg, Now: func() time.Time { return now },
		Interfaces: &InterfaceOptions{Registry: ifs, Stats: fakeStats{}, Changes: store, RawRetention: 7 * 24 * time.Hour}})}

	var res InterfacesResponse
	_ = json.Unmarshal(c.do("GET", "/api/exporters/192.168.10.1/interfaces", "").Body.Bytes(), &res)
	if !res.HasInterfaceNumbers || len(res.Interfaces) != 2 || !res.Interfaces[0].SuggestedWAN || res.ClassificationMode != exporters.ByAddress {
		t.Fatalf("%+v", res)
	}

	for idx, name := range map[string]string{"1": "ether1-WAN", "2": "bridge"} {
		if w := c.do("PUT", "/api/exporters/192.168.10.1/interfaces/"+idx, `{"name":"`+name+`"}`); w.Code != http.StatusOK {
			t.Fatalf("name %s: %d %s", idx, w.Code, w.Body.String())
		}
	}
	if w := c.do("PUT", "/api/exporters/192.168.10.1/interfaces/0", `{"name":"x"}`); w.Code != http.StatusBadRequest {
		t.Errorf("index 0: %d", w.Code)
	}
	w := c.do("PUT", "/api/exporters/192.168.10.1/classification", `{"classificationMode":"interface","roles":{"ether1-WAN":"wan","bridge":"lan"}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("classification: %d %s", w.Code, w.Body.String())
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	if res.ClassificationMode != exporters.ByInterface || res.Interfaces[0].Role != interfaces.RoleWAN || res.Interfaces[0].Name != "ether1-WAN" {
		t.Errorf("%+v", res)
	}
	if !reg.ByInterface(router) {
		t.Error("registry not updated")
	}
	if w := c.do("PUT", "/api/exporters/192.168.10.1/classification", `{"classificationMode":"interface","roles":{"x":"dmz"}}`); w.Code != http.StatusBadRequest {
		t.Errorf("bad role: %d", w.Code)
	}
	changes, _ := store.ClassificationChanges(ctx, now.Add(-time.Hour), now.Add(time.Hour), router)
	if len(changes) != 1 { // only the classification change is a chart marker
		t.Errorf("changes %+v", changes)
	}
}
