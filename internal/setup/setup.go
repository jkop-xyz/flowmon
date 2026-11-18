package setup

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base32"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
)

// Step is where first-run setup stands.
type Step string

// Steps.
const (
	StepDatabase Step = "database"
	StepAdmin    Step = "admin"
	StepDone     Step = "done"
)

// Errors returned by the Controller.
var (
	ErrBadToken    = errors.New("setup: wrong setup token")
	ErrWrongStep   = errors.New("setup: that step is already done")
	ErrLocked      = errors.New("setup: the database is set by FLOWMON_DATABASE_URL")
	ErrBadUsername = errors.New("setup: username must be 1–64 letters, digits, dots, dashes or underscores")
)

// Options configure a Controller.
type Options struct {
	Holder *appstore.Holder
	Auth   *auth.Manager
	// BootstrapPath is where the database choice is saved.
	BootstrapPath string
	// DefaultSQLitePath is used when SQLite is chosen.
	DefaultSQLitePath string
	// Locked is set when FLOWMON_DATABASE_URL decides the database.
	Locked bool
	// OnConfigured runs once the setup screen has opened the App Store, like the
	// startup path does for a configured one.
	OnConfigured func(*appstore.Store)
	Logger       *slog.Logger
	Now          func() time.Time
}

// Controller runs first-run setup. It is safe for concurrent use.
type Controller struct {
	opts  Options
	token string
	mu    sync.Mutex // one setup action at a time
}

// New returns a Controller with a fresh setup token.
func New(opts Options) *Controller {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	raw := make([]byte, 15)
	_, _ = rand.Read(raw)
	t := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw)
	return &Controller{opts: opts, token: t[:6] + "-" + t[6:12] + "-" + t[12:18] + "-" + t[18:]}
}

// Token is the one-time setup token. Print it to the logs only.
func (c *Controller) Token() string { return c.token }

// CheckToken compares in constant time; dashes, spaces and case don't matter.
func (c *Controller) CheckToken(t string) bool {
	norm := func(s string) string {
		return strings.ToUpper(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(s)))
	}
	return subtle.ConstantTimeCompare([]byte(norm(t)), []byte(norm(c.token))) == 1
}

// Status is what the setup screen needs to know.
type Status struct {
	Step Step `json:"step"`
	// DatabaseLocked is set when FLOWMON_DATABASE_URL decides the database.
	DatabaseLocked bool `json:"databaseLocked"`
	// Database describes the App Store in use, without its password.
	Database string `json:"database,omitempty"`
	// DatabaseUnavailable is set when the App Store is configured but can't be reached.
	DatabaseUnavailable bool     `json:"databaseUnavailable"`
	SSLModes            []string `json:"sslModes"`
}

// Status reports the current step.
func (c *Controller) Status(ctx context.Context) (Status, error) {
	st := Status{DatabaseLocked: c.opts.Locked, SSLModes: SSLModes}
	store := c.opts.Holder.Get()
	if store == nil {
		st.Step = StepDatabase
		if f, _ := Load(c.opts.BootstrapPath); f != nil || c.opts.Locked {
			st.DatabaseUnavailable = true
		}
		return st, nil
	}
	n, err := store.CountUsers(ctx)
	if err != nil {
		return st, err
	}
	st.Step = StepDone
	if n == 0 {
		st.Step = StepAdmin
	}
	st.Database = string(store.Driver())
	return st, nil
}

// TestDatabase checks that a Postgres choice can connect.
func (c *Controller) TestDatabase(ctx context.Context, token string, d Database) error {
	if !c.CheckToken(token) {
		return ErrBadToken
	}
	cfg, err := d.Config(c.opts.DefaultSQLitePath)
	if err != nil {
		return err
	}
	return appstore.TestConnection(ctx, cfg)
}

// ConfigureDatabase opens the chosen App Store, runs its migrations and saves the
// choice to bootstrap.json.
func (c *Controller) ConfigureDatabase(ctx context.Context, token string, d Database) error {
	if !c.CheckToken(token) {
		return ErrBadToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.opts.Locked {
		return ErrLocked
	}
	if c.opts.Holder.Get() != nil {
		return ErrWrongStep
	}
	cfg, err := d.Config(c.opts.DefaultSQLitePath)
	if err != nil {
		return err
	}
	store, err := appstore.Open(ctx, cfg)
	if err != nil {
		return err
	}
	if d.Driver == "" {
		d.Driver = appstore.SQLite
	}
	if d.Driver == appstore.SQLite && d.Path == "" {
		d.Path = c.opts.DefaultSQLitePath
	}
	if err := Save(c.opts.BootstrapPath, File{Database: d}); err != nil {
		store.Close()
		return fmt.Errorf("saving %s: %w", c.opts.BootstrapPath, err)
	}
	c.opts.Holder.Set(store)
	c.opts.Logger.Info("app store configured", "database", cfg.Redacted())
	if c.opts.OnConfigured != nil {
		c.opts.OnConfigured(store)
	}
	return nil
}

func validUsername(u string) bool {
	if len(u) == 0 || len(u) > 64 {
		return false
	}
	for _, r := range u {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// CreateAdmin creates the first account and logs it in.
func (c *Controller) CreateAdmin(ctx context.Context, w http.ResponseWriter, r *http.Request, token, username, password string) error {
	if !c.CheckToken(token) {
		return ErrBadToken
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	store := c.opts.Holder.Get()
	if store == nil {
		return ErrWrongStep
	}
	n, err := store.CountUsers(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrWrongStep
	}
	username = strings.TrimSpace(username)
	if !validUsername(username) {
		return ErrBadUsername
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return err
	}
	user, err := store.CreateUser(ctx, username, hash, true, c.opts.Now())
	if err != nil {
		return err
	}
	c.opts.Logger.Info("admin account created", "username", user.Username)
	return c.opts.Auth.StartSession(ctx, w, r, user)
}

// SeedAdmin creates an admin from environment variables if no account exists yet.
func SeedAdmin(ctx context.Context, store *appstore.Store, username, password string, now time.Time) (bool, error) {
	if username == "" || password == "" {
		return false, nil
	}
	n, err := store.CountUsers(ctx)
	if err != nil || n > 0 {
		return false, err
	}
	if !validUsername(username) {
		return false, ErrBadUsername
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return false, err
	}
	_, err = store.CreateUser(ctx, username, hash, true, now)
	return err == nil, err
}
