// Package interfaces keeps each Exporter's Interfaces: interface number → name mappings
// with the time range each was valid, and Interface Roles by name (ADR 0003). Interfaces
// are identified by name, because numbers can change after a reconnect or reboot.
package interfaces

import (
	"context"
	"errors"
	"net/netip"
	"sort"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/classify"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// Role is an Interface Role (GLOSSARY.md). The empty role means none is set.
type Role string

// Roles.
const (
	RoleWAN    Role = "wan"
	RoleLAN    Role = "lan"
	RoleTunnel Role = "tunnel"
)

// Name sources, from most to least trusted.
const (
	SourceUser    = "user"
	SourceSNMP    = "snmp"
	SourceOptions = "options"
)

func priority(source string) int {
	switch source {
	case SourceUser:
		return 3
	case SourceSNMP:
		return 2
	}
	return 1
}

// Persister stores mappings and roles. *flowstore.Store implements it.
type Persister interface {
	InterfaceMappings(ctx context.Context) ([]flowstore.MappingRecord, error)
	ReplaceMapping(ctx context.Context, m flowstore.MappingRecord) error
	InterfaceRoles(ctx context.Context) (map[netip.Addr]map[string]string, error)
	SetInterfaceRoles(ctx context.Context, exporter netip.Addr, roles map[string]string) error
}

type mapping struct {
	name     string
	source   string
	from, to time.Time // to is zero while current
}

// Change is a mapping change, or a name an Exporter reported that conflicts with one
// the user set. Shown as a warning on the Exporters page.
type Change struct {
	Index    uint32    `json:"index"`
	OldName  string    `json:"oldName"`
	NewName  string    `json:"newName"`
	Source   string    `json:"source"`
	At       time.Time `json:"at"`
	Conflict bool      `json:"conflict"`
}

// ChangeWindow is how long changes stay listed.
const ChangeWindow = 7 * 24 * time.Hour

// Registry is safe for concurrent use.
type Registry struct {
	store Persister

	mu       sync.RWMutex
	mappings map[netip.Addr]map[uint32][]mapping // oldest first
	roles    map[netip.Addr]map[string]Role
	changes  map[netip.Addr][]Change
}

// Load returns a Registry with the stored mappings and roles.
func Load(ctx context.Context, store Persister) (*Registry, error) {
	r := &Registry{store: store, mappings: map[netip.Addr]map[uint32][]mapping{}, roles: map[netip.Addr]map[string]Role{}, changes: map[netip.Addr][]Change{}}
	if store == nil {
		return r, nil
	}
	ms, err := store.InterfaceMappings(ctx)
	if err != nil {
		return nil, err
	}
	for _, m := range ms {
		if r.mappings[m.Exporter] == nil {
			r.mappings[m.Exporter] = map[uint32][]mapping{}
		}
		r.mappings[m.Exporter][m.Index] = append(r.mappings[m.Exporter][m.Index], mapping{name: m.Name, source: m.Source, from: m.ValidFrom, to: m.ValidTo})
	}
	roles, err := store.InterfaceRoles(ctx)
	if err != nil {
		return nil, err
	}
	for exp, byName := range roles {
		r.roles[exp] = map[string]Role{}
		for name, role := range byName {
			r.roles[exp][name] = Role(role)
		}
	}
	return r, nil
}

func current(hist []mapping) (mapping, bool) {
	if len(hist) == 0 || !hist[len(hist)-1].to.IsZero() {
		return mapping{}, false
	}
	return hist[len(hist)-1], true
}

// ErrBadName is returned for an empty or overly long interface name.
var ErrBadName = errors.New("interfaces: name must be 1–64 characters")

// SetName maps an interface number to a name from a source. A name the user set is only
// replaced by the user; a different name from SNMP or an Options Template is recorded as
// a conflict instead. It reports whether the mapping changed.
func (r *Registry) SetName(ctx context.Context, exporter netip.Addr, index uint32, name, source string, now time.Time) (bool, error) {
	if name == "" || len([]rune(name)) > 64 {
		return false, ErrBadName
	}
	exporter = exporter.Unmap()
	r.mu.Lock()
	cur, ok := current(r.mappings[exporter][index])
	switch {
	case ok && cur.name == name:
		r.mu.Unlock()
		return false, nil
	case ok && priority(source) < priority(cur.source):
		r.recordLocked(exporter, Change{Index: index, OldName: cur.name, NewName: name, Source: source, At: now.UTC(), Conflict: true})
		r.mu.Unlock()
		return false, nil
	}
	r.mu.Unlock()

	if r.store != nil {
		if err := r.store.ReplaceMapping(ctx, flowstore.MappingRecord{Exporter: exporter, Index: index, Name: name, Source: source, ValidFrom: now}); err != nil {
			return false, err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.mappings[exporter] == nil {
		r.mappings[exporter] = map[uint32][]mapping{}
	}
	hist := r.mappings[exporter][index]
	if len(hist) > 0 && hist[len(hist)-1].to.IsZero() {
		hist[len(hist)-1].to = now.UTC()
	}
	r.mappings[exporter][index] = append(hist, mapping{name: name, source: source, from: now.UTC()})
	if ok {
		r.recordLocked(exporter, Change{Index: index, OldName: cur.name, NewName: name, Source: source, At: now.UTC()})
	}
	return true, nil
}

func (r *Registry) recordLocked(exporter netip.Addr, c Change) {
	list := r.changes[exporter]
	if n := len(list); n > 0 && list[n-1].Index == c.Index && list[n-1].NewName == c.NewName && list[n-1].Conflict == c.Conflict {
		list[n-1].At = c.At // the same conflict reported again
		return
	}
	if len(list) >= 100 {
		list = list[1:]
	}
	r.changes[exporter] = append(list, c)
}

// NameAt returns the name of an interface number at time t.
func (r *Registry) NameAt(exporter netip.Addr, index uint32, t time.Time) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	hist := r.mappings[exporter.Unmap()][index]
	for i := len(hist) - 1; i >= 0; i-- {
		m := hist[i]
		if !t.Before(m.from) && (m.to.IsZero() || t.Before(m.to)) {
			return m.name, true
		}
	}
	// Before the first mapping, the earliest name is the best guess.
	if len(hist) > 0 && t.Before(hist[0].from) {
		return hist[0].name, true
	}
	return "", false
}

// Interface is an Exporter's current interface.
type Interface struct {
	Index  uint32 `json:"index"`
	Name   string `json:"name"`
	Source string `json:"source"`
	Role   Role   `json:"role"`
}

// Current returns an Exporter's current mappings, by interface number.
func (r *Registry) Current(exporter netip.Addr) []Interface {
	r.mu.RLock()
	defer r.mu.RUnlock()
	exporter = exporter.Unmap()
	var out []Interface
	for idx, hist := range r.mappings[exporter] {
		if m, ok := current(hist); ok {
			out = append(out, Interface{Index: idx, Name: m.name, Source: m.source, Role: r.roles[exporter][m.name]})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Index < out[j].Index })
	return out
}

// Roles returns an Exporter's roles by interface name.
func (r *Registry) Roles(exporter netip.Addr) map[string]Role {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := map[string]Role{}
	for k, v := range r.roles[exporter.Unmap()] {
		out[k] = v
	}
	return out
}

// ErrBadRole is returned for an unknown role.
var ErrBadRole = errors.New("interfaces: role must be wan, lan or tunnel")

// SetRoles replaces an Exporter's Interface Roles. Interfaces not listed have no role.
func (r *Registry) SetRoles(ctx context.Context, exporter netip.Addr, roles map[string]Role) error {
	exporter = exporter.Unmap()
	stored := map[string]string{}
	for name, role := range roles {
		switch role {
		case RoleWAN, RoleLAN, RoleTunnel:
			stored[name] = string(role)
		case "":
		default:
			return ErrBadRole
		}
	}
	if r.store != nil {
		if err := r.store.SetInterfaceRoles(ctx, exporter, stored); err != nil {
			return err
		}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.roles[exporter] = map[string]Role{}
	for name, role := range stored {
		r.roles[exporter][name] = Role(role)
	}
	return nil
}

// Changes returns mapping changes and conflicts from the last ChangeWindow.
func (r *Registry) Changes(exporter netip.Addr, now time.Time) []Change {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := []Change{}
	for _, c := range r.changes[exporter.Unmap()] {
		if now.Sub(c.At) <= ChangeWindow {
			out = append(out, c)
		}
	}
	return out
}

// RoleAt is the classifier's view of an interface: interface 0 is the Exporter itself.
func (r *Registry) RoleAt(exporter netip.Addr, index uint32, at time.Time) classify.InterfaceRole {
	if index == 0 {
		return classify.IfSelf
	}
	name, ok := r.NameAt(exporter, index, at)
	if !ok {
		return classify.IfUnknown
	}
	r.mu.RLock()
	role := r.roles[exporter.Unmap()][name]
	r.mu.RUnlock()
	switch role {
	case RoleWAN:
		return classify.IfWAN
	case RoleLAN:
		return classify.IfLAN
	case RoleTunnel:
		return classify.IfTunnel
	}
	return classify.IfUnknown
}
