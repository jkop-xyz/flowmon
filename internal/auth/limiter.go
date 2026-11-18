package auth

import (
	"sync"
	"time"
)

// Limiter limits failed logins per key (client address, and separately username).
// After MaxFailures within Window, attempts are refused until the oldest failure ages
// out. It is safe for concurrent use.
type Limiter struct {
	MaxFailures int
	Window      time.Duration

	mu       sync.Mutex
	failures map[string][]time.Time
}

// NewLimiter returns a Limiter allowing max failures per window.
func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{MaxFailures: max, Window: window, failures: map[string][]time.Time{}}
}

// maxKeys bounds memory under a flood of distinct keys.
const maxKeys = 10_000

func (l *Limiter) prune(key string, now time.Time) []time.Time {
	kept := l.failures[key][:0]
	for _, t := range l.failures[key] {
		if now.Sub(t) < l.Window {
			kept = append(kept, t)
		}
	}
	if len(kept) == 0 {
		delete(l.failures, key)
		return nil
	}
	l.failures[key] = kept
	return kept
}

// Allowed reports whether another attempt is allowed, and if not, when it will be.
func (l *Limiter) Allowed(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	f := l.prune(key, now)
	if len(f) < l.MaxFailures {
		return true, 0
	}
	return false, l.Window - now.Sub(f[0])
}

// Fail records a failed attempt.
func (l *Limiter) Fail(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if _, ok := l.failures[key]; !ok && len(l.failures) >= maxKeys {
		for k := range l.failures {
			l.prune(k, now)
		}
		if len(l.failures) >= maxKeys {
			return
		}
	}
	l.failures[key] = append(l.prune(key, now), now)
}

// Reset clears a key after a successful login.
func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}
