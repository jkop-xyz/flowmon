package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"sort"

	"github.com/jkop-xyz/flowmon/internal/snmp"
)

// SNMPOptions wire SNMP settings, polling and Reconciliation.
type SNMPOptions struct {
	Settings interface {
		Save(ctx context.Context, exporter netip.Addr, c snmp.Config) error
	}
	Poller *snmp.Poller
	Store  snmp.Store
	// WAN returns an Exporter's WAN Interfaces: name → current interface number.
	WAN func(exporter netip.Addr) map[string]uint32
}

type snmpView struct {
	Config  snmp.Public    `json:"config"`
	Status  snmp.Status    `json:"status"`
	Presets map[string]int `json:"overheadPresets"`
}

type reconciliationView struct {
	OverheadBytes int                    `json:"overheadBytes"`
	Enabled       bool                   `json:"enabled"`
	Interfaces    []snmp.InterfaceReport `json:"interfaces"`
	Thresholds    map[string]float64     `json:"thresholds"`
}

func registerSNMP(mux *http.ServeMux, opts Options) {
	so := opts.SNMP
	if so == nil || so.Poller == nil {
		return
	}
	address := func(w http.ResponseWriter, r *http.Request) (netip.Addr, bool) {
		a, err := netip.ParseAddr(r.PathValue("address"))
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "invalid Exporter address")
			return netip.Addr{}, false
		}
		return a.Unmap(), true
	}
	current := func(a netip.Addr) snmp.Config {
		c, ok := so.Poller.Config(a)
		if !ok {
			c = snmp.Config{Version: "v3", AuthProtocol: "SHA256", PrivProtocol: "AES", Overhead: "ethernet"}
		}
		return c
	}
	view := func(a netip.Addr) snmpView {
		return snmpView{Config: current(a).Public(), Status: so.Poller.Status(a), Presets: snmp.OverheadPresets}
	}
	readConfig := func(w http.ResponseWriter, r *http.Request, a netip.Addr) (snmp.Config, bool) {
		var req struct {
			Config snmp.Config `json:"config"`
		}
		if !readJSON(w, r, &req) {
			return snmp.Config{}, false
		}
		c := req.Config.Merge(current(a))
		if err := c.Validate(); err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error()[len("snmp: "):])
			return snmp.Config{}, false
		}
		return c, true
	}

	mux.HandleFunc("GET /api/exporters/{address}/snmp", func(w http.ResponseWriter, r *http.Request) {
		if a, ok := address(w, r); ok {
			writeJSON(w, http.StatusOK, view(a))
		}
	})
	mux.HandleFunc("PUT /api/exporters/{address}/snmp", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		c, ok := readConfig(w, r, a)
		if !ok {
			return
		}
		if err := so.Settings.Save(r.Context(), a, c); err != nil {
			opts.Logger.Error("saving SNMP settings", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't save the SNMP settings")
			return
		}
		so.Poller.Set(a, c)
		opts.Logger.Info("snmp settings saved", "exporter", a.String(), "enabled", c.Enabled, "version", c.Version, "by", currentUser(r.Context()))
		if c.Enabled {
			go func() { _ = so.Poller.Poll(context.Background(), a) }()
		}
		writeJSON(w, http.StatusOK, view(a))
	})
	mux.HandleFunc("POST /api/exporters/{address}/snmp/test", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		c, ok := readConfig(w, r, a)
		if !ok {
			return
		}
		names, err := so.Poller.Test(r.Context(), a, c)
		if err != nil {
			errorJSON(w, http.StatusBadGateway, err.Error())
			return
		}
		list := make([]string, 0, len(names))
		for _, n := range names {
			list = append(list, n)
		}
		sort.Strings(list)
		writeJSON(w, http.StatusOK, map[string]any{"interfaces": list})
	})
	mux.HandleFunc("GET /api/exporters/{address}/reconciliation", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		c := current(a)
		res := reconciliationView{OverheadBytes: c.OverheadPerPacket(), Enabled: c.Enabled, Interfaces: []snmp.InterfaceReport{},
			Thresholds: map[string]float64{"gap": snmp.GapThreshold, "sustainedHours": snmp.SustainedHours, "minBytesPerHour": snmp.MinBytesPerHour}}
		if c.Enabled && so.Store != nil && so.WAN != nil {
			reps, err := snmp.Reconcile(r.Context(), so.Store, a, so.WAN(a), c.OverheadPerPacket(), opts.Now())
			if err != nil {
				opts.Logger.Error("reconciliation", "err", err)
				errorJSON(w, http.StatusInternalServerError, "reconciliation failed")
				return
			}
			res.Interfaces = reps
		}
		writeJSON(w, http.StatusOK, res)
	})
}
