package httpserver

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

func TestFlowStoreBackupDownload(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "flows.duckdb")
	store, err := flowstore.Open(ctx, path, flowstore.Options{Threads: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetSettings(ctx, map[string]string{"local_networks": "10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	c, ctrl := newAuthServer(t, func(o *Options) {
		o.FlowStore = &FlowStoreOptions{Store: store, Path: path, MaxBytes: 1 << 30, BackupToken: "b4ckup"}
	})

	// Neither a session nor the right token: refused.
	if w := c.do("GET", "/api/flow-store/backup", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("anonymous: %d", w.Code)
	}
	if w := c.do("GET", "/api/flow-store/backup", "", "Authorization", "Bearer wrong"); w.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d", w.Code)
	}

	// The token alone is enough, for scheduled backups.
	w := c.do("GET", "/api/flow-store/backup", "", "Authorization", "Bearer b4ckup")
	if w.Code != http.StatusOK || !strings.HasPrefix(w.Header().Get("Content-Disposition"), `attachment; filename="flowmon-flows-`) {
		t.Fatalf("with the token: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if got := w.Header().Get("Content-Length"); got == "" || got != strconv.Itoa(w.Body.Len()) {
		t.Errorf("Content-Length %s for %d bytes", got, w.Body.Len())
	}
	restored := filepath.Join(t.TempDir(), "flows.duckdb")
	if err := os.WriteFile(restored, w.Body.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := flowstore.Open(ctx, restored, flowstore.Options{Threads: 2})
	if err != nil {
		t.Fatal("restoring the download:", err)
	}
	if v, ok, _ := r.Setting(ctx, "local_networks"); !ok || v != "10.0.0.0/8" {
		t.Errorf("restored setting %q %v", v, ok)
	}
	r.Close()
	if left, _ := filepath.Glob(filepath.Join(dir, ".flows-snapshot-*")); len(left) != 0 {
		t.Errorf("snapshot files left: %v", left)
	}

	// With a session, too, which is how the Settings page downloads it.
	c.do("POST", "/api/setup/database", `{"token":"`+ctrl.Token()+`","database":{"driver":"sqlite"}}`)
	c.do("POST", "/api/setup/admin", `{"token":"`+ctrl.Token()+`","username":"admin","password":"long enough password"}`)
	if w := c.do("GET", "/api/flow-store/backup", ""); w.Code != http.StatusOK || w.Body.Len() == 0 {
		t.Errorf("with a session: %d", w.Code)
	}

	// The Settings card's data, and the banner's.
	w = c.do("GET", "/api/flow-store", "")
	var info flowStoreJSON
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil || info.Path != path || info.UsedBytes == 0 || info.Threads != 2 ||
		info.MaxBytes != 1<<30 || info.Cap == nil || info.Cap.Over || !info.BackupToken {
		t.Errorf("%d %s", w.Code, w.Body.String())
	}
	if w := c.do("GET", "/api/status", ""); !strings.Contains(w.Body.String(), `"flowStore":{"usedBytes":`) || !strings.Contains(w.Body.String(), `"overCap":false`) {
		t.Errorf("status %s", w.Body.String())
	}
}
