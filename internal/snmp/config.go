// Package snmp polls Exporters over SNMP for interface names (ifName) and interface
// counters (64-bit ifHC*), and reconciles those counters with Flow bytes.
package snmp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/netip"
	"strconv"

	"github.com/jkop-xyz/flowmon/internal/secret"
)

// Overhead presets: layer-2 bytes per packet that interface counters include and
// IPFIX octet counts don't. They are approximations; Custom sets an exact value.
var OverheadPresets = map[string]int{
	"none":          0,
	"ethernet":      14,
	"ethernet-vlan": 18,
	"pppoe":         22,
}

// Config is an Exporter's SNMP settings. Secrets are never returned by the API.
type Config struct {
	Enabled bool `json:"enabled"`
	// Version is "v2c" or "v3".
	Version string `json:"version"`
	// Target is host or host:port; empty means the Exporter's address, port 161.
	Target    string `json:"target"`
	Community string `json:"community,omitempty"`
	User      string `json:"user,omitempty"`
	// AuthProtocol: "", MD5, SHA, SHA224, SHA256, SHA384, SHA512. PrivProtocol: "", DES, AES, AES192, AES256.
	AuthProtocol string `json:"authProtocol,omitempty"`
	AuthPassword string `json:"authPassword,omitempty"`
	PrivProtocol string `json:"privProtocol,omitempty"`
	PrivPassword string `json:"privPassword,omitempty"`
	// Overhead is a preset name or "custom" with OverheadBytes.
	Overhead      string `json:"overhead"`
	OverheadBytes int    `json:"overheadBytes"`
}

// Validation errors.
var (
	ErrVersion  = errors.New("snmp: version must be v2c or v3")
	ErrV3User   = errors.New("snmp: SNMPv3 needs a user")
	ErrAuth     = errors.New("snmp: unknown authentication protocol")
	ErrPriv     = errors.New("snmp: unknown privacy protocol; privacy also needs authentication")
	ErrOverhead = errors.New("snmp: unknown overhead preset, or custom bytes outside 0–64")
	ErrTarget   = errors.New("snmp: target must be host or host:port")
)

// Validate checks a config.
func (c Config) Validate() error {
	switch c.Version {
	case "v2c":
	case "v3":
		if c.User == "" {
			return ErrV3User
		}
		switch c.AuthProtocol {
		case "", "MD5", "SHA", "SHA224", "SHA256", "SHA384", "SHA512":
		default:
			return ErrAuth
		}
		switch c.PrivProtocol {
		case "":
		case "DES", "AES", "AES192", "AES256":
			if c.AuthProtocol == "" {
				return ErrPriv
			}
		default:
			return ErrPriv
		}
	default:
		return ErrVersion
	}
	if c.Overhead == "custom" {
		if c.OverheadBytes < 0 || c.OverheadBytes > 64 {
			return ErrOverhead
		}
	} else if _, ok := OverheadPresets[c.Overhead]; !ok && c.Overhead != "" {
		return ErrOverhead
	}
	if c.Target != "" {
		if _, _, err := splitTarget(c.Target); err != nil {
			return ErrTarget
		}
	}
	return nil
}

// OverheadPerPacket is the layer-2 bytes subtracted per packet.
func (c Config) OverheadPerPacket() int {
	if c.Overhead == "custom" {
		return c.OverheadBytes
	}
	if c.Overhead == "" {
		return OverheadPresets["ethernet"]
	}
	return OverheadPresets[c.Overhead]
}

func splitTarget(t string) (string, uint16, error) {
	host, port, err := net.SplitHostPort(t)
	if err != nil {
		if _, perr := netip.ParseAddr(t); perr == nil || !containsColon(t) {
			return t, 161, nil
		}
		return "", 0, err
	}
	p, err := strconv.ParseUint(port, 10, 16)
	if err != nil || p == 0 {
		return "", 0, ErrTarget
	}
	return host, uint16(p), nil
}

func containsColon(s string) bool {
	for _, r := range s {
		if r == ':' {
			return true
		}
	}
	return false
}

// Public is the config without secrets, plus which secrets are set.
type Public struct {
	Config
	HasCommunity    bool `json:"hasCommunity"`
	HasAuthPassword bool `json:"hasAuthPassword"`
	HasPrivPassword bool `json:"hasPrivPassword"`
}

// Public strips the secrets.
func (c Config) Public() Public {
	p := Public{Config: c, HasCommunity: c.Community != "", HasAuthPassword: c.AuthPassword != "", HasPrivPassword: c.PrivPassword != ""}
	p.Community, p.AuthPassword, p.PrivPassword = "", "", ""
	return p
}

// Merge keeps existing secrets when an update leaves them empty, so the UI can save
// other fields without re-entering passwords.
func (c Config) Merge(prev Config) Config {
	if c.Community == "" {
		c.Community = prev.Community
	}
	if c.AuthPassword == "" {
		c.AuthPassword = prev.AuthPassword
	}
	if c.PrivPassword == "" {
		c.PrivPassword = prev.PrivPassword
	}
	return c
}

// Persister stores configs as strings. *flowstore.Store implements it.
type Persister interface {
	SNMPConfigs(ctx context.Context) (map[netip.Addr]string, error)
	SetSNMPConfig(ctx context.Context, exporter netip.Addr, cfg string) error
}

// Settings stores configs with their secrets encrypted.
type Settings struct {
	store Persister
	box   *secret.Box
}

// NewSettings returns Settings over a store and key.
func NewSettings(store Persister, box *secret.Box) *Settings {
	return &Settings{store: store, box: box}
}

// All returns every Exporter's config, decrypted.
func (s *Settings) All(ctx context.Context) (map[netip.Addr]Config, error) {
	raw, err := s.store.SNMPConfigs(ctx)
	if err != nil {
		return nil, err
	}
	out := map[netip.Addr]Config{}
	for exp, js := range raw {
		var c Config
		if err := json.Unmarshal([]byte(js), &c); err != nil {
			continue
		}
		for _, f := range []*string{&c.Community, &c.AuthPassword, &c.PrivPassword} {
			if *f, err = s.box.Open(*f); err != nil {
				return nil, err
			}
		}
		out[exp] = c
	}
	return out, nil
}

// Save stores a config with its secrets sealed.
func (s *Settings) Save(ctx context.Context, exporter netip.Addr, c Config) error {
	var err error
	for _, f := range []*string{&c.Community, &c.AuthPassword, &c.PrivPassword} {
		if *f, err = s.box.Seal(*f); err != nil {
			return err
		}
	}
	js, err := json.Marshal(c)
	if err != nil {
		return err
	}
	return s.store.SetSNMPConfig(ctx, exporter, string(js))
}
