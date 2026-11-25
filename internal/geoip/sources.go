package geoip

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Source is where a database came from. Sources are tried in this order for each kind:
// files the user mounted, MaxMind GeoLite2 (with an account), then DB-IP Lite.
type Source string

// Sources.
const (
	SourceFile    Source = "file"
	SourceMaxMind Source = "maxmind"
	SourceDBIP    Source = "dbip"
)

// Refresh timing: downloads are replaced weekly, and a failed download is retried after
// a few hours rather than every check.
const (
	RefreshAfter = 7 * 24 * time.Hour
	RetryAfter   = 6 * time.Hour
	CheckEvery   = time.Hour
	// maxDownload bounds a downloaded file; GeoLite2-City, the largest, is about 70 MB.
	maxDownload = 256 << 20
)

// Options configure a Manager.
type Options struct {
	// Dir holds MMDB files the user provides. They win over downloads.
	Dir string
	// DownloadDir is where downloaded databases are kept.
	DownloadDir string
	// Download enables downloads. Off, only files in Dir are used.
	Download bool
	// MaxMindAccountID and MaxMindLicenseKey enable GeoLite2 downloads.
	MaxMindAccountID, MaxMindLicenseKey string
	// Locks say which of those environment variables set, so Settings can't change them.
	Locks Locks
	// UserAgent is sent with downloads.
	UserAgent string
	Client    *http.Client
	Logger    *slog.Logger
	Now       func() time.Time
	// MaxMindURL and DBIPURL are the download bases; tests replace them.
	MaxMindURL, DBIPURL string
}

// Config is what Settings can change: whether to download, and the MaxMind account.
type Config struct {
	Download                            bool
	MaxMindAccountID, MaxMindLicenseKey string
}

func (c Config) maxMind() bool {
	return c.Download && c.MaxMindAccountID != "" && c.MaxMindLicenseKey != ""
}

// Locks say which settings environment variables set.
type Locks struct {
	Download bool `json:"download"`
	MaxMind  bool `json:"maxMind"`
}

// Manager finds, downloads and refreshes databases and keeps the Resolver current.
type Manager struct {
	opts     Options
	resolver *Resolver
	// kick asks Run to refresh now.
	kick chan struct{}

	mu          sync.Mutex
	cfg         Config
	downloading bool
	// maxMindWarning says why a stored MaxMind account isn't used.
	maxMindWarning string
	attempts       map[string]*attempt // by download file name
	loaded         map[Kind]stamp
	loadErr        map[Kind]string
	warnings       []string
	scanned        map[string]scanned // mounted files, by path
}

type attempt struct {
	at  time.Time
	err string
}

// stamp identifies a file version, to tell when it changed.
type stamp struct {
	path string
	mod  time.Time
	size int64
}

type scanned struct {
	stamp
	kind Kind
	err  error
}

// NewManager returns a Manager that keeps resolver current.
func NewManager(opts Options, resolver *Resolver) *Manager {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 10 * time.Minute}
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "flowmon"
	}
	if opts.MaxMindURL == "" {
		opts.MaxMindURL = "https://download.maxmind.com/geoip/databases"
	}
	if opts.DBIPURL == "" {
		opts.DBIPURL = "https://download.db-ip.com/free"
	}
	return &Manager{opts: opts, resolver: resolver, kick: make(chan struct{}, 1), attempts: map[string]*attempt{},
		cfg:    Config{Download: opts.Download, MaxMindAccountID: opts.MaxMindAccountID, MaxMindLicenseKey: opts.MaxMindLicenseKey},
		loaded: map[Kind]stamp{}, loadErr: map[Kind]string{}, scanned: map[string]scanned{}}
}

func (m *Manager) config() Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

// Configure applies changed settings and refreshes soon. A new MaxMind account is tried
// at once rather than after the wait for a failed one.
func (m *Manager) Configure(c Config) {
	m.mu.Lock()
	if c.MaxMindAccountID != m.cfg.MaxMindAccountID || c.MaxMindLicenseKey != m.cfg.MaxMindLicenseKey {
		for _, k := range Kinds {
			delete(m.attempts, downloadName(SourceMaxMind, k))
		}
		m.maxMindWarning = ""
	}
	m.cfg = c
	m.mu.Unlock()
	m.Kick()
}

// RetryNow forgets failed downloads and refreshes soon, instead of waiting RetryAfter.
// Databases younger than RefreshAfter aren't downloaded again.
func (m *Manager) RetryNow() {
	m.mu.Lock()
	clear(m.attempts)
	m.mu.Unlock()
	m.Kick()
}

// Kick asks Run to refresh now.
func (m *Manager) Kick() {
	select {
	case m.kick <- struct{}{}:
	default:
	}
}

func downloadName(s Source, k Kind) string { return string(s) + "-" + string(k) + ".mmdb" }

// candidate is a file that could serve a kind, in priority order.
type candidate struct {
	source Source
	path   string
}

// candidates lists, for each kind, the files that exist and could serve it.
func (m *Manager) candidates() map[Kind][]candidate {
	out := map[Kind][]candidate{}
	for _, f := range m.mountedFiles() {
		out[f.kind] = append(out[f.kind], candidate{SourceFile, f.path})
	}
	cfg := m.config()
	var sources []Source
	if cfg.maxMind() {
		sources = append(sources, SourceMaxMind)
	}
	if cfg.Download {
		sources = append(sources, SourceDBIP)
	}
	for _, k := range Kinds {
		for _, s := range sources {
			p := filepath.Join(m.opts.DownloadDir, downloadName(s, k))
			if _, err := os.Stat(p); err == nil {
				out[k] = append(out[k], candidate{s, p})
			}
		}
	}
	return out
}

// mountedFiles lists the readable .mmdb files in Dir with their kind, sorted by name.
// Files are only opened again when they change.
func (m *Manager) mountedFiles() []scanned {
	if m.opts.Dir == "" {
		return nil
	}
	entries, err := os.ReadDir(m.opts.Dir)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			m.warn(fmt.Sprintf("reading %s: %v", m.opts.Dir, err))
		}
		return nil
	}
	var out []scanned
	seen := map[string]bool{}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".mmdb") {
			continue
		}
		p := filepath.Join(m.opts.Dir, e.Name())
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		seen[p] = true
		st := stamp{p, info.ModTime(), info.Size()}
		m.mu.Lock()
		s, ok := m.scanned[p]
		m.mu.Unlock()
		if !ok || s.stamp != st {
			s = scanned{stamp: st}
			db, err := OpenFile(p, SourceFile)
			if err != nil {
				s.err = err
				m.opts.Logger.Warn("ignoring a GeoIP file", "path", p, "err", err)
			} else {
				s.kind = db.Kind
			}
			m.mu.Lock()
			m.scanned[p] = s
			m.mu.Unlock()
		}
		if s.err != nil {
			m.warn(s.err.Error())
			continue
		}
		out = append(out, s)
	}
	m.mu.Lock()
	for p := range m.scanned {
		if !seen[p] {
			delete(m.scanned, p)
		}
	}
	m.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

func (m *Manager) warn(s string) {
	m.mu.Lock()
	m.warnings = append(m.warnings, s)
	m.mu.Unlock()
}

// Load picks the best existing file for each kind and loads it if it changed. It never
// downloads, so it is quick enough to run before collection starts.
func (m *Manager) Load() {
	m.mu.Lock()
	m.warnings = nil
	m.mu.Unlock()
	cands := m.candidates()
	for _, k := range Kinds {
		m.loadKind(k, cands[k])
	}
}

func (m *Manager) loadKind(k Kind, cands []candidate) {
	var errs []string
	for _, c := range cands {
		info, err := os.Stat(c.path)
		if err != nil {
			continue
		}
		st := stamp{c.path, info.ModTime(), info.Size()}
		m.mu.Lock()
		same := m.loaded[k] == st
		m.mu.Unlock()
		if same && m.resolver.Current(k) != nil {
			m.mu.Lock()
			m.loadErr[k] = strings.Join(errs, "; ")
			m.mu.Unlock()
			return
		}
		db, err := OpenFile(c.path, c.source)
		if err == nil && db.Kind != k {
			err = fmt.Errorf("%s holds %s data, not %s", c.path, db.Kind, k)
		}
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		m.resolver.Set(k, db)
		m.mu.Lock()
		m.loaded[k] = st
		m.loadErr[k] = strings.Join(errs, "; ")
		m.mu.Unlock()
		m.opts.Logger.Info("GeoIP database loaded", "kind", k, "source", c.source, "type", db.Type, "built", db.BuiltAt.Format(time.DateOnly))
		return
	}
	m.resolver.Set(k, nil)
	m.mu.Lock()
	delete(m.loaded, k)
	m.loadErr[k] = strings.Join(errs, "; ")
	m.mu.Unlock()
}

// Refresh downloads databases that are missing or older than RefreshAfter, then loads
// the best file for each kind. A kind served by a mounted file is never downloaded.
func (m *Manager) Refresh(ctx context.Context) {
	if cfg := m.config(); cfg.Download {
		m.mu.Lock()
		m.downloading = true
		m.mu.Unlock()
		defer func() {
			m.mu.Lock()
			m.downloading = false
			m.mu.Unlock()
		}()
		mounted := map[Kind]bool{}
		for _, f := range m.mountedFiles() {
			mounted[f.kind] = true
		}
		if err := os.MkdirAll(m.opts.DownloadDir, 0o750); err != nil {
			m.opts.Logger.Error("GeoIP download directory", "err", err)
		} else {
			for _, k := range Kinds {
				if mounted[k] {
					continue
				}
				// MaxMind first when configured; DB-IP when it isn't, or when MaxMind has
				// never succeeded (a wrong key shouldn't leave the map empty).
				if cfg.maxMind() && m.ensure(ctx, cfg, SourceMaxMind, k) {
					m.mu.Lock()
					delete(m.attempts, downloadName(SourceDBIP, k)) // no longer relevant
					m.mu.Unlock()
					continue
				}
				m.ensure(ctx, cfg, SourceDBIP, k)
			}
		}
	}
	m.Load()
}

// ensure downloads s's database for k if it is due, and reports whether a usable file
// exists afterwards.
func (m *Manager) ensure(ctx context.Context, cfg Config, s Source, k Kind) bool {
	name := downloadName(s, k)
	path := filepath.Join(m.opts.DownloadDir, name)
	now := m.opts.Now()
	info, statErr := os.Stat(path)
	exists := statErr == nil
	if exists && now.Sub(info.ModTime()) < RefreshAfter {
		return true
	}
	m.mu.Lock()
	last := m.attempts[name]
	m.mu.Unlock()
	if last != nil && now.Sub(last.at) < RetryAfter {
		return exists
	}
	err := m.download(ctx, cfg, s, k, path)
	m.mu.Lock()
	m.attempts[name] = &attempt{at: now}
	if err != nil {
		m.attempts[name].err = err.Error()
	}
	m.mu.Unlock()
	if err != nil {
		m.opts.Logger.Warn("GeoIP download failed", "source", s, "kind", k, "err", err)
		return exists
	}
	return true
}

func (m *Manager) download(ctx context.Context, cfg Config, s Source, k Kind, path string) error {
	var mmdb io.Reader
	switch s {
	case SourceMaxMind:
		edition := "GeoLite2-Country"
		if k == KindASN {
			edition = "GeoLite2-ASN"
		}
		body, err := m.get(ctx, m.opts.MaxMindURL+"/"+edition+"/download?suffix=tar.gz", &cfg)
		if err != nil {
			return err
		}
		defer body.Close()
		if mmdb, err = mmdbFromTarGz(body); err != nil {
			return err
		}
	case SourceDBIP:
		// DB-IP publishes monthly; early in a month the new file may not be out yet.
		now := m.opts.Now().UTC()
		var body io.ReadCloser
		var err error
		for _, month := range []time.Time{now, now.AddDate(0, 0, -now.Day())} {
			url := fmt.Sprintf("%s/dbip-%s-lite-%s.mmdb.gz", m.opts.DBIPURL, k, month.Format("2006-01"))
			if body, err = m.get(ctx, url, nil); !errors.Is(err, errNotFound) {
				break
			}
		}
		if err != nil {
			return err
		}
		defer body.Close()
		gz, err := gzip.NewReader(body)
		if err != nil {
			return fmt.Errorf("unpacking: %w", err)
		}
		mmdb = gz
	default:
		return fmt.Errorf("unknown source %q", s)
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".download-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	n, err := io.Copy(tmp, io.LimitReader(mmdb, maxDownload+1))
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("downloading: %w", err)
	}
	if n > maxDownload {
		return errors.New("download is larger than expected")
	}
	// Only a file that opens as the right kind replaces the current one.
	db, err := OpenFile(tmp.Name(), s)
	if err != nil {
		return errors.New("the download isn't a valid MMDB database")
	}
	if db.Kind != k {
		return fmt.Errorf("downloaded %s data, expected %s", db.Kind, k)
	}
	return os.Rename(tmp.Name(), path)
}

var sourceNames = map[Source]string{SourceFile: "File", SourceMaxMind: "MaxMind", SourceDBIP: "DB-IP"}

var errNotFound = errors.New("not found")

// get fetches url, with maxmind's account when it is set.
func (m *Manager) get(ctx context.Context, url string, maxmind *Config) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", m.opts.UserAgent)
	if maxmind != nil {
		// Go drops this header when MaxMind redirects to its storage host.
		req.SetBasicAuth(maxmind.MaxMindAccountID, maxmind.MaxMindLicenseKey)
	}
	resp, err := m.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp.Body, nil
	}
	resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errNotFound
	case maxmind != nil && (resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden):
		return nil, errors.New("refused the account ID or license key")
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("download limit reached; will retry later")
	}
	return nil, fmt.Errorf("download failed: %s", resp.Status)
}

// mmdbFromTarGz returns the first .mmdb file in a MaxMind tar.gz archive.
func mmdbFromTarGz(r io.Reader) (io.Reader, error) {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return nil, fmt.Errorf("unpacking: %w", err)
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, errors.New("the archive holds no .mmdb file")
			}
			return nil, fmt.Errorf("unpacking: %w", err)
		}
		if h.Typeflag == tar.TypeReg && strings.HasSuffix(h.Name, ".mmdb") {
			return tr, nil
		}
	}
}

// Run loads what exists, then refreshes now and every CheckEvery until ctx is done.
func (m *Manager) Run(ctx context.Context) {
	t := time.NewTicker(CheckEvery)
	defer t.Stop()
	for {
		m.Refresh(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.kick:
		}
	}
}

// Status is shown in Settings.
type Status struct {
	DownloadsEnabled  bool `json:"downloadsEnabled"`
	MaxMindConfigured bool `json:"maxMindConfigured"`
	// MaxMindAccountID is shown; the license key never is.
	MaxMindAccountID string `json:"maxMindAccountId,omitempty"`
	Locks            Locks  `json:"locks"`
	// MaxMindWarning says why a stored MaxMind account isn't used.
	MaxMindWarning string `json:"maxMindWarning,omitempty"`
	// Downloading says a refresh is checking for or downloading databases now.
	Downloading bool             `json:"downloading"`
	Dir         string           `json:"dir"`
	Databases   []DatabaseStatus `json:"databases"`
	Warnings    []string         `json:"warnings"`
}

// DatabaseStatus describes the database in use for one kind.
type DatabaseStatus struct {
	Kind Kind `json:"kind"`
	// Source is empty when no database is loaded for this kind.
	Source  Source     `json:"source,omitempty"`
	Type    string     `json:"type,omitempty"`
	Path    string     `json:"path,omitempty"`
	BuiltAt *time.Time `json:"builtAt,omitempty"`
	// UpdatedAt is when the file was last written (downloaded or copied in).
	UpdatedAt *time.Time `json:"updatedAt,omitempty"`
	// LastAttempt and Error describe the most recent download for this kind.
	LastAttempt *time.Time `json:"lastAttempt,omitempty"`
	Error       string     `json:"error,omitempty"`
}

// Status reports the databases in use and any problems.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	st := Status{DownloadsEnabled: m.cfg.Download, MaxMindConfigured: m.cfg.MaxMindAccountID != "" && m.cfg.MaxMindLicenseKey != "",
		MaxMindAccountID: m.cfg.MaxMindAccountID, MaxMindWarning: m.maxMindWarning, Locks: m.opts.Locks, Downloading: m.downloading,
		Dir: m.opts.Dir, Databases: []DatabaseStatus{}, Warnings: append([]string{}, m.warnings...)}
	for _, k := range Kinds {
		ds := DatabaseStatus{Kind: k, Error: m.loadErr[k]}
		if db := m.resolver.Current(k); db != nil {
			built := db.BuiltAt
			updated := m.loaded[k].mod.UTC()
			ds.Source, ds.Type, ds.Path, ds.BuiltAt, ds.UpdatedAt = db.Source, db.Type, db.Path, &built, &updated
		}
		// The newest download attempt, and every source whose last attempt failed.
		errs := []string{}
		if ds.Error != "" {
			errs = append(errs, ds.Error)
		}
		for _, s := range []Source{SourceMaxMind, SourceDBIP} {
			a := m.attempts[downloadName(s, k)]
			if a == nil {
				continue
			}
			if ds.LastAttempt == nil || a.at.After(*ds.LastAttempt) {
				at := a.at.UTC()
				ds.LastAttempt = &at
			}
			if a.err != "" {
				errs = append(errs, sourceNames[s]+": "+a.err)
			}
		}
		ds.Error = strings.Join(errs, "; ")
		st.Databases = append(st.Databases, ds)
	}
	return st
}
