// Package httpserver serves Flowmon's JSON API and the embedded web UI.
package httpserver

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/netip"
	"path"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/exporters"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/setup"
)

// Options configures the handler.
type Options struct {
	// Version is reported by the health endpoint.
	Version string
	// UI is the built frontend, rooted at the directory that contains index.html.
	UI fs.FS
	// Logger receives request errors.
	Logger *slog.Logger
	// Collector provides the Exporters page. Nil serves an empty list.
	Collector interface{ Status() collector.Status }
	// Timing adds per-Exporter timing to the Exporters page.
	Timing interface {
		Timing(exporter netip.Addr) *collector.TimingStatus
	}
	// Flows answers the Overview's queries.
	Flows interface {
		Bandwidth(ctx context.Context, q flowstore.BandwidthQuery) (flowstore.BandwidthResult, error)
		Top(ctx context.Context, q flowstore.TopQuery) (flowstore.TopResult, error)
	}
	// Watermark says how recent data may still change, for "incomplete" shading.
	Watermark interface{ MaxWatermark() time.Duration }
	// Auth and Setup enable login and first-run setup. Without Auth, the API is open
	// (tests only).
	Auth  *auth.Manager
	Setup *setup.Controller
	// Exporters holds approvals; nil treats every Exporter as approved (tests).
	Exporters *exporters.Registry
	// Interfaces wires Interfaces, Interface Roles and reprocessing.
	Interfaces *InterfaceOptions
	// SNMP wires SNMP settings and Reconciliation.
	SNMP *SNMPOptions
	// Now returns the current time; tests replace it.
	Now func() time.Time
}

// New returns the root handler: the API under /api/ and the single-page UI everywhere
// else. Everything under /api/ except health, setup and login needs a session. Unsafe
// cross-origin browser requests are rejected (CSRF protection).
func New(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}

	public := http.NewServeMux()
	public.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": opts.Version})
	})
	registerAuth(public, opts)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/exporters", func(w http.ResponseWriter, _ *http.Request) {
		if opts.Collector == nil {
			writeJSON(w, http.StatusOK, collector.Status{Listeners: []string{}, Exporters: []collector.ExporterStatus{}})
			return
		}
		st := opts.Collector.Status()
		mergeExporterStatus(&st, opts.Exporters)
		if opts.Timing != nil {
			for i := range st.Exporters {
				if addr, err := netip.ParseAddr(st.Exporters[i].Address); err == nil {
					st.Exporters[i].Timing = opts.Timing.Timing(addr)
				}
			}
		}
		writeJSON(w, http.StatusOK, st)
	})
	registerExporters(mux, opts)
	registerInterfaces(mux, opts)
	registerSNMP(mux, opts)
	mux.HandleFunc("GET /api/overview/bandwidth", func(w http.ResponseWriter, r *http.Request) {
		if opts.Flows == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "flow store not available"})
			return
		}
		q, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		res, err := opts.Flows.Bandwidth(r.Context(), q)
		if queryError(w, opts.Logger, "bandwidth", err) {
			return
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: res, IncompleteFrom: incompleteFrom(opts),
			Changes: classificationChanges(r, opts, q.From, q.To, q.Exporter)})
	})
	mux.HandleFunc("GET /api/overview/top", func(w http.ResponseWriter, r *http.Request) {
		if opts.Flows == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "flow store not available"})
			return
		}
		bq, err := bandwidthQuery(r, opts.Now())
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		q := flowstore.TopQuery{Dimension: flowstore.TopDimension(r.URL.Query().Get("dimension")), From: bq.From, To: bq.To, Exporter: bq.Exporter, Limit: 10}
		res, err := opts.Flows.Top(r.Context(), q)
		if errors.Is(err, flowstore.ErrUnknownDimension) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "dimension must be hosts, protocols or ports"})
			return
		}
		if queryError(w, opts.Logger, "top", err) {
			return
		}
		writeJSON(w, http.StatusOK, withIncomplete{Result: res, IncompleteFrom: incompleteFrom(opts)})
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	public.Handle("/api/", requireSession(mux, opts))
	public.Handle("/", spaHandler(opts.UI, opts.Logger))
	return http.NewCrossOriginProtection().Handler(public)
}

// NewServer wraps the handler with timeouts suitable for a small self-hosted dashboard.
func NewServer(addr string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

// withIncomplete adds the "incomplete" boundary to a query result.
type withIncomplete struct {
	Result any `json:"result"`
	// IncompleteFrom is the time after which data may still change (now minus the
	// largest Watermark). The UI shades it.
	IncompleteFrom time.Time `json:"incompleteFrom"`
	// Changes marks when an Exporter's classification changed (charts only).
	Changes []flowstore.ClassificationChange `json:"changes,omitempty"`
}

func incompleteFrom(opts Options) time.Time {
	w := 2 * time.Minute
	if opts.Watermark != nil {
		w = opts.Watermark.MaxWatermark()
	}
	return opts.Now().Add(-w).UTC()
}

// queryError writes the response for a failed query and reports whether there was one.
func queryError(w http.ResponseWriter, logger *slog.Logger, what string, err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, flowstore.ErrInvalidRange):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "the time range must be positive and at most 2 years"})
	default:
		logger.Error(what+" query", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "query failed"})
	}
	return true
}

// bandwidthQuery reads from, to (RFC 3339, default the last hour), exporter and points.
func bandwidthQuery(r *http.Request, now time.Time) (flowstore.BandwidthQuery, error) {
	v := r.URL.Query()
	q := flowstore.BandwidthQuery{From: now.Add(-time.Hour), To: now}
	var err error
	if s := v.Get("from"); s != "" {
		if q.From, err = time.Parse(time.RFC3339, s); err != nil {
			return q, errors.New("from must be an RFC 3339 time")
		}
	}
	if s := v.Get("to"); s != "" {
		if q.To, err = time.Parse(time.RFC3339, s); err != nil {
			return q, errors.New("to must be an RFC 3339 time")
		}
	}
	if s := v.Get("exporter"); s != "" {
		if q.Exporter, err = netip.ParseAddr(s); err != nil {
			return q, errors.New("exporter must be an IP address")
		}
	}
	return q, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const notBuiltPage = `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Flowmon</title></head>
<body style="font-family:system-ui,sans-serif;padding:2rem">
<h1>Flowmon</h1>
<p>The web UI hasn't been built into this binary. Run <code>npm run build</code> in <code>web/</code>, then rebuild.</p>
</body></html>
`

// staticExtensions are file types the UI build can contain. A missing path with one of these
// extensions is a 404 rather than a client-side route.
var staticExtensions = map[string]bool{
	".js": true, ".mjs": true, ".css": true, ".map": true, ".html": true, ".json": true,
	".svg": true, ".png": true, ".jpg": true, ".jpeg": true, ".gif": true, ".webp": true, ".ico": true,
	".woff": true, ".woff2": true, ".ttf": true, ".txt": true, ".webmanifest": true,
}

// spaHandler serves files from ui and falls back to index.html for client-side routes.
func spaHandler(ui fs.FS, logger *slog.Logger) http.Handler {
	fileServer := http.FileServerFS(ui)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" && name != "index.html" {
			if info, err := fs.Stat(ui, name); err == nil && !info.IsDir() {
				if strings.HasPrefix(name, "assets/") {
					// Vite puts a content hash in every asset file name.
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
			if strings.HasPrefix(name, "assets/") || staticExtensions[path.Ext(name)] {
				// A missing file such as /favicon.png is a 404, not the app. Routes can
				// contain dots (/hosts/10.0.0.1), so only known file types count as files.
				http.NotFound(w, r)
				return
			}
		}

		index, err := fs.ReadFile(ui, "index.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		if errors.Is(err, fs.ErrNotExist) {
			_, _ = w.Write([]byte(notBuiltPage))
			return
		}
		if err != nil {
			logger.Error("reading index.html", "err", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(index)
	})
}
