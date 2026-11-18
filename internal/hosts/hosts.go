// Package hosts gives Local Hosts their Host Names (see GLOSSARY.md): a Host Label the
// user set wins, then the reverse DNS name from the resolver configured for Local
// Hosts, then the address. Host Labels on a prefix also group a host's rotating
// addresses into one Local Host, identified by that prefix.
package hosts

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/enrichment"
	"github.com/jkop-xyz/flowmon/internal/storecache"
)

// MaxNameLength bounds a Host Label's name.
const MaxNameLength = 64

// ParseKey reads a Local Host as the UI and API name it: an address, or a prefix for a
// Host Label that groups addresses. The result is masked, with IPv4-mapped IPv6
// addresses unmapped.
func ParseKey(s string) (netip.Prefix, error) {
	if !strings.Contains(s, "/") {
		a, err := netip.ParseAddr(s)
		if err != nil || a.Zone() != "" {
			return netip.Prefix{}, fmt.Errorf("%q is not an IP address or prefix", s)
		}
		a = a.Unmap()
		return netip.PrefixFrom(a, a.BitLen()), nil
	}
	p, err := netip.ParsePrefix(s)
	if err != nil {
		return netip.Prefix{}, fmt.Errorf("%q is not an IP address or prefix", s)
	}
	if a := p.Addr(); a.Is4In6() && p.Bits() >= 96 {
		p = netip.PrefixFrom(a.Unmap(), p.Bits()-96)
	}
	return p.Masked(), nil
}

// KeyString is the inverse of ParseKey: a single address is written without its length.
func KeyString(p netip.Prefix) string {
	if p.IsSingleIP() {
		return p.Addr().String()
	}
	return p.String()
}

// LabelStore keeps Host Labels. *appstore.Store implements it.
type LabelStore interface {
	HostLabels(ctx context.Context) ([]appstore.HostLabel, error)
	CreateHostLabel(ctx context.Context, prefix netip.Prefix, name string, now time.Time) (appstore.HostLabel, error)
	UpdateHostLabel(ctx context.Context, id string, prefix netip.Prefix, name string, now time.Time) (appstore.HostLabel, error)
	DeleteHostLabel(ctx context.Context, id string) error
}

// Resolver answers reverse DNS from cache and queues the rest. *enrichment.ReverseDNS
// implements it.
type Resolver interface {
	Names(ips []netip.Addr) map[netip.Addr]enrichment.Name
}

// Errors from label changes.
var (
	ErrUnavailable = errors.New("the App Store is unavailable")
	ErrInvalid     = errors.New("invalid Host Label")
)

// Directory holds Host Labels in memory, longest prefix first, and names Local Hosts.
type Directory struct {
	// store returns the App Store, or nil while there is none.
	store  func() LabelStore
	dns    Resolver
	now    func() time.Time
	labels *storecache.Snapshot[[]appstore.HostLabel]
}

// NewDirectory returns a Directory. dns may be nil (no reverse DNS for Local Hosts).
func NewDirectory(store func() LabelStore, dns Resolver) *Directory {
	d := &Directory{store: store, dns: dns, now: time.Now}
	d.labels = storecache.New(func(ctx context.Context) ([]appstore.HostLabel, error) {
		s := store()
		if s == nil {
			return nil, ErrUnavailable
		}
		labels, err := s.HostLabels(ctx)
		if err != nil {
			return nil, err
		}
		slices.SortStableFunc(labels, func(a, b appstore.HostLabel) int { return b.Prefix.Bits() - a.Prefix.Bits() })
		return labels, nil
	})
	return d
}

// Labels returns every Host Label, longest prefix first. Labels are loaded on first use
// and after every change; while the App Store is down the last ones loaded are used.
func (d *Directory) Labels(ctx context.Context) []appstore.HostLabel { return d.labels.Get(ctx) }

// Reload reads the labels again.
func (d *Directory) Reload(ctx context.Context) error { return d.labels.Reload(ctx) }

// Match returns the Host Label for an address: the one with the longest prefix that
// contains it.
func Match(labels []appstore.HostLabel, a netip.Addr) (appstore.HostLabel, bool) {
	a = a.Unmap()
	for _, l := range labels {
		if l.Prefix.Contains(a) {
			return l, true
		}
	}
	return appstore.HostLabel{}, false
}

// Groups returns every Host Label's prefix. The addresses whose longest matching label
// is on a wider prefix count as one Local Host; a label on one address keeps it apart.
func (d *Directory) Groups(ctx context.Context) []netip.Prefix {
	var out []netip.Prefix
	for _, l := range d.Labels(ctx) {
		out = append(out, l.Prefix)
	}
	return out
}

// Name sources.
const (
	SourceLabel = "label"
	SourceDNS   = "dns"
)

// Name is a Local Host's Host Name. Without a name the UI shows the address.
type Name struct {
	Name string `json:"name,omitempty"`
	// Source is label or dns; empty when there is no name.
	Source string `json:"source,omitempty"`
	// Status is the reverse DNS status (see enrichment), or resolved for a label.
	Status string `json:"status"`
	// Label is the Host Label that named it, which may be on a wider prefix.
	Label *LabelRef `json:"label,omitempty"`
}

// LabelRef identifies a Host Label.
type LabelRef struct {
	ID     string `json:"id"`
	Prefix string `json:"prefix"`
	Name   string `json:"name"`
}

// Names returns the Host Name of each Local Host. Addresses without a label are looked
// up in reverse DNS, which answers from cache and reports the rest as pending.
func (d *Directory) Names(ctx context.Context, keys []netip.Prefix) map[netip.Prefix]Name {
	labels := d.Labels(ctx)
	out := make(map[netip.Prefix]Name, len(keys))
	var lookup []netip.Addr
	for _, k := range keys {
		var l appstore.HostLabel
		var ok bool
		if k.IsSingleIP() {
			l, ok = Match(labels, k.Addr())
		} else {
			i := slices.IndexFunc(labels, func(l appstore.HostLabel) bool { return l.Prefix == k })
			if ok = i >= 0; ok {
				l = labels[i]
			}
		}
		switch {
		case ok:
			out[k] = Name{Name: l.Name, Source: SourceLabel, Status: enrichment.NameResolved,
				Label: &LabelRef{ID: l.ID, Prefix: KeyString(l.Prefix), Name: l.Name}}
		case k.IsSingleIP() && d.dns != nil:
			lookup = append(lookup, k.Addr())
		default:
			out[k] = Name{Status: enrichment.NameNone}
		}
	}
	if len(lookup) > 0 {
		for a, n := range d.dns.Names(lookup) {
			name := Name{Name: n.Name, Status: n.Status}
			if n.Name != "" {
				name.Source = SourceDNS
			}
			out[netip.PrefixFrom(a, a.BitLen())] = name
		}
	}
	return out
}

// LabelInput is a new or changed Host Label.
type LabelInput struct {
	Prefix string
	Name   string
}

func (in LabelInput) parse() (netip.Prefix, string, error) {
	p, err := ParseKey(strings.TrimSpace(in.Prefix))
	if err != nil {
		return p, "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if p.Bits() == 0 {
		return p, "", fmt.Errorf("%w: a label can't cover every address", ErrInvalid)
	}
	name := strings.TrimSpace(in.Name)
	switch {
	case name == "":
		return p, "", fmt.Errorf("%w: the name is empty", ErrInvalid)
	case len([]rune(name)) > MaxNameLength:
		return p, "", fmt.Errorf("%w: the name is longer than %d characters", ErrInvalid, MaxNameLength)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return p, "", fmt.Errorf("%w: the name contains control characters", ErrInvalid)
	}
	return p, name, nil
}

// Create adds a Host Label.
func (d *Directory) Create(ctx context.Context, in LabelInput) (appstore.HostLabel, error) {
	p, name, err := in.parse()
	if err != nil {
		return appstore.HostLabel{}, err
	}
	return d.change(ctx, func(s LabelStore) (appstore.HostLabel, error) { return s.CreateHostLabel(ctx, p, name, d.now()) })
}

// Update changes a Host Label.
func (d *Directory) Update(ctx context.Context, id string, in LabelInput) (appstore.HostLabel, error) {
	p, name, err := in.parse()
	if err != nil {
		return appstore.HostLabel{}, err
	}
	return d.change(ctx, func(s LabelStore) (appstore.HostLabel, error) { return s.UpdateHostLabel(ctx, id, p, name, d.now()) })
}

// Delete removes a Host Label.
func (d *Directory) Delete(ctx context.Context, id string) error {
	_, err := d.change(ctx, func(s LabelStore) (appstore.HostLabel, error) {
		return appstore.HostLabel{}, s.DeleteHostLabel(ctx, id)
	})
	return err
}

func (d *Directory) change(ctx context.Context, f func(LabelStore) (appstore.HostLabel, error)) (appstore.HostLabel, error) {
	s := d.store()
	if s == nil {
		return appstore.HostLabel{}, ErrUnavailable
	}
	l, err := f(s)
	if err != nil {
		return l, err
	}
	// The change is saved; a failed reload is retried on the next change.
	_ = d.Reload(ctx)
	return l, nil
}
