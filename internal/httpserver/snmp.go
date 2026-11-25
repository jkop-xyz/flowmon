package httpserver

import (
	"context"
	"net/http"
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/snmp"
)

// SNMPOptions wire an Exporter's SNMP: the SNMP Data Source that polls it, and
// Reconciliation on that source's Interface Counters.
type SNMPOptions struct {
	// SourceFor returns the SNMP Data Source that polls an Exporter, if any.
	SourceFor func(exporter netip.Addr) (datasource.Status, bool)
	Store     interface {
		ReconciliationHours(ctx context.Context, source string, exporter netip.Addr, ifName string, from, to time.Time) ([]flowstore.SNMPHour, error)
		FlowBytesByInterface(ctx context.Context, exporter netip.Addr, index uint32, from, to time.Time) (map[time.Time]flowstore.InterfaceHour, error)
	}
	// WAN returns an Exporter's WAN Interfaces: name → current interface number.
	WAN func(exporter netip.Addr) map[string]uint32
}

type snmpSourceView struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Health  datasource.Health `json:"health"`
}

type reconciliationView struct {
	OverheadBytes int                    `json:"overheadBytes"`
	Enabled       bool                   `json:"enabled"`
	Interfaces    []snmp.InterfaceReport `json:"interfaces"`
	Thresholds    map[string]float64     `json:"thresholds"`
}

// reconStore reads Reconciliation's hours from one SNMP source's Interface Counters.
type reconStore struct {
	so     *SNMPOptions
	source string
}

func (r reconStore) SNMPHours(ctx context.Context, exporter netip.Addr, ifName string, from, to time.Time) ([]flowstore.SNMPHour, error) {
	return r.so.Store.ReconciliationHours(ctx, r.source, exporter, ifName, from, to)
}

func (r reconStore) FlowBytesByInterface(ctx context.Context, exporter netip.Addr, index uint32, from, to time.Time) (map[time.Time]flowstore.InterfaceHour, error) {
	return r.so.Store.FlowBytesByInterface(ctx, exporter, index, from, to)
}

func registerSNMP(mux *http.ServeMux, opts Options) {
	so := opts.SNMP
	if so == nil || so.SourceFor == nil {
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

	// The SNMP Data Source polling an Exporter, or none.
	mux.HandleFunc("GET /api/exporters/{address}/snmp", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		out := map[string]any{"source": nil}
		if src, ok := so.SourceFor(a); ok {
			out["source"] = snmpSourceView{src.ID, src.Name, src.Enabled, src.Health}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("GET /api/exporters/{address}/reconciliation", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		res := reconciliationView{Interfaces: []snmp.InterfaceReport{},
			Thresholds: map[string]float64{"gap": snmp.GapThreshold, "sustainedHours": snmp.SustainedHours, "minBytesPerHour": snmp.MinBytesPerHour}}
		src, ok := so.SourceFor(a)
		if ok {
			res.OverheadBytes, res.Enabled = snmp.ConfigOf(src.Settings).OverheadPerPacket(), src.Enabled
		}
		if res.Enabled && so.Store != nil && so.WAN != nil {
			reps, err := snmp.Reconcile(r.Context(), reconStore{so, src.ID}, a, so.WAN(a), res.OverheadBytes, opts.Now())
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
