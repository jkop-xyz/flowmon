package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/netip"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/exporters"
)

type userKey struct{}

// currentUser is the logged-in user, set by requireSession.
func currentUser(ctx context.Context) string {
	if u, ok := ctx.Value(userKey{}).(appstore.User); ok {
		return u.Username
	}
	return "unknown"
}

// mergeExporterStatus adds names and roles from the registry, and approved or ignored
// Exporters that haven't sent anything since Flowmon started.
func mergeExporterStatus(st *collector.Status, reg *exporters.Registry) {
	if reg == nil {
		return
	}
	seen := map[string]bool{}
	for i := range st.Exporters {
		e := &st.Exporters[i]
		seen[e.Address] = true
		if a, err := netip.ParseAddr(e.Address); err == nil {
			if c, ok := reg.Config(a); ok {
				e.Name, e.Role, e.ClassificationMode = c.Name, string(c.Role), string(c.ClassificationMode)
			}
		}
	}
	for _, c := range reg.Configs() {
		if seen[c.Address.String()] {
			continue
		}
		st.Exporters = append(st.Exporters, collector.ExporterStatus{
			Address: c.Address.String(), Approval: string(c.Status), Seen: false,
			Name: c.Name, Role: string(c.Role), ClassificationMode: string(c.ClassificationMode),
			ObservationDomains: []uint32{}, Deviations: []collector.DeviationStatus{},
			Templates: []collector.TemplateStatus{}, Findings: []collector.Finding{},
		})
	}
}

// ExporterOption is one entry of the global Exporter filter.
type ExporterOption struct {
	Address string `json:"address"`
	Name    string `json:"name"`
}

func registerExporters(mux *http.ServeMux, opts Options) {
	reg := opts.Exporters
	if reg == nil {
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
	decided := func(w http.ResponseWriter, c exporters.Config, err error) {
		switch {
		case errors.Is(err, exporters.ErrUnsupportedRole), errors.Is(err, exporters.ErrUnsupportedMode), errors.Is(err, exporters.ErrNameTooLong):
			errorJSON(w, http.StatusBadRequest, err.Error()[len("exporters: "):])
		case err != nil:
			opts.Logger.Error("saving exporter decision", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't save the decision")
		default:
			writeJSON(w, http.StatusOK, c)
		}
	}

	mux.HandleFunc("GET /api/exporters/options", func(w http.ResponseWriter, _ *http.Request) {
		out := []ExporterOption{}
		for _, c := range reg.Configs() {
			if c.Status == exporters.StatusApproved {
				out = append(out, ExporterOption{Address: c.Address.String(), Name: c.Name})
			}
		}
		writeJSON(w, http.StatusOK, out)
	})
	mux.HandleFunc("POST /api/exporters/{address}/approve", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		var req struct {
			Name               string                       `json:"name"`
			Role               exporters.Role               `json:"role"`
			ClassificationMode exporters.ClassificationMode `json:"classificationMode"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		c, err := reg.Approve(r.Context(), a, req.Role, req.ClassificationMode, req.Name, currentUser(r.Context()))
		if err == nil {
			opts.Logger.Info("exporter approved", "exporter", a.String(), "by", c.DecidedBy)
		}
		decided(w, c, err)
	})
	mux.HandleFunc("POST /api/exporters/{address}/ignore", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		c, err := reg.Ignore(r.Context(), a, currentUser(r.Context()))
		if err == nil {
			opts.Logger.Info("exporter ignored", "exporter", a.String(), "by", c.DecidedBy)
		}
		decided(w, c, err)
	})
	mux.HandleFunc("DELETE /api/exporters/{address}", func(w http.ResponseWriter, r *http.Request) {
		a, ok := address(w, r)
		if !ok {
			return
		}
		if err := reg.Forget(r.Context(), a); err != nil {
			opts.Logger.Error("forgetting exporter", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't forget the Exporter")
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}
