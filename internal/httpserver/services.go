package httpserver

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/services"
)

type serviceNameJSON struct {
	Protocol  uint8     `json:"protocol"`
	Port      uint16    `json:"port"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updatedAt"`
	// IANAName is what the registry calls the port, for comparison.
	IANAName string `json:"ianaName,omitempty"`
}

func toServiceNameJSON(n appstore.ServiceName) serviceNameJSON {
	out := serviceNameJSON{Protocol: n.Protocol, Port: n.Port, Name: n.Name, UpdatedAt: n.UpdatedAt}
	protos := []uint8{n.Protocol}
	if n.Protocol == 0 {
		protos = []uint8{6, 17}
	}
	for _, p := range protos {
		if name, _, ok := services.IANA(services.Port{Protocol: p, Port: n.Port}); ok {
			out.IANAName = name
			break
		}
	}
	return out
}

func registerServices(mux *http.ServeMux, opts Options) {
	dir := opts.Services
	available := func(w http.ResponseWriter) bool {
		if dir == nil {
			errorJSON(w, http.StatusServiceUnavailable, "Service Names are not available")
			return false
		}
		return true
	}
	port := func(w http.ResponseWriter, r *http.Request) (services.Port, bool) {
		p, err := services.ParsePort(r.PathValue("protocol") + "/" + r.PathValue("port"))
		if err != nil {
			errorJSON(w, http.StatusBadRequest, err.Error())
			return p, false
		}
		return p, true
	}
	changeError := func(w http.ResponseWriter, err error) {
		switch {
		case errors.Is(err, services.ErrInvalid):
			errorJSON(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, appstore.ErrNotFound):
			errorJSON(w, http.StatusNotFound, "that port has no name of yours")
		case errors.Is(err, services.ErrUnavailable):
			errorJSON(w, http.StatusServiceUnavailable, "the App Store is unavailable, so Service Names can't be changed")
		default:
			opts.Logger.Error("service name", "err", err)
			errorJSON(w, http.StatusInternalServerError, "saving the Service Name failed")
		}
	}

	mux.HandleFunc("GET /api/service-names", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		out := []serviceNameJSON{}
		for _, n := range dir.UserNames(r.Context()) {
			out = append(out, toServiceNameJSON(n))
		}
		sortServiceNames(out)
		writeJSON(w, http.StatusOK, map[string]any{"names": out})
	})
	// Names for the ports on screen: ?port=6/443&port=17/53.
	mux.HandleFunc("GET /api/service-names/lookup", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		raw := r.URL.Query()["port"]
		if len(raw) > maxNames {
			errorJSON(w, http.StatusBadRequest, "at most 200 ports at a time")
			return
		}
		ports := make([]services.Port, len(raw))
		for i, s := range raw {
			p, err := services.ParsePort(s)
			if err != nil {
				errorJSON(w, http.StatusBadRequest, err.Error())
				return
			}
			ports[i] = p
		}
		names := dir.Names(r.Context(), ports)
		out := make(map[string]services.Name, len(raw))
		for i, s := range raw {
			out[s] = names[ports[i]]
		}
		writeJSON(w, http.StatusOK, map[string]any{"names": out})
	})
	mux.HandleFunc("PUT /api/service-names/{protocol}/{port}", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Name string `json:"name"`
		}
		if !available(w) {
			return
		}
		p, ok := port(w, r)
		if !ok || !readJSON(w, r, &req) {
			return
		}
		n, err := dir.Set(r.Context(), p, req.Name)
		if err != nil {
			changeError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, toServiceNameJSON(n))
	})
	mux.HandleFunc("DELETE /api/service-names/{protocol}/{port}", func(w http.ResponseWriter, r *http.Request) {
		if !available(w) {
			return
		}
		p, ok := port(w, r)
		if !ok {
			return
		}
		if err := dir.Delete(r.Context(), p); err != nil {
			changeError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	})
}

func sortServiceNames(names []serviceNameJSON) {
	slices.SortFunc(names, func(a, b serviceNameJSON) int {
		if a.Port != b.Port {
			return int(a.Port) - int(b.Port)
		}
		return int(a.Protocol) - int(b.Protocol)
	})
}
