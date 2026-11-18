package secret

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSealOpenAndKeyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.key")
	b, err := LoadOrCreate(path)
	if err != nil {
		t.Fatal(err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 || info.Size() != 32 {
		t.Errorf("key file %v %d", info.Mode().Perm(), info.Size())
	}
	sealed, err := b.Seal("authPass123")
	if err != nil || sealed == "authPass123" {
		t.Fatalf("%q %v", sealed, err)
	}
	if again, _ := b.Seal("authPass123"); again == sealed {
		t.Error("sealing must use a fresh nonce")
	}
	// The same key is loaded again after a restart.
	b2, _ := LoadOrCreate(path)
	if got, err := b2.Open(sealed); err != nil || got != "authPass123" {
		t.Errorf("%q %v", got, err)
	}
	other, _ := LoadOrCreate(filepath.Join(t.TempDir(), "other.key"))
	if _, err := other.Open(sealed); err != ErrCorrupt {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := b.Open("plain"); err != ErrCorrupt {
		t.Errorf("not sealed: %v", err)
	}
	if s, _ := b.Seal(""); s != "" {
		t.Error("empty stays empty")
	}
}
