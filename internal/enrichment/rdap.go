package enrichment

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
)

// RDAP timing: registrations are cached for a week ("not found" for a day), the IANA
// bootstrap files for a day.
const (
	RDAPTTL          = 7 * 24 * time.Hour
	rdapNotFoundTTL  = 24 * time.Hour
	bootstrapTTL     = 24 * time.Hour
	bootstrapRetry   = 10 * time.Minute
	rdapMemory       = 2000
	maxRDAPResponse  = 2 << 20
	defaultBootstrap = "https://data.iana.org/rdap/"
)

// RDAP statuses.
const (
	RDAPFound    = "found"
	RDAPNotFound = "not-found"
)

// Registration is what the registry says about the network an address belongs to.
type Registration struct {
	Handle  string   `json:"handle,omitempty"`
	Name    string   `json:"name,omitempty"`
	Type    string   `json:"type,omitempty"`
	Country string   `json:"country,omitempty"`
	CIDRs   []string `json:"cidrs"`
	// Start and End are the network's first and last address.
	Start string `json:"start,omitempty"`
	End   string `json:"end,omitempty"`
	// Organisation is the registrant: who holds the netblock.
	Organisation string     `json:"organisation,omitempty"`
	AbuseEmail   string     `json:"abuseEmail,omitempty"`
	Registered   *time.Time `json:"registered,omitempty"`
	LastChanged  *time.Time `json:"lastChanged,omitempty"`
	// Server is the RDAP server that answered, e.g. "rdap.arin.net".
	Server string `json:"server,omitempty"`
}

// RDAPResult is a registration lookup.
type RDAPResult struct {
	Status       string        `json:"status"`
	Registration *Registration `json:"registration,omitempty"`
	FetchedAt    time.Time     `json:"fetchedAt"`
}

// RDAPOptions configure RDAP.
type RDAPOptions struct {
	Client *http.Client
	Store  PersisterFunc
	// BootstrapURL is where ipv4.json and ipv6.json live; tests replace it.
	BootstrapURL string
	// Interval spaces requests to registries; 0 means one a second.
	Interval  time.Duration
	UserAgent string
	Now       func() time.Time
}

// RDAP looks up registration details on demand, finding each address's registry through
// the IANA bootstrap files (RFC 9224).
type RDAP struct {
	opts  RDAPOptions
	cache *cache
	limit *limiter

	mu        sync.Mutex
	services  []rdapService
	fetchedAt time.Time
	failedAt  time.Time
}

type rdapService struct {
	prefix netip.Prefix
	base   string
}

// NewRDAP returns an RDAP client.
func NewRDAP(opts RDAPOptions) *RDAP {
	if opts.Client == nil {
		opts.Client = &http.Client{Timeout: 15 * time.Second}
	}
	if opts.BootstrapURL == "" {
		opts.BootstrapURL = defaultBootstrap
	}
	if opts.Interval <= 0 {
		opts.Interval = time.Second
	}
	if opts.UserAgent == "" {
		opts.UserAgent = "flowmon"
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &RDAP{opts: opts, cache: newCache("rdap", opts.Store, rdapMemory), limit: &limiter{interval: opts.Interval}}
}

// ErrNotPublic is returned for addresses no registry describes (private, loopback …).
var ErrNotPublic = errors.New("not a public address")

// Lookup returns the registration for ip, from cache or from its registry.
func (r *RDAP) Lookup(ctx context.Context, ip netip.Addr) (RDAPResult, error) {
	ip = ip.Unmap()
	if !public(ip) {
		return RDAPResult{}, ErrNotPublic
	}
	now := r.opts.Now()
	if e, ok := r.cache.get(ctx, ip, now, RDAPTTL); ok {
		var res RDAPResult
		if json.Unmarshal([]byte(e.value), &res) == nil {
			return res, nil
		}
	}
	base, err := r.server(ctx, ip)
	if err != nil {
		return RDAPResult{}, err
	}
	if err := r.limit.wait(ctx); err != nil {
		return RDAPResult{}, err
	}
	res, err := r.fetch(ctx, base, ip)
	if err != nil {
		return RDAPResult{}, err
	}
	res.FetchedAt = now.UTC()
	ttl := RDAPTTL
	if res.Status == RDAPNotFound {
		ttl = rdapNotFoundTTL
	}
	b, _ := json.Marshal(res)
	r.cache.put(ctx, ip, string(b), now, ttl, true)
	return res, nil
}

// server finds the RDAP base URL for ip: the longest matching bootstrap prefix.
func (r *RDAP) server(ctx context.Context, ip netip.Addr) (string, error) {
	if err := r.loadBootstrap(ctx); err != nil {
		return "", err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	best, bits := "", -1
	for _, s := range r.services {
		if s.prefix.Contains(ip) && s.prefix.Bits() > bits {
			best, bits = s.base, s.prefix.Bits()
		}
	}
	if best == "" {
		return "", errors.New("no registry is listed for this address")
	}
	return best, nil
}

func (r *RDAP) loadBootstrap(ctx context.Context) error {
	now := r.opts.Now()
	r.mu.Lock()
	fresh := len(r.services) > 0 && now.Sub(r.fetchedAt) < bootstrapTTL
	recentFail := now.Sub(r.failedAt) < bootstrapRetry
	have := len(r.services) > 0
	r.mu.Unlock()
	if fresh || (have && recentFail) {
		return nil
	}
	var services []rdapService
	var err error
	for _, file := range []string{"ipv4.json", "ipv6.json"} {
		var s []rdapService
		if s, err = r.fetchBootstrap(ctx, file); err != nil {
			break
		}
		services = append(services, s...)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err != nil {
		r.failedAt = now
		if have {
			return nil // keep using the copy we have
		}
		return fmt.Errorf("reading the IANA RDAP bootstrap: %w", err)
	}
	r.services, r.fetchedAt = services, now
	return nil
}

func (r *RDAP) fetchBootstrap(ctx context.Context, file string) ([]rdapService, error) {
	body, err := r.get(ctx, strings.TrimSuffix(r.opts.BootstrapURL, "/")+"/"+file, "application/json")
	if err != nil {
		return nil, err
	}
	var doc struct {
		Services [][][]string `json:"services"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("%s: %w", file, err)
	}
	var out []rdapService
	for _, svc := range doc.Services {
		if len(svc) != 2 {
			continue
		}
		base := pickURL(svc[1])
		if base == "" {
			continue
		}
		for _, p := range svc[0] {
			if prefix, err := netip.ParsePrefix(p); err == nil {
				out = append(out, rdapService{prefix.Masked(), base})
			}
		}
	}
	return out, nil
}

// pickURL prefers an HTTPS base URL.
func pickURL(urls []string) string {
	for _, u := range urls {
		if strings.HasPrefix(u, "https://") {
			return u
		}
	}
	if len(urls) > 0 {
		return urls[0]
	}
	return ""
}

var errRDAPNotFound = errors.New("not found")

func (r *RDAP) get(ctx context.Context, u, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", r.opts.UserAgent)
	resp, err := r.opts.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, errRDAPNotFound
	case resp.StatusCode == http.StatusTooManyRequests:
		return nil, errors.New("the registry is limiting requests; try again later")
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("the registry answered %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxRDAPResponse+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxRDAPResponse {
		return nil, errors.New("the registry's answer is too large")
	}
	return b, nil
}

func (r *RDAP) fetch(ctx context.Context, base string, ip netip.Addr) (RDAPResult, error) {
	u := strings.TrimSuffix(base, "/") + "/ip/" + ip.String()
	body, err := r.get(ctx, u, "application/rdap+json")
	if errors.Is(err, errRDAPNotFound) {
		return RDAPResult{Status: RDAPNotFound}, nil
	}
	if err != nil {
		return RDAPResult{}, err
	}
	reg, err := parseRegistration(body)
	if err != nil {
		return RDAPResult{}, err
	}
	if pu, err := url.Parse(base); err == nil {
		reg.Server = pu.Host
	}
	return RDAPResult{Status: RDAPFound, Registration: reg}, nil
}

// rdapNetwork is the part of an RDAP IP network object (RFC 9083) we show.
type rdapNetwork struct {
	ObjectClassName string       `json:"objectClassName"`
	Handle          string       `json:"handle"`
	StartAddress    string       `json:"startAddress"`
	EndAddress      string       `json:"endAddress"`
	Name            string       `json:"name"`
	Type            string       `json:"type"`
	Country         string       `json:"country"`
	Events          []rdapEvent  `json:"events"`
	Entities        []rdapEntity `json:"entities"`
	CIDRs           []struct {
		V4Prefix string `json:"v4prefix"`
		V6Prefix string `json:"v6prefix"`
		Length   int    `json:"length"`
	} `json:"cidr0_cidrs"`
}

type rdapEvent struct {
	Action string    `json:"eventAction"`
	Date   time.Time `json:"eventDate"`
}

type rdapEntity struct {
	Handle   string          `json:"handle"`
	Roles    []string        `json:"roles"`
	VCard    json.RawMessage `json:"vcardArray"`
	Entities []rdapEntity    `json:"entities"`
}

func parseRegistration(body []byte) (*Registration, error) {
	var n rdapNetwork
	if err := json.Unmarshal(body, &n); err != nil {
		return nil, fmt.Errorf("reading the registry's answer: %w", err)
	}
	if n.ObjectClassName != "" && n.ObjectClassName != "ip network" {
		return nil, fmt.Errorf("the registry answered with a %q, not an IP network", n.ObjectClassName)
	}
	reg := &Registration{Handle: n.Handle, Name: n.Name, Type: n.Type, Country: strings.ToUpper(n.Country),
		Start: n.StartAddress, End: n.EndAddress, CIDRs: []string{}}
	for _, c := range n.CIDRs {
		p := c.V4Prefix
		if p == "" {
			p = c.V6Prefix
		}
		if prefix, err := netip.ParsePrefix(fmt.Sprintf("%s/%d", p, c.Length)); err == nil {
			reg.CIDRs = append(reg.CIDRs, prefix.String())
		}
	}
	if len(reg.CIDRs) == 0 {
		start, err1 := netip.ParseAddr(n.StartAddress)
		end, err2 := netip.ParseAddr(n.EndAddress)
		if err1 == nil && err2 == nil {
			for _, p := range rangePrefixes(start, end) {
				reg.CIDRs = append(reg.CIDRs, p.String())
			}
		}
	}
	for _, e := range n.Events {
		d := e.Date.UTC()
		switch e.Action {
		case "registration":
			reg.Registered = &d
		case "last changed":
			reg.LastChanged = &d
		}
	}
	// The registrant holds the block; the abuse contact is often nested under it.
	walkEntities(n.Entities, func(e rdapEntity) {
		name, email := vcard(e.VCard)
		for _, role := range e.Roles {
			switch role {
			case "registrant":
				if reg.Organisation == "" {
					reg.Organisation = name
				}
			case "abuse":
				if reg.AbuseEmail == "" {
					reg.AbuseEmail = email
				}
			}
		}
	})
	return reg, nil
}

func walkEntities(es []rdapEntity, f func(rdapEntity)) {
	for _, e := range es {
		f(e)
		walkEntities(e.Entities, f)
	}
}

// vcard reads the formatted name and first email from a jCard (RFC 7095):
// ["vcard", [["fn", {}, "text", "Example Inc."], ["email", {}, "text", "abuse@example.net"], …]].
func vcard(raw json.RawMessage) (name, email string) {
	var card []json.RawMessage
	if json.Unmarshal(raw, &card) != nil || len(card) != 2 {
		return "", ""
	}
	var props [][]json.RawMessage
	if json.Unmarshal(card[1], &props) != nil {
		return "", ""
	}
	for _, p := range props {
		if len(p) < 4 {
			continue
		}
		var key, value string
		if json.Unmarshal(p[0], &key) != nil || json.Unmarshal(p[3], &value) != nil {
			continue
		}
		switch key {
		case "fn":
			if name == "" {
				name = value
			}
		case "email":
			if email == "" {
				email = value
			}
		}
	}
	return name, email
}

// rangePrefixes splits [start, end] into the fewest prefixes, for registries that don't
// send cidr0_cidrs.
func rangePrefixes(start, end netip.Addr) []netip.Prefix {
	if start.BitLen() != end.BitLen() || end.Less(start) {
		return nil
	}
	var out []netip.Prefix
	for len(out) < 64 {
		bits := start.BitLen()
		// Grow the prefix while it starts at start and stays within end.
		best := netip.PrefixFrom(start, bits)
		for b := bits - 1; b >= 0; b-- {
			p := netip.PrefixFrom(start, b).Masked()
			if p.Addr() != start || lastAddr(p).Compare(end) > 0 {
				break
			}
			best = p
		}
		out = append(out, best)
		last := lastAddr(best)
		if last.Compare(end) >= 0 {
			break
		}
		start = last.Next()
		if !start.IsValid() {
			break
		}
	}
	return out
}

func lastAddr(p netip.Prefix) netip.Addr {
	b := p.Addr().AsSlice()
	for i := p.Bits(); i < len(b)*8; i++ {
		b[i/8] |= 1 << (7 - i%8)
	}
	a, _ := netip.AddrFromSlice(b)
	return a
}
