package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
)

var t0 = time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC)

func TestPasswordHashing(t *testing.T) {
	h, err := HashPassword("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=2,p=2$") {
		t.Errorf("hash format %q", h)
	}
	if ok, err := VerifyPassword(h, "correct horse battery"); err != nil || !ok {
		t.Errorf("right password: %v %v", ok, err)
	}
	if ok, _ := VerifyPassword(h, "correct horse battery!"); ok {
		t.Error("wrong password accepted")
	}
	h2, _ := HashPassword("correct horse battery")
	if h == h2 {
		t.Error("hashes must be salted")
	}
	if _, err := HashPassword("short"); !errors.Is(err, ErrWeakPassword) {
		t.Errorf("short password: %v", err)
	}
	for _, bad := range []string{"", "plain", "$argon2id$v=19$m=999999999,t=1,p=1$c2FsdA$aGFzaA"} {
		if ok, err := VerifyPassword(bad, "x"); ok || err == nil {
			t.Errorf("%q: %v %v", bad, ok, err)
		}
	}
}

func TestLimiter(t *testing.T) {
	l := NewLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allowed("a", t0); !ok {
			t.Fatalf("attempt %d refused", i)
		}
		l.Fail("a", t0)
	}
	if ok, wait := l.Allowed("a", t0.Add(10*time.Second)); ok || wait != 50*time.Second {
		t.Errorf("after 3 failures: %v %v", ok, wait)
	}
	if ok, _ := l.Allowed("b", t0); !ok {
		t.Error("other keys are unaffected")
	}
	if ok, _ := l.Allowed("a", t0.Add(61*time.Second)); !ok {
		t.Error("failures age out")
	}
	l.Fail("c", t0)
	l.Reset("c")
	if l.failures["c"] != nil {
		t.Error("reset")
	}
}

func req(remote string, headers map[string]string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.RemoteAddr = remote
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	return r
}

func TestProxies(t *testing.T) {
	p := Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}
	cases := []struct {
		remote, xff, want string
	}{
		{"192.168.10.44:5000", "", "192.168.10.44"},
		{"192.168.10.44:5000", "1.2.3.4", "192.168.10.44"},  // untrusted sender: header ignored
		{"10.0.0.5:5000", "192.168.10.44", "192.168.10.44"}, // through the proxy
		{"10.0.0.5:5000", "6.6.6.6, 192.168.10.44, 10.0.0.9", "192.168.10.44"},
		{"10.0.0.5:5000", "garbage", "10.0.0.5"},
	}
	for _, c := range cases {
		if got := p.ClientIP(req(c.remote, map[string]string{"X-Forwarded-For": c.xff})); got.String() != c.want {
			t.Errorf("%s via %q: %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
	if !p.IsHTTPS(req("10.0.0.5:1", map[string]string{"X-Forwarded-Proto": "https"})) {
		t.Error("trusted proxy saying https")
	}
	if p.IsHTTPS(req("192.168.10.44:1", map[string]string{"X-Forwarded-Proto": "https"})) {
		t.Error("an untrusted client can't claim https")
	}
}

func setup(t *testing.T) (*Manager, *appstore.Store, *time.Time) {
	t.Helper()
	s, err := appstore.Open(context.Background(), appstore.Config{Driver: appstore.SQLite, Path: filepath.Join(t.TempDir(), "a.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	hash, _ := HashPassword("correct horse battery")
	if _, err := s.CreateUser(context.Background(), "admin", hash, true, t0); err != nil {
		t.Fatal(err)
	}
	now := t0
	m := NewManager(func() *appstore.Store { return s }, Options{Now: func() time.Time { return now }})
	return m, s, &now
}

func login(m *Manager, user, pass string) (*httptest.ResponseRecorder, error) {
	w := httptest.NewRecorder()
	_, err := m.Login(context.Background(), w, req("192.168.10.44:5000", nil), user, pass)
	return w, err
}

func withCookie(w *httptest.ResponseRecorder) *http.Request {
	r := req("192.168.10.44:5000", nil)
	for _, c := range w.Result().Cookies() {
		r.AddCookie(c)
	}
	return r
}

func TestLoginSessionLogout(t *testing.T) {
	m, _, _ := setup(t)
	w, err := login(m, "Admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	c := w.Result().Cookies()[0]
	if c.Name != CookieName || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Secure {
		t.Errorf("cookie %+v", c)
	}
	r := withCookie(w)
	if u, err := m.Authenticate(context.Background(), r); err != nil || u.Username != "admin" {
		t.Fatalf("authenticate: %+v %v", u, err)
	}

	out := httptest.NewRecorder()
	if err := m.Logout(context.Background(), out, r); err != nil {
		t.Fatal(err)
	}
	if out.Result().Cookies()[0].MaxAge >= 0 {
		t.Error("logout must clear the cookie")
	}
	if _, err := m.Authenticate(context.Background(), r); !errors.Is(err, ErrNoSession) {
		t.Errorf("after logout: %v", err)
	}
}

func TestBadCredentialsAndRateLimit(t *testing.T) {
	m, _, _ := setup(t)
	if _, err := login(m, "admin", "wrong password!"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("wrong password: %v", err)
	}
	if _, err := login(m, "nobody", "whatever-password"); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("unknown user: %v", err)
	}
	for i := 0; i < 5; i++ {
		_, _ = login(m, "admin", "wrong password!")
	}
	_, err := login(m, "admin", "correct horse battery")
	var rl RateLimitError
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		t.Errorf("after repeated failures even the right password is refused for a while: %v", err)
	}
}

func TestSessionCacheBridgesAnOutage(t *testing.T) {
	m, s, now := setup(t)
	w, _ := login(m, "admin", "correct horse battery")
	r := withCookie(w)
	if _, err := m.Authenticate(context.Background(), r); err != nil {
		t.Fatal(err)
	}
	s.Close() // the App Store goes away
	*now = now.Add(30 * time.Second)
	if _, err := m.Authenticate(context.Background(), r); err != nil {
		t.Errorf("within the cache period the session still works: %v", err)
	}
	*now = now.Add(2 * time.Minute)
	if _, err := m.Authenticate(context.Background(), r); err == nil {
		t.Error("after the cache period the store must be asked again")
	}
}

func TestSecureCookieBehindTrustedProxy(t *testing.T) {
	m, _, _ := setup(t)
	m.opts.Proxies = Proxies{Trusted: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}}
	w := httptest.NewRecorder()
	r := req("10.0.0.5:1", map[string]string{"X-Forwarded-Proto": "https", "X-Forwarded-For": "192.168.10.44"})
	if _, err := m.Login(context.Background(), w, r, "admin", "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	if !w.Result().Cookies()[0].Secure {
		t.Error("HTTPS through a trusted proxy must give a Secure cookie")
	}
}
