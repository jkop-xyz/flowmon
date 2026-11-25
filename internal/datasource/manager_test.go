package datasource_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/secret"
)

// fakeKind's runs poll when told to: each value sent on a run's results is one attempt.
type fakeKind struct {
	mu   sync.Mutex
	runs []*fakeRun
	// exitEarly makes Run return at once, panicIn makes it panic.
	exitEarly, panicIn bool
	// hold, when set as a run starts, keeps that run from returning after it is
	// cancelled until hold is closed.
	hold chan struct{}
}

type fakeRun struct {
	src     datasource.Source
	results chan error
	stopped chan struct{}
}

func (k *fakeKind) Info() datasource.KindInfo {
	return datasource.KindInfo{Name: "fake", Title: "Fake", Fields: []datasource.Field{
		{Key: "url", Label: "URL", Type: datasource.FieldURL, Required: true, Endpoint: true},
		{Key: "user", Label: "User", Type: datasource.FieldText},
		{Key: "password", Label: "Password", Type: datasource.FieldSecret},
		{Key: "skip_tls_verify", Label: "Skip TLS verification", Type: datasource.FieldBool, Default: "false"},
	}}
}

func (k *fakeKind) Validate(s datasource.Settings) error {
	if s["user"] == "root" {
		return errors.New("not as root")
	}
	return nil
}

func (k *fakeKind) Test(ctx context.Context, s datasource.Settings) (string, error) {
	return "connected to " + s["url"] + " as " + s["user"] + " with " + s["password"], nil
}

func (k *fakeKind) Run(ctx context.Context, src datasource.Source, r datasource.Reporter) {
	run := &fakeRun{src: src, results: make(chan error), stopped: make(chan struct{})}
	k.mu.Lock()
	k.runs = append(k.runs, run)
	exit, panicking, hold := k.exitEarly, k.panicIn, k.hold
	k.mu.Unlock()
	defer close(run.stopped)
	if hold != nil {
		defer func() { <-hold }()
	}
	if panicking {
		panic("boom")
	}
	if exit {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case err := <-run.results:
			if err != nil {
				r.Failed(err)
			} else {
				r.Succeeded("polled")
			}
		}
	}
}

func (k *fakeKind) run(t *testing.T, n int) *fakeRun {
	t.Helper()
	var r *fakeRun
	eventually(t, func() bool {
		k.mu.Lock()
		defer k.mu.Unlock()
		if len(k.runs) > n {
			r = k.runs[n]
			return true
		}
		return false
	})
	return r
}

func (k *fakeKind) runCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.runs)
}

// running counts the runs that haven't returned.
func (k *fakeKind) running() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	n := 0
	for _, r := range k.runs {
		select {
		case <-r.stopped:
		default:
			n++
		}
	}
	return n
}

func eventually(t *testing.T, ok func() bool) {
	t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if ok() {
			return
		}
	}
	t.Fatal("condition not met in 5 s")
}

type env struct {
	dir   string
	store *flowstore.Store
	box   *secret.Box
}

func newEnv(t *testing.T) *env {
	t.Helper()
	dir := t.TempDir()
	store, err := flowstore.Open(context.Background(), filepath.Join(dir, "flows.duckdb"), flowstore.Options{MemoryLimit: "256MB", Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	box, err := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	return &env{dir: dir, store: store, box: box}
}

func (e *env) manager(t *testing.T, k *fakeKind, srcEnv []datasource.Source) *datasource.Manager {
	t.Helper()
	m, err := datasource.New(datasource.Options{Store: e.store, Box: e.box, Kinds: []datasource.Kind{k}, Env: srcEnv, RestartDelay: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	m.Start(ctx)
	return m
}

func state(t *testing.T, m *datasource.Manager, id string) datasource.Health {
	t.Helper()
	st, err := m.Get(id)
	if err != nil {
		t.Fatal(err)
	}
	return st.Health
}

func waitState(t *testing.T, m *datasource.Manager, id string, want datasource.State) datasource.Health {
	t.Helper()
	var h datasource.Health
	eventually(t, func() bool { h = state(t, m, id); return h.State == want })
	return h
}

var fakeSource = datasource.Source{Kind: "fake", Name: "Home", Enabled: true,
	Settings: datasource.Settings{"url": "https://dns.example/", "user": "admin", "password": " s3cret "}}

func TestSourceRunsFailsAndRecovers(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{}
	m := e.manager(t, k, nil)

	st, err := m.Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}
	if st.Health.State != datasource.StateStarting {
		t.Errorf("new source is %s, want starting", st.Health.State)
	}
	if _, shown := st.Settings["password"]; shown || len(st.SecretsSet) != 1 || st.SecretsSet[0] != "password" {
		t.Errorf("status shows settings %v, secrets set %v; want the password hidden and listed as set", st.Settings, st.SecretsSet)
	}

	run := k.run(t, 0)
	if got := run.src.Settings; got["url"] != "https://dns.example" || got["password"] != " s3cret " || got["skip_tls_verify"] != "false" {
		t.Errorf("run got settings %v; want the trailing slash trimmed, the password as typed and the default filled", got)
	}

	run.results <- nil
	ok := waitState(t, m, st.ID, datasource.StateOK)
	if ok.LastSuccess == nil || ok.Detail != "polled" {
		t.Errorf("health after a success: %+v", ok)
	}

	run.results <- errors.New("connection refused")
	failing := waitState(t, m, st.ID, datasource.StateFailing)
	if failing.Error != "connection refused" || failing.LastSuccess == nil {
		t.Errorf("health after a failure: %+v; want the error and the earlier success kept", failing)
	}
	since := failing.Since

	run.results <- errors.New("still refused")
	eventually(t, func() bool { return state(t, m, st.ID).Error == "still refused" })
	if h := state(t, m, st.ID); !h.Since.Equal(since) {
		t.Errorf("a second failure moved Since from %v to %v", since, h.Since)
	}

	run.results <- nil
	waitState(t, m, st.ID, datasource.StateOK)
}

func TestSourcesSurviveARestartWithSecretsSealed(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{}
	st, err := e.manager(t, k, nil).Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}

	stored, err := e.store.DataSources(context.Background())
	if err != nil || len(stored) != 1 {
		t.Fatalf("stored %v, %v", stored, err)
	}
	if strings.Contains(stored[0].Settings, "s3cret") || !strings.Contains(stored[0].Settings, "dns.example") {
		t.Errorf("stored settings %s: want the password sealed and the rest readable", stored[0].Settings)
	}

	k2 := &fakeKind{}
	m2 := e.manager(t, k2, nil)
	if got := k2.run(t, 0).src.Settings["password"]; got != " s3cret " {
		t.Errorf("after a restart the source runs with password %q", got)
	}
	if list := m2.List(); len(list) != 1 || list[0].ID != st.ID || list[0].Name != "Home" {
		t.Errorf("after a restart: %+v", list)
	}
}

func TestLostSecretKeyTurnsTheSourceOff(t *testing.T) {
	e := newEnv(t)
	st, err := e.manager(t, &fakeKind{}, nil).Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(e.dir, "secret.key")); err != nil {
		t.Fatal(err)
	}
	if e.box, err = secret.LoadOrCreate(filepath.Join(e.dir, "secret.key")); err != nil {
		t.Fatal(err)
	}

	k := &fakeKind{}
	m := e.manager(t, k, nil)
	got, err := m.Get(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || got.Health.State != datasource.StateFailing || !strings.Contains(got.Health.Error, "Enter them again") {
		t.Errorf("source with undecryptable secrets: enabled %v, health %+v", got.Enabled, got.Health)
	}
	time.Sleep(20 * time.Millisecond)
	if n := k.runCount(); n != 0 {
		t.Errorf("it ran %d times; want it off", n)
	}
}

func TestUpdateRestartsKeepsSecretsAndIgnoresTheOldRun(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{}
	m := e.manager(t, k, nil)
	st, err := m.Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}
	old := k.run(t, 0)

	upd := fakeSource
	upd.Name = "AdGuard"
	upd.Settings = datasource.Settings{"url": " https://dns.example ", "user": "bob", "password": ""}
	got, err := m.Update(context.Background(), st.ID, upd)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "AdGuard" || got.Health.State != datasource.StateStarting {
		t.Errorf("after the edit: %+v", got)
	}
	select {
	case <-old.stopped:
	default:
		t.Error("the old run is still going after Update returned")
	}
	next := k.run(t, 1)
	if next.src.Settings["user"] != "bob" || next.src.Settings["password"] != " s3cret " {
		t.Errorf("new run settings %v; want the new user and the stored password", next.src.Settings)
	}

	next.results <- nil
	waitState(t, m, st.ID, datasource.StateOK)

	upd.Enabled = false
	if _, err := m.Update(context.Background(), st.ID, upd); err != nil {
		t.Fatal(err)
	}
	if h := state(t, m, st.ID); h.State != datasource.StateDisabled {
		t.Errorf("disabled source is %s", h.State)
	}
	<-next.stopped
	if n := k.runCount(); n != 2 {
		t.Errorf("%d runs; a disabled source must not run", n)
	}
}

func TestDelete(t *testing.T) {
	e := newEnv(t)
	k := &fakeKind{}
	m := e.manager(t, k, nil)
	st, err := m.Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}
	run := k.run(t, 0)
	if err := m.Delete(context.Background(), st.ID); err != nil {
		t.Fatal(err)
	}
	<-run.stopped
	if _, err := m.Get(st.ID); !errors.Is(err, datasource.ErrNotFound) {
		t.Errorf("Get after Delete: %v", err)
	}
	if stored, _ := e.store.DataSources(context.Background()); len(stored) != 0 {
		t.Errorf("still stored: %v", stored)
	}
}

// Edits that overlap, as from a double-click or a second tab, while the old run takes a
// while to stop, leave one run; a Delete among them leaves none, and nothing stored.
func TestOverlappingEditsLeaveNoStrayRun(t *testing.T) {
	for _, second := range []string{"update", "delete"} {
		t.Run(second, func(t *testing.T) {
			e := newEnv(t)
			hold := make(chan struct{})
			k := &fakeKind{hold: hold}
			m := e.manager(t, k, nil)
			st, err := m.Create(context.Background(), fakeSource)
			if err != nil {
				t.Fatal(err)
			}
			k.run(t, 0)
			k.mu.Lock()
			k.hold = nil
			k.mu.Unlock()

			upd := fakeSource
			upd.Settings = datasource.Settings{"url": "https://dns2.example", "user": "admin"}
			var wg sync.WaitGroup
			wg.Go(func() {
				if _, err := m.Update(context.Background(), st.ID, upd); err != nil {
					t.Error(err)
				}
			})
			// The first Update is waiting for the old run to stop.
			time.Sleep(50 * time.Millisecond)
			wg.Go(func() {
				var err error
				if second == "update" {
					_, err = m.Update(context.Background(), st.ID, upd)
				} else {
					err = m.Delete(context.Background(), st.ID)
				}
				if err != nil {
					t.Error(err)
				}
			})
			time.Sleep(50 * time.Millisecond)
			close(hold)
			wg.Wait()

			want := 1
			if second == "delete" {
				want = 0
				if stored, _ := e.store.DataSources(context.Background()); len(stored) != 0 {
					t.Errorf("deleted source is stored again: %v", stored)
				}
			}
			eventually(t, func() bool { return k.running() <= want })
			time.Sleep(50 * time.Millisecond)
			if n := k.running(); n != want {
				t.Errorf("%d runs going, want %d", n, want)
			}
		})
	}
}

func TestInvalidSettings(t *testing.T) {
	m := newEnv(t).manager(t, &fakeKind{}, nil)
	for name, tc := range map[string]struct {
		settings datasource.Settings
		field    string
	}{
		"missing URL":    {datasource.Settings{"user": "a"}, "url"},
		"not a web URL":  {datasource.Settings{"url": "ftp://dns.example"}, "url"},
		"bad bool":       {datasource.Settings{"url": "http://dns.example", "skip_tls_verify": "maybe"}, "skip_tls_verify"},
		"kind's own say": {datasource.Settings{"url": "http://dns.example", "user": "root"}, ""},
	} {
		t.Run(name, func(t *testing.T) {
			src := fakeSource
			src.Settings = tc.settings
			_, err := m.Create(context.Background(), src)
			var inv *datasource.InvalidError
			if !errors.As(err, &inv) || inv.Field != tc.field {
				t.Errorf("got %v; want an InvalidError for field %q", err, tc.field)
			}
		})
	}
	src := fakeSource
	src.Name = " "
	if _, err := m.Create(context.Background(), src); err == nil {
		t.Error("a source without a name was accepted")
	}
	if len(m.List()) != 0 {
		t.Error("an invalid source was kept")
	}
}

func TestEnvironmentSourcesAreLocked(t *testing.T) {
	srcs, err := datasource.FromEnviron([]string{
		"FLOWMON_SOURCE_HOME_KIND=fake",
		"FLOWMON_SOURCE_HOME_URL=http://dns.example",
		"FLOWMON_SOURCE_HOME_PASSWORD=pw",
		"FLOWMON_SOURCE_HOME_SKIP_TLS_VERIFY=yes",
		"FLOWMON_SOURCE_HOME_NAME=AdGuard at home",
		"FLOWMON_HTTP_ADDR=:8080",
	})
	if err != nil {
		t.Fatal(err)
	}
	e := newEnv(t)
	k := &fakeKind{}
	m := e.manager(t, k, srcs)

	list := m.List()
	if len(list) != 1 || list[0].ID != "env-home" || !list[0].Locked || list[0].Name != "AdGuard at home" {
		t.Fatalf("listed %+v", list)
	}
	if got := k.run(t, 0).src.Settings; got["password"] != "pw" || got["skip_tls_verify"] != "true" {
		t.Errorf("env source runs with %v", got)
	}
	if _, err := m.Update(context.Background(), "env-home", fakeSource); !errors.Is(err, datasource.ErrLocked) {
		t.Errorf("Update of an env source: %v", err)
	}
	if err := m.Delete(context.Background(), "env-home"); !errors.Is(err, datasource.ErrLocked) {
		t.Errorf("Delete of an env source: %v", err)
	}
	if stored, _ := e.store.DataSources(context.Background()); len(stored) != 0 {
		t.Errorf("env source was stored: %v", stored)
	}
}

func TestEnvironmentMistakesAreErrors(t *testing.T) {
	for name, environ := range map[string][]string{
		"no kind":       {"FLOWMON_SOURCE_HOME_URL=http://dns.example"},
		"bad id":        {"FLOWMON_SOURCE_home-1_KIND=fake"},
		"bad enabled":   {"FLOWMON_SOURCE_HOME_KIND=fake", "FLOWMON_SOURCE_HOME_ENABLED=sometimes"},
		"unknown kind":  {"FLOWMON_SOURCE_HOME_KIND=pihole", "FLOWMON_SOURCE_HOME_URL=http://dns.example"},
		"typo":          {"FLOWMON_SOURCE_HOME_KIND=fake", "FLOWMON_SOURCE_HOME_URL=http://dns.example", "FLOWMON_SOURCE_HOME_PASWORD=pw"},
		"invalid value": {"FLOWMON_SOURCE_HOME_KIND=fake", "FLOWMON_SOURCE_HOME_URL=dns.example"},
	} {
		t.Run(name, func(t *testing.T) {
			srcs, err := datasource.FromEnviron(environ)
			if err == nil {
				_, err = datasource.New(datasource.Options{Kinds: []datasource.Kind{&fakeKind{}}, Env: srcs})
			}
			if err == nil {
				t.Error("accepted")
			}
		})
	}
}

func TestRunThatEndsEarlyOrPanicsIsRestarted(t *testing.T) {
	for name, k := range map[string]*fakeKind{"ends early": {exitEarly: true}, "panics": {panicIn: true}} {
		t.Run(name, func(t *testing.T) {
			m := newEnv(t).manager(t, k, nil)
			st, err := m.Create(context.Background(), fakeSource)
			if err != nil {
				t.Fatal(err)
			}
			k.run(t, 2)
			if h := state(t, m, st.ID); h.State != datasource.StateFailing || h.Error == "" {
				t.Errorf("health %+v; want failing with a reason", h)
			}
		})
	}
}

func TestTestUsesStoredSecretsForAnEdit(t *testing.T) {
	m := newEnv(t).manager(t, &fakeKind{}, nil)
	st, err := m.Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}
	edit := datasource.Source{Settings: datasource.Settings{"url": "https://dns.example", "user": "bob"}}
	got, err := m.Test(context.Background(), st.ID, edit)
	if err != nil || got != "connected to https://dns.example as bob with  s3cret " {
		t.Errorf("Test = %q, %v", got, err)
	}
	if _, err := m.Test(context.Background(), "", datasource.Source{Kind: "fake", Settings: datasource.Settings{}}); err == nil {
		t.Error("Test without the required URL passed")
	}
}

// A stored secret goes only to the address it was entered for: an edit to another one
// has to enter it again.
func TestStoredSecretsStayWithTheirEndpoint(t *testing.T) {
	m := newEnv(t).manager(t, &fakeKind{}, nil)
	st, err := m.Create(context.Background(), fakeSource)
	if err != nil {
		t.Fatal(err)
	}
	moved := datasource.Source{Name: "Home", Enabled: true, Settings: datasource.Settings{"url": "http://attacker.example", "user": "admin"}}
	var inv *datasource.InvalidError
	if got, err := m.Test(context.Background(), st.ID, moved); !errors.As(err, &inv) || inv.Field != "password" {
		t.Errorf("Test against another URL = %q, %v; want the password asked for again", got, err)
	}
	if _, err := m.Update(context.Background(), st.ID, moved); !errors.As(err, &inv) || inv.Field != "password" {
		t.Errorf("Update to another URL: %v; want the password asked for again", err)
	}
	if cur, _ := m.Get(st.ID); cur.Settings["url"] != "https://dns.example" {
		t.Errorf("after the refused Update: %v", cur.Settings)
	}

	moved.Settings["password"] = "new"
	if got, err := m.Test(context.Background(), st.ID, moved); err != nil || got != "connected to http://attacker.example as admin with new" {
		t.Errorf("Test with the password entered = %q, %v", got, err)
	}
	if _, err := m.Update(context.Background(), st.ID, moved); err != nil {
		t.Errorf("Update with the password entered: %v", err)
	}
}
