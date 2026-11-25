package httpserver

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
	"github.com/jkop-xyz/flowmon/internal/secret"
)

type testKind struct{}

func (testKind) Info() datasource.KindInfo {
	return datasource.KindInfo{Name: "dns", Title: "Test DNS", Fields: []datasource.Field{
		{Key: "url", Label: "Address", Type: datasource.FieldURL, Required: true},
		{Key: "password", Label: "Password", Type: datasource.FieldSecret},
	}}
}
func (testKind) Validate(datasource.Settings) error { return nil }
func (testKind) Test(_ context.Context, s datasource.Settings) (string, error) {
	if s["password"] != "right" {
		return "", errors.New("wrong password")
	}
	return "connected to " + s["url"], nil
}
func (testKind) Run(ctx context.Context, _ datasource.Source, r datasource.Reporter) {
	r.Succeeded("ran")
	<-ctx.Done()
}

func TestSourceEndpoints(t *testing.T) {
	dir := t.TempDir()
	store, err := flowstore.Open(context.Background(), filepath.Join(dir, "f.duckdb"), flowstore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	box, _ := secret.LoadOrCreate(filepath.Join(dir, "secret.key"))
	env := []datasource.Source{{ID: "env-home", Kind: "dns", Name: "home", Enabled: true, Settings: datasource.Settings{"url": "http://a.example"}}}
	m, err := datasource.New(datasource.Options{Store: store, Box: box, Kinds: []datasource.Kind{testKind{}}, Env: env})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	c := &client{t: t, h: New(Options{UI: builtUI, Sources: m})}

	rec := c.do("GET", "/api/sources", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"Test DNS"`) || !strings.Contains(rec.Body.String(), `"id":"env-home"`) {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	rec = c.do("POST", "/api/sources", `{"kind":"dns","name":"","enabled":true,"settings":{"url":"http://b.example"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"name"`) {
		t.Errorf("no name: %d %s", rec.Code, rec.Body)
	}
	rec = c.do("POST", "/api/sources", `{"kind":"dns","name":"Backup","enabled":true,"settings":{"url":"ftp://b.example"}}`)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"field":"url"`) {
		t.Errorf("bad URL: %d %s", rec.Code, rec.Body)
	}
	rec = c.do("POST", "/api/sources", `{"kind":"dns","name":"Backup","enabled":true,"settings":{"url":"http://b.example","password":"right"}}`)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), `"right"`) || !strings.Contains(rec.Body.String(), `"secretsSet":["password"]`) {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var id string
	for _, s := range m.List() {
		if s.Name == "Backup" {
			id = s.ID
		}
	}

	// Test with the stored password, and with a wrong one typed in.
	rec = c.do("POST", "/api/sources/test", `{"id":"`+id+`","kind":"dns","settings":{"url":"http://c.example"}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":true`) || !strings.Contains(rec.Body.String(), "c.example") {
		t.Errorf("test with the stored password: %d %s", rec.Code, rec.Body)
	}
	rec = c.do("POST", "/api/sources/test", `{"kind":"dns","settings":{"url":"http://c.example","password":"nope"}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "wrong password") {
		t.Errorf("test with a wrong password: %d %s", rec.Code, rec.Body)
	}

	rec = c.do("PUT", "/api/sources/"+id, `{"name":"Backup DNS","enabled":false,"settings":{"url":"http://b.example"}}`)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"state":"disabled"`) || !strings.Contains(rec.Body.String(), `"secretsSet":["password"]`) {
		t.Errorf("disable: %d %s", rec.Code, rec.Body)
	}
	if rec := c.do("PUT", "/api/sources/env-home", `{"name":"x","enabled":true,"settings":{"url":"http://a.example"}}`); rec.Code != http.StatusBadRequest {
		t.Errorf("editing a locked source: %d", rec.Code)
	}
	if rec := c.do("DELETE", "/api/sources/"+id, ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec := c.do("DELETE", "/api/sources/"+id, ""); rec.Code != http.StatusNotFound {
		t.Errorf("delete again: %d", rec.Code)
	}
}
