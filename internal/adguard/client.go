package adguard

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// client talks to AdGuard Home's HTTP API (openapi/openapi.yaml in its repository).
type client struct {
	base       string
	user, pass string
	http       *http.Client
}

func newClient(base, user, pass string, skipTLSVerify bool) *client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if skipTLSVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the user chose it for a self-signed certificate
	}
	return &client{base: base, user: user, pass: pass, http: &http.Client{Transport: tr, Timeout: 15 * time.Second}}
}

// errUnauthorized is a wrong user or password.
var errUnauthorized = errors.New("AdGuard Home refused the user name or password")

func (c *client) get(ctx context.Context, path string, q url.Values, into any) error {
	u := c.base + "/control/" + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if c.user != "" || c.pass != "" {
		req.SetBasicAuth(c.user, c.pass)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errUnauthorized
	case resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusSeeOther:
		return errUnauthorized // older versions redirect to the login page
	case resp.StatusCode != http.StatusOK:
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return fmt.Errorf("AdGuard Home answered %s to %s: %s", resp.Status, path, body)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(into); err != nil {
		return fmt.Errorf("reading AdGuard Home's %s: %w", path, err)
	}
	return nil
}

type status struct {
	Version string `json:"version"`
	Running bool   `json:"running"`
}

func (c *client) status(ctx context.Context) (status, error) {
	var s status
	err := c.get(ctx, "status", nil, &s)
	return s, err
}

type logConfig struct {
	Enabled bool `json:"enabled"`
	// Interval is how long the log is kept, in milliseconds.
	Interval          float64 `json:"interval"`
	AnonymizeClientIP bool    `json:"anonymize_client_ip"`
}

func (c *client) logConfig(ctx context.Context) (logConfig, error) {
	var lc logConfig
	err := c.get(ctx, "querylog/config", nil, &lc)
	return lc, err
}

type logPage struct {
	// Oldest is the time of the page's oldest entry, as AdGuard Home writes it; it is
	// passed back as older_than for the next page.
	Oldest string     `json:"oldest"`
	Data   []logEntry `json:"data"`
}

type logEntry struct {
	Time     string `json:"time"`
	Client   string `json:"client"`
	Question struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"question"`
	Answer     []answer `json:"answer"`
	Status     string   `json:"status"`
	Reason     string   `json:"reason"`
	Rules      []rule   `json:"rules"`
	Upstream   string   `json:"upstream"`
	ElapsedMs  string   `json:"elapsedMs"`
	Cached     bool     `json:"cached"`
	ClientInfo struct {
		Name string `json:"name"`
	} `json:"client_info"`
	ClientProto string `json:"client_proto"`
}

type answer struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

type rule struct {
	Text string `json:"text"`
}

// queryLog returns up to limit entries, newest first, older than olderThan ("" for
// the newest).
func (c *client) queryLog(ctx context.Context, olderThan string, limit int) (logPage, error) {
	q := url.Values{"limit": {strconv.Itoa(limit)}}
	if olderThan != "" {
		q.Set("older_than", olderThan)
	}
	var p logPage
	err := c.get(ctx, "querylog", q, &p)
	return p, err
}
