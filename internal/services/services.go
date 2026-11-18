// Package services gives Service Ports their Service Names (see GLOSSARY.md), such as
// 443/tcp https: a name the user set wins over the IANA registry's.
package services

// To update services.tsv, download service-names-port-numbers.csv from IANA here, then
// run go generate.
//
//go:generate go run ./gen -in service-names-port-numbers.csv -out services.tsv

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/storecache"
)

// services.tsv is generated from IANA's registry: protocol, port, name, description.
//
//go:embed services.tsv
var registry string

// Port is a protocol and port. Protocol 0 means every protocol (user names only).
type Port struct {
	Protocol uint8
	Port     uint16
}

// String writes a Port as the API takes it: "6/443", or "443" for every protocol.
func (p Port) String() string {
	if p.Protocol == 0 {
		return strconv.Itoa(int(p.Port))
	}
	return fmt.Sprintf("%d/%d", p.Protocol, p.Port)
}

// ParsePort reads "protocol/port", or a bare port for every protocol.
func ParsePort(s string) (Port, error) {
	proto, port, ok := strings.Cut(s, "/")
	if !ok {
		proto, port = "0", s
	}
	pr, err1 := strconv.ParseUint(proto, 10, 8)
	po, err2 := strconv.ParseUint(port, 10, 16)
	if err1 != nil || err2 != nil || po == 0 {
		return Port{}, fmt.Errorf("%q is not a protocol/port such as 6/443", s)
	}
	return Port{Protocol: uint8(pr), Port: uint16(po)}, nil
}

type ianaEntry struct{ name, description string }

var (
	ianaOnce  sync.Once
	ianaNames map[Port]ianaEntry
)

// IANA returns the registry's Service Name and description for a protocol and port.
func IANA(p Port) (name, description string, ok bool) {
	ianaOnce.Do(func() {
		ianaNames = make(map[Port]ianaEntry, 12_000)
		sc := bufio.NewScanner(strings.NewReader(registry))
		for sc.Scan() {
			f := strings.SplitN(sc.Text(), "\t", 4)
			if len(f) != 4 {
				continue
			}
			pr, err1 := strconv.ParseUint(f[0], 10, 8)
			po, err2 := strconv.ParseUint(f[1], 10, 16)
			if err1 == nil && err2 == nil {
				ianaNames[Port{uint8(pr), uint16(po)}] = ianaEntry{f[2], f[3]}
			}
		}
	})
	e, ok := ianaNames[p]
	return e.name, e.description, ok
}

// Name sources.
const (
	SourceUser = "user"
	SourceIANA = "iana"
)

// Name is a Service Port's Service Name; empty when it has none.
type Name struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	// Source is user or iana.
	Source string `json:"source,omitempty"`
}

// Store keeps user-defined Service Names. *appstore.Store implements it.
type Store interface {
	ServiceNames(ctx context.Context) ([]appstore.ServiceName, error)
	SetServiceName(ctx context.Context, protocol uint8, port uint16, name string, now time.Time) (appstore.ServiceName, error)
	DeleteServiceName(ctx context.Context, protocol uint8, port uint16) error
}

// Errors from changes.
var (
	ErrUnavailable = errors.New("the App Store is unavailable")
	ErrInvalid     = errors.New("invalid Service Name")
)

// MaxNameLength bounds a user-defined Service Name.
const MaxNameLength = 40

// Directory names Service Ports, with user-defined names held in memory.
type Directory struct {
	store func() Store
	now   func() time.Time
	user  *storecache.Snapshot[map[Port]appstore.ServiceName]
}

// NewDirectory returns a Directory. store returns the App Store, or nil while there is none.
func NewDirectory(store func() Store) *Directory {
	return &Directory{store: store, now: time.Now, user: storecache.New(func(ctx context.Context) (map[Port]appstore.ServiceName, error) {
		s := store()
		if s == nil {
			return nil, ErrUnavailable
		}
		names, err := s.ServiceNames(ctx)
		if err != nil {
			return nil, err
		}
		m := make(map[Port]appstore.ServiceName, len(names))
		for _, n := range names {
			m[Port{n.Protocol, n.Port}] = n
		}
		return m, nil
	})}
}

// UserNames returns the user-defined Service Names.
func (d *Directory) UserNames(ctx context.Context) map[Port]appstore.ServiceName {
	return d.user.Get(ctx)
}

// Names returns the Service Name of each port: the user's for that protocol, then the
// user's for every protocol, then IANA's.
func (d *Directory) Names(ctx context.Context, ports []Port) map[Port]Name {
	user := d.user.Get(ctx)
	out := make(map[Port]Name, len(ports))
	for _, p := range ports {
		if n, ok := user[p]; ok {
			out[p] = Name{Name: n.Name, Source: SourceUser}
		} else if n, ok := user[Port{Port: p.Port}]; ok {
			out[p] = Name{Name: n.Name, Source: SourceUser}
		} else if name, desc, ok := IANA(p); ok {
			out[p] = Name{Name: name, Description: desc, Source: SourceIANA}
		} else {
			out[p] = Name{}
		}
	}
	return out
}

// Set names a Service Port.
func (d *Directory) Set(ctx context.Context, p Port, name string) (appstore.ServiceName, error) {
	name = strings.TrimSpace(name)
	switch {
	case p.Port == 0:
		return appstore.ServiceName{}, fmt.Errorf("%w: port 0 has no service", ErrInvalid)
	case name == "":
		return appstore.ServiceName{}, fmt.Errorf("%w: the name is empty", ErrInvalid)
	case len([]rune(name)) > MaxNameLength:
		return appstore.ServiceName{}, fmt.Errorf("%w: the name is longer than %d characters", ErrInvalid, MaxNameLength)
	case strings.IndexFunc(name, unicode.IsControl) >= 0:
		return appstore.ServiceName{}, fmt.Errorf("%w: the name contains control characters", ErrInvalid)
	}
	s := d.store()
	if s == nil {
		return appstore.ServiceName{}, ErrUnavailable
	}
	n, err := s.SetServiceName(ctx, p.Protocol, p.Port, name, d.now())
	if err != nil {
		return n, err
	}
	_ = d.user.Reload(ctx) // a failed reload is retried on next use
	return n, nil
}

// Delete removes a user-defined Service Name.
func (d *Directory) Delete(ctx context.Context, p Port) error {
	s := d.store()
	if s == nil {
		return ErrUnavailable
	}
	if err := s.DeleteServiceName(ctx, p.Protocol, p.Port); err != nil {
		return err
	}
	_ = d.user.Reload(ctx)
	return nil
}
