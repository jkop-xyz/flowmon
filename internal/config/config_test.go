package config

import (
	"log/slog"
	"testing"
	"time"
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
	if len(cfg.FlowUDPAddrs) != 2 || cfg.FlowUDPAddrs[0] != ":2055" || cfg.FlowUDPAddrs[1] != ":4739" {
		t.Errorf("flow addrs %v", cfg.FlowUDPAddrs)
	}
	if cfg.DebugCapture != "" || cfg.DebugCaptureMaxBytes != 64<<20 || cfg.DebugCaptureMaxDuration != time.Hour || cfg.DebugCaptureFiles != 3 {
		t.Errorf("debug capture defaults %+v", cfg)
	}
	if cfg.MaxExporters != 64 {
		t.Errorf("max exporters %d", cfg.MaxExporters)
	}
}

func TestOverrides(t *testing.T) {
	cfg, err := fromLookup(lookupFrom(map[string]string{
		"FLOWMON_HTTP_ADDR":      "127.0.0.1:9000",
		"FLOWMON_DATA_DIR":       " /srv/flowmon ",
		"FLOWMON_LOG_LEVEL":      "debug",
		"FLOWMON_LOG_FORMAT":     "JSON",
		"FLOWMON_FLOW_UDP_ADDRS": " 127.0.0.1:9995 ,, ",
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
	if len(cfg.FlowUDPAddrs) != 1 || cfg.FlowUDPAddrs[0] != "127.0.0.1:9995" {
		t.Errorf("flow addrs %v", cfg.FlowUDPAddrs)
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
		{"FLOWMON_MAX_EXPORTERS": "0"},
		{"FLOWMON_DEBUG_CAPTURE_MAX_MB": "lots"},
		{"FLOWMON_DEBUG_CAPTURE_MAX_DURATION": "-5m"},
	} {
		if _, err := fromLookup(lookupFrom(env)); err == nil {
			t.Errorf("%v: expected an error", env)
		}
	}
}
