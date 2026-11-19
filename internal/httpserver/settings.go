package httpserver

import (
	"errors"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/settings"
)

type localNetworksJSON struct {
	Value    []string `json:"value"`
	Defaults []string `json:"defaults"`
	Locked   bool     `json:"locked"`
	Env      string   `json:"env"`
}

type retentionJSON struct {
	Seconds float64 `json:"seconds"`
	Locked  bool    `json:"locked"`
	Env     string  `json:"env"`
}

type settingsJSON struct {
	LocalNetworks localNetworksJSON        `json:"localNetworks"`
	Retention     map[string]retentionJSON `json:"retention"`
	MinDays       int                      `json:"minRetentionDays"`
	MaxDays       int                      `json:"maxRetentionDays"`
}

func prefixStrings(ps []netip.Prefix) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.String()
	}
	return out
}

func toSettingsJSON(m *settings.Manager) settingsJSON {
	v, l := m.Current(), m.Locks()
	return settingsJSON{
		LocalNetworks: localNetworksJSON{Value: prefixStrings(v.LocalNetworks), Defaults: prefixStrings(m.DefaultLocalNetworks()),
			Locked: l.LocalNetworks, Env: "FLOWMON_LOCAL_NETWORKS"},
		Retention: map[string]retentionJSON{
			"raw":    {v.Retention.Raw.Seconds(), l.RetentionRaw, "FLOWMON_RETENTION_RAW"},
			"minute": {v.Retention.Minute.Seconds(), l.RetentionMinute, "FLOWMON_RETENTION_MINUTE"},
			"hour":   {v.Retention.Hour.Seconds(), l.RetentionHour, "FLOWMON_RETENTION_HOUR"},
		},
		MinDays: int(settings.MinRetention.Hours() / 24),
		MaxDays: int(settings.MaxRetention.Hours() / 24),
	}
}

func registerSettings(mux *http.ServeMux, opts Options) {
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]any{}
		if opts.AppStore != nil {
			h := opts.AppStore.Health()
			st := map[string]any{"configured": opts.AppStore.Get() != nil, "available": h.Available}
			if !h.Since.IsZero() {
				st["since"] = h.Since
			}
			if h.Error != "" {
				st["error"] = h.Error
			}
			if s := opts.AppStore.Get(); s != nil {
				st["driver"] = s.Driver()
				st["location"] = s.Location()
			}
			out["appStore"] = st
		}
		out["collection"] = collectionHealth(opts)
		if fs := flowStoreStatus(r.Context(), opts); fs != nil {
			out["flowStore"] = fs
		}
		writeJSON(w, http.StatusOK, out)
	})

	m := opts.Settings
	mux.HandleFunc("GET /api/settings", func(w http.ResponseWriter, _ *http.Request) {
		if m == nil {
			errorJSON(w, http.StatusServiceUnavailable, "settings are not available")
			return
		}
		writeJSON(w, http.StatusOK, toSettingsJSON(m))
	})
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		if m == nil {
			errorJSON(w, http.StatusServiceUnavailable, "settings are not available")
			return
		}
		var req struct {
			LocalNetworks []string           `json:"localNetworks"`
			Retention     map[string]float64 `json:"retentionDays"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		var u settings.Update
		if req.LocalNetworks != nil {
			u.LocalNetworks = []netip.Prefix{}
			for _, s := range req.LocalNetworks {
				p, err := netip.ParsePrefix(strings.TrimSpace(s))
				if err != nil {
					errorJSON(w, http.StatusBadRequest, s+" is not a prefix such as 192.168.88.0/24")
					return
				}
				u.LocalNetworks = append(u.LocalNetworks, p)
			}
		}
		for name, days := range req.Retention {
			d := time.Duration(days * float64(24*time.Hour))
			switch name {
			case "raw":
				u.Raw = &d
			case "minute":
				u.Minute = &d
			case "hour":
				u.Hour = &d
			default:
				errorJSON(w, http.StatusBadRequest, "retention is raw, minute or hour")
				return
			}
		}
		_, err := m.Update(r.Context(), u)
		switch {
		case errors.Is(err, settings.ErrInvalid), errors.Is(err, settings.ErrLocked):
			errorJSON(w, http.StatusBadRequest, err.Error())
		case err != nil:
			opts.Logger.Error("saving settings", "err", err)
			errorJSON(w, http.StatusInternalServerError, "saving the settings failed")
		default:
			writeJSON(w, http.StatusOK, toSettingsJSON(m))
		}
	})
}

// AppStoreHolder reports the App Store and its health. *appstore.Holder implements it.
type AppStoreHolder interface {
	Get() *appstore.Store
	Health() appstore.Health
}
