package geoip

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// SettingsStore keeps settings. *flowstore.Store implements it.
type SettingsStore interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSettings(ctx context.Context, values map[string]string) error
}

// Sealer encrypts the license key at rest. *secret.Box implements it.
type Sealer interface {
	Seal(plaintext string) (string, error)
	Open(sealed string) (string, error)
}

// Storage keys.
const (
	keyDownload   = "geoip_download"
	keyAccountID  = "maxmind_account_id"
	keyLicenseKey = "maxmind_license_key"
)

// Errors from Update.
var (
	ErrLocked  = errors.New("set by an environment variable")
	ErrInvalid = errors.New("invalid GeoIP setting")
)

// Settings are the GeoIP settings edited in the UI, stored in the Flow Store with the
// license key sealed. An environment variable wins and locks its setting.
type Settings struct {
	store SettingsStore
	box   Sealer
	m     *Manager
}

// LoadSettings applies the stored settings to m, over its environment defaults. A
// license key that no longer opens (secret.key was replaced) leaves MaxMind off.
func LoadSettings(ctx context.Context, store SettingsStore, box Sealer, m *Manager) (*Settings, error) {
	s := &Settings{store: store, box: box, m: m}
	cfg := m.config()
	locks := m.opts.Locks
	if !locks.Download {
		if v, ok, err := store.Setting(ctx, keyDownload); err != nil {
			return nil, err
		} else if ok {
			cfg.Download = v == "true"
		}
	}
	if !locks.MaxMind {
		id, ok, err := store.Setting(ctx, keyAccountID)
		if err != nil {
			return nil, err
		}
		sealed, _, err := store.Setting(ctx, keyLicenseKey)
		if err != nil {
			return nil, err
		}
		if ok && id != "" {
			key, err := box.Open(sealed)
			if err != nil {
				m.opts.Logger.Warn("the stored MaxMind license key can't be read (was secret.key replaced?); enter it again in Settings", "err", err)
				cfg.MaxMindAccountID = id
				m.mu.Lock()
				m.maxMindWarning = "The stored MaxMind license key can't be read (secret.key was replaced), so GeoLite2 is off until it is entered again."
				m.mu.Unlock()
			} else {
				cfg.MaxMindAccountID, cfg.MaxMindLicenseKey = id, key
			}
		}
	}
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
	return s, nil
}

// Update is a change; nil fields stay as they are.
type Update struct {
	Download *bool
	// MaxMindAccountID "" removes the account. LicenseKey nil or "" keeps the stored key.
	MaxMindAccountID, MaxMindLicenseKey *string
}

// Status reports the databases and settings.
func (s *Settings) Status() Status { return s.m.Status() }

// RetryNow retries failed downloads now.
func (s *Settings) RetryNow() { s.m.RetryNow() }

// Update validates and saves a change, then applies it.
func (s *Settings) Update(ctx context.Context, u Update) error {
	cfg := s.m.config()
	locks := s.m.opts.Locks
	save := map[string]string{}
	if u.Download != nil {
		if locks.Download {
			return fmt.Errorf("downloads are %w (FLOWMON_GEOIP_DOWNLOAD)", ErrLocked)
		}
		cfg.Download = *u.Download
		save[keyDownload] = strconv.FormatBool(cfg.Download)
	}
	if u.MaxMindAccountID != nil || u.MaxMindLicenseKey != nil {
		if locks.MaxMind {
			return fmt.Errorf("the MaxMind account is %w (FLOWMON_MAXMIND_ACCOUNT_ID)", ErrLocked)
		}
		id := cfg.MaxMindAccountID
		if u.MaxMindAccountID != nil {
			id = strings.TrimSpace(*u.MaxMindAccountID)
		}
		key := cfg.MaxMindLicenseKey
		if u.MaxMindLicenseKey != nil && strings.TrimSpace(*u.MaxMindLicenseKey) != "" {
			key = strings.TrimSpace(*u.MaxMindLicenseKey)
		}
		switch {
		case id == "":
			key = ""
		case !digits(id):
			return fmt.Errorf("%w: the account ID is the number MaxMind shows on its account page", ErrInvalid)
		case key == "":
			return fmt.Errorf("%w: a MaxMind account needs its license key", ErrInvalid)
		case len(key) > 128 || strings.ContainsAny(key, " \t\r\n"):
			return fmt.Errorf("%w: that doesn't look like a license key", ErrInvalid)
		}
		sealed := ""
		if key != "" {
			var err error
			if sealed, err = s.box.Seal(key); err != nil {
				return err
			}
		}
		cfg.MaxMindAccountID, cfg.MaxMindLicenseKey = id, key
		save[keyAccountID], save[keyLicenseKey] = id, sealed
	}
	if len(save) == 0 {
		return nil
	}
	if err := s.store.SetSettings(ctx, save); err != nil {
		return err
	}
	s.m.Configure(cfg)
	return nil
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}
