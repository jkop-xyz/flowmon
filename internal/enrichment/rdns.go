package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"
)

// Reverse DNS timing: names are cached for a day (also "no name"), failures briefly.
const (
	ReverseDNSTTL    = 24 * time.Hour
	reverseDNSRetry  = 10 * time.Minute
	reverseDNSWait   = 3 * time.Second
	reverseDNSMemory = 20_000
)

// Name statuses.
const (
	NameResolved = "resolved"
	// NameNone means there is no reverse DNS name, or the address isn't public.
	NameNone    = "none"
	NamePending = "pending"
	// NameFailed means the lookup failed (timeout, server error); it is retried later.
	NameFailed = "failed"
)

// Name is an address's reverse DNS name.
type Name struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status"`
}

// AddrResolver looks up PTR names. *net.Resolver implements it.
type AddrResolver interface {
	LookupAddr(ctx context.Context, addr string) ([]string, error)
}

// ReverseDNSOptions configure ReverseDNS.
type ReverseDNSOptions struct {
	Resolver AddrResolver
	Store    PersisterFunc
	// Interval spaces lookups; 0 means 50 ms (20 a second).
	Interval time.Duration
	// Workers is how many lookups may wait on DNS at once; 0 means 4.
	Workers int
	Logger  *slog.Logger
	Now     func() time.Time
}

// ReverseDNS resolves names for addresses that are on screen, through a rate-limited
// background queue. Names never blocks: it answers from cache and queues the rest.
type ReverseDNS struct {
	opts    ReverseDNSOptions
	cache   *cache
	limit   *limiter
	queue   chan netip.Addr
	mu      sync.Mutex
	pending map[netip.Addr]bool
}

// NewReverseDNS returns a ReverseDNS. Call Run to start resolving.
func NewReverseDNS(opts ReverseDNSOptions) *ReverseDNS {
	if opts.Resolver == nil {
		opts.Resolver = net.DefaultResolver
	}
	if opts.Interval <= 0 {
		opts.Interval = 50 * time.Millisecond
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &ReverseDNS{opts: opts, cache: newCache("rdns", opts.Store, reverseDNSMemory), limit: &limiter{interval: opts.Interval},
		queue: make(chan netip.Addr, 2048), pending: map[netip.Addr]bool{}}
}

// Names returns what is known for each address now. Unknown public addresses are queued
// and reported as pending; ask again to get their names.
func (r *ReverseDNS) Names(ips []netip.Addr) map[netip.Addr]Name {
	now := r.opts.Now()
	out := make(map[netip.Addr]Name, len(ips))
	for _, ip := range ips {
		ip = ip.Unmap()
		if !public(ip) {
			out[ip] = Name{Status: NameNone}
			continue
		}
		if e, ok := r.cache.memory(ip, now); ok {
			var n Name
			if json.Unmarshal([]byte(e.value), &n) == nil {
				out[ip] = n
				continue
			}
		}
		out[ip] = Name{Status: NamePending}
		r.enqueue(ip)
	}
	return out
}

func (r *ReverseDNS) enqueue(ip netip.Addr) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.pending[ip] {
		return
	}
	select {
	case r.queue <- ip:
		r.pending[ip] = true
	default:
		// Full: the address is queued again the next time it is asked for.
	}
}

// Run resolves queued addresses until ctx is done.
func (r *ReverseDNS) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for range r.opts.Workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case ip := <-r.queue:
					r.resolve(ctx, ip)
					r.mu.Lock()
					delete(r.pending, ip)
					r.mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
}

func (r *ReverseDNS) resolve(ctx context.Context, ip netip.Addr) {
	now := r.opts.Now()
	// The App Store may already know; that costs no DNS query.
	if _, ok := r.cache.get(ctx, ip, now, ReverseDNSTTL); ok {
		return
	}
	if err := r.limit.wait(ctx); err != nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, reverseDNSWait)
	names, err := r.opts.Resolver.LookupAddr(lctx, ip.String())
	cancel()
	if ctx.Err() != nil {
		return
	}
	n := Name{Status: NameNone}
	var dnsErr *net.DNSError
	switch {
	case err == nil && len(names) > 0:
		n = Name{Name: strings.TrimSuffix(names[0], "."), Status: NameResolved}
	case err == nil, errors.As(err, &dnsErr) && dnsErr.IsNotFound:
		// No PTR record: a real answer, cached like a name.
	default:
		b, _ := json.Marshal(Name{Status: NameFailed})
		r.cache.put(ctx, ip, string(b), now, reverseDNSRetry, false)
		r.opts.Logger.Debug("reverse DNS failed", "ip", ip.String(), "err", err)
		return
	}
	b, _ := json.Marshal(n)
	r.cache.put(ctx, ip, string(b), now, ReverseDNSTTL, true)
}
