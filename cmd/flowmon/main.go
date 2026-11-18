// Command flowmon is the Flowmon collector, API and web UI in one binary.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/healthcheck"
	"github.com/jkop-xyz/flowmon/internal/httpserver"
	"github.com/jkop-xyz/flowmon/web"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const usage = `Usage: flowmon <command>

Commands:
  serve        Run the collector, API and web UI (default)
  healthcheck  Exit 0 if the local server is healthy, 1 otherwise
  version      Print the version

Configuration is read from FLOWMON_* environment variables.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd = args[0]
	}

	switch cmd {
	case "serve":
		return serve(stderr)
	case "healthcheck":
		return check(stderr)
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version)
		return 0
	case "help", "--help", "-h":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n%s", cmd, usage)
		return 2
	}
}

func newLogger(cfg config.Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: cfg.LogLevel}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

func serve(stderr io.Writer) int {
	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 2
	}
	logger := newLogger(cfg, stderr)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := httpserver.NewServer(cfg.HTTPAddr, httpserver.New(httpserver.Options{
		Version: version,
		UI:      web.Dist(),
		Logger:  logger,
	}))

	errCh := make(chan error, 1)
	go func() {
		logger.Info("flowmon starting", "version", version, "http_addr", cfg.HTTPAddr, "data_dir", cfg.DataDir)
		errCh <- srv.ListenAndServe()
	}()

	select {
	case err := <-errCh:
		logger.Error("http server stopped", "err", err)
		return 1
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("shutdown", "err", err)
		return 1
	}
	return 0
}

func check(stderr io.Writer) int {
	cfg, err := config.FromEnv()
	if err != nil {
		fmt.Fprintln(stderr, "configuration error:", err)
		return 1
	}
	url, err := healthcheck.URL(cfg.HTTPAddr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	if err := healthcheck.Check(context.Background(), url); err != nil {
		fmt.Fprintln(stderr, "unhealthy:", err)
		return 1
	}
	return 0
}
