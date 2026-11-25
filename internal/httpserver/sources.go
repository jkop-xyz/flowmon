package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/jkop-xyz/flowmon/internal/datasource"
)

// Sources configures and shows Data Sources. *datasource.Manager implements it.
type Sources interface {
	Kinds() []datasource.KindInfo
	List() []datasource.Status
	Create(ctx context.Context, src datasource.Source) (datasource.Status, error)
	Update(ctx context.Context, id string, src datasource.Source) (datasource.Status, error)
	Delete(ctx context.Context, id string) error
	Test(ctx context.Context, id string, src datasource.Source) (string, error)
}

// sourceJSON is a Source as the UI sends it.
type sourceJSON struct {
	Kind     string              `json:"kind"`
	Name     string              `json:"name"`
	Enabled  bool                `json:"enabled"`
	Settings datasource.Settings `json:"settings"`
}

func (s sourceJSON) source() datasource.Source {
	return datasource.Source{Kind: s.Kind, Name: s.Name, Enabled: s.Enabled, Settings: s.Settings}
}

// sourceError answers a Manager error: what the user can fix is a 400 naming the field.
func sourceError(w http.ResponseWriter, opts Options, what string, err error) bool {
	var inv *datasource.InvalidError
	switch {
	case err == nil:
		return false
	case errors.As(err, &inv):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": inv.Msg, "field": inv.Field})
	case errors.Is(err, datasource.ErrNotFound):
		errorJSON(w, http.StatusNotFound, err.Error())
	case errors.Is(err, datasource.ErrLocked), errors.Is(err, datasource.ErrUnknownKind):
		errorJSON(w, http.StatusBadRequest, err.Error())
	default:
		opts.Logger.Error(what, "err", err)
		errorJSON(w, http.StatusInternalServerError, what+" failed")
	}
	return true
}

func registerSources(mux *http.ServeMux, opts Options) {
	available := func(w http.ResponseWriter) bool {
		if opts.Sources == nil {
			errorJSON(w, http.StatusServiceUnavailable, "data sources are not available")
			return false
		}
		return true
	}
	mux.HandleFunc("GET /api/sources", func(w http.ResponseWriter, _ *http.Request) {
		if !available(w) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"kinds": opts.Sources.Kinds(), "sources": opts.Sources.List()})
	})
	mux.HandleFunc("POST /api/sources", func(w http.ResponseWriter, r *http.Request) {
		var req sourceJSON
		if !available(w) || !readJSON(w, r, &req) {
			return
		}
		st, err := opts.Sources.Create(r.Context(), req.source())
		if sourceError(w, opts, "adding a data source", err) {
			return
		}
		opts.Logger.Info("data source added", "source", st.ID, "kind", st.Kind, "name", st.Name, "enabled", st.Enabled, "by", currentUser(r.Context()))
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("PUT /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req sourceJSON
		if !available(w) || !readJSON(w, r, &req) {
			return
		}
		st, err := opts.Sources.Update(r.Context(), r.PathValue("id"), req.source())
		if sourceError(w, opts, "saving a data source", err) {
			return
		}
		opts.Logger.Info("data source saved", "source", st.ID, "kind", st.Kind, "name", st.Name, "enabled", st.Enabled, "by", currentUser(r.Context()))
		writeJSON(w, http.StatusOK, st)
	})
	mux.HandleFunc("DELETE /api/sources/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		id := r.PathValue("id")
		if sourceError(w, opts, "deleting a data source", opts.Sources.Delete(r.Context(), id)) {
			return
		}
		opts.Logger.Info("data source deleted", "source", id, "by", currentUser(r.Context()))
		w.WriteHeader(http.StatusNoContent)
	})
	// Test checks settings before they are saved; with an id, empty passwords mean the
	// stored ones.
	mux.HandleFunc("POST /api/sources/test", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
			sourceJSON
		}
		if !available(w) || !readJSON(w, r, &req) {
			return
		}
		msg, err := opts.Sources.Test(r.Context(), req.ID, req.source())
		var inv *datasource.InvalidError
		switch {
		case errors.As(err, &inv), errors.Is(err, datasource.ErrNotFound), errors.Is(err, datasource.ErrUnknownKind):
			sourceError(w, opts, "testing a data source", err)
		case err != nil:
			// The connection failed: that is the answer, not a server error.
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		default:
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
		}
	})
}
