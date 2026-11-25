package datasource

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/secret"
)

// Store keeps Sources. *flowstore.Store implements it.
type Store interface {
	DataSources(ctx context.Context) ([]flowstore.DataSource, error)
	PutDataSource(ctx context.Context, d flowstore.DataSource) error
	DeleteDataSource(ctx context.Context, id string) error
}

// Errors the API maps to responses.
var (
	ErrNotFound    = errors.New("no such data source")
	ErrLocked      = errors.New("this data source is set by environment variables and can't be changed here")
	ErrUnknownKind = errors.New("unknown data source kind")
)

// InvalidError is a setting the user has to correct.
type InvalidError struct {
	// Field is the Field key at fault, or "" for the Source as a whole.
	Field string
	Msg   string
}

func (e *InvalidError) Error() string {
	if e.Field == "" {
		return e.Msg
	}
	return e.Field + ": " + e.Msg
}

// DefaultRestartDelay is how long the Manager waits before running a Kind again whose
// Run returned early.
const DefaultRestartDelay = 30 * time.Second

// testTimeout bounds a Kind's Test.
const testTimeout = 20 * time.Second

// Options configure a Manager.
type Options struct {
	Store Store
	Box   *secret.Box
	Kinds []Kind
	// Env are the Sources set by environment variables (see FromEnviron).
	Env          []Source
	Logger       *slog.Logger
	Now          func() time.Time
	RestartDelay time.Duration
}

// Status is a Source as shown to the user: secret settings blanked, with its health.
type Status struct {
	Source
	// SecretsSet lists the secret settings that have a value.
	SecretsSet []string `json:"secretsSet"`
	// Locked is set for Sources given by environment variables.
	Locked bool   `json:"locked"`
	Health Health `json:"health"`
}

// Manager stores Sources and runs one collection loop per enabled Source.
type Manager struct {
	store        Store
	box          *secret.Box
	kinds        map[string]Kind
	logger       *slog.Logger
	now          func() time.Time
	restartDelay time.Duration

	mu      sync.Mutex
	ctx     context.Context // set by Start; nil until then
	entries map[string]*entry
	gen     uint64
}

type entry struct {
	src    Source
	locked bool
	health Health
	run    *run
}

type run struct {
	gen    uint64
	cancel context.CancelFunc
	done   chan struct{}
}

// New returns a Manager with the environment's Sources. A Source in Env of an unknown
// kind, or with invalid settings, is an error: the configuration has to be fixed.
func New(opts Options) (*Manager, error) {
	m := &Manager{store: opts.Store, box: opts.Box, kinds: map[string]Kind{}, logger: opts.Logger,
		now: opts.Now, restartDelay: opts.RestartDelay, entries: map[string]*entry{}}
	if m.logger == nil {
		m.logger = slog.Default()
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.restartDelay == 0 {
		m.restartDelay = DefaultRestartDelay
	}
	for _, k := range opts.Kinds {
		m.kinds[k.Info().Name] = k
	}
	for _, src := range opts.Env {
		k, ok := m.kinds[src.Kind]
		if !ok {
			return nil, fmt.Errorf("data source %s: %w %q", src.ID, ErrUnknownKind, src.Kind)
		}
		for key := range src.Settings {
			if !slices.ContainsFunc(k.Info().Fields, func(f Field) bool { return f.Key == key }) {
				return nil, fmt.Errorf("data source %s: %s has no setting %q", src.ID, k.Info().Title, strings.ToUpper(key))
			}
		}
		s, err := normalize(k, src.Settings)
		if err != nil {
			return nil, fmt.Errorf("data source %s: %w", src.ID, err)
		}
		src.Settings = s
		m.entries[src.ID] = &entry{src: src, locked: true, health: m.idle(src)}
	}
	return m, nil
}

// Load reads the stored Sources. A Source whose secrets can't be decrypted (secret.key
// was lost or replaced) is kept, disabled, with an error asking for them again.
func (m *Manager) Load(ctx context.Context) error {
	stored, err := m.store.DataSources(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range stored {
		if _, ok := m.entries[d.ID]; ok {
			continue
		}
		src := Source{ID: d.ID, Kind: d.Kind, Name: d.Name, Enabled: d.Enabled, Created: d.Created}
		e := &entry{src: src}
		k, known := m.kinds[d.Kind]
		var problem string
		switch {
		case !known:
			src.Enabled, problem = false, fmt.Sprintf("This version of Flowmon doesn't know %q data sources.", d.Kind)
		default:
			if src.Settings, err = m.open(k, d.Settings); err != nil {
				src.Enabled, problem = false, "Its passwords can't be decrypted (secret.key changed). Enter them again."
				m.logger.Error("data source secrets can't be decrypted; it is off until they are entered again", "source", d.ID, "err", err)
			}
		}
		e.src = src
		e.health = m.idle(src)
		if problem != "" {
			t := m.now()
			e.health.State, e.health.Error, e.health.LastError = StateFailing, problem, &t
		}
		m.entries[d.ID] = e
	}
	return nil
}

// Start runs every enabled Source until ctx ends. Sources changed later start and stop
// as they change.
func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ctx = ctx
	for _, e := range m.entries {
		if e.src.Enabled {
			m.startLocked(e)
		}
	}
}

// Kinds describes the registered Kinds, by title.
func (m *Manager) Kinds() []KindInfo {
	out := make([]KindInfo, 0, len(m.kinds))
	for _, k := range m.kinds {
		out = append(out, k.Info())
	}
	slices.SortFunc(out, func(a, b KindInfo) int { return strings.Compare(a.Title, b.Title) })
	return out
}

// List returns every Source: those set by environment variables first, then the rest
// oldest first.
func (m *Manager) List() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.entries))
	for _, e := range m.entries {
		out = append(out, m.statusLocked(e))
	}
	slices.SortFunc(out, func(a, b Status) int {
		if a.Locked != b.Locked {
			if a.Locked {
				return -1
			}
			return 1
		}
		if c := a.Created.Compare(b.Created); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

// Get returns one Source.
func (m *Manager) Get(id string) (Status, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[id]
	if !ok {
		return Status{}, ErrNotFound
	}
	return m.statusLocked(e), nil
}

// Create stores a new Source and starts it if it is enabled. ID and Created are set here.
func (m *Manager) Create(ctx context.Context, src Source) (Status, error) {
	k, ok := m.kinds[src.Kind]
	if !ok {
		return Status{}, ErrUnknownKind
	}
	if err := checkName(src.Name); err != nil {
		return Status{}, err
	}
	s, err := normalize(k, src.Settings)
	if err != nil {
		return Status{}, err
	}
	src.ID, src.Settings, src.Created = newID(), s, m.now().UTC()
	if err := m.put(ctx, k, src); err != nil {
		return Status{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e := &entry{src: src, health: m.idle(src)}
	m.entries[src.ID] = e
	if src.Enabled {
		m.startLocked(e)
	}
	return m.statusLocked(e), nil
}

// Update changes a Source's name, enabled state and settings, and restarts it. Its kind
// can't change. An empty secret setting keeps the stored one.
func (m *Manager) Update(ctx context.Context, id string, src Source) (Status, error) {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return Status{}, ErrNotFound
	}
	if e.locked {
		m.mu.Unlock()
		return Status{}, ErrLocked
	}
	prev := e.src
	m.mu.Unlock()

	k, ok := m.kinds[prev.Kind]
	if !ok {
		return Status{}, ErrUnknownKind
	}
	if err := checkName(src.Name); err != nil {
		return Status{}, err
	}
	s, err := normalize(k, keepSecrets(k, src.Settings, prev.Settings))
	if err != nil {
		return Status{}, err
	}
	next := Source{ID: id, Kind: prev.Kind, Name: src.Name, Enabled: src.Enabled, Settings: s, Created: prev.Created}
	if err := m.put(ctx, k, next); err != nil {
		return Status{}, err
	}

	m.mu.Lock()
	r := m.stopLocked(e)
	m.mu.Unlock()
	if r != nil {
		<-r.done
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e.src, e.health = next, m.idle(next)
	if next.Enabled {
		m.startLocked(e)
	}
	return m.statusLocked(e), nil
}

// Delete stops and removes a Source. What it collected stays until its retention ends.
func (m *Manager) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	e, ok := m.entries[id]
	if !ok {
		m.mu.Unlock()
		return ErrNotFound
	}
	if e.locked {
		m.mu.Unlock()
		return ErrLocked
	}
	m.mu.Unlock()
	if err := m.store.DeleteDataSource(ctx, id); err != nil {
		return err
	}
	m.mu.Lock()
	r := m.stopLocked(e)
	delete(m.entries, id)
	m.mu.Unlock()
	if r != nil {
		<-r.done
	}
	return nil
}

// Test checks src's settings and connects once. With an id, src is an edit of that
// Source, so empty secret settings mean the stored ones.
func (m *Manager) Test(ctx context.Context, id string, src Source) (string, error) {
	kind := src.Kind
	var prev Settings
	if id != "" {
		m.mu.Lock()
		e, ok := m.entries[id]
		if ok {
			kind, prev = e.src.Kind, e.src.Settings
		}
		m.mu.Unlock()
		if !ok {
			return "", ErrNotFound
		}
	}
	k, ok := m.kinds[kind]
	if !ok {
		return "", ErrUnknownKind
	}
	s, err := normalize(k, keepSecrets(k, src.Settings, prev))
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, testTimeout)
	defer cancel()
	return k.Test(ctx, s)
}

// idle is the health of a Source that isn't running yet.
func (m *Manager) idle(src Source) Health {
	if src.Enabled {
		return Health{State: StateStarting, Since: m.now()}
	}
	return Health{State: StateDisabled, Since: m.now()}
}

func (m *Manager) statusLocked(e *entry) Status {
	st := Status{Source: e.src, Locked: e.locked, Health: e.health, SecretsSet: []string{}}
	st.Settings = maps.Clone(e.src.Settings)
	st.Health.Warnings = slices.Clone(e.health.Warnings)
	if k, ok := m.kinds[e.src.Kind]; ok {
		for _, f := range k.Info().Fields {
			if f.Type == FieldSecret {
				if st.Settings[f.Key] != "" {
					st.SecretsSet = append(st.SecretsSet, f.Key)
				}
				delete(st.Settings, f.Key)
			}
		}
	}
	return st
}

// startLocked runs e's Source. m.mu must be held, and e must not be running. Before
// Start, it does nothing: Start runs every enabled Source.
func (m *Manager) startLocked(e *entry) {
	if m.ctx == nil {
		return
	}
	k, ok := m.kinds[e.src.Kind]
	if !ok {
		return
	}
	m.gen++
	ctx, cancel := context.WithCancel(m.ctx)
	r := &run{gen: m.gen, cancel: cancel, done: make(chan struct{})}
	e.run = r
	src := e.src
	src.Settings = maps.Clone(e.src.Settings)
	rep := &reporter{m: m, id: src.ID, gen: r.gen}
	go func() {
		defer close(r.done)
		for {
			m.runOnce(ctx, k, src, rep)
			if ctx.Err() != nil {
				return
			}
			rep.Failed(errors.New("collection stopped unexpectedly; restarting"))
			select {
			case <-ctx.Done():
				return
			case <-time.After(m.restartDelay):
			}
		}
	}()
}

// runOnce runs k once, turning a panic into a failure so one Kind can't stop Flowmon.
func (m *Manager) runOnce(ctx context.Context, k Kind, src Source, rep *reporter) {
	defer func() {
		if p := recover(); p != nil {
			m.logger.Error("data source panicked", "source", src.ID, "kind", src.Kind, "panic", p)
			rep.Failed(fmt.Errorf("internal error: %v", p))
		}
	}()
	k.Run(ctx, src, rep)
}

// stopLocked cancels e's run, if any, and returns it so the caller can wait for it
// without holding m.mu.
func (m *Manager) stopLocked(e *entry) *run {
	r := e.run
	if r != nil {
		r.cancel()
		e.run = nil
	}
	return r
}

// put stores src with its secrets sealed.
func (m *Manager) put(ctx context.Context, k Kind, src Source) error {
	sealed := maps.Clone(src.Settings)
	for _, f := range k.Info().Fields {
		if f.Type != FieldSecret {
			continue
		}
		v, err := m.box.Seal(sealed[f.Key])
		if err != nil {
			return err
		}
		sealed[f.Key] = v
	}
	js, err := json.Marshal(sealed)
	if err != nil {
		return err
	}
	return m.store.PutDataSource(ctx, flowstore.DataSource{ID: src.ID, Kind: src.Kind, Name: src.Name,
		Enabled: src.Enabled, Settings: string(js), Created: src.Created})
}

// open reads stored settings and decrypts their secrets.
func (m *Manager) open(k Kind, stored string) (Settings, error) {
	var s Settings
	if err := json.Unmarshal([]byte(stored), &s); err != nil {
		return nil, err
	}
	if s == nil {
		s = Settings{}
	}
	for _, f := range k.Info().Fields {
		if f.Type != FieldSecret {
			continue
		}
		v, err := m.box.Open(s[f.Key])
		if err != nil {
			return nil, err
		}
		s[f.Key] = v
	}
	return s, nil
}

// reporter records one run's reports. Reports from a run that was stopped are dropped,
// so a slow poll finishing after an edit can't overwrite the new run's health.
type reporter struct {
	m   *Manager
	id  string
	gen uint64
}

func (r *reporter) update(f func(h *Health, now time.Time)) {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	e, ok := r.m.entries[r.id]
	if !ok || e.run == nil || e.run.gen != r.gen {
		return
	}
	f(&e.health, r.m.now())
}

func (r *reporter) Succeeded(detail string) {
	r.update(func(h *Health, now time.Time) {
		if h.State != StateOK {
			h.State, h.Since = StateOK, now
		}
		h.LastSuccess, h.Detail = &now, detail
	})
}

func (r *reporter) Failed(err error) {
	r.update(func(h *Health, now time.Time) {
		if h.State != StateFailing {
			h.State, h.Since = StateFailing, now
		}
		h.LastError, h.Error = &now, err.Error()
	})
}

func (r *reporter) Warn(warnings []string) {
	r.update(func(h *Health, _ time.Time) { h.Warnings = slices.Clone(warnings) })
}

// normalize keeps only k's fields, fills defaults, checks required fields and types, then
// asks k.
func normalize(k Kind, in Settings) (Settings, error) {
	out := Settings{}
	for _, f := range k.Info().Fields {
		v := strings.TrimSpace(in[f.Key])
		if f.Type == FieldSecret {
			v = in[f.Key] // passwords may start or end with spaces
		}
		if v == "" {
			v = f.Default
		}
		switch {
		case v == "" && f.Required:
			return nil, &InvalidError{Field: f.Key, Msg: f.Label + " is required."}
		case v == "":
		case f.Type == FieldBool:
			switch strings.ToLower(v) {
			case "true", "1", "yes", "on":
				v = "true"
			case "false", "0", "no", "off":
				v = "false"
			default:
				return nil, &InvalidError{Field: f.Key, Msg: f.Label + " must be true or false."}
			}
		case f.Type == FieldSelect && !slices.Contains(f.Options, v):
			return nil, &InvalidError{Field: f.Key, Msg: f.Label + " must be one of " + strings.Join(f.Options, ", ") + "."}
		case f.Type == FieldURL:
			u, err := url.Parse(v)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
				return nil, &InvalidError{Field: f.Key, Msg: f.Label + " must be an http:// or https:// address."}
			}
			v = strings.TrimRight(v, "/")
		}
		if v != "" {
			out[f.Key] = v
		}
	}
	if err := k.Validate(out); err != nil {
		var inv *InvalidError
		if !errors.As(err, &inv) {
			err = &InvalidError{Msg: err.Error()}
		}
		return nil, err
	}
	return out, nil
}

// keepSecrets fills empty secret settings in next from prev.
func keepSecrets(k Kind, next, prev Settings) Settings {
	out := maps.Clone(next)
	if out == nil {
		out = Settings{}
	}
	for _, f := range k.Info().Fields {
		if f.Type == FieldSecret && out[f.Key] == "" && prev[f.Key] != "" {
			out[f.Key] = prev[f.Key]
		}
	}
	return out
}

func checkName(name string) error {
	if strings.TrimSpace(name) == "" {
		return &InvalidError{Field: "name", Msg: "Name is required."}
	}
	if len(name) > 100 {
		return &InvalidError{Field: "name", Msg: "Name is too long (at most 100 characters)."}
	}
	return nil
}

func newID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
