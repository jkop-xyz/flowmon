package httpserver

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/setup"
)

// maxBody bounds JSON request bodies.
const maxBody = 64 << 10

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return false
	}
	return true
}

func errorJSON(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

type userJSON struct {
	Username string `json:"username"`
	IsAdmin  bool   `json:"isAdmin"`
}

// registerAuth adds setup and login endpoints. They are reachable without a session.
func registerAuth(mux *http.ServeMux, opts Options) {
	if opts.Setup == nil || opts.Auth == nil {
		return
	}
	limited := func(w http.ResponseWriter, r *http.Request) bool {
		ip := opts.Auth.Proxies().ClientIP(r).String()
		if ok, wait := opts.Auth.IPLimiter.Allowed(ip, opts.Now()); !ok {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			errorJSON(w, http.StatusTooManyRequests, "too many failed attempts; try again later")
			return true
		}
		return false
	}
	failed := func(r *http.Request) {
		opts.Auth.IPLimiter.Fail(opts.Auth.Proxies().ClientIP(r).String(), opts.Now())
	}
	setupError := func(w http.ResponseWriter, r *http.Request, err error) {
		switch {
		case errors.Is(err, setup.ErrBadToken):
			failed(r)
			errorJSON(w, http.StatusForbidden, "wrong setup token: copy it from Flowmon's logs")
		case errors.Is(err, setup.ErrWrongStep), errors.Is(err, setup.ErrLocked):
			errorJSON(w, http.StatusConflict, strings.TrimPrefix(err.Error(), "setup: "))
		case errors.Is(err, setup.ErrBadUsername), errors.Is(err, auth.ErrWeakPassword), errors.Is(err, appstore.ErrExists):
			errorJSON(w, http.StatusBadRequest, strings.TrimPrefix(strings.TrimPrefix(err.Error(), "setup: "), "appstore: "))
		default:
			// Connection and validation errors: show them, they are what the user must fix.
			// They never contain the password.
			errorJSON(w, http.StatusBadRequest, err.Error())
		}
	}

	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		st, err := opts.Setup.Status(r.Context())
		if err != nil {
			opts.Logger.Error("setup status", "err", err)
			errorJSON(w, http.StatusServiceUnavailable, "the App Store isn't available")
			return
		}
		writeJSON(w, http.StatusOK, st)
	})

	type databaseRequest struct {
		Token    string         `json:"token"`
		Database setup.Database `json:"database"`
	}
	mux.HandleFunc("POST /api/setup/database/test", func(w http.ResponseWriter, r *http.Request) {
		var req databaseRequest
		if limited(w, r) || !readJSON(w, r, &req) {
			return
		}
		if err := opts.Setup.TestDatabase(r.Context(), req.Token, req.Database); err != nil {
			setupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/setup/database", func(w http.ResponseWriter, r *http.Request) {
		var req databaseRequest
		if limited(w, r) || !readJSON(w, r, &req) {
			return
		}
		if err := opts.Setup.ConfigureDatabase(r.Context(), req.Token, req.Database); err != nil {
			setupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("POST /api/setup/admin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Token    string `json:"token"`
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if limited(w, r) || !readJSON(w, r, &req) {
			return
		}
		if err := opts.Setup.CreateAdmin(r.Context(), w, r, req.Token, req.Username, req.Password); err != nil {
			setupError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})

	mux.HandleFunc("POST /api/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !readJSON(w, r, &req) {
			return
		}
		user, err := opts.Auth.Login(r.Context(), w, r, req.Username, req.Password)
		var rl auth.RateLimitError
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]userJSON{"user": {user.Username, user.IsAdmin}})
		case errors.As(err, &rl):
			w.Header().Set("Retry-After", strconv.Itoa(int(rl.RetryAfter.Seconds())+1))
			errorJSON(w, http.StatusTooManyRequests, "too many failed logins; try again later")
		case errors.Is(err, auth.ErrBadCredentials):
			errorJSON(w, http.StatusUnauthorized, "wrong username or password")
		case errors.Is(err, auth.ErrNoStore):
			errorJSON(w, http.StatusServiceUnavailable, "the App Store isn't available")
		default:
			opts.Logger.Error("login", "err", err)
			errorJSON(w, http.StatusServiceUnavailable, "the App Store isn't available")
		}
	})
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		if err := opts.Auth.Logout(r.Context(), w, r); err != nil {
			opts.Logger.Warn("logout", "err", err)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	mux.HandleFunc("GET /api/auth/session", func(w http.ResponseWriter, r *http.Request) {
		user, err := opts.Auth.Authenticate(r.Context(), r)
		if err != nil {
			unauthenticated(w, opts, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]userJSON{"user": {user.Username, user.IsAdmin}})
	})
}

func unauthenticated(w http.ResponseWriter, opts Options, err error) {
	switch {
	case errors.Is(err, auth.ErrNoSession):
		errorJSON(w, http.StatusUnauthorized, "unauthenticated")
	case errors.Is(err, auth.ErrNoStore):
		errorJSON(w, http.StatusUnauthorized, "setup_required")
	default:
		opts.Logger.Error("checking session", "err", err)
		errorJSON(w, http.StatusServiceUnavailable, "the App Store isn't available")
	}
}

// requireSession lets requests through only with a valid session.
func requireSession(next http.Handler, opts Options) http.Handler {
	if opts.Auth == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := opts.Auth.Authenticate(r.Context(), r); err != nil {
			unauthenticated(w, opts, err)
			return
		}
		next.ServeHTTP(w, r)
	})
}
