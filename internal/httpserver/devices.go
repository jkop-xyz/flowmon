package httpserver

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"strings"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/oui"
)

// DeviceStore answers about Devices (ADR 0005). *flowstore.Store implements it.
type DeviceStore interface {
	DeviceAddressesOf(ctx context.Context, mac flow.MAC) ([]flowstore.DeviceAddress, error)
	DevicesAt(ctx context.Context, addr netip.Addr) ([]flowstore.DeviceAddress, error)
	IsRouterMAC(ctx context.Context, mac flow.MAC) (bool, error)
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
		out := describeMAC(m)
		out.Router = router
		for _, a := range addrs {
			out.Addresses = append(out.Addresses, deviceAddressJSON{a.Address.String(), a.FirstSeen.UTC().Format(timeLayout), a.LastSeen.UTC().Format(timeLayout)})
		}
		writeJSON(w, http.StatusOK, out)
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
