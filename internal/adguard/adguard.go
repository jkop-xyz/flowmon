// Package adguard collects AdGuard Home's query log as DNS Queries (ADR 0005): a Data
// Source kind that polls /control/querylog every 15 seconds for entries newer than the
// last one stored.
package adguard

import (
	"context"
	"fmt"
	"log/slog"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/datasource"
	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// Store keeps DNS Queries. *flowstore.Store implements it.
type Store interface {
	WriteDNS(ctx context.Context, qs []flowstore.DNSQuery) error
	LatestDNS(ctx context.Context, source string) (time.Time, []flowstore.DNSQuery, error)
}

// Options configure the kind.
type Options struct {
	Store Store
	// IsExporter says whether an address is an Exporter's, for the warning that clients
	// reach AdGuard Home through the router.
	IsExporter func(netip.Addr) bool
	// Retention is how long DNS Queries are kept: a poll never fetches older ones.
	Retention func() time.Duration
	Logger    *slog.Logger
	Now       func() time.Time
	// Interval between polls; 15 s when 0.
	Interval time.Duration
	// PageSize is how many entries a request asks for; 500 when 0.
	PageSize int
}

// FirstBackfill is how far back the first poll of a new source reaches.
const FirstBackfill = 24 * time.Hour

// Kind is the AdGuard Home Data Source kind.
type Kind struct{ opts Options }

// New returns the kind.
func New(opts Options) *Kind {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Interval == 0 {
		opts.Interval = 15 * time.Second
	}
	if opts.PageSize == 0 {
		opts.PageSize = 500
	}
	if opts.IsExporter == nil {
		opts.IsExporter = func(netip.Addr) bool { return false }
	}
	return &Kind{opts: opts}
}

// Info describes the kind.
func (k *Kind) Info() datasource.KindInfo {
	return datasource.KindInfo{
		Name:  "adguard",
		Title: "AdGuard Home",
		Description: "Reads AdGuard Home's query log every 15 seconds: every DNS lookup, with the device that asked, " +
			"the name, the answer and whether it was blocked. DNS logs say much more about what people do than Flows, " +
			"so nothing is collected until a source is added. DNS Queries are kept for the DNS retention on the Settings " +
			"page (7 days unless changed), apart from Flows.",
		Fields: []datasource.Field{
			{Key: "url", Label: "Address", Type: datasource.FieldURL, Required: true,
				Help: "AdGuard Home's web address, such as http://192.168.10.2:3000."},
			{Key: "username", Label: "User name", Type: datasource.FieldText},
			{Key: "password", Label: "Password", Type: datasource.FieldSecret},
			{Key: "skip_tls_verify", Label: "Skip TLS verification", Type: datasource.FieldBool, Default: "false",
				Help: "For an https address with a self-signed certificate."},
		},
	}
}

// Validate has nothing to add to the fields' own checks.
func (k *Kind) Validate(datasource.Settings) error { return nil }

func clientFor(s datasource.Settings) *client {
	return newClient(s["url"], s["username"], s["password"], s.Bool("skip_tls_verify"))
}

// Test connects and says AdGuard Home's version and how long it keeps its log.
func (k *Kind) Test(ctx context.Context, s datasource.Settings) (string, error) {
	c := clientFor(s)
	st, err := c.status(ctx)
	if err != nil {
		return "", err
	}
	lc, err := c.logConfig(ctx)
	if err != nil {
		return "", err
	}
	if !lc.Enabled {
		return "", fmt.Errorf("AdGuard Home %s answers, but its query log is off: turn it on under Settings → General settings", st.Version)
	}
	msg := fmt.Sprintf("Connected to AdGuard Home %s. Its query log keeps %s.", st.Version, days(time.Duration(lc.Interval)*time.Millisecond))
	if lc.AnonymizeClientIP {
		msg += " It anonymizes client addresses, so queries can't be tied to devices: turn that off for per-device DNS."
	}
	return msg, nil
}

func days(d time.Duration) string {
	switch n := int(d.Hours() / 24); {
	case d < 24*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case n == 1:
		return "1 day"
	default:
		return fmt.Sprintf("%d days", n)
	}
}

// Run polls until ctx ends.
func (k *Kind) Run(ctx context.Context, src datasource.Source, r datasource.Reporter) {
	p := &poller{k: k, src: src.ID, c: clientFor(src.Settings), r: r}
	t := time.NewTicker(k.opts.Interval)
	defer t.Stop()
	for {
		if err := p.poll(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.Failed(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// poller is one run's state.
type poller struct {
	k   *Kind
	src string
	c   *client
	r   datasource.Reporter

	loaded bool
	// cursor is the time of the newest stored query, atCursor the keys of the queries
	// stored at exactly that time.
	cursor   time.Time
	atCursor map[string]bool
	// recent holds the clients of the latest queries, for the relay warning.
	recent []netip.Addr
	// gap is the newest hole in the log, kept as a warning for a day.
	gap, gapSeen time.Time
	gapFrom      time.Time
}

// recentSize is how many queries the relay warning looks at, and relayShare the share
// of them from one Exporter that triggers it.
const (
	recentSize = 2000
	relayShare = 0.9
	relayMin   = 200
)

func key(q flowstore.DNSQuery) string {
	return q.Time.UTC().Format(time.RFC3339Nano) + "|" + q.Client.String() + "|" + q.Name + "|" + q.Type
}

func (p *poller) poll(ctx context.Context) error {
	now := p.k.opts.Now()
	if !p.loaded {
		at, qs, err := p.k.opts.Store.LatestDNS(ctx, p.src)
		if err != nil {
			return fmt.Errorf("reading the newest stored query: %w", err)
		}
		p.cursor, p.atCursor = at, map[string]bool{}
		for _, q := range qs {
			p.atCursor[key(q)] = true
		}
		p.loaded = true
	}
	floor := now.Add(-FirstBackfill)
	if !p.cursor.IsZero() {
		floor = p.cursor
		if r := p.k.opts.Retention; r != nil && r() > 0 && floor.Before(now.Add(-r())) {
			floor = now.Add(-r())
		}
	}

	var fresh []flowstore.DNSQuery
	olderThan, reachedFloor, endOfLog := "", false, false
	var oldestSeen time.Time
	for !reachedFloor && !endOfLog {
		page, err := p.c.queryLog(ctx, olderThan, p.k.opts.PageSize)
		if err != nil {
			return err
		}
		for _, e := range page.Data {
			q, ok := convert(e, p.src)
			if !ok {
				continue
			}
			oldestSeen = q.Time
			if q.Time.Before(floor) || (q.Time.Equal(p.cursor) && p.atCursor[key(q)]) {
				if q.Time.Before(floor) {
					reachedFloor = true
					break
				}
				continue
			}
			fresh = append(fresh, q)
		}
		endOfLog = len(page.Data) < p.k.opts.PageSize || page.Oldest == "" || page.Oldest == olderThan
		olderThan = page.Oldest
	}

	// The log ran out before reaching what was stored: AdGuard Home dropped entries
	// Flowmon never saw.
	if endOfLog && !reachedFloor && !p.cursor.IsZero() && !oldestSeen.IsZero() && oldestSeen.After(p.cursor) {
		p.gapFrom, p.gap, p.gapSeen = p.cursor, oldestSeen, now
	}

	if len(fresh) > 0 {
		slices.SortFunc(fresh, func(a, b flowstore.DNSQuery) int { return a.Time.Compare(b.Time) })
		if err := p.k.opts.Store.WriteDNS(ctx, fresh); err != nil {
			return fmt.Errorf("storing queries: %w", err)
		}
		newest := fresh[len(fresh)-1].Time
		if newest.After(p.cursor) {
			p.cursor, p.atCursor = newest, map[string]bool{}
		}
		for _, q := range fresh {
			if q.Time.Equal(p.cursor) {
				p.atCursor[key(q)] = true
			}
			p.recent = append(p.recent, q.Client)
		}
		if n := len(p.recent); n > recentSize {
			p.recent = slices.Clone(p.recent[n-recentSize:])
		}
	}
	p.r.Warn(p.warnings(now))
	if len(fresh) == 1 {
		p.r.Succeeded("1 new query")
	} else {
		p.r.Succeeded(strconv.Itoa(len(fresh)) + " new queries")
	}
	return nil
}

func (p *poller) warnings(now time.Time) []string {
	var out []string
	if !p.gapSeen.IsZero() && now.Sub(p.gapSeen) < 24*time.Hour {
		out = append(out, fmt.Sprintf("AdGuard Home's query log no longer had the queries from %s to %s: Flowmon was off longer than AdGuard Home keeps its log, so that time has no DNS.",
			p.gapFrom.UTC().Format(time.RFC3339), p.gap.UTC().Format(time.RFC3339)))
	}
	if len(p.recent) >= relayMin {
		counts := map[netip.Addr]int{}
		var top netip.Addr
		for _, a := range p.recent {
			counts[a]++
			if counts[a] > counts[top] {
				top = a
			}
		}
		if share := float64(counts[top]) / float64(len(p.recent)); share >= relayShare && p.k.opts.IsExporter(top) {
			out = append(out, fmt.Sprintf("%.0f%% of queries come from %s, which is an Exporter: devices reach AdGuard Home through the router, "+
				"so their DNS can't be told apart and domains can't be matched to their traffic. Hand out AdGuard Home's address "+
				"by DHCP instead of the router's, or turn on EDNS Client Subnet on the router.", share*100, top))
		}
	}
	return out
}

// blockedReasons are the reasons AdGuard Home gives for a blocked lookup. Rewrites and
// safe search answer the lookup, so they are not blocks.
var blockedReasons = map[string]bool{
	"FilteredBlackList": true, "FilteredSafeBrowsing": true, "FilteredParental": true,
	"FilteredBlockedService": true, "FilteredInvalid": true,
}

// convert turns a log entry into a DNS Query, or false for one Flowmon can't use.
func convert(e logEntry, src string) (flowstore.DNSQuery, bool) {
	t, err := time.Parse(time.RFC3339Nano, e.Time)
	if err != nil {
		return flowstore.DNSQuery{}, false
	}
	client, err := netip.ParseAddr(e.Client)
	if err != nil {
		return flowstore.DNSQuery{}, false
	}
	name := domains.Normalize(e.Question.Name)
	q := flowstore.DNSQuery{
		Time: t.UTC(), Source: src, Client: client.Unmap(), ClientName: e.ClientInfo.Name,
		Name: name, Registrable: domains.Registrable(name), Type: e.Question.Type, Status: e.Status,
		Blocked: blockedReasons[e.Reason], Reason: e.Reason, Upstream: e.Upstream, Cached: e.Cached, Protocol: e.ClientProto,
	}
	for _, a := range e.Answer {
		switch a.Type {
		case "A", "AAAA":
			if addr, err := netip.ParseAddr(a.Value); err == nil {
				q.Answers = append(q.Answers, addr.Unmap())
			}
		case "CNAME":
			q.CNAMEs = append(q.CNAMEs, domains.Normalize(a.Value))
		}
	}
	var rules []string
	for _, r := range e.Rules {
		if r.Text != "" {
			rules = append(rules, r.Text)
		}
	}
	q.Rule = strings.Join(rules, "\n")
	if ms, err := strconv.ParseFloat(e.ElapsedMs, 64); err == nil {
		q.Elapsed = time.Duration(ms * float64(time.Millisecond))
	}
	return q, true
}

var _ datasource.Kind = (*Kind)(nil)
