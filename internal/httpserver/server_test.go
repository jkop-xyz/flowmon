package httpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func newTestHandler(ui fstest.MapFS) http.Handler {
	return New(Options{Version: "1.2.3", UI: ui})
}

var builtUI = fstest.MapFS{
	"index.html":        {Data: []byte("<html>app</html>")},
	"favicon.svg":       {Data: []byte("<svg/>")},
	"assets/index-a.js": {Data: []byte("console.log(1)")},
}

func get(t *testing.T, h http.Handler, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestHealth(t *testing.T) {
	rec := get(t, newTestHandler(builtUI), "/api/health")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["version"] != "1.2.3" {
		t.Errorf("body %v", body)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	rec := get(t, newTestHandler(builtUI), "/api/nope")
	if rec.Code != http.StatusNotFound || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Errorf("status %d, content type %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestClientRoutesServeIndex(t *testing.T) {
	h := newTestHandler(builtUI)
	for _, target := range []string{"/", "/exporters", "/hosts/10.0.0.1", "/remote/2001:db8::1", "/index.html"} {
		rec := get(t, h, target)
		if rec.Code != http.StatusOK || rec.Body.String() != "<html>app</html>" {
			t.Errorf("%s: status %d, body %q", target, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("%s: cache-control %q", target, rec.Header().Get("Cache-Control"))
		}
	}
}

func TestStaticFiles(t *testing.T) {
	h := newTestHandler(builtUI)

	rec := get(t, h, "/assets/index-a.js")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
		t.Errorf("asset: status %d, cache-control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}

	rec = get(t, h, "/favicon.svg")
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("favicon: status %d, cache-control %q", rec.Code, rec.Header().Get("Cache-Control"))
	}

	for _, target := range []string{"/missing.png", "/assets/gone.js"} {
		if rec := get(t, h, target); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d", target, rec.Code)
		}
	}
}

func TestUnbuiltUI(t *testing.T) {
	rec := get(t, newTestHandler(fstest.MapFS{".gitkeep": {}}), "/")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "hasn't been built") {
		t.Errorf("status %d, body %q", rec.Code, rec.Body.String())
	}
}

func TestRejectsWritesToUI(t *testing.T) {
	rec := httptest.NewRecorder()
	newTestHandler(builtUI).ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/", nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("status %d", rec.Code)
	}
}
