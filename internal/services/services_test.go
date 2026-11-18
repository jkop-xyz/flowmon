package services

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
)

type fakeStore struct{ names map[Port]appstore.ServiceName }

func (f *fakeStore) ServiceNames(context.Context) ([]appstore.ServiceName, error) {
	var out []appstore.ServiceName
	for _, n := range f.names {
		out = append(out, n)
	}
	return out, nil
}

func (f *fakeStore) SetServiceName(_ context.Context, proto uint8, port uint16, name string, now time.Time) (appstore.ServiceName, error) {
	n := appstore.ServiceName{Protocol: proto, Port: port, Name: name, UpdatedAt: now}
	f.names[Port{proto, port}] = n
	return n, nil
}

func (f *fakeStore) DeleteServiceName(_ context.Context, proto uint8, port uint16) error {
	if _, ok := f.names[Port{proto, port}]; !ok {
		return appstore.ErrNotFound
	}
	delete(f.names, Port{proto, port})
	return nil
}

func TestIANA(t *testing.T) {
	for p, want := range map[Port]string{{6, 443}: "https", {17, 53}: "domain", {6, 80}: "http", {132, 443}: "https", {17, 123}: "ntp"} {
		if name, _, ok := IANA(p); !ok || name != want {
			t.Errorf("%v: %q %v, want %q", p, name, ok, want)
		}
	}
	if _, _, ok := IANA(Port{6, 51820}); ok {
		t.Error("unassigned port has a name")
	}
	if _, _, ok := IANA(Port{1, 443}); ok {
		t.Error("ICMP has a Service Name")
	}
}

func TestParsePort(t *testing.T) {
	for in, want := range map[string]Port{"6/443": {6, 443}, "443": {0, 443}, "17/53": {17, 53}} {
		if p, err := ParsePort(in); err != nil || p != want || p.String() != in {
			t.Errorf("%q: %v %v", in, p, err)
		}
	}
	for _, in := range []string{"", "tcp/443", "6/0", "6/70000", "300/1"} {
		if _, err := ParsePort(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestNamesUserWinsOverIANA(t *testing.T) {
	store := &fakeStore{names: map[Port]appstore.ServiceName{}}
	var current Store
	d := NewDirectory(func() Store { return current })
	ctx := context.Background()
	if _, err := d.Set(ctx, Port{6, 8006}, "proxmox"); !errors.Is(err, ErrUnavailable) {
		t.Errorf("without an App Store: %v", err)
	}
	current = store
	for _, name := range []string{" ", "a\tb", "this name is far too long to be a service name"} {
		if _, err := d.Set(ctx, Port{6, 8006}, name); !errors.Is(err, ErrInvalid) {
			t.Errorf("%q: %v", name, err)
		}
	}
	if _, err := d.Set(ctx, Port{6, 8006}, " proxmox "); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Set(ctx, Port{0, 443}, "my https"); err != nil {
		t.Fatal(err)
	}
	got := d.Names(ctx, []Port{{6, 8006}, {17, 8006}, {6, 443}, {17, 53}, {6, 51820}})
	want := map[Port]Name{
		{6, 8006}:  {Name: "proxmox", Source: SourceUser},
		{17, 8006}: {Name: "wpl-disc", Description: "World Programming analytics discovery", Source: SourceIANA},
		{6, 443}:   {Name: "my https", Source: SourceUser},
		{17, 53}:   {Name: "domain", Description: "Domain Name Server", Source: SourceIANA},
		{6, 51820}: {},
	}
	for p, w := range want {
		if got[p] != w {
			t.Errorf("%v: %+v, want %+v", p, got[p], w)
		}
	}
	if err := d.Delete(ctx, Port{6, 8006}); err != nil {
		t.Fatal(err)
	}
	if n := d.Names(ctx, []Port{{6, 8006}})[Port{6, 8006}]; n.Source != SourceIANA {
		t.Errorf("after delete: %+v", n)
	}
}
