package config

import (
	"log/slog"
	"testing"
)

func lookupFrom(env map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
}

func TestDefaults(t *testing.T) {
	cfg, err := fromLookup(lookupFrom(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr || cfg.DataDir != DefaultDataDir {
		t.Errorf("got addr %q, data dir %q", cfg.HTTPAddr, cfg.DataDir)
	}
	if cfg.LogLevel != slog.LevelInfo || cfg.LogFormat != "text" {
		t.Errorf("got level %v, format %q", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestOverrides(t *testing.T) {
	cfg, err := fromLookup(lookupFrom(map[string]string{
		"FLOWMON_HTTP_ADDR":  "127.0.0.1:9000",
		"FLOWMON_DATA_DIR":   " /srv/flowmon ",
		"FLOWMON_LOG_LEVEL":  "debug",
		"FLOWMON_LOG_FORMAT": "JSON",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:9000" || cfg.DataDir != "/srv/flowmon" {
		t.Errorf("got addr %q, data dir %q", cfg.HTTPAddr, cfg.DataDir)
	}
	if cfg.LogLevel != slog.LevelDebug || cfg.LogFormat != "json" {
		t.Errorf("got level %v, format %q", cfg.LogLevel, cfg.LogFormat)
	}
}

func TestEmptyValueUsesDefault(t *testing.T) {
	cfg, err := fromLookup(lookupFrom(map[string]string{"FLOWMON_HTTP_ADDR": "  "}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != DefaultHTTPAddr {
		t.Errorf("got %q", cfg.HTTPAddr)
	}
}

func TestInvalidValues(t *testing.T) {
	for _, env := range []map[string]string{
		{"FLOWMON_LOG_LEVEL": "loud"},
		{"FLOWMON_LOG_FORMAT": "xml"},
	} {
		if _, err := fromLookup(lookupFrom(env)); err == nil {
			t.Errorf("%v: expected an error", env)
		}
	}
}
