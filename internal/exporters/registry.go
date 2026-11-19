// Package exporters decides which Exporters Flowmon accepts. A new Exporter is a
// Pending Exporter until an admin approves it, unless auto-accept or the allowlist lets
// it in. Decisions are kept in memory for the collector and persisted to the Flow Store.
package exporters

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// Status is an Exporter's approval state.
type Status string

// Statuses.
const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	// StatusIgnored Exporters are dropped without decoding.
	StatusIgnored Status = "ignored"
)

// Role is where an Exporter sits (GLOSSARY.md). Only Edge exists in v1.0.
type Role string

// Roles.
const (
	RoleEdge     Role = "edge"
	RoleInternal Role = "internal"
)

// ClassificationMode is how an Exporter's Flows are sorted into local and remote.
type ClassificationMode string

// Classification modes. By interface needs Interface Roles, which come later.
const (
	ByAddress   ClassificationMode = "address"
	ByInterface ClassificationMode = "interface"
)

// Config is a decision about one Exporter.
type Config struct {
	Address            netip.Addr         `json:"address"`
	Status             Status             `json:"status"`
	Role               Role               `json:"role"`
	ClassificationMode ClassificationMode `json:"classificationMode"`
	Name               string             `json:"name"`
	DecidedAt          time.Time          `json:"decidedAt"`
	DecidedBy          string             `json:"decidedBy"`
	// SamplingRate is N for an Exporter that samples 1 in N packets without announcing
	// it; 0 when unset. A rate the Exporter announces wins.
	SamplingRate uint32 `json:"samplingRate"`
}

// Persister stores decisions. *flowstore.Store implements it.
type Persister interface {
	ExporterRecords(ctx context.Context) ([]flowstore.ExporterRecord, error)
	SaveExporterRecord(ctx context.Context, r flowstore.ExporterRecord) error
	DeleteExporterRecord(ctx context.Context, addr netip.Addr) error
}

// Options configure a Registry.
type Options struct {
	Persister Persister
	// AutoAccept approves every new Exporter (FLOWMON_EXPORTER_AUTO_ACCEPT).
	AutoAccept bool
	// Allowlist approves new Exporters whose address is in one of these prefixes.
	Allowlist []netip.Prefix
	Now       func() time.Time
}

// Registry is safe for concurrent use.
type Registry struct {
	opts    Options
	mu      sync.RWMutex
	configs map[netip.Addr]Config
}

// Load returns a Registry with the stored decisions.
func Load(ctx context.Context, opts Options) (*Registry, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	r := &Registry{opts: opts, configs: map[netip.Addr]Config{}}
	if opts.Persister == nil {
		return r, nil
	}
	recs, err := opts.Persister.ExporterRecords(ctx)
	if err != nil {
		return nil, fmt.Errorf("loading exporters: %w", err)
	}
	for _, rec := range recs {
		r.configs[rec.Address] = Config{
			Address: rec.Address, Status: Status(rec.Status), Role: Role(rec.Role),
			ClassificationMode: ClassificationMode(rec.ClassificationMode), Name: rec.Name,
			DecidedAt: rec.DecidedAt, DecidedBy: rec.DecidedBy, SamplingRate: rec.SamplingRate,
		}
	}
	return r, nil
}

func (r *Registry) allowlisted(a netip.Addr) bool {
	for _, p := range r.opts.Allowlist {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Status returns an Exporter's status. An undecided Exporter that auto-accept or the
// allowlist lets in is approved on the spot, and the decision is persisted so it
// survives turning auto-accept off.
func (r *Registry) Status(addr netip.Addr) Status {
	addr = addr.Unmap()
	r.mu.RLock()
	c, ok := r.configs[addr]
	r.mu.RUnlock()
	if ok {
		return c.Status
	}
	if !r.opts.AutoAccept && !r.allowlisted(addr) {
		return StatusPending
	}
	by := "auto-accept"
	if r.allowlisted(addr) {
		by = "allowlist"
	}
	c = Config{Address: addr, Status: StatusApproved, Role: RoleEdge, ClassificationMode: ByAddress, DecidedAt: r.opts.Now().UTC(), DecidedBy: by}
	r.mu.Lock()
	if existing, ok := r.configs[addr]; ok {
		r.mu.Unlock()
		return existing.Status
	}
	r.configs[addr] = c
	r.mu.Unlock()
	if r.opts.Persister != nil {
		// Best effort from the collector's path: the in-memory decision already applies.
		go func() { _ = r.opts.Persister.SaveExporterRecord(context.Background(), record(c)) }()
	}
	return StatusApproved
}

// Config returns the decision for an Exporter, if there is one.
func (r *Registry) Config(addr netip.Addr) (Config, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.configs[addr.Unmap()]
	return c, ok
}

// Configs returns every decision, ordered by address.
func (r *Registry) Configs() []Config {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Config, 0, len(r.configs))
	for _, c := range r.configs {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address.Less(out[j].Address) })
	return out
}

// Errors from decisions.
var (
	ErrUnsupportedRole = errors.New("exporters: only the Edge role is available in this version")
	ErrUnsupportedMode = errors.New("exporters: classification mode must be address or interface")
	ErrNameTooLong     = errors.New("exporters: name must be at most 64 characters")
	ErrNotApproved     = errors.New("exporters: approve the Exporter first")
)

// Approve approves an Exporter with a role and Classification Mode.
func (r *Registry) Approve(ctx context.Context, addr netip.Addr, role Role, mode ClassificationMode, name, by string) (Config, error) {
	if role == "" {
		role = RoleEdge
	}
	if mode == "" {
		mode = ByAddress
	}
	switch {
	case role != RoleEdge:
		return Config{}, ErrUnsupportedRole
	case mode != ByAddress && mode != ByInterface:
		return Config{}, ErrUnsupportedMode
	case len([]rune(name)) > 64:
		return Config{}, ErrNameTooLong
	}
	return r.decide(ctx, Config{Address: addr.Unmap(), Status: StatusApproved, Role: role, ClassificationMode: mode, Name: name, DecidedBy: by})
}

// SetMode changes an approved Exporter's Classification Mode.
func (r *Registry) SetMode(ctx context.Context, addr netip.Addr, mode ClassificationMode, by string) (Config, error) {
	if mode != ByAddress && mode != ByInterface {
		return Config{}, ErrUnsupportedMode
	}
	c, ok := r.Config(addr)
	if !ok || c.Status != StatusApproved {
		return Config{}, ErrNotApproved
	}
	c.ClassificationMode, c.DecidedBy = mode, by
	return r.decide(ctx, c)
}

// ErrSamplingRate is returned for a sampling rate out of range.
var ErrSamplingRate = fmt.Errorf("exporters: sampling rate must be between 1 and %d (or 0 for none)", flow.MaxSamplingRate)

// SetSamplingRate sets the rate an approved Exporter samples at when it doesn't announce
// one: N for 1 in N packets, 0 (or 1) for none.
func (r *Registry) SetSamplingRate(ctx context.Context, addr netip.Addr, rate uint32, by string) (Config, error) {
	if rate > flow.MaxSamplingRate {
		return Config{}, ErrSamplingRate
	}
	if rate == 1 {
		rate = 0
	}
	c, ok := r.Config(addr)
	if !ok || c.Status != StatusApproved {
		return Config{}, ErrNotApproved
	}
	c.SamplingRate, c.DecidedBy = rate, by
	return r.decide(ctx, c)
}

// SamplingRate is the rate set by hand for an Exporter, 0 if none.
func (r *Registry) SamplingRate(addr netip.Addr) uint32 {
	c, _ := r.Config(addr)
	return c.SamplingRate
}

// ByInterface reports whether an Exporter classifies by interface.
func (r *Registry) ByInterface(addr netip.Addr) bool {
	c, ok := r.Config(addr)
	return ok && c.Status == StatusApproved && c.ClassificationMode == ByInterface
}

// Ignore stops accepting anything from an Exporter.
func (r *Registry) Ignore(ctx context.Context, addr netip.Addr, by string) (Config, error) {
	c, _ := r.Config(addr)
	c.Address, c.Status, c.DecidedBy = addr.Unmap(), StatusIgnored, by
	if c.Role == "" {
		c.Role, c.ClassificationMode = RoleEdge, ByAddress
	}
	return r.decide(ctx, c)
}

// Forget removes the decision, so the Exporter is pending again.
func (r *Registry) Forget(ctx context.Context, addr netip.Addr) error {
	addr = addr.Unmap()
	if r.opts.Persister != nil {
		if err := r.opts.Persister.DeleteExporterRecord(ctx, addr); err != nil {
			return err
		}
	}
	r.mu.Lock()
	delete(r.configs, addr)
	r.mu.Unlock()
	return nil
}

func (r *Registry) decide(ctx context.Context, c Config) (Config, error) {
	c.DecidedAt = r.opts.Now().UTC()
	if r.opts.Persister != nil {
		if err := r.opts.Persister.SaveExporterRecord(ctx, record(c)); err != nil {
			return Config{}, err
		}
	}
	r.mu.Lock()
	r.configs[c.Address] = c
	r.mu.Unlock()
	return c, nil
}

func record(c Config) flowstore.ExporterRecord {
	return flowstore.ExporterRecord{Address: c.Address, Status: string(c.Status), Role: string(c.Role),
		ClassificationMode: string(c.ClassificationMode), Name: c.Name, DecidedAt: c.DecidedAt, DecidedBy: c.DecidedBy,
		SamplingRate: c.SamplingRate}
}
