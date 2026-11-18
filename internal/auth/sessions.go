package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
)

// CookieName is the session cookie.
const CookieName = "flowmon_session"

// SecureCookies says when the session cookie gets the Secure attribute.
type SecureCookies string

// Secure cookie modes. Auto sets Secure whenever the browser used HTTPS, directly or
// through a trusted proxy.
const (
	SecureAuto   SecureCookies = "auto"
	SecureAlways SecureCookies = "always"
	SecureNever  SecureCookies = "never"
)

// Options configure a Manager.
type Options struct {
	// TTL is how long a session lasts without use; each use extends it.
	TTL           time.Duration
	Proxies       Proxies
	SecureCookies SecureCookies
	// CacheTTL keeps recently checked sessions valid in memory without asking the App
	// Store again.
	CacheTTL time.Duration
	// OutageGrace keeps sessions confirmed this recently valid while the App Store is
	// unavailable, so an outage doesn't log everyone out. 0 means an hour.
	OutageGrace time.Duration
	// Available reports whether the App Store answers; nil means it always does.
	Available func() bool
	Now       func() time.Time
}

// Manager issues and checks sessions.
type Manager struct {
	opts  Options
	store func() *appstore.Store

	mu    sync.Mutex
	cache map[string]cached

	// IPLimiter and UserLimiter limit failed logins per client address and per username.
	IPLimiter   *Limiter
	UserLimiter *Limiter
}

type cached struct {
	user    appstore.User
	checked time.Time
}

// NewManager returns a Manager. store returns the App Store, or nil before setup.
func NewManager(store func() *appstore.Store, opts Options) *Manager {
	if opts.TTL <= 0 {
		opts.TTL = 30 * 24 * time.Hour
	}
	if opts.CacheTTL <= 0 {
		opts.CacheTTL = time.Minute
	}
	if opts.OutageGrace <= 0 {
		opts.OutageGrace = time.Hour
	}
	if opts.Available == nil {
		opts.Available = func() bool { return true }
	}
	if opts.SecureCookies == "" {
		opts.SecureCookies = SecureAuto
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Manager{
		opts: opts, store: store, cache: map[string]cached{},
		IPLimiter:   NewLimiter(10, 15*time.Minute),
		UserLimiter: NewLimiter(5, 15*time.Minute),
	}
}

// Proxies returns the trusted proxy configuration.
func (m *Manager) Proxies() Proxies { return m.opts.Proxies }

// Errors from Login and Authenticate.
var (
	ErrBadCredentials = errors.New("auth: wrong username or password")
	ErrRateLimited    = errors.New("auth: too many failed logins")
	ErrNoStore        = errors.New("auth: the App Store isn't available")
	ErrNoSession      = errors.New("auth: not logged in")
	// ErrUnavailable means the App Store is set up but not answering.
	ErrUnavailable = errors.New("auth: the App Store is unavailable")
)

// RateLimitError says how long to wait.
type RateLimitError struct{ RetryAfter time.Duration }

func (e RateLimitError) Error() string { return ErrRateLimited.Error() }
func (e RateLimitError) Unwrap() error { return ErrRateLimited }

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// Login checks credentials, and on success creates a session and sets its cookie.
func (m *Manager) Login(ctx context.Context, w http.ResponseWriter, r *http.Request, username, password string) (appstore.User, error) {
	now := m.opts.Now()
	ip := m.opts.Proxies.ClientIP(r).String()
	if ok, wait := m.IPLimiter.Allowed(ip, now); !ok {
		return appstore.User{}, RateLimitError{wait}
	}
	if ok, wait := m.UserLimiter.Allowed(username, now); !ok {
		return appstore.User{}, RateLimitError{wait}
	}
	store := m.store()
	if store == nil {
		return appstore.User{}, ErrNoStore
	}
	if !m.opts.Available() {
		return appstore.User{}, ErrUnavailable
	}

	user, err := store.UserByUsername(ctx, username)
	hash := user.PasswordHash
	if err != nil || hash == "" || user.Disabled {
		hash = dummyHash // same work as a real check
	}
	match, verr := VerifyPassword(hash, password)
	if err != nil && !errors.Is(err, appstore.ErrNotFound) {
		return appstore.User{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if err != nil || verr != nil || !match || user.Disabled || user.PasswordHash == "" {
		m.IPLimiter.Fail(ip, now)
		m.UserLimiter.Fail(username, now)
		return appstore.User{}, ErrBadCredentials
	}
	m.IPLimiter.Reset(ip)
	m.UserLimiter.Reset(username)
	return user, m.StartSession(ctx, w, r, user)
}

// StartSession creates a session for user and sets its cookie.
func (m *Manager) StartSession(ctx context.Context, w http.ResponseWriter, r *http.Request, user appstore.User) error {
	store := m.store()
	if store == nil {
		return ErrNoStore
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	now := m.opts.Now()
	ua := r.UserAgent()
	if len(ua) > 256 {
		ua = ua[:256]
	}
	if err := store.CreateSession(ctx, appstore.Session{
		TokenHash: hashToken(token), UserID: user.ID, CreatedAt: now, ExpiresAt: now.Add(m.opts.TTL), LastSeenAt: now,
		ClientIP: m.opts.Proxies.ClientIP(r).String(), UserAgent: ua,
	}); err != nil {
		return err
	}
	http.SetCookie(w, m.cookie(r, token, now.Add(m.opts.TTL)))
	return nil
}

func (m *Manager) cookie(r *http.Request, value string, expires time.Time) *http.Cookie {
	secure := m.opts.SecureCookies == SecureAlways || (m.opts.SecureCookies == SecureAuto && m.opts.Proxies.IsHTTPS(r))
	c := &http.Cookie{Name: CookieName, Value: value, Path: "/", HttpOnly: true, Secure: secure, SameSite: http.SameSiteLaxMode}
	if value == "" {
		c.MaxAge = -1
	} else {
		c.Expires = expires
	}
	return c
}

// Authenticate returns the user of the request's session. Recently confirmed sessions
// are served from memory, which also bridges short App Store outages.
func (m *Manager) Authenticate(ctx context.Context, r *http.Request) (appstore.User, error) {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		if m.store() == nil {
			return appstore.User{}, ErrNoStore
		}
		return appstore.User{}, ErrNoSession
	}
	key := hashToken(c.Value)
	now := m.opts.Now()

	m.mu.Lock()
	hit, ok := m.cache[key]
	m.mu.Unlock()
	if ok && now.Sub(hit.checked) < m.opts.CacheTTL {
		return hit.user, nil
	}

	store := m.store()
	if store == nil {
		return appstore.User{}, ErrNoStore
	}
	// During an outage, a session confirmed recently stays valid from memory.
	outage := func(err error) (appstore.User, error) {
		if ok && now.Sub(hit.checked) < m.opts.OutageGrace {
			return hit.user, nil
		}
		return appstore.User{}, fmt.Errorf("%w: %w", ErrUnavailable, err)
	}
	if !m.opts.Available() {
		return outage(errors.New("not answering"))
	}
	sess, err := store.SessionByTokenHash(ctx, key, now)
	if errors.Is(err, appstore.ErrNotFound) {
		m.forget(key)
		return appstore.User{}, ErrNoSession
	}
	if err != nil {
		return outage(err)
	}
	user, err := store.UserByID(ctx, sess.UserID)
	if err != nil {
		return outage(err)
	}
	if user.Disabled {
		return appstore.User{}, ErrNoSession
	}
	// Extend the session at most once per cache period, not on every request.
	_ = store.TouchSession(ctx, key, now, now.Add(m.opts.TTL))

	m.mu.Lock()
	if len(m.cache) > 1000 {
		m.cache = map[string]cached{}
	}
	m.cache[key] = cached{user: user, checked: now}
	m.mu.Unlock()
	return user, nil
}

func (m *Manager) forget(key string) {
	m.mu.Lock()
	delete(m.cache, key)
	m.mu.Unlock()
}

// Logout ends the request's session and clears the cookie.
func (m *Manager) Logout(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
	http.SetCookie(w, m.cookie(r, "", time.Time{}))
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return nil
	}
	key := hashToken(c.Value)
	m.forget(key)
	if store := m.store(); store != nil {
		return store.DeleteSession(ctx, key)
	}
	return nil
}
