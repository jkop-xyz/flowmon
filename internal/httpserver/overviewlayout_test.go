package httpserver

import (
	"context"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
	"github.com/jkop-xyz/flowmon/internal/setup"
)

func TestOverviewLayoutEndpoints(t *testing.T) {
	dir := t.TempDir()
	holder := &appstore.Holder{}
	t.Cleanup(func() {
		if s := holder.Get(); s != nil {
			s.Close()
		}
	})
	m := auth.NewManager(holder.Get, auth.Options{})
	ctrl := setup.New(setup.Options{Holder: holder, Auth: m, BootstrapPath: filepath.Join(dir, "bootstrap.json"), DefaultSQLitePath: filepath.Join(dir, "flowmon.db")})
	h := New(Options{UI: builtUI, Auth: m, Setup: ctrl, AppStore: holder})
	alice := &client{t: t, h: h}
	anon := &client{t: t, h: h}

	const path = "/api/me/overview-layout"
	const empty = `{"sections":null}`
	for _, method := range []string{"GET", "PUT", "DELETE"} {
		if w := anon.do(method, path, `{"sections":[{"id":"load","hidden":false}]}`); w.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d", method, w.Code)
		}
	}

	if w := alice.do("POST", "/api/setup/database", `{"token":"`+ctrl.Token()+`","database":{"driver":"sqlite"}}`); w.Code != http.StatusOK {
		t.Fatalf("database step: %d %s", w.Code, w.Body)
	}
	if w := alice.do("POST", "/api/setup/admin", `{"token":"`+ctrl.Token()+`","username":"alice","password":"long enough password"}`); w.Code != http.StatusOK {
		t.Fatalf("admin step: %d %s", w.Code, w.Body)
	}
	hash, err := auth.HashPassword("another long password")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.Get().CreateUser(context.Background(), "bob", hash, false, time.Now()); err != nil {
		t.Fatal(err)
	}
	bob := &client{t: t, h: h}
	if w := bob.do("POST", "/api/auth/login", `{"username":"bob","password":"another long password"}`); w.Code != http.StatusOK {
		t.Fatalf("bob login: %d %s", w.Code, w.Body)
	}

	body := func(w interface{ String() string }) string { return strings.TrimSpace(w.String()) }
	if w := alice.do("GET", path, ""); w.Code != http.StatusOK || body(w.Body) != empty {
		t.Errorf("never saved: %d %s", w.Code, w.Body)
	}
	saved := `{"sections":[{"id":"top","hidden":true},{"id":"load","hidden":false},{"id":"future-thing","hidden":false}]}`
	if w := alice.do("PUT", path, saved); w.Code != http.StatusOK || body(w.Body) != saved {
		t.Fatalf("save: %d %s", w.Code, w.Body)
	}
	if w := alice.do("GET", path, ""); body(w.Body) != saved {
		t.Errorf("read back: %s", w.Body)
	}
	if w := bob.do("GET", path, ""); body(w.Body) != empty {
		t.Errorf("bob sees alice's layout: %s", w.Body)
	}
	bobs := `{"sections":[{"id":"health","hidden":false}]}`
	if w := bob.do("PUT", path, bobs); w.Code != http.StatusOK {
		t.Fatalf("bob saves: %d %s", w.Code, w.Body)
	}
	if w := alice.do("GET", path, ""); body(w.Body) != saved {
		t.Errorf("bob's save changed alice's layout: %s", w.Body)
	}
	replaced := `{"sections":[{"id":"dns","hidden":false}]}`
	if w := alice.do("PUT", path, replaced); w.Code != http.StatusOK {
		t.Fatalf("replace: %d", w.Code)
	}
	if w := alice.do("GET", path, ""); body(w.Body) != replaced {
		t.Errorf("replaced: %s", w.Body)
	}

	many := func(n, idLen int) string {
		var items []string
		for i := range n {
			id := "a" + strings.Repeat("b", idLen-3) + string(rune('a'+i/26)) + string(rune('a'+i%26))
			items = append(items, `{"id":"`+id+`","hidden":false}`)
		}
		return `{"sections":[` + strings.Join(items, ",") + `]}`
	}
	bad := map[string]string{
		"not an object":      `[]`,
		"no sections":        `{}`,
		"null sections":      `{"sections":null}`,
		"empty sections":     `{"sections":[]}`,
		"too many":           many(33, 8),
		"empty id":           `{"sections":[{"id":"","hidden":false}]}`,
		"uppercase id":       `{"sections":[{"id":"Load","hidden":false}]}`,
		"id starts with 1":   `{"sections":[{"id":"1load","hidden":false}]}`,
		"id with underscore": `{"sections":[{"id":"a_b","hidden":false}]}`,
		"id too long":        many(1, 33),
		"duplicate id":       `{"sections":[{"id":"load","hidden":false},{"id":"load","hidden":true}]}`,
		"hidden not boolean": `{"sections":[{"id":"load","hidden":"yes"}]}`,
		"unknown field":      `{"sections":[{"id":"load","hidden":false,"x":1}]}`,
	}
	for name, b := range bad {
		if w := alice.do("PUT", path, b); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), `"error":"`) {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	for name, b := range map[string]string{"32 sections": many(32, 8), "32-character id": many(1, 32)} {
		if w := alice.do("PUT", path, b); w.Code != http.StatusOK {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
		}
	}
	if w := alice.do("GET", path, ""); !strings.Contains(w.Body.String(), "bbbb") {
		t.Errorf("rejected bodies must not change the saved layout: %s", w.Body)
	}

	if w := alice.do("DELETE", path, ""); w.Code != http.StatusNoContent {
		t.Errorf("reset: %d", w.Code)
	}
	if w := alice.do("GET", path, ""); body(w.Body) != empty {
		t.Errorf("after reset: %s", w.Body)
	}
	if w := bob.do("GET", path, ""); body(w.Body) != bobs {
		t.Errorf("alice's reset touched bob: %s", w.Body)
	}
}
