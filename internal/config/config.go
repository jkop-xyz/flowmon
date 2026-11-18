// Package config reads Flowmon's process configuration from the environment.
//
// Only settings needed before the App Store is reachable live here. Everything else is
// stored in the App Store, seeded from environment variables.
package config

import (
	"fmt"
	"log/slog"
	"net"
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
	// Retention of raw Flows and of minute and hour Rollups.
	RetentionRaw, RetentionMinute, RetentionHour time.Duration
	// RetentionFromEnv says which retention values were set by environment variable,
	// which locks them in Settings.
	RetentionFromEnv struct{ Raw, Minute, Hour bool }
	// WatermarkOverride replaces the learned Watermark when set.
	WatermarkOverride time.Duration
	// DatabaseURL, when set, decides the App Store and locks the setup screen.
	DatabaseURL string
	// DatabasePasswordFile holds the App Store password (e.g. a Docker secret).
	DatabasePasswordFile string
	// AdminUsername and AdminPassword create the admin account if none exists.
	AdminUsername, AdminPassword string
	// TrustedProxies are CIDRs whose X-Forwarded-For/-Proto headers are believed.
	TrustedProxies string
	// SecureCookies is "auto", "always" or "never".
	SecureCookies string
	// ExporterAutoAccept approves every new Exporter without asking.
	ExporterAutoAccept bool
	// ExporterAllowlist approves new Exporters in these CIDRs without asking.
	ExporterAllowlist string
	// GeoIPDir holds MMDB files the user provides; they win over downloads.
	GeoIPDir string
	// GeoIPDownload enables downloading GeoIP databases (MaxMind with an account, else DB-IP Lite).
	GeoIPDownload bool
	// MaxMindAccountID and MaxMindLicenseKey enable GeoLite2 downloads.
	MaxMindAccountID, MaxMindLicenseKey string
	// UDPReceiveBuffer is the socket receive buffer the collector asks for, in bytes.
	UDPReceiveBuffer int
	// NoFlowsWarning is how long without Flows before the UI warns; 0 turns it off.
	NoFlowsWarning time.Duration
	// MetricsToken, when set, is required as a bearer token on /metrics.
	MetricsToken string
	// LocalDNSServer (host:port) answers reverse DNS for Local Hosts, such as the router
	// or a Pi-hole. Empty uses the system resolver.
	LocalDNSServer string
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

	for _, r := range []struct {
		key, def string
		into     *time.Duration
		fromEnv  *bool
	}{
		{"FLOWMON_RETENTION_RAW", "7d", &cfg.RetentionRaw, &cfg.RetentionFromEnv.Raw},
		{"FLOWMON_RETENTION_MINUTE", "90d", &cfg.RetentionMinute, &cfg.RetentionFromEnv.Minute},
		{"FLOWMON_RETENTION_HOUR", "730d", &cfg.RetentionHour, &cfg.RetentionFromEnv.Hour},
	} {
		*r.fromEnv = get(r.key, "") != ""
		if *r.into, err = ParseDuration(get(r.key, r.def)); err != nil || *r.into <= 0 {
			return Config{}, fmt.Errorf("%s: must be a positive duration such as 7d or 12h", r.key)
		}
	}
	if s := get("FLOWMON_WATERMARK", ""); s != "" {
		if cfg.WatermarkOverride, err = ParseDuration(s); err != nil || cfg.WatermarkOverride <= 0 {
			return Config{}, fmt.Errorf("FLOWMON_WATERMARK: must be a positive duration such as 5m")
		}
	}

	cfg.DatabaseURL = get("FLOWMON_DATABASE_URL", "")
	cfg.DatabasePasswordFile = get("FLOWMON_DATABASE_PASSWORD_FILE", "")
	cfg.AdminUsername = get("FLOWMON_ADMIN_USERNAME", "")
	cfg.AdminPassword = get("FLOWMON_ADMIN_PASSWORD", "")
	if f := get("FLOWMON_ADMIN_PASSWORD_FILE", ""); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return Config{}, fmt.Errorf("FLOWMON_ADMIN_PASSWORD_FILE: %w", err)
		}
		cfg.AdminPassword = strings.TrimRight(string(b), "\r\n")
	}
	cfg.TrustedProxies = get("FLOWMON_TRUSTED_PROXIES", "")
	if cfg.ExporterAutoAccept, err = strconv.ParseBool(get("FLOWMON_EXPORTER_AUTO_ACCEPT", "false")); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_EXPORTER_AUTO_ACCEPT: must be true or false")
	}
	cfg.ExporterAllowlist = get("FLOWMON_EXPORTER_ALLOWLIST", "")

	cfg.GeoIPDir = get("FLOWMON_GEOIP_DIR", strings.TrimRight(cfg.DataDir, "/")+"/geoip")
	if cfg.GeoIPDownload, err = strconv.ParseBool(get("FLOWMON_GEOIP_DOWNLOAD", "true")); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_GEOIP_DOWNLOAD: must be true or false")
	}
	cfg.MaxMindAccountID = get("FLOWMON_MAXMIND_ACCOUNT_ID", "")
	cfg.MaxMindLicenseKey = get("FLOWMON_MAXMIND_LICENSE_KEY", "")
	if f := get("FLOWMON_MAXMIND_LICENSE_KEY_FILE", ""); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return Config{}, fmt.Errorf("FLOWMON_MAXMIND_LICENSE_KEY_FILE: %w", err)
		}
		cfg.MaxMindLicenseKey = strings.TrimSpace(string(b))
	}
	if (cfg.MaxMindAccountID == "") != (cfg.MaxMindLicenseKey == "") {
		return Config{}, fmt.Errorf("FLOWMON_MAXMIND_ACCOUNT_ID and FLOWMON_MAXMIND_LICENSE_KEY must be set together")
	}
	mb, err := positiveInt(get("FLOWMON_UDP_RECEIVE_BUFFER_MB", "8"))
	if err != nil || mb > 1024 {
		return Config{}, fmt.Errorf("FLOWMON_UDP_RECEIVE_BUFFER_MB: must be between 1 and 1024")
	}
	cfg.UDPReceiveBuffer = mb << 20
	switch s := strings.ToLower(get("FLOWMON_NO_FLOWS_WARNING", "5m")); s {
	case "0", "off", "false":
	default:
		if cfg.NoFlowsWarning, err = ParseDuration(s); err != nil || cfg.NoFlowsWarning <= 0 {
			return Config{}, fmt.Errorf("FLOWMON_NO_FLOWS_WARNING: must be a duration such as 5m, or off")
		}
	}
	cfg.MetricsToken = get("FLOWMON_METRICS_TOKEN", "")
	if f := get("FLOWMON_METRICS_TOKEN_FILE", ""); f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return Config{}, fmt.Errorf("FLOWMON_METRICS_TOKEN_FILE: %w", err)
		}
		cfg.MetricsToken = strings.TrimSpace(string(b))
	}
	if s := strings.TrimSpace(get("FLOWMON_LOCAL_DNS_SERVER", "")); s != "" {
		if cfg.LocalDNSServer, err = dnsServer(s); err != nil {
			return Config{}, fmt.Errorf("FLOWMON_LOCAL_DNS_SERVER: %w", err)
		}
	}
	cfg.SecureCookies = strings.ToLower(get("FLOWMON_SECURE_COOKIES", "auto"))
	if cfg.SecureCookies != "auto" && cfg.SecureCookies != "always" && cfg.SecureCookies != "never" {
		return Config{}, fmt.Errorf("FLOWMON_SECURE_COOKIES: must be auto, always or never")
	}

	if err := cfg.LogLevel.UnmarshalText([]byte(get("FLOWMON_LOG_LEVEL", "info"))); err != nil {
		return Config{}, fmt.Errorf("FLOWMON_LOG_LEVEL: %w", err)
	}
	if cfg.LogFormat != "text" && cfg.LogFormat != "json" {
		return Config{}, fmt.Errorf("FLOWMON_LOG_FORMAT: must be \"text\" or \"json\", got %q", cfg.LogFormat)
	}
	return cfg, nil
}

// dnsServer reads a DNS server as host, host:port, IPv6 address or [IPv6]:port; the port
// defaults to 53.
func dnsServer(s string) (string, error) {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		host, port = strings.Trim(s, "[]"), "53"
	}
	if host == "" || strings.ContainsAny(host, "/ ") {
		return "", fmt.Errorf("must be a host or address, optionally with a port, got %q", s)
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return "", fmt.Errorf("invalid port in %q", s)
	}
	return net.JoinHostPort(host, port), nil
}

func positiveInt(s string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("must be a positive integer, got %q", s)
	}
	return n, nil
}

// ParseDuration accepts Go durations ("36h", "90m") plus whole days ("7d").
func ParseDuration(s string) (time.Duration, error) {
	if d, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}
