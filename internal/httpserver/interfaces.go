package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"sort"
	"strconv"
	"time"

	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/ingest"
	"github.com/jkop-xyz/flowmon/internal/interfaces"
	"github.com/jkop-xyz/flowmon/internal/reprocess"
)

// InterfaceOptions wire the Interfaces endpoints.
type InterfaceOptions struct {
	Registry *interfaces.Registry
	Stats    interface {
		Interfaces(exporter netip.Addr) ([]ingest.InterfaceStats, uint32)
	}
	Changes interface {
		AddClassificationChange(ctx context.Context, exporter netip.Addr, at time.Time, description string) error
		ClassificationChanges(ctx context.Context, from, to time.Time, exporter netip.Addr) ([]flowstore.ClassificationChange, error)
	}
	Reprocess *reprocess.Runner
	// RawRetention bounds how far back reprocessing can reach; it can change in Settings.
	RawRetention func() time.Duration
}

// InterfaceView is one interface number of an Exporter, with its live counts.
type InterfaceView struct {
	Index        uint32          `json:"index"`
	Name         string          `json:"name"`
	Source       string          `json:"source"`
	Role         interfaces.Role `json:"role"`
	SuggestedWAN bool            `json:"suggestedWan"`
	ingest.InterfaceStats
}

// InterfacesResponse is the Interfaces section of an Exporter.
type InterfacesResponse struct {
	ClassificationMode exporters.ClassificationMode `json:"classificationMode"`
	// HasInterfaceNumbers is false when the Exporter's Flows carry no interface numbers,
	// so only classification by address is possible.
	HasInterfaceNumbers bool                `json:"hasInterfaceNumbers"`
	Interfaces          []InterfaceView     `json:"interfaces"`
	Changes             []interfaces.Change `json:"changes"`
	Reprocess           reprocess.Status    `json:"reprocess"`
	ReprocessFrom       time.Time           `json:"reprocessFrom"`
}

func registerInterfaces(mux *http.ServeMux, opts Options) {
	io := opts.Interfaces
	if io == nil || io.Registry == nil || opts.Exporters == nil {
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
	view := func(a netip.Addr) InterfacesResponse {
		res := InterfacesResponse{ClassificationMode: exporters.ByAddress, Interfaces: []InterfaceView{},
			Changes: io.Registry.Changes(a, opts.Now()), ReprocessFrom: opts.Now().Add(-io.RawRetention()).UTC()}
		if c, ok := opts.Exporters.Config(a); ok {
			res.ClassificationMode = c.ClassificationMode
		}
		if io.Reprocess != nil {
			res.Reprocess = io.Reprocess.Status()
		}
		byIndex := map[uint32]*InterfaceView{}
		for _, cur := range io.Registry.Current(a) {
			byIndex[cur.Index] = &InterfaceView{Index: cur.Index, Name: cur.Name, Source: cur.Source, Role: cur.Role}
		}
		var suggested uint32
		if io.Stats != nil {
			var stats []ingest.InterfaceStats
			stats, suggested = io.Stats.Interfaces(a)
			for _, st := range stats {
				v := byIndex[st.Index]
				if v == nil {
					v = &InterfaceView{Index: st.Index}
					byIndex[st.Index] = v
				}
				v.InterfaceStats = st
			}
			res.HasInterfaceNumbers = len(stats) > 0
		}
		for _, v := range byIndex {
			v.InterfaceStats.Index = v.Index
			v.SuggestedWAN = v.Index == suggested && suggested != 0
			res.Interfaces = append(res.Interfaces, *v)
		}
		sort.Slice(res.Interfaces, func(i, j int) bool { return res.Interfaces[i].Index < res.Interfaces[j].Index })
		return res
	}
	record := func(r *http.Request, a netip.Addr, what string) {
		if io.Changes != nil {
			desc := fmt.Sprintf("%s (by %s)", what, currentUser(r.Context()))
			if err := io.Changes.AddClassificationChange(r.Context(), a, opts.Now(), desc); err != nil {
				opts.Logger.Warn("recording classification change", "err", err)
			}
		}
	}

	mux.HandleFunc("GET /api/exporters/{address}/interfaces", func(w http.ResponseWriter, r *http.Request) {
		if a, ok := address(w, r); ok {
			writeJSON(w, http.StatusOK, view(a))
		}
	})
	mux.HandleFunc("PUT /api/exporters/{address}/interfaces/{index}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		idx, err := strconv.ParseUint(r.PathValue("index"), 10, 32)
		if err != nil || idx == 0 {
			errorJSON(w, http.StatusBadRequest, "invalid interface number")
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		changed, err := io.Registry.SetName(r.Context(), a, uint32(idx), req.Name, interfaces.SourceUser, opts.Now())
		if errors.Is(err, interfaces.ErrBadName) {
			errorJSON(w, http.StatusBadRequest, "name must be 1–64 characters")
			return
		}
		if err != nil {
			opts.Logger.Error("saving interface name", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't save the name")
			return
		}
		// Renaming alone doesn't change classification (roles follow names), so it isn't
		// marked on charts; mapping changes are listed on the Exporters page instead.
		_ = changed
		writeJSON(w, http.StatusOK, view(a))
	})
	mux.HandleFunc("PUT /api/exporters/{address}/classification", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		var req struct {
			Mode  exporters.ClassificationMode `json:"classificationMode"`
			Roles map[string]interfaces.Role   `json:"roles"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		if err := io.Registry.SetRoles(r.Context(), a, req.Roles); err != nil {
			if errors.Is(err, interfaces.ErrBadRole) {
				errorJSON(w, http.StatusBadRequest, "role must be wan, lan or tunnel")
				return
			}
			opts.Logger.Error("saving interface roles", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't save the roles")
			return
		}
		if _, err := opts.Exporters.SetMode(r.Context(), a, req.Mode, currentUser(r.Context())); err != nil {
			if errors.Is(err, exporters.ErrUnsupportedMode) || errors.Is(err, exporters.ErrNotApproved) {
				errorJSON(w, http.StatusBadRequest, err.Error()[len("exporters: "):])
				return
			}
			opts.Logger.Error("saving classification mode", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't save the classification mode")
			return
		}
		record(r, a, fmt.Sprintf("classification set to %s, %d interface roles", req.Mode, len(req.Roles)))
		opts.Logger.Info("classification changed", "exporter", a.String(), "mode", req.Mode, "roles", req.Roles)
		writeJSON(w, http.StatusOK, view(a))
	})
	mux.HandleFunc("POST /api/exporters/{address}/reprocess", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok || io.Reprocess == nil {
			return
		}
		if err := io.Reprocess.Start(a, opts.Now().Add(-io.RawRetention())); errors.Is(err, reprocess.ErrBusy) {
			errorJSON(w, http.StatusConflict, "a reprocessing job is already running")
			return
		}
		writeJSON(w, http.StatusAccepted, io.Reprocess.Status())
	})
}

// classificationChanges returns chart markers for a query, or none.
func classificationChanges(r *http.Request, opts Options, from, to time.Time, exporter netip.Addr) []flowstore.ClassificationChange {
	if opts.Interfaces == nil || opts.Interfaces.Changes == nil {
		return []flowstore.ClassificationChange{}
	}
	ch, err := opts.Interfaces.Changes.ClassificationChanges(r.Context(), from, to, exporter)
	if err != nil {
		opts.Logger.Warn("classification changes", "err", err)
		return []flowstore.ClassificationChange{}
	}
	return ch
}
