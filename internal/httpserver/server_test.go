package httpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/geoip"
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

func send(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	c := &client{t: t, h: h}
	return c.do(method, target, body)
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

func TestExportersWithoutCollector(t *testing.T) {
	rec := get(t, newTestHandler(builtUI), "/api/exporters")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"exporters":[]`) {
		t.Errorf("status %d, body %s", rec.Code, rec.Body.String())
	}
}

func TestExportersFromCollector(t *testing.T) {
	c := collector.New(collector.Options{})
	c.HandleDatagram(netip.MustParseAddrPort("192.168.10.1:5000"), netip.MustParseAddrPort("0.0.0.0:4739"), []byte{0, 9, 0, 0})
	h := New(Options{Version: "1", UI: builtUI, Collector: c})
	rec := get(t, h, "/api/exporters")
	var st collector.Status
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if len(st.Exporters) != 1 || st.Exporters[0].Address != "192.168.10.1" {
		t.Errorf("status %+v", st)
	}
}

type fakeFlows struct{ got flowstore.BandwidthQuery }

func (f *fakeFlows) Bandwidth(_ context.Context, q flowstore.BandwidthQuery) (flowstore.BandwidthResult, error) {
	f.got = q
	if q.To.Sub(q.From) > flowstore.MaxRange {
		return flowstore.BandwidthResult{}, flowstore.ErrInvalidRange
	}
	return flowstore.BandwidthResult{From: q.From, To: q.To, StepS: 10, Points: []flowstore.BandwidthPoint{{Time: q.From, InboundBps: 8}}}, nil
}

func (f *fakeFlows) Top(_ context.Context, q flowstore.TopQuery) (flowstore.TopResult, error) {
	if q.Dimension != flowstore.TopLocalHosts {
		return flowstore.TopResult{}, flowstore.ErrUnknownDimension
	}
	return flowstore.TopResult{Items: []flowstore.TopItem{{Host: "192.168.10.44", TotalBytes: 5}}, TotalBytes: 5}, nil
}

type fixedWatermark time.Duration

func (w fixedWatermark) MaxWatermark() time.Duration { return time.Duration(w) }

func TestTopEndpointAndIncomplete(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	h := New(Options{UI: builtUI, Flows: &fakeFlows{}, Watermark: fixedWatermark(5 * time.Minute), Now: func() time.Time { return now }})
	rec := get(t, h, "/api/overview/top?dimension=hosts")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"host":"192.168.10.44"`) ||
		!strings.Contains(rec.Body.String(), `"incompleteFrom":"2026-11-18T11:55:00Z"`) {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	if rec := get(t, h, "/api/overview/top?dimension=colours"); rec.Code != http.StatusBadRequest {
		t.Errorf("unknown dimension: %d", rec.Code)
	}
	for limit, want := range map[string]int{"250": http.StatusOK, "0": http.StatusBadRequest, "301": http.StatusBadRequest, "x": http.StatusBadRequest} {
		if rec := get(t, h, "/api/overview/top?dimension=hosts&limit="+limit); rec.Code != want {
			t.Errorf("limit %s: %d, want %d", limit, rec.Code, want)
		}
	}
}

type fakeGeoIP struct{}

func (fakeGeoIP) Status() geoip.Status {
	return geoip.Status{DownloadsEnabled: true, Databases: []geoip.DatabaseStatus{{Kind: geoip.KindCountry, Source: geoip.SourceDBIP}}}
}
func (fakeGeoIP) Update(_ context.Context, u geoip.Update) error {
	switch {
	case u.Download != nil:
		return fmt.Errorf("downloads are %w", geoip.ErrLocked)
	case u.MaxMindAccountID != nil && *u.MaxMindAccountID == "x":
		return fmt.Errorf("%w: bad account", geoip.ErrInvalid)
	}
	return nil
}
func (fakeGeoIP) RetryNow() {}

func TestGeoIPEndpoint(t *testing.T) {
	rec := get(t, New(Options{UI: builtUI, GeoIP: fakeGeoIP{}}), "/api/geoip")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"source":"dbip"`) {
		t.Errorf("%d %s", rec.Code, rec.Body.String())
	}
	h := New(Options{UI: builtUI, GeoIP: fakeGeoIP{}})
	for body, want := range map[string]int{
		`{"maxMindAccountId":"12345","maxMindLicenseKey":"k"}`: http.StatusOK,
		`{"maxMindAccountId":"x"}`:                             http.StatusBadRequest,
		`{"download":false}`:                                   http.StatusConflict,
		`{"licenseKey":"k"}`:                                   http.StatusBadRequest,
	} {
		if rec := send(t, h, http.MethodPut, "/api/geoip", body); rec.Code != want {
			t.Errorf("PUT %s: %d %s, want %d", body, rec.Code, rec.Body, want)
		}
	}
	if rec := send(t, h, http.MethodPost, "/api/geoip/refresh", ""); rec.Code != http.StatusAccepted {
		t.Errorf("refresh: %d", rec.Code)
	}
	rec = get(t, New(Options{UI: builtUI}), "/api/geoip")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"databases":[]`) {
		t.Errorf("without GeoIP: %d %s", rec.Code, rec.Body.String())
	}
}

func TestBandwidthEndpoint(t *testing.T) {
	now := time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)
	flows := &fakeFlows{}
	h := New(Options{UI: builtUI, Flows: flows, Now: func() time.Time { return now }})

	rec := get(t, h, "/api/overview/bandwidth")
	if rec.Code != http.StatusOK || !flows.got.From.Equal(now.Add(-time.Hour)) || !flows.got.To.Equal(now) {
		t.Errorf("default range: %d %+v", rec.Code, flows.got)
	}
	rec = get(t, h, "/api/overview/bandwidth?from=2026-11-18T11:00:00Z&to=2026-11-18T11:30:00Z&exporter=192.168.10.1")
	if rec.Code != http.StatusOK || flows.got.Exporter != netip.MustParseAddr("192.168.10.1") || !strings.Contains(rec.Body.String(), `"inboundBps":8`) || !strings.Contains(rec.Body.String(), `"incompleteFrom"`) {
		t.Errorf("explicit: %d %+v %s", rec.Code, flows.got, rec.Body.String())
	}
	for _, bad := range []string{"?from=yesterday", "?exporter=router", "?from=2020-01-01T00:00:00Z"} {
		if rec := get(t, h, "/api/overview/bandwidth"+bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if rec := get(t, New(Options{UI: builtUI}), "/api/overview/bandwidth"); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no store: %d", rec.Code)
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
