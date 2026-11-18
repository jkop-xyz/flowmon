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
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/jkop-xyz/flowmon/internal/capture"
	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/collector"
	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/healthcheck"
	"github.com/jkop-xyz/flowmon/internal/httpserver"
	"github.com/jkop-xyz/flowmon/internal/ingest"
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

	var debugCapture *capture.Writer
	if cfg.DebugCapture != "" {
		debugCapture, err = capture.Open(capture.Options{
			Path:         cfg.DebugCapture,
			MaxFileBytes: cfg.DebugCaptureMaxBytes,
			MaxFiles:     cfg.DebugCaptureFiles,
			MaxDuration:  cfg.DebugCaptureMaxDuration,
		}, time.Now())
		if err != nil {
			logger.Error("debug capture", "err", err)
			return 1
		}
		defer debugCapture.Close()
		logger.Warn("debug capture is on: received datagrams are written to disk and contain private traffic metadata",
			"path", cfg.DebugCapture, "max_duration", cfg.DebugCaptureMaxDuration)
	}

	extra, err := classify.ParseLocalNetworks(cfg.ExtraLocalNetworks)
	if err != nil {
		fmt.Fprintln(stderr, "configuration error: FLOWMON_LOCAL_NETWORKS:", err)
		return 2
	}
	classifier := classify.New(append(classify.DefaultLocalNetworks(), extra...))

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		logger.Error("data directory", "err", err)
		return 1
	}
	store, err := flowstore.Open(ctx, filepath.Join(cfg.DataDir, "flows.duckdb"), flowstore.Options{
		MemoryLimit: cfg.FlowStoreMemoryLimit,
		Threads:     cfg.FlowStoreThreads,
	})
	if err != nil {
		logger.Error("flow store", "err", err)
		return 1
	}
	defer store.Close()

	pipeline := ingest.New(ingest.Options{Writer: store, Classifier: classifier, Logger: logger, WatermarkOverride: cfg.WatermarkOverride})
	pipelineCtx, stopPipeline := context.WithCancel(ctx)
	var pipelineDone sync.WaitGroup
	pipelineDone.Add(1)
	go func() {
		defer pipelineDone.Done()
		pipeline.Run(pipelineCtx)
	}()
	// Runs before store.Close (defers are LIFO), on every exit path: the pipeline stops
	// and writes its last batch before the store closes.
	defer func() {
		stopPipeline()
		pipelineDone.Wait()
	}()

	retention := flowstore.Retention{Raw: cfg.RetentionRaw, Minute: cfg.RetentionMinute, Hour: cfg.RetentionHour}
	go maintain(pipelineCtx, logger, store, pipeline, retention)

	coll := collector.New(collector.Options{
		MaxExporters: cfg.MaxExporters,
		Capture:      debugCapture,
		Logger:       logger,
		Sink:         pipeline,
	})
	for _, addr := range cfg.FlowUDPAddrs {
		if err := coll.ListenUDP(ctx, addr); err != nil {
			logger.Error("collector", "err", err)
			return 1
		}
	}

	srv := httpserver.NewServer(cfg.HTTPAddr, httpserver.New(httpserver.Options{
		Version:   version,
		UI:        web.Dist(),
		Logger:    logger,
		Collector: coll,
		Timing:    pipeline,
		Flows:     store,
		Watermark: pipeline,
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

// maintain compacts Rollups and applies retention every ten minutes.
func maintain(ctx context.Context, logger *slog.Logger, store *flowstore.Store, pipeline *ingest.Pipeline, r flowstore.Retention) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		if err := store.Compact(ctx); err != nil && ctx.Err() == nil {
			logger.Error("compacting rollups", "err", err)
		}
		dropped, err := store.ApplyRetention(ctx, r, time.Now(), pipeline.MaxWatermark())
		if err != nil && ctx.Err() == nil {
			logger.Error("applying retention", "err", err)
		}
		if len(dropped) > 0 {
			logger.Info("retention dropped old data", "tables", dropped)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
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
