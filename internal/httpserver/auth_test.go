package httpserver

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/setup"
)

type client struct {
	t       *testing.T
	h       http.Handler
	cookies []*http.Cookie
}

func (c *client) do(method, target, body string, headers ...string) *httptest.ResponseRecorder {
	c.t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	r.RemoteAddr = "192.168.10.44:5000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	for i := 0; i+1 < len(headers); i += 2 {
		r.Header.Set(headers[i], headers[i+1])
	}
	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, ck := range w.Result().Cookies() {
		c.cookies = []*http.Cookie{ck}
	}
	return w
}

func newAuthServer(t *testing.T, configure ...func(*Options)) (*client, *setup.Controller) {
	t.Helper()
	dir := t.TempDir()
	holder := &appstore.Holder{}
	t.Cleanup(func() {
		if s := holder.Get(); s != nil {
			s.Close()
		}
	})
	m := auth.NewManager(holder.Get, auth.Options{})
	ctrl := setup.New(setup.Options{Holder: holder, Auth: m, BootstrapPath: filepath.Join(dir, "bootstrap.json"), DefaultSQLitePath: filepath.Join(dir, "flowmon.db")})
	opts := Options{UI: builtUI, Auth: m, Setup: ctrl}
	for _, f := range configure {
		f(&opts)
	}
	return &client{t: t, h: New(opts)}, ctrl
}

func TestSetupLoginAndProtectedAPI(t *testing.T) {
	c, ctrl := newAuthServer(t)

	if w := c.do("GET", "/api/health", ""); w.Code != http.StatusOK {
		t.Errorf("health must stay public: %d", w.Code)
	}
	if w := c.do("GET", "/api/exporters", ""); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "setup_required") {
		t.Errorf("before setup: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/setup/status", ""); !strings.Contains(w.Body.String(), `"step":"database"`) {
		t.Errorf("status %s", w.Body.String())
	}
	if w := c.do("POST", "/api/setup/database", `{"token":"nope","database":{"driver":"sqlite"}}`); w.Code != http.StatusForbidden {
		t.Errorf("wrong token: %d", w.Code)
	}
	if w := c.do("POST", "/api/setup/database", `{"token":"`+ctrl.Token()+`","database":{"driver":"sqlite"}}`); w.Code != http.StatusOK {
		t.Fatalf("database step: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/setup/admin", `{"token":"`+ctrl.Token()+`","username":"admin","password":"long enough password"}`); w.Code != http.StatusOK {
		t.Fatalf("admin step: %d %s", w.Code, w.Body.String())
	}
	// Setup logged us in.
	if w := c.do("GET", "/api/exporters", ""); w.Code != http.StatusOK {
		t.Errorf("after setup: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/auth/session", ""); !strings.Contains(w.Body.String(), `"username":"admin"`) {
		t.Errorf("session %s", w.Body.String())
	}

	c.do("POST", "/api/auth/logout", "")
	if w := c.do("GET", "/api/exporters", ""); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "unauthenticated") {
		t.Errorf("after logout: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("POST", "/api/auth/login", `{"username":"admin","password":"wrong password"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong password: %d", w.Code)
	}
	if w := c.do("POST", "/api/auth/login", `{"username":"admin","password":"long enough password"}`); w.Code != http.StatusOK {
		t.Fatalf("login: %d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/exporters", ""); w.Code != http.StatusOK {
		t.Errorf("after login: %d", w.Code)
	}
	// The UI itself is always served, so the login page can load.
	anon := &client{t: t, h: c.h}
	if w := anon.do("GET", "/exporters", ""); w.Code != http.StatusOK {
		t.Errorf("UI: %d", w.Code)
	}
}

func TestLoginRateLimitOverHTTP(t *testing.T) {
	c, ctrl := newAuthServer(t)
	c.do("POST", "/api/setup/database", `{"token":"`+ctrl.Token()+`","database":{"driver":"sqlite"}}`)
	c.do("POST", "/api/setup/admin", `{"token":"`+ctrl.Token()+`","username":"admin","password":"long enough password"}`)
	var last *httptest.ResponseRecorder
	for i := 0; i < 7; i++ {
		last = c.do("POST", "/api/auth/login", `{"username":"admin","password":"wrong password"}`)
	}
	if last.Code != http.StatusTooManyRequests || last.Header().Get("Retry-After") == "" {
		t.Errorf("after 7 failures: %d %v", last.Code, last.Header())
	}
}

func TestSetupTokenGuessingIsLimited(t *testing.T) {
	c, _ := newAuthServer(t)
	var last *httptest.ResponseRecorder
	for i := 0; i < 12; i++ {
		last = c.do("POST", "/api/setup/database", `{"token":"guess","database":{"driver":"sqlite"}}`)
	}
	if last.Code != http.StatusTooManyRequests {
		t.Errorf("after 12 wrong tokens: %d", last.Code)
	}
}

func TestCrossOriginPostsAreRejected(t *testing.T) {
	c, _ := newAuthServer(t)
	w := c.do("POST", "/api/auth/login", `{"username":"admin","password":"x"}`, "Sec-Fetch-Site", "cross-site")
	if w.Code != http.StatusForbidden {
		t.Errorf("cross-site POST: %d", w.Code)
	}
	w = c.do("POST", "/api/auth/login", `{"username":"admin","password":"x"}`, "Sec-Fetch-Site", "same-origin")
	if w.Code == http.StatusForbidden {
		t.Errorf("same-origin POST must pass CSRF protection: %d", w.Code)
	}
}
