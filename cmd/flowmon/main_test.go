package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"version"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut.String())
	}
	if strings.TrimSpace(out.String()) != version {
		t.Errorf("got %q", out.String())
	}
}

func TestUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"frobnicate"}, &out, &errOut); code != 2 {
		t.Fatalf("exit %d", code)
	}
	if !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("stderr %q", errOut.String())
	}
}

func TestHealthcheckFailsWithoutServer(t *testing.T) {
	t.Setenv("FLOWMON_HTTP_ADDR", "127.0.0.1:1")
	var out, errOut bytes.Buffer
	if code := run([]string{"healthcheck"}, &out, &errOut); code != 1 {
		t.Fatalf("exit %d", code)
	}
}
