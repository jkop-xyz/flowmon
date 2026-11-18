package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/appstore"
	"github.com/jkop-xyz/flowmon/internal/config"
	"github.com/jkop-xyz/flowmon/internal/setup"
)

func bootstrapPath(cfg config.Config) string { return filepath.Join(cfg.DataDir, "bootstrap.json") }
func defaultSQLite(cfg config.Config) string { return filepath.Join(cfg.DataDir, "flowmon.db") }

// errNotConfigured means first-run setup hasn't chosen an App Store yet.
var errNotConfigured = errors.New("the App Store isn't set up yet")

// appStoreConfig decides where the App Store lives: FLOWMON_DATABASE_URL wins (and locks
// the setup screen), then bootstrap.json.
func appStoreConfig(cfg config.Config) (c appstore.Config, locked bool, err error) {
	if cfg.DatabaseURL != "" {
		c, err = appstore.ParseURL(cfg.DatabaseURL)
		if err != nil {
			return c, true, fmt.Errorf("FLOWMON_DATABASE_URL: %w", err)
		}
		if cfg.DatabasePasswordFile != "" {
			b, err := os.ReadFile(cfg.DatabasePasswordFile)
			if err != nil {
				return c, true, fmt.Errorf("FLOWMON_DATABASE_PASSWORD_FILE: %w", err)
			}
			c.Password = strings.TrimRight(string(b), "\r\n")
		}
		return c, true, nil
	}
	f, err := setup.Load(bootstrapPath(cfg))
	if err != nil {
		return c, false, err
	}
	if f == nil {
		return c, false, errNotConfigured
	}
	c, err = f.Database.Config(defaultSQLite(cfg))
	return c, false, err
}

// openAppStore opens the App Store, retrying in the background until it works:
// collection never waits for it (ADR 0001). onOpen runs once it is open.
func openAppStore(ctx context.Context, logger *slog.Logger, c appstore.Config, holder *appstore.Holder, onOpen func(*appstore.Store)) {
	try := func() bool {
		s, err := appstore.Open(ctx, c)
		if err != nil {
			logger.Error("app store unavailable; retrying in 30 s", "database", c.Redacted(), "err", err)
			return false
		}
		holder.Set(s)
		logger.Info("app store open", "database", c.Redacted())
		onOpen(s)
		return true
	}
	if try() {
		return
	}
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if try() {
					return
				}
			}
		}
	}()
}
