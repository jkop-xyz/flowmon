package healthcheck

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestURL(t *testing.T) {
	cases := map[string]string{
		":8080":          "http://127.0.0.1:8080/api/health",
		"0.0.0.0:8080":   "http://127.0.0.1:8080/api/health",
		"[::]:8080":      "http://[::1]:8080/api/health",
		"10.0.0.5:9000":  "http://10.0.0.5:9000/api/health",
		"localhost:8080": "http://localhost:8080/api/health",
	}
	for addr, want := range cases {
		got, err := URL(addr)
		if err != nil || got != want {
			t.Errorf("URL(%q) = %q, %v; want %q", addr, got, err, want)
		}
	}
	if _, err := URL("8080"); err == nil {
		t.Error("expected an error for an address without a port separator")
	}
}

func TestCheck(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr bool
	}{
		{"healthy", http.StatusOK, `{"status":"ok","version":"dev"}`, false},
		{"unhealthy status", http.StatusOK, `{"status":"starting"}`, true},
		{"server error", http.StatusInternalServerError, `{"status":"ok"}`, true},
		{"not json", http.StatusOK, `hello`, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()

			err := Check(context.Background(), srv.URL)
			if (err != nil) != tc.wantErr {
				t.Errorf("err = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestCheckUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	if err := Check(context.Background(), url); err == nil {
		t.Error("expected an error for a closed server")
	}
}
