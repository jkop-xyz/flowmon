// Package httpserver serves Flowmon's JSON API and the embedded web UI.
package httpserver

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/collector"
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
}

// New returns the root handler: the API under /api/ and the single-page UI everywhere else.
func New(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "version": opts.Version})
	})
	mux.HandleFunc("GET /api/exporters", func(w http.ResponseWriter, _ *http.Request) {
		if opts.Collector == nil {
			writeJSON(w, http.StatusOK, collector.Status{Listeners: []string{}, Exporters: []collector.ExporterStatus{}})
			return
		}
		writeJSON(w, http.StatusOK, opts.Collector.Status())
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
	})
	mux.Handle("/", spaHandler(opts.UI, opts.Logger))
	return mux
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
