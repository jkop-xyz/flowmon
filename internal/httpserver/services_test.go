package httpserver

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/services"
)

func TestServiceNameEndpoints(t *testing.T) {
	store, err := appstore.Open(context.Background(), appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h := New(Options{UI: builtUI, Services: services.NewDirectory(func() services.Store { return store })})
	c := &client{t: t, h: h}

	if rec := c.do("PUT", "/api/service-names/6/8006", `{"name":"proxmox"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"proxmox"`) {
		t.Fatalf("set: %d %s", rec.Code, rec.Body.String())
	}
	if rec := c.do("PUT", "/api/service-names/0/1883", `{"name":"mqtt"}`); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ianaName":"mqtt"`) {
		t.Fatalf("set any protocol: %d %s", rec.Code, rec.Body.String())
	}
	for path, body := range map[string]string{"/api/service-names/6/0": `{"name":"x"}`, "/api/service-names/tcp/80": `{"name":"x"}`, "/api/service-names/6/80": `{"name":""}`} {
		if rec := c.do("PUT", path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d", path, body, rec.Code)
		}
	}
	rec := get(t, h, "/api/service-names")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"port":1883`) || strings.Index(rec.Body.String(), "1883") > strings.Index(rec.Body.String(), "8006") {
		t.Errorf("list: %d %s", rec.Code, rec.Body.String())
	}
	rec = get(t, h, "/api/service-names/lookup?port=6/8006&port=17/1883&port=6/443&port=6/51820")
	for _, want := range []string{`"6/8006":{"name":"proxmox","source":"user"}`, `"17/1883":{"name":"mqtt","source":"user"}`, `"6/443":{"name":"https","description":"http protocol over TLS/SSL","source":"iana"}`, `"6/51820":{}`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("lookup lacks %s: %s", want, rec.Body.String())
		}
	}
	if rec := get(t, h, "/api/service-names/lookup?port=x"); rec.Code != http.StatusBadRequest {
		t.Errorf("bad port: %d", rec.Code)
	}
	if rec := c.do("DELETE", "/api/service-names/6/8006", ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := c.do("DELETE", "/api/service-names/6/8006", ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete twice: %d", rec.Code)
	}
	if rec := get(t, New(Options{UI: builtUI}), "/api/service-names"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("without services: %d", rec.Code)
	}
}
