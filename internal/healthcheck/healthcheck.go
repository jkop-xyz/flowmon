// Package healthcheck implements `flowmon healthcheck`, which container run configs use
// because Podman ignores the Dockerfile HEALTHCHECK instruction for OCI images.
package healthcheck

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// URL returns the health endpoint for a server listening on listenAddr. Wildcard and
// empty hosts are replaced by the loopback address so the check works inside a container.
func URL(listenAddr string) (string, error) {
	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return "", fmt.Errorf("invalid listen address %q: %w", listenAddr, err)
	}
	switch host {
	case "", "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return "http://" + net.JoinHostPort(host, port) + "/api/health", nil
}

// Check calls the health endpoint and returns an error unless the server reports "ok".
func Check(ctx context.Context, url string) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("health endpoint returned %s", resp.Status)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decoding health response: %w", err)
	}
	if body.Status != "ok" {
		return fmt.Errorf("server reports status %q", body.Status)
	}
	return nil
}
