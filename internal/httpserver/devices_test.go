package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type fakeDeviceStore struct{}

var testPhone, _ = flow.ParseMAC("3c:5a:b4:00:00:44")

func (fakeDeviceStore) DeviceAddressesOf(_ context.Context, m flow.MAC) ([]flowstore.DeviceAddress, error) {
	if m != testPhone {
		return nil, nil
	}
	t := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	return []flowstore.DeviceAddress{{MAC: m, Address: netip.MustParseAddr("192.168.10.77"), FirstSeen: t, LastSeen: t}}, nil
}

func (fakeDeviceStore) DevicesAt(_ context.Context, a netip.Addr) ([]flowstore.DeviceAddress, error) {
	return []flowstore.DeviceAddress{{MAC: testPhone, Address: a}}, nil
}

func (fakeDeviceStore) IsRouterMAC(context.Context, flow.MAC) (bool, error) { return false, nil }

func TestDeviceEndpoints(t *testing.T) {
	h := New(Options{UI: builtUI, Devices: fakeDeviceStore{}})
	rec := get(t, h, "/api/devices/3C:5A:B4:00:00:44")
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `"vendor":"Google"`) || !strings.Contains(body, `"address":"192.168.10.77"`) {
		t.Errorf("device: %d %s", rec.Code, body)
	}
	if rec := get(t, h, "/api/devices?address=192.168.10.77"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mac":"3c:5a:b4:00:00:44"`) {
		t.Errorf("devices at: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"/api/devices/nope", "/api/devices?address=nope"} {
		if rec := get(t, h, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	if _, mac, err := parseSubject("device:3C:5A:B4:00:00:44"); err != nil || mac != "3c:5a:b4:00:00:44" {
		t.Errorf("parseSubject: %q %v", mac, err)
	}
	if _, _, err := parseSubject("device:nope"); err == nil {
		t.Error("parseSubject took device:nope")
	}
}

func TestDeviceLabelEndpoints(t *testing.T) {
	var holder appstore.Holder
	h := New(Options{UI: builtUI, AppStore: &holder})
	if rec := get(t, h, "/api/device-labels"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"labels":{}`) {
		t.Errorf("without an App Store: %d %s", rec.Code, rec.Body)
	}
	c := &client{t: t, h: h}
	if rec := c.do("PUT", "/api/device-labels/3c:5a:b4:00:00:44", `{"name":"Phone"}`); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("naming without an App Store: %d", rec.Code)
	}

	s, err := appstore.Open(context.Background(), appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "flowmon.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	holder.Set(s)
	if rec := c.do("PUT", "/api/device-labels/3C:5A:B4:00:00:44", `{"name":" Phone "}`); rec.Code != http.StatusOK {
		t.Fatalf("naming: %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/device-labels"); !strings.Contains(rec.Body.String(), `"3c:5a:b4:00:00:44":"Phone"`) {
		t.Errorf("labels: %s", rec.Body)
	}
	for body, path := range map[string]string{`{"name":"x"}`: "/api/device-labels/nope", `{"name":"` + strings.Repeat("x", 65) + `"}`: "/api/device-labels/3c:5a:b4:00:00:44"} {
		if rec := c.do("PUT", path, body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s %s: %d", path, body, rec.Code)
		}
	}
	if rec := c.do("PUT", "/api/device-labels/3c:5a:b4:00:00:44", `{"name":""}`); rec.Code != http.StatusOK {
		t.Errorf("removing: %d", rec.Code)
	}
	if rec := get(t, h, "/api/device-labels"); !strings.Contains(rec.Body.String(), `"labels":{}`) {
		t.Errorf("after removing: %s", rec.Body)
	}
}
