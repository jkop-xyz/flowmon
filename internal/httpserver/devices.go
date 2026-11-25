package httpserver

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/oui"
)

// DeviceStore answers about Devices (ADR 0005). *flowstore.Store implements it.
type DeviceStore interface {
	DeviceAddressesOf(ctx context.Context, mac flow.MAC) ([]flowstore.DeviceAddress, error)
	DevicesAt(ctx context.Context, addr netip.Addr) ([]flowstore.DeviceAddress, error)
	IsRouterMAC(ctx context.Context, mac flow.MAC) (bool, error)
	DeviceMerges(ctx context.Context) ([]flowstore.DeviceMerge, error)
	MergeDevice(ctx context.Context, mac, into flow.MAC, reason string, at time.Time) error
	SplitDevice(ctx context.Context, mac flow.MAC) error
}

type mergeJSON struct {
	MAC      string `json:"mac"`
	Vendor   string `json:"vendor,omitempty"`
	Reason   string `json:"reason"`
	MergedAt string `json:"mergedAt"`
}

// timeLayout is how times are sent: RFC 3339 with milliseconds, in UTC.
const timeLayout = "2006-01-02T15:04:05.000Z07:00"

type deviceAddressJSON struct {
	Address   string `json:"address"`
	FirstSeen string `json:"firstSeen"`
	LastSeen  string `json:"lastSeen"`
}

type deviceJSON struct {
	MAC        string `json:"mac"`
	Vendor     string `json:"vendor,omitempty"`
	PrivateMac bool   `json:"privateMac,omitempty"`
	// Router is set for a MAC that is a router hop, never a Device.
	Router    bool                `json:"router,omitempty"`
	Addresses []deviceAddressJSON `json:"addresses"`
	// Merged are the MACs counted as this Device; MergedInto the Device this MAC counts as.
	Merged     []mergeJSON `json:"merged,omitempty"`
	MergedInto string      `json:"mergedInto,omitempty"`
}

func describeMAC(m flow.MAC) deviceJSON {
	hw := net.HardwareAddr(m[:])
	return deviceJSON{MAC: m.String(), Vendor: oui.Vendor(hw), PrivateMac: oui.Private(hw), Addresses: []deviceAddressJSON{}}
}

func registerDevices(mux *http.ServeMux, opts Options) {
	ds := opts.Devices
	available := func(w http.ResponseWriter) bool {
		if ds == nil {
			errorJSON(w, http.StatusServiceUnavailable, "flow store not available")
			return false
		}
		return true
	}
	// A Device's identity and every address it used.
	mux.HandleFunc("GET /api/devices/{mac}", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		m, err := flow.ParseMAC(strings.ToLower(r.PathValue("mac")))
		if err != nil || m.IsZero() {
			errorJSON(w, http.StatusBadRequest, "not a MAC address such as aa:bb:cc:dd:ee:ff")
			return
		}
		addrs, err := ds.DeviceAddressesOf(r.Context(), m)
		if queryError(w, opts.Logger, "device", err) {
			return
		}
		router, err := ds.IsRouterMAC(r.Context(), m)
		if queryError(w, opts.Logger, "device", err) {
			return
		}
		merges, err := ds.DeviceMerges(r.Context())
		if queryError(w, opts.Logger, "device", err) {
			return
		}
		out := describeMAC(m)
		out.Router = router
		for _, mg := range merges {
			switch {
			case mg.Into == m:
				out.Merged = append(out.Merged, mergeJSON{mg.MAC.String(), oui.Vendor(net.HardwareAddr(mg.MAC[:])), mg.Reason, mg.MergedAt.UTC().Format(timeLayout)})
			case mg.MAC == m:
				out.MergedInto = mg.Into.String()
			}
		}
		for _, a := range addrs {
			out.Addresses = append(out.Addresses, deviceAddressJSON{a.Address.String(), a.FirstSeen.UTC().Format(timeLayout), a.LastSeen.UTC().Format(timeLayout)})
		}
		writeJSON(w, http.StatusOK, out)
	})
	// Names given to Devices, by MAC. Kept in the App Store: when it is unavailable there
	// are none, and they can't be changed.
	mux.HandleFunc("GET /api/device-labels", func(w http.ResponseWriter, r *http.Request) {
		labels := map[string]string{}
		if opts.AppStore != nil {
			if st := opts.AppStore.Get(); st != nil && opts.AppStore.Health().Available {
				got, err := st.DeviceLabels(r.Context())
				if err != nil {
					opts.Logger.Warn("device labels", "err", err)
				} else {
					labels = got
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"labels": labels})
	})
	mux.HandleFunc("PUT /api/device-labels/{mac}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		m, err := flow.ParseMAC(strings.ToLower(r.PathValue("mac")))
		if err != nil || m.IsZero() {
			errorJSON(w, http.StatusBadRequest, "not a MAC address such as aa:bb:cc:dd:ee:ff")
			return
		}
		name := strings.TrimSpace(req.Name)
		if len(name) > 64 {
			errorJSON(w, http.StatusBadRequest, "a name is at most 64 characters")
			return
		}
		var st *appstore.Store
		if opts.AppStore != nil && opts.AppStore.Health().Available {
			st = opts.AppStore.Get()
		}
		if st == nil {
			errorJSON(w, http.StatusServiceUnavailable, "the App Store is unavailable, so names can't be changed")
			return
		}
		if err := st.SetDeviceLabel(r.Context(), m.String(), name, opts.Now()); err != nil {
			opts.Logger.Error("device label", "err", err)
			errorJSON(w, http.StatusInternalServerError, "saving the name failed")
			return
		}
		opts.Logger.Info("device named", "mac", m.String(), "name", name, "by", currentUser(r.Context()))
		writeJSON(w, http.StatusOK, map[string]string{"mac": m.String(), "name": name})
	})
	// Merging a Device into another (into), and splitting it off again.
	parseMAC := func(w http.ResponseWriter, s string) (flow.MAC, bool) {
		m, err := flow.ParseMAC(strings.ToLower(strings.TrimSpace(s)))
		if err != nil || m.IsZero() {
			errorJSON(w, http.StatusBadRequest, "not a MAC address such as aa:bb:cc:dd:ee:ff")
			return m, false
		}
		return m, true
	}
	mux.HandleFunc("POST /api/devices/{mac}/merge", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Into string `json:"into"`
		}
		if !available(w) || !readJSON(w, r, &req) {
			return
		}
		m, ok := parseMAC(w, r.PathValue("mac"))
		if !ok {
			return
		}
		into, ok := parseMAC(w, req.Into)
		if !ok {
			return
		}
		err := ds.MergeDevice(r.Context(), m, into, "manual", opts.Now())
		if errors.Is(err, flowstore.ErrMergeSelf) || (err == nil && m == into) {
			errorJSON(w, http.StatusBadRequest, "a Device can't be merged into itself")
			return
		}
		if queryError(w, opts.Logger, "merging devices", err) {
			return
		}
		opts.Logger.Info("devices merged", "mac", m.String(), "into", into.String(), "by", currentUser(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /api/devices/{mac}/merge", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		m, ok := parseMAC(w, r.PathValue("mac"))
		if !ok {
			return
		}
		if queryError(w, opts.Logger, "splitting a device", ds.SplitDevice(r.Context(), m)) {
			return
		}
		opts.Logger.Info("device split off", "mac", m.String(), "by", currentUser(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	})
	// The Devices that used an address, most recently first: a Local Host page's links.
	mux.HandleFunc("GET /api/devices", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		a, err := netip.ParseAddr(r.URL.Query().Get("address"))
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "address must be an IP address")
			return
		}
		at, err := ds.DevicesAt(r.Context(), a)
		if queryError(w, opts.Logger, "devices", err) {
			return
		}
		out := []deviceJSON{}
		for _, d := range at {
			if router, err := ds.IsRouterMAC(r.Context(), d.MAC); err != nil || router {
				continue
			}
			dj := describeMAC(d.MAC)
			dj.Addresses = []deviceAddressJSON{{d.Address.String(), d.FirstSeen.UTC().Format(timeLayout), d.LastSeen.UTC().Format(timeLayout)}}
			out = append(out, dj)
		}
		writeJSON(w, http.StatusOK, map[string]any{"devices": out})
	})
}
