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
	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

type fakeDeviceStore struct {
	merged map[flow.MAC]flow.MAC
}

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

func (f fakeDeviceStore) DeviceMerges(context.Context) ([]flowstore.DeviceMerge, error) {
	var out []flowstore.DeviceMerge
	for m, into := range f.merged {
		out = append(out, flowstore.DeviceMerge{MAC: m, Into: into, Reason: "manual"})
	}
	return out, nil
}

func (f fakeDeviceStore) MergeDevice(_ context.Context, m, into flow.MAC, _ string, _ time.Time) error {
	if f.merged[into] == m {
		return flowstore.ErrMergeSelf
	}
	f.merged[m] = into
	return nil
}

func (f fakeDeviceStore) SplitDevice(_ context.Context, m flow.MAC) error {
	delete(f.merged, m)
	return nil
}

func (fakeDeviceStore) NeighborsOf(_ context.Context, m flow.MAC) ([]flowstore.Neighbor, error) {
	t := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	return []flowstore.Neighbor{{Source: "core", MAC: m, Address: netip.MustParseAddr("192.168.10.77"), IfIndex: 2, Interface: "bridge", FirstSeen: t, LastSeen: t}}, nil
}

func (fakeDeviceStore) QuietDevices(context.Context, time.Time, time.Time) ([]flowstore.QuietDevice, error) {
	return []flowstore.QuietDevice{{MAC: "00:1b:a9:00:00:60", Address: "192.168.10.60"}}, nil
}

func TestDeviceEndpoints(t *testing.T) {
	store := fakeDeviceStore{merged: map[flow.MAC]flow.MAC{}}
	h := New(Options{UI: builtUI, Devices: store, Sources: fakeSourceList{{Source: datasource.Source{ID: "core", Kind: "snmp", Name: "Core router"}}}})
	rec := get(t, h, "/api/devices/3C:5A:B4:00:00:44")
	if body := rec.Body.String(); rec.Code != http.StatusOK || !strings.Contains(body, `"vendor":"Google"`) || !strings.Contains(body, `"address":"192.168.10.77"`) ||
		!strings.Contains(body, `"sourceName":"Core router"`) || !strings.Contains(body, `"interface":"bridge"`) {
		t.Errorf("device: %d %s", rec.Code, body)
	}
	if rec := get(t, h, "/api/devices/quiet"); !strings.Contains(rec.Body.String(), `"mac":"00:1b:a9:00:00:60"`) {
		t.Errorf("quiet devices: %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/devices?address=192.168.10.77"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"mac":"3c:5a:b4:00:00:44"`) {
		t.Errorf("devices at: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{"/api/devices/nope", "/api/devices?address=nope"} {
		if rec := get(t, h, bad); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, rec.Code)
		}
	}
	c := &client{t: t, h: h}
	if rec := c.do("POST", "/api/devices/da:a1:19:00:00:01/merge", `{"into":"3c:5a:b4:00:00:44"}`); rec.Code != http.StatusNoContent {
		t.Fatalf("merge: %d %s", rec.Code, rec.Body)
	}
	if rec := get(t, h, "/api/devices/3c:5a:b4:00:00:44"); !strings.Contains(rec.Body.String(), `"merged":[{"mac":"da:a1:19:00:00:01"`) {
		t.Errorf("merged MACs: %s", rec.Body)
	}
	if rec := get(t, h, "/api/devices/da:a1:19:00:00:01"); !strings.Contains(rec.Body.String(), `"mergedInto":"3c:5a:b4:00:00:44"`) {
		t.Errorf("merged into: %s", rec.Body)
	}
	for _, bad := range []string{`{"into":"da:a1:19:00:00:01"}`, `{"into":"nope"}`} {
		if rec := c.do("POST", "/api/devices/da:a1:19:00:00:01/merge", bad); rec.Code != http.StatusBadRequest {
			t.Errorf("merge %s: %d", bad, rec.Code)
		}
	}
	if rec := c.do("DELETE", "/api/devices/da:a1:19:00:00:01/merge", ""); rec.Code != http.StatusNoContent || len(store.merged) != 0 {
		t.Errorf("split: %d %v", rec.Code, store.merged)
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
