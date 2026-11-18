// Package storecache keeps a copy of something read from the App Store in memory, so
// every request doesn't query it, and so the last copy keeps working while the App
// Store is unavailable.
package storecache

import (
	"context"
	"sync"
	"time"
)

// RetryAfter spaces attempts to load while loading fails.
const RetryAfter = 30 * time.Second

// Snapshot is a value loaded on first use and again after every change (Reload).
type Snapshot[T any] struct {
	load func(ctx context.Context) (T, error)
	now  func() time.Time

	mu      sync.Mutex
	value   T
	loaded  bool
	lastTry time.Time
}

// New returns a Snapshot that loads with load.
func New[T any](load func(ctx context.Context) (T, error)) *Snapshot[T] {
	return &Snapshot[T]{load: load, now: time.Now}
}

// SetClock replaces the clock (tests).
func (s *Snapshot[T]) SetClock(now func() time.Time) { s.now = now }

// Get returns the value, loading it if it hasn't been loaded yet (at most every
// RetryAfter while that fails). Until a load succeeds it is T's zero value.
func (s *Snapshot[T]) Get(ctx context.Context) T {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded && s.now().Sub(s.lastTry) >= RetryAfter {
		s.lastTry = s.now()
		_ = s.reloadLocked(ctx)
	}
	return s.value
}

// Reload loads the value again. On failure the old value is kept and loading is
// retried by Get.
func (s *Snapshot[T]) Reload(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reloadLocked(ctx)
}

func (s *Snapshot[T]) reloadLocked(ctx context.Context) error {
	v, err := s.load(ctx)
	if err != nil {
		s.loaded = false
		return err
	}
	s.value, s.loaded = v, true
	return nil
}
