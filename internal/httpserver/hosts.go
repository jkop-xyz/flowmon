package httpserver

import (
	"context"
	"errors"
	"net/http"
	"net/netip"
	"strconv"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/hosts"
)

// HostOptions wires Host Labels, Host Names and the Local Host screens.
type HostOptions struct {
	Store interface {
		HostDetail(ctx context.Context, q flowstore.HostQuery) (flowstore.HostSummary, error)
		HostTraffic(ctx context.Context, q flowstore.HostQuery) (flowstore.HostTrafficResult, error)
		HostAddresses(ctx context.Context, q flowstore.HostQuery) ([]string, error)
		TopRemoteIPs(ctx context.Context, q flowstore.RemoteQuery) (flowstore.RemoteResult, error)
	}
	Directory *hosts.Directory
}

type labelJSON struct {
	ID        string    `json:"id"`
	Prefix    string    `json:"prefix"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toLabelJSON(l appstore.HostLabel) labelJSON {
	return labelJSON{ID: l.ID, Prefix: hosts.KeyString(l.Prefix), Name: l.Name, CreatedAt: l.CreatedAt, UpdatedAt: l.UpdatedAt}
}

// hostGroups returns the prefixes whose addresses count as one Local Host.
func hostGroups(ctx context.Context, opts Options) []netip.Prefix {
	if opts.Hosts == nil || opts.Hosts.Directory == nil {
		return nil
	}
	return opts.Hosts.Directory.Groups(ctx)
}

func registerHosts(mux *http.ServeMux, opts Options) {
	ho := opts.Hosts
	directory := func(w http.ResponseWriter) bool {
		if ho == nil || ho.Directory == nil {
			errorJSON(w, http.StatusServiceUnavailable, "Host Labels are not available")
			return false
		}
		return true
	}
	labelError := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, hosts.ErrInvalid):
			errorJSON(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, appstore.ErrExists):
			errorJSON(w, http.StatusConflict, "that address or prefix already has a label")
		case errors.Is(err, appstore.ErrNotFound):
			errorJSON(w, http.StatusNotFound, "no such label")
		case errors.Is(err, hosts.ErrUnavailable):
			errorJSON(w, http.StatusServiceUnavailable, "the App Store is unavailable, so labels can't be changed")
		default:
			opts.Logger.Error("host label", "err", err)
			errorJSON(w, http.StatusInternalServerError, "saving the label failed")
		}
	}

	mux.HandleFunc("GET /api/host-labels", func(w http.ResponseWriter, r *http.Request) {
		if !directory(w) {
			return
		}
		out := []labelJSON{}
		for _, l := range ho.Directory.Labels(r.Context()) {
			out = append(out, toLabelJSON(l))
		}
		writeJSON(w, http.StatusOK, map[string]any{"labels": out})
	})
	type labelRequest struct {
		Prefix string `json:"prefix"`
		Name   string `json:"name"`
	}
	mux.HandleFunc("POST /api/host-labels", func(w http.ResponseWriter, r *http.Request) {
		var req labelRequest
		if !directory(w) || !readJSON(w, r, &req) {
			return
		}
		l, err := ho.Directory.Create(r.Context(), hosts.LabelInput(req))
		if err != nil {
			labelError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, toLabelJSON(l))
	})
	mux.HandleFunc("PUT /api/host-labels/{id}", func(w http.ResponseWriter, r *http.Request) {
		var req labelRequest
		if !directory(w) || !readJSON(w, r, &req) {
			return
		}
		l, err := ho.Directory.Update(r.Context(), r.PathValue("id"), hosts.LabelInput(req))
		if err != nil {
			labelError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toLabelJSON(l))
	})
	mux.HandleFunc("DELETE /api/host-labels/{id}", func(w http.ResponseWriter, r *http.Request) {
		if !directory(w) {
			return
		}
		if err := ho.Directory.Delete(r.Context(), r.PathValue("id")); err != nil {
			labelError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})

	mux.HandleFunc("GET /api/host-names", func(w http.ResponseWriter, r *http.Request) {
		if !directory(w) {
			return
		}
		raw := r.URL.Query()["host"]
		if len(raw) > maxNames {
			errorJSON(w, http.StatusBadRequest, "at most 200 Local Hosts at a time")
			return
		}
		keys := make([]netip.Prefix, len(raw))
		for i, s := range raw {
			k, err := hosts.ParseKey(s)
			if err != nil {
				errorJSON(w, http.StatusBadRequest, err.Error())
				return
			}
			keys[i] = k
		}
		names := ho.Directory.Names(r.Context(), keys)
		// Keyed by the Local Host as the client sent it, so it can match its own strings.
		out := make(map[string]hosts.Name, len(raw))
		for i, s := range raw {
			out[s] = names[keys[i]]
		}
		writeJSON(w, http.StatusOK, map[string]any{"names": out})
	})

	hostQuery := func(w http.ResponseWriter, r *http.Request) (flowstore.HostQuery, bool) {
		if ho == nil || ho.Store == nil {
			errorJSON(w, http.StatusServiceUnavailable, "flow store not available")
			return flowstore.HostQuery{}, false
		}
		key, err := hosts.ParseKey(r.URL.Query().Get("host"))
		if err != nil {
			errorJSON(w, http.StatusBadRequest, "host must be an IP address or prefix")
			return flowstore.HostQuery{}, false
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return flowstore.HostQuery{}, false
		}
		return flowstore.HostQuery{Host: key, From: bq.From, To: bq.To, Exporter: bq.Exporter,
			HostGroups: hostGroups(r.Context(), opts)}, true
	}
	mux.HandleFunc("GET /api/local-hosts/summary", func(w http.ResponseWriter, r *http.Request) {
		q, ok := hostQuery(w, r)
		if !ok {
			return
		}
		res, err := ho.Store.HostDetail(r.Context(), q)
		if queryError(w, opts.Logger, "local host", err) {
			return
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: res, IncompleteFrom: incompleteFrom(opts)})
	})
	mux.HandleFunc("GET /api/local-hosts/traffic", func(w http.ResponseWriter, r *http.Request) {
		q, ok := hostQuery(w, r)
		if !ok {
			return
		}
		res, err := ho.Store.HostTraffic(r.Context(), q)
		if queryError(w, opts.Logger, "local host traffic", err) {
			return
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: res, IncompleteFrom: incompleteFrom(opts)})
	})
	mux.HandleFunc("GET /api/local-hosts/remote-ips", func(w http.ResponseWriter, r *http.Request) {
		q, ok := hostQuery(w, r)
		if !ok {
			return
		}
		limit := 20
		if s := r.URL.Query().Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > flowstore.MaxTopLimit {
				errorJSON(w, http.StatusBadRequest, "limit must be between 1 and 300")
				return
			}
			limit = n
		}
		addrs, err := ho.Store.HostAddresses(r.Context(), q)
		if queryError(w, opts.Logger, "local host addresses", err) {
			return
		}
		rq := flowstore.RemoteQuery{From: q.From, To: q.To, Exporter: q.Exporter, LocalHosts: addrs, Limit: limit, Now: opts.Now()}
		if !remoteSort(w, r, &rq) {
			return
		}
		res, err := ho.Store.TopRemoteIPs(r.Context(), rq)
		if queryError(w, opts.Logger, "local host remote IPs", err) {
			return
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: res, IncompleteFrom: incompleteFrom(opts)})
	})
}
