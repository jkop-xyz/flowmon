package httpserver

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/settings"
)

type memSettings map[string]string

func (m memSettings) Setting(_ context.Context, key string) (string, bool, error) {
	v, ok := m[key]
	return v, ok, nil
}

func (m memSettings) SetSettings(_ context.Context, values map[string]string) error {
	if values["retention_raw"] == "fail" {
		return errors.New("disk full")
	}
	for k, v := range values {
		m[k] = v
	}
	return nil
}

func TestSettingsEndpoints(t *testing.T) {
	env := settings.Env{LocalNetworks: classify.DefaultLocalNetworks(), Retention: flowstore.DefaultRetention(),
		Locks: settings.Locks{RetentionHour: true}}
	m, err := settings.Load(context.Background(), memSettings{}, env)
	if err != nil {
		t.Fatal(err)
	}
	h := New(Options{UI: builtUI, Settings: m})
	c := &client{t: t, h: h}

	rec := get(t, h, "/api/settings")
	for _, want := range []string{`"value":["10.0.0.0/8"`, `"hour":{"seconds":63072000,"locked":true,"env":"FLOWMON_RETENTION_HOUR"}`, `"minRetentionDays":1`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Errorf("settings lack %s: %s", want, rec.Body.String())
		}
	}
	rec = c.do("PUT", "/api/settings", `{"localNetworks":["192.168.88.0/24","fd00::/8"],"retentionDays":{"raw":14,"minute":30,"dns":3}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"value":["192.168.88.0/24","fd00::/8"]`) || m.Current().Retention.Raw != 14*24*time.Hour || m.Current().Retention.DNS != 3*24*time.Hour {
		t.Fatalf("update: %d %s", rec.Code, rec.Body.String())
	}
	for body, want := range map[string]string{
		`{"localNetworks":["nope"]}`:     "not a prefix",
		`{"retentionDays":{"hour":100}}`: "FLOWMON_RETENTION_HOUR",
		`{"retentionDays":{"raw":60}}`:   "no longer than",
		`{"retentionDays":{"weekly":3}}`: "raw, minute, hour or dns",
		`{"localNetworks":[]}`:           "at least one",
	} {
		if rec := c.do("PUT", "/api/settings", body); rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), want) {
			t.Errorf("%s: %d %s", body, rec.Code, rec.Body.String())
		}
	}
}

func TestStatusReportsAppStoreHealth(t *testing.T) {
	var holder appstore.Holder
	h := New(Options{UI: builtUI, AppStore: &holder})
	if rec := get(t, h, "/api/status"); !strings.Contains(rec.Body.String(), `"available":false,"configured":false`) {
		t.Errorf("before setup: %s", rec.Body.String())
	}
	s, err := appstore.Open(context.Background(), appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	holder.Set(s)
	if rec := get(t, h, "/api/status"); !strings.Contains(rec.Body.String(), `"available":true`) || !strings.Contains(rec.Body.String(), `"driver":"sqlite"`) {
		t.Errorf("available: %s", rec.Body.String())
	}
	s.Close()
	holder.Check(context.Background(), time.Second)
	if rec := get(t, h, "/api/status"); !strings.Contains(rec.Body.String(), `"available":false`) || !strings.Contains(rec.Body.String(), `"error"`) {
		t.Errorf("unavailable: %s", rec.Body.String())
	}
}
