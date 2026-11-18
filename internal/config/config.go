// Package config reads Flowmon's process configuration from the environment.
//
// Only settings needed before the App Store is reachable live here. Everything else is
// stored in the App Store, seeded from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is the process configuration.
type Config struct {
	// HTTPAddr is the address the web UI and API listen on, for example ":8080" or "127.0.0.1:8080".
	HTTPAddr string
	// DataDir holds everything Flowmon keeps on disk.
	DataDir string
	// LogLevel is the minimum level that is logged.
	LogLevel slog.Level
	// LogFormat is "text" or "json".
	LogFormat string
	// FlowUDPAddrs are the UDP addresses flow export is received on.
	FlowUDPAddrs []string
	// MaxExporters caps how many Exporters are tracked.
	MaxExporters int
	// DebugCapture, when set, records received datagrams to this pcap path.
	DebugCapture            string
	DebugCaptureMaxBytes    int64
	DebugCaptureMaxDuration time.Duration
	DebugCaptureFiles       int
	// ExtraLocalNetworks are added to the default Local Networks (comma-separated CIDRs).
	ExtraLocalNetworks string
	// FlowStoreMemoryLimit is DuckDB's memory_limit, e.g. "1GB".
	FlowStoreMemoryLimit string
	// FlowStoreThreads is DuckDB's thread count; 0 lets DuckDB decide.
	FlowStoreThreads int
}

// Defaults for the environment variables read by FromEnv.
const (
	DefaultHTTPAddr = ":8080"
	DefaultDataDir  = "/data"
	// DefaultFlowUDPAddrs are the NetFlow port (where RouterOS points by default) and the
	// IANA IPFIX port. Both accept any version; the datagram header says which it is.
	DefaultFlowUDPAddrs = ":2055,:4739"
)

// FromEnv reads the configuration from FLOWMON_* environment variables.
func FromEnv() (Config, error) {
	return fromLookup(os.LookupEnv)
}

func fromLookup(lookup func(string) (string, bool)) (Config, error) {
	get := func(key, fallback string) string {
		if v, ok := lookup(key); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
		return fallback
	}

	cfg := Config{
		HTTPAddr:  get("FLOWMON_HTTP_ADDR", DefaultHTTPAddr),
		DataDir:   get("FLOWMON_DATA_DIR", DefaultDataDir),
		LogFormat: strings.ToLower(get("FLOWMON_LOG_FORMAT", "text")),
	}

	for _, a := range strings.Split(get("FLOWMON_FLOW_UDP_ADDRS", DefaultFlowUDPAddrs), ",") {
		if a = strings.TrimSpace(a); a != "" {
			cfg.FlowUDPAddrs = append(cfg.FlowUDPAddrs, a)
		}
	}

	var err error
	if cfg.MaxExporters, err = positiveInt(get("FLOWMON_MAX_EXPORTERS", "64")); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_MAX_EXPORTERS: %w", err)
	}
	cfg.DebugCapture = get("FLOWMON_DEBUG_CAPTURE", "")
	maxMB, err := positiveInt(get("FLOWMON_DEBUG_CAPTURE_MAX_MB", "64"))
	if err != nil {
		return Config{}, fmt.Errorf("FLOWMON_DEBUG_CAPTURE_MAX_MB: %w", err)
	}
	cfg.DebugCaptureMaxBytes = int64(maxMB) << 20
	if cfg.DebugCaptureMaxDuration, err = time.ParseDuration(get("FLOWMON_DEBUG_CAPTURE_MAX_DURATION", "1h")); err != nil || cfg.DebugCaptureMaxDuration <= 0 {
		return Config{}, fmt.Errorf("FLOWMON_DEBUG_CAPTURE_MAX_DURATION: must be a positive duration such as 30m")
	}
	if cfg.DebugCaptureFiles, err = positiveInt(get("FLOWMON_DEBUG_CAPTURE_FILES", "3")); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_DEBUG_CAPTURE_FILES: %w", err)
	}

	cfg.ExtraLocalNetworks = get("FLOWMON_LOCAL_NETWORKS", "")
	cfg.FlowStoreMemoryLimit = get("FLOWMON_FLOW_STORE_MEMORY_LIMIT", "1GB")
	if s := get("FLOWMON_FLOW_STORE_THREADS", ""); s != "" {
		if cfg.FlowStoreThreads, err = positiveInt(s); err != nil {
			return Config{}, fmt.Errorf("FLOWMON_FLOW_STORE_THREADS: %w", err)
		}
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("FLOWMON_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_LOG_LEVEL: %w", err)
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		return Config{}, fmt.Errorf("FLOWMON_LOG_FORMAT: must be \"text\" or \"json\", got %q", cfg.LogFormat)
	}
	return cfg, nil
}

func positiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive integer, got %q", s)
	}
	return n, nil
}
