package httpserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/jkop-xyz/flowmon/internal/geoip"
)

// GeoIP is the GeoIP databases' status and settings (*geoip.Settings).
type GeoIP interface {
	Status() geoip.Status
	Update(ctx context.Context, u geoip.Update) error
	RetryNow()
}

func registerGeoIP(mux *http.ServeMux, opts Options) {
	mux.HandleFunc("GET /api/geoip", func(w http.ResponseWriter, _ *http.Request) {
		if opts.GeoIP == nil {
			writeJSON(w, http.StatusOK, geoip.Status{Databases: []geoip.DatabaseStatus{}, Warnings: []string{}})
			return
		}
		writeJSON(w, http.StatusOK, opts.GeoIP.Status())
	})
	mux.HandleFunc("PUT /api/geoip", func(w http.ResponseWriter, r *http.Request) {
		if opts.GeoIP == nil {
			errorJSON(w, http.StatusServiceUnavailable, "GeoIP is not available")
			return
		}
		var req struct {
			Download          *bool   `json:"download"`
			MaxMindAccountID  *string `json:"maxMindAccountId"`
			MaxMindLicenseKey *string `json:"maxMindLicenseKey"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		err := opts.GeoIP.Update(r.Context(), geoip.Update{Download: req.Download, MaxMindAccountID: req.MaxMindAccountID, MaxMindLicenseKey: req.MaxMindLicenseKey})
		switch {
		case errors.Is(err, geoip.ErrLocked):
			errorJSON(w, http.StatusConflict, err.Error())
		case errors.Is(err, geoip.ErrInvalid):
			errorJSON(w, http.StatusBadRequest, err.Error())
		case err != nil:
			opts.Logger.Error("saving GeoIP settings", "err", err)
			errorJSON(w, http.StatusInternalServerError, "couldn't save the GeoIP settings")
		default:
			writeJSON(w, http.StatusOK, opts.GeoIP.Status())
		}
	})
	mux.HandleFunc("POST /api/geoip/refresh", func(w http.ResponseWriter, _ *http.Request) {
		if opts.GeoIP == nil {
			errorJSON(w, http.StatusServiceUnavailable, "GeoIP is not available")
			return
		}
		opts.GeoIP.RetryNow()
		writeJSON(w, http.StatusAccepted, opts.GeoIP.Status())
	})
}
