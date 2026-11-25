// Package settings holds the collection settings edited in the UI: Local Networks and
// retention. They are stored in the Flow Store, not the App Store, so collection never
// depends on the App Store (ADR 0001), and held in memory. A setting given by
// environment variable wins and can't be changed in the UI.
package settings

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// Store keeps settings. *flowstore.Store implements it.
type Store interface {
	Setting(ctx context.Context, key string) (string, bool, error)
	SetSettings(ctx context.Context, values map[string]string) error
}

// Storage keys.
const (
	keyLocalNetworks   = "local_networks"
	keyRetentionRaw    = "retention_raw"
	keyRetentionMinute = "retention_minute"
	keyRetentionHour   = "retention_hour"
	keyRetentionDNS    = "retention_dns"
	keyLineDown        = "line_down_bps"
	keyLineUp          = "line_up_bps"
)

// Values are the settings in effect.
type Values struct {
	LocalNetworks []netip.Prefix
	Retention     flowstore.Retention
	// Line is the internet line's speed, which the Overview measures WAN load against.
	Line Line
}

// Line is an internet line's speed in bits per second; 0 is unknown.
type Line struct{ Down, Up uint64 }

// Locks say which settings are set by environment variable.
type Locks struct {
	LocalNetworks                                bool
	RetentionRaw, RetentionMinute, RetentionHour bool
	RetentionDNS                                 bool
}

// Env is what the environment says: defaults to seed with, and which are locked.
type Env struct {
	// LocalNetworks are the default Local Networks plus FLOWMON_LOCAL_NETWORKS.
	LocalNetworks []netip.Prefix
	Retention     flowstore.Retention
	Locks         Locks
}

// FromConfig reads the environment's settings.
func FromConfig(cfg config.Config) (Env, error) {
	extra, err := classify.ParseLocalNetworks(cfg.ExtraLocalNetworks)
	if err != nil {
		return Env{}, fmt.Errorf("FLOWMON_LOCAL_NETWORKS: %w", err)
	}
	return Env{
		LocalNetworks: dedupe(append(classify.DefaultLocalNetworks(), extra...)),
		Retention:     flowstore.Retention{Raw: cfg.RetentionRaw, Minute: cfg.RetentionMinute, Hour: cfg.RetentionHour, DNS: cfg.RetentionDNS},
		Locks: Locks{LocalNetworks: len(extra) > 0, RetentionRaw: cfg.RetentionFromEnv.Raw,
			RetentionMinute: cfg.RetentionFromEnv.Minute, RetentionHour: cfg.RetentionFromEnv.Hour, RetentionDNS: cfg.RetentionFromEnv.DNS},
	}, nil
}

// Manager holds the settings in effect and changes them.
type Manager struct {
	store Store
	env   Env

	mu       sync.Mutex
	current  Values
	onChange []func(Values)
}

// Load reads the stored settings. Unset or locked ones come from env.
func Load(ctx context.Context, store Store, env Env) (*Manager, error) {
	m := &Manager{store: store, env: env, current: Values{LocalNetworks: env.LocalNetworks, Retention: env.Retention}}
	if !env.Locks.LocalNetworks {
		if v, ok, err := store.Setting(ctx, keyLocalNetworks); err != nil {
			return nil, err
		} else if ok {
			nets, err := classify.ParseLocalNetworks(v)
			if err != nil {
				return nil, fmt.Errorf("stored Local Networks: %w", err)
			}
			m.current.LocalNetworks = nets
		}
	}
	for key, into := range map[string]*uint64{keyLineDown: &m.current.Line.Down, keyLineUp: &m.current.Line.Up} {
		v, ok, err := store.Setting(ctx, key)
		if err != nil {
			return nil, err
		}
		if ok {
			if *into, err = strconv.ParseUint(v, 10, 64); err != nil {
				return nil, fmt.Errorf("stored %s: %w", key, err)
			}
		}
	}
	for _, r := range m.retentionFields(&m.current.Retention) {
		if r.locked {
			continue
		}
		v, ok, err := store.Setting(ctx, r.key)
		if err != nil {
			return nil, err
		}
		if ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				return nil, fmt.Errorf("stored %s: %w", r.key, err)
			}
			*r.into = d
		}
	}
	return m, nil
}

type retentionField struct {
	key    string
	into   *time.Duration
	locked bool
}

func (m *Manager) retentionFields(r *flowstore.Retention) []retentionField {
	return []retentionField{
		{keyRetentionRaw, &r.Raw, m.env.Locks.RetentionRaw},
		{keyRetentionMinute, &r.Minute, m.env.Locks.RetentionMinute},
		{keyRetentionHour, &r.Hour, m.env.Locks.RetentionHour},
		{keyRetentionDNS, &r.DNS, m.env.Locks.RetentionDNS},
	}
}

// Current returns the settings in effect.
func (m *Manager) Current() Values {
	m.mu.Lock()
	defer m.mu.Unlock()
	v := m.current
	v.LocalNetworks = slices.Clone(v.LocalNetworks)
	return v
}

// Locks says which settings are set by environment variable.
func (m *Manager) Locks() Locks { return m.env.Locks }

// DefaultLocalNetworks are what Local Networks reset to.
func (m *Manager) DefaultLocalNetworks() []netip.Prefix { return slices.Clone(m.env.LocalNetworks) }

// OnChange registers f to run after every change, with the new settings.
func (m *Manager) OnChange(f func(Values)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onChange = append(m.onChange, f)
}

// Errors from Update.
var (
	ErrLocked  = errors.New("set by an environment variable")
	ErrInvalid = errors.New("invalid setting")
)

// Update is a change; nil fields stay as they are.
type Update struct {
	LocalNetworks          []netip.Prefix
	Raw, Minute, Hour, DNS *time.Duration
	Line                   *Line
}

// MaxLine bounds a line's speed: 100 Gbit/s.
const MaxLine = 100_000_000_000

// MinRetention and MaxRetention bound retention: a day, so the newest data is always
// rolled up and listed before it can go, and the longest range a query accepts.
const (
	MinRetention = 24 * time.Hour
	MaxRetention = flowstore.MaxRange
)

// Update validates and saves a change, then applies it.
func (m *Manager) Update(ctx context.Context, u Update) (Values, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := m.current
	save := map[string]string{}
	if u.LocalNetworks != nil {
		if m.env.Locks.LocalNetworks {
			return Values{}, fmt.Errorf("Local Networks are %w (FLOWMON_LOCAL_NETWORKS)", ErrLocked)
		}
		nets := dedupe(u.LocalNetworks)
		if len(nets) == 0 {
			return Values{}, fmt.Errorf("%w: at least one Local Network is needed", ErrInvalid)
		}
		for _, p := range nets {
			if p.Bits() == 0 {
				return Values{}, fmt.Errorf("%w: %s would make every address local", ErrInvalid, p)
			}
		}
		next.LocalNetworks = nets
		save[keyLocalNetworks] = joinPrefixes(nets)
	}
	for _, f := range []struct {
		value  *time.Duration
		field  retentionField
		envVar string
	}{
		{u.Raw, m.retentionFields(&next.Retention)[0], "FLOWMON_RETENTION_RAW"},
		{u.Minute, m.retentionFields(&next.Retention)[1], "FLOWMON_RETENTION_MINUTE"},
		{u.Hour, m.retentionFields(&next.Retention)[2], "FLOWMON_RETENTION_HOUR"},
		{u.DNS, m.retentionFields(&next.Retention)[3], "FLOWMON_RETENTION_DNS"},
	} {
		if f.value == nil {
			continue
		}
		if f.field.locked {
			return Values{}, fmt.Errorf("this retention is %w (%s)", ErrLocked, f.envVar)
		}
		if *f.value < MinRetention || *f.value > MaxRetention {
			return Values{}, fmt.Errorf("%w: retention must be between 1 and %d days", ErrInvalid, int(MaxRetention.Hours()/24))
		}
		*f.field.into = *f.value
		save[f.field.key] = f.value.String()
	}
	if u.Line != nil {
		if u.Line.Down > MaxLine || u.Line.Up > MaxLine {
			return Values{}, fmt.Errorf("%w: a line is at most 100 Gbit/s", ErrInvalid)
		}
		next.Line = *u.Line
		save[keyLineDown], save[keyLineUp] = strconv.FormatUint(u.Line.Down, 10), strconv.FormatUint(u.Line.Up, 10)
	}
	r := next.Retention
	if r.Raw > r.Minute || r.Minute > r.Hour {
		return Values{}, fmt.Errorf("%w: keep raw Flows no longer than minute Rollups, and minute Rollups no longer than hour Rollups", ErrInvalid)
	}
	if len(save) == 0 {
		return m.current, nil
	}
	if err := m.store.SetSettings(ctx, save); err != nil {
		return Values{}, err
	}
	m.current = next
	for _, f := range m.onChange {
		f(next)
	}
	return next, nil
}

func dedupe(nets []netip.Prefix) []netip.Prefix {
	var out []netip.Prefix
	for _, p := range nets {
		p = p.Masked()
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	return out
}

func joinPrefixes(nets []netip.Prefix) string {
	s := make([]string, len(nets))
	for i, p := range nets {
		s[i] = p.String()
	}
	return strings.Join(s, ",")
}
