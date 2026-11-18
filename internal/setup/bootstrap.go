// Package setup holds where the App Store lives (bootstrap.json) and the first-run
// setup flow: choose the App Store, then create the admin account. Both steps need the
// one-time setup token printed to the logs.
package setup

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/jkop-xyz/flowmon/internal/appstore"
)

// Database is the App Store choice as saved in bootstrap.json and sent by the setup
// screen. For Postgres the fields are kept separate, as the screen shows them.
type Database struct {
	Driver   appstore.Driver `json:"driver"`
	Path     string          `json:"path,omitempty"`
	Host     string          `json:"host,omitempty"`
	Port     int             `json:"port,omitempty"`
	Database string          `json:"database,omitempty"`
	User     string          `json:"user,omitempty"`
	Password string          `json:"password,omitempty"`
	// PasswordFile is read instead of Password when set, e.g. a Docker secret.
	PasswordFile string `json:"passwordFile,omitempty"`
	Schema       string `json:"schema,omitempty"`
	SSLMode      string `json:"sslMode,omitempty"`
}

// SSLModes are the libpq sslmode values the setup screen offers.
var SSLModes = []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}

// Config turns the choice into an App Store config. defaultSQLite is used when no
// SQLite path was given.
func (d Database) Config(defaultSQLite string) (appstore.Config, error) {
	switch d.Driver {
	case appstore.SQLite, "":
		p := d.Path
		if p == "" {
			p = defaultSQLite
		}
		return appstore.Config{Driver: appstore.SQLite, Path: p}, nil
	case appstore.Postgres:
		if d.Host == "" || d.Database == "" || d.User == "" {
			return appstore.Config{}, errors.New("host, database and user are required")
		}
		port := d.Port
		if port == 0 {
			port = 5432
		}
		if port < 1 || port > 65535 {
			return appstore.Config{}, errors.New("port must be between 1 and 65535")
		}
		mode := d.SSLMode
		if mode == "" {
			mode = "prefer"
		}
		valid := false
		for _, m := range SSLModes {
			valid = valid || m == mode
		}
		if !valid {
			return appstore.Config{}, fmt.Errorf("sslmode %q is not one of %s", mode, strings.Join(SSLModes, ", "))
		}
		password := d.Password
		if d.PasswordFile != "" {
			b, err := os.ReadFile(d.PasswordFile)
			if err != nil {
				return appstore.Config{}, fmt.Errorf("reading the password file: %w", err)
			}
			password = strings.TrimRight(string(b), "\r\n")
		}
		u := url.URL{Scheme: "postgres", User: url.User(d.User), Host: net.JoinHostPort(d.Host, strconv.Itoa(port)), Path: "/" + d.Database,
			RawQuery: url.Values{"sslmode": {mode}}.Encode()}
		return appstore.Config{Driver: appstore.Postgres, URL: u.String(), Password: password, Schema: d.Schema}, nil
	}
	return appstore.Config{}, fmt.Errorf("unknown database driver %q", d.Driver)
}

// File is bootstrap.json.
type File struct {
	Version  int      `json:"version"`
	Database Database `json:"database"`
}

// Load reads bootstrap.json. A missing file returns nil and no error.
func Load(path string) (*File, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var f File
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &f, nil
}

// Save writes bootstrap.json atomically, readable only by Flowmon's user: it can hold
// the database password.
func Save(path string, f File) error {
	f.Version = 1
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".bootstrap-*.json")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}
