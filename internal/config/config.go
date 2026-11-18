// Package config reads Flowmon's process configuration from the environment.
//
// Only settings needed before the App Store is reachable live here. Everything else is
// stored in the App Store, seeded from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
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
}

// Defaults for the environment variables read by FromEnv.
const (
	DefaultHTTPAddr = ":8080"
	DefaultDataDir  = "/data"
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

	if err := cfg.LogLevel.UnmarshalText([]byte(get("FLOWMON_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_LOG_LEVEL: %w", err)
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		return Config{}, fmt.Errorf("FLOWMON_LOG_FORMAT: must be \"text\" or \"json\", got %q", cfg.LogFormat)
	}
	return cfg, nil
}
