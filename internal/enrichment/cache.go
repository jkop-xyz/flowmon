// Package enrichment is the IP Enrichment that is looked up on demand rather than with
// every Flow (see GLOSSARY.md): reverse DNS names and RDAP registration details for
// Remote IPs. Results are cached in memory and in the App Store, so a restart or a
// second viewer doesn't ask again; when the App Store is unavailable the memory cache
// carries on alone.
package enrichment

import (
	"context"
	"net/netip"
	"sync"
	"time"
)

// Persister stores cached results. *appstore.Store implements it.
type Persister interface {
	CachedIPInfo(ctx context.Context, kind, ip string, now time.Time) (string, time.Time, error)
	CacheIPInfo(ctx context.Context, kind, ip, value string, now time.Time, ttl time.Duration) error
}

// PersisterFunc returns the App Store, or nil while it is unavailable.
type PersisterFunc func() Persister

type entry struct {
	value   string
	fetched time.Time
	expires time.Time
}

// cache is a bounded in-memory cache in front of the App Store.
type cache struct {
	kind  string
	store PersisterFunc
	max   int

	mu  sync.Mutex
	mem map[netip.Addr]entry
}

func newCache(kind string, store PersisterFunc, max int) *cache {
	return &cache{kind: kind, store: store, max: max, mem: map[netip.Addr]entry{}}
}

// memory returns a cached value from memory only.
func (c *cache) memory(ip netip.Addr, now time.Time) (entry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.mem[ip]
	if !ok || !now.Before(e.expires) {
		return entry{}, false
	}
	return e, true
}

// get looks in memory, then in the App Store, which refills memory.
func (c *cache) get(ctx context.Context, ip netip.Addr, now time.Time, ttl time.Duration) (entry, bool) {
	if e, ok := c.memory(ip, now); ok {
		return e, true
	}
	if c.store == nil {
		return entry{}, false
	}
	p := c.store()
	if p == nil {
		return entry{}, false
	}
	v, fetched, err := p.CachedIPInfo(ctx, c.kind, ip.String(), now)
	if err != nil {
		return entry{}, false
	}
	e := entry{value: v, fetched: fetched, expires: fetched.Add(ttl)}
	c.remember(ip, e, now)
	return e, true
}

// put caches a value in memory for ttl, and in the App Store too when persist is set.
func (c *cache) put(ctx context.Context, ip netip.Addr, value string, now time.Time, ttl time.Duration, persist bool) {
	c.remember(ip, entry{value: value, fetched: now, expires: now.Add(ttl)}, now)
	if !persist || c.store == nil {
		return
	}
	if p := c.store(); p != nil {
		// A failed write only costs a repeat lookup later.
		_ = p.CacheIPInfo(ctx, c.kind, ip.String(), value, now, ttl)
	}
}

func (c *cache) remember(ip netip.Addr, e entry, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.mem) >= c.max {
		for k, v := range c.mem {
			if !now.Before(v.expires) {
				delete(c.mem, k)
			}
		}
		// Still full: drop arbitrary entries; they are in the App Store anyway.
		for k := range c.mem {
			if len(c.mem) < c.max {
				break
			}
			delete(c.mem, k)
		}
	}
	c.mem[ip] = e
}

// public reports whether an address is worth asking about: public unicast only.
func public(a netip.Addr) bool {
	return a.IsValid() && a.IsGlobalUnicast() && !a.IsPrivate()
}

// limiter spaces calls at least interval apart.
type limiter struct {
	interval time.Duration
	mu       sync.Mutex
	next     time.Time
}

// wait blocks until the next call may go, or ctx is done.
func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	at := l.next
	if at.Before(now) {
		at = now
	}
	l.next = at.Add(l.interval)
	l.mu.Unlock()
	if d := time.Until(at); d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		}
	}
	return nil
}
