package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/auth"
)

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, nil, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != version {
		t.Errorf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"frobnicate"}, nil, &out, &errOut); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr %q", errOut.String())
	}
}

func TestHealthcheckFailsWithoutServer(t *testing.T) {
	t.Setenv("FLOWMON_HTTP_ADDR", "127.0.0.1:1")
	var out, errOut bytes.Buffer
	if code := run([]string{"healthcheck"}, nil, &out, &errOut); code != 1 {
		t.Fatalf("exit %d", code)
	}
}

func TestAdminResetPassword(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("FLOWMON_DATA_DIR", dir)
	t.Setenv("FLOWMON_DATABASE_URL", "sqlite://"+dir+"/flowmon.db")
	ctx := context.Background()
	s, err := appstore.Open(ctx, appstore.Config{Driver: appstore.SQLite, Path: dir + "/flowmon.db"})
	if err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("old password here")
	u, _ := s.CreateUser(ctx, "admin", hash, true, time.Now())
	s.Close()

	var out, errOut bytes.Buffer
	if code := run([]string{"admin", "reset-password", "admin"}, strings.NewReader("brand new password\n"), &out, &errOut); code != 0 {
		t.Fatalf("exit %d: %s", code, errOut.String())
	}
	s, _ = appstore.Open(ctx, appstore.Config{Driver: appstore.SQLite, Path: dir + "/flowmon.db"})
	defer s.Close()
	got, _ := s.UserByID(ctx, u.ID)
	if ok, _ := auth.VerifyPassword(got.PasswordHash, "brand new password"); !ok {
		t.Error("password not changed")
	}

	if code := run([]string{"admin", "reset-password", "nobody"}, strings.NewReader("brand new password\n"), &out, &errOut); code != 1 {
		t.Errorf("unknown user: exit %d", code)
	}
	if code := run([]string{"admin", "reset-password", "admin"}, strings.NewReader("short\n"), &out, &errOut); code != 1 {
		t.Errorf("weak password: exit %d", code)
	}
	if code := run([]string{"admin"}, nil, &out, &errOut); code != 2 {
		t.Errorf("usage: exit %d", code)
	}
}
