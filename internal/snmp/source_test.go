package snmp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// fakeTable serves tables in turn: each Table call moves the counters on.
type fakeTable struct {
	mu    sync.Mutex
	n     int
	fail  error
	speed uint64
}

func (f *fakeTable) Table(context.Context) (Table, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail != nil {
		return Table{}, f.fail
	}
	f.n++
	in := uint64(f.n) * 7_500_000 // 1 Mbit/s over 60 s per poll
	return Table{Uptime: time.Duration(f.n) * time.Minute, Rows: map[uint32]IfRow{
		1: {Name: "ether1", Alias: "WAN", SpeedMbps: f.speed, Up: true, InOctets: in, OutOctets: in / 10},
	}}, nil
}

func (f *fakeTable) Close() error { return nil }

type reports struct {
	mu        sync.Mutex
	successes []string
	failures  []error
	warnings  []string
}

func (r *reports) Succeeded(d string) {
	r.mu.Lock()
	r.successes = append(r.successes, d)
	r.mu.Unlock()
}
func (r *reports) Failed(err error) { r.mu.Lock(); r.failures = append(r.failures, err); r.mu.Unlock() }
func (r *reports) Warn(w []string) {
	r.mu.Lock()
	r.warnings = append(r.warnings, w...)
	r.mu.Unlock()
}

func TestSourcePollsIntoInterfaceCounters(t *testing.T) {
	ctx := context.Background()
	store, err := flowstore.Open(ctx, filepath.Join(t.TempDir(), "f.duckdb"), flowstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fake := &fakeTable{speed: 1000}
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := t0
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); now = now.Add(time.Minute); return now }
	k := NewSourceKind(SourceOptions{Writer: store, Dial: func(Config) (TableReader, error) { return fake, nil }, Interval: 5 * time.Millisecond, Now: clock})
	rep := &reports{}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		k.Run(runCtx, datasource.Source{ID: "sw1", Settings: datasource.Settings{"address": "192.168.10.2", "version": "v2c", "community": "public"}}, rep)
		close(done)
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		rep.mu.Lock()
		n := len(rep.successes)
		rep.mu.Unlock()
		if n >= 3 {
			break
		}
	}
	cancel()
	<-done
	ifs, err := store.SNMPInterfaces(ctx, "sw1")
	if err != nil || len(ifs) != 1 || ifs[0].Speed != 1_000_000_000 || ifs[0].Alias != "WAN" {
		t.Fatalf("interfaces %+v %v", ifs, err)
	}
	pts, err := store.InterfaceSeries(ctx, "sw1", 1, t0, t0.Add(time.Hour))
	if err != nil || len(pts) < 2 || pts[0].InBps != 1_000_000 {
		t.Errorf("points %+v %v; want 1 Mbit/s from the second poll on", pts, err)
	}
	if rep.successes[0] != "1 interfaces" {
		t.Errorf("reported %v", rep.successes)
	}

	fake.fail = errors.New("request timeout")
	rep2 := &reports{}
	runCtx, cancel = context.WithCancel(ctx)
	done = make(chan struct{})
	go func() {
		k.Run(runCtx, datasource.Source{ID: "sw1", Settings: datasource.Settings{"address": "x"}}, rep2)
		close(done)
	}()
	time.Sleep(30 * time.Millisecond)
	cancel()
	<-done
	if len(rep2.failures) == 0 || !strings.Contains(rep2.failures[0].Error(), "timeout") {
		t.Errorf("failures %v", rep2.failures)
	}
}

func TestSourceSettings(t *testing.T) {
	k := NewSourceKind(SourceOptions{})
	for name, tc := range map[string]struct {
		s     datasource.Settings
		field string
	}{
		"v2c without community": {datasource.Settings{"address": "sw1", "version": "v2c"}, "community"},
		"v3 without user":       {datasource.Settings{"address": "sw1", "version": "v3"}, "user"},
		"privacy without auth":  {datasource.Settings{"address": "sw1", "version": "v3", "user": "flowmon", "priv_protocol": "AES"}, "priv_protocol"},
		"bad address":           {datasource.Settings{"address": "sw1:notaport", "version": "v2c", "community": "public"}, "address"},
	} {
		var inv *datasource.InvalidError
		if err := k.Validate(tc.s); !errors.As(err, &inv) || inv.Field != tc.field {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := k.Validate(datasource.Settings{"address": "sw1", "version": "v3", "user": "flowmon", "auth_protocol": "SHA", "priv_protocol": "AES"}); err != nil {
		t.Errorf("a good v3 config: %v", err)
	}
	fake := &fakeTable{speed: 100}
	k = NewSourceKind(SourceOptions{Dial: func(Config) (TableReader, error) { return fake, nil }})
	msg, err := k.Test(context.Background(), datasource.Settings{"address": "sw1"})
	if err != nil || !strings.Contains(msg, "1 interfaces, 1 up") {
		t.Errorf("Test = %q, %v", msg, err)
	}
}

// healthyTable is a device with Device Health that restarts at its fourth poll.
type healthyTable struct {
	fakeTable
	polls int
}

func (h *healthyTable) Health(context.Context) (Health, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.polls++
	up := time.Duration(h.polls)*time.Minute + 30*24*time.Hour
	if h.polls >= 4 {
		up = time.Duration(h.polls-3) * time.Minute
	}
	return Health{Uptime: up, Metrics: map[string]float64{"uptime": up.Seconds(), "cpu": 12}}, nil
}

func TestSourcePollsDeviceHealth(t *testing.T) {
	ctx := context.Background()
	store, err := flowstore.Open(ctx, filepath.Join(t.TempDir(), "f.duckdb"), flowstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	fake := &healthyTable{fakeTable: fakeTable{speed: 1000}}
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	now := t0
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); now = now.Add(time.Minute); return now }
	k := NewSourceKind(SourceOptions{Writer: store, Health: store, Dial: func(Config) (TableReader, error) { return fake, nil }, Interval: 5 * time.Millisecond, Now: clock})
	rep := &reports{}
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		k.Run(runCtx, datasource.Source{ID: "ap", Settings: datasource.Settings{"address": "192.168.10.3"}}, rep)
		close(done)
	}()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		rep.mu.Lock()
		n := len(rep.successes)
		rep.mu.Unlock()
		if n >= 5 {
			break
		}
	}
	cancel()
	<-done
	latest, err := store.LatestHealth(ctx, "ap")
	if err != nil || latest["ap"].Metrics["cpu"] != 12 {
		t.Fatalf("latest %+v %v", latest, err)
	}
	restarts, err := store.Restarts(ctx, "ap", t0, t0.Add(time.Hour))
	if err != nil || len(restarts) != 1 || !restarts[0].Equal(t0.Add(3*time.Minute)) {
		t.Errorf("restarts %v %v, want one at 12:03", restarts, err)
	}
	rep.mu.Lock()
	defer rep.mu.Unlock()
	if len(rep.warnings) != 1 || !strings.Contains(rep.warnings[0], "restarted") {
		t.Errorf("warnings %v", rep.warnings)
	}
}
