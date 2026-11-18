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
	if cfg.RetentionRaw != 7*24*time.Hour || cfg.RetentionMinute != 90*24*time.Hour || cfg.RetentionHour != 730*24*time.Hour || cfg.WatermarkOverride != 0 {
		t.Errorf("retention %v %v %v, watermark %v", cfg.RetentionRaw, cfg.RetentionMinute, cfg.RetentionHour, cfg.WatermarkOverride)
	}
}

func TestParseDuration(t *testing.T) {
	for in, want := range map[string]time.Duration{"7d": 7 * 24 * time.Hour, "36h": 36 * time.Hour, "90m": 90 * time.Minute} {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("%s: %v %v", in, got, err)
		}
	}
	if _, err := ParseDuration("xd"); err == nil {
		t.Error("expected an error")
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
		{"FLOWMON_RETENTION_RAW": "forever"},
		{"FLOWMON_WATERMARK": "0s"},
	} {
		if _, err := fromLookup(lookupFrom(env)); err == nil {
			t.Errorf("%v: expected an error", env)
		}
	}
}
