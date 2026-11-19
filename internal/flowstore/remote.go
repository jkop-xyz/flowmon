package flowstore

import (
	"context"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/rollup"
)

// Top Remote IPs (ADR 0004). Rollups leave Remote IPs out to stay small, so the busiest
// Remote IPs are kept separately: per hour and per day, for each Exporter and Local Host,
// the top TopRemoteKeep Remote IPs by bytes and the top TopRemoteKeep by Flow count, with
// the rest summed into one "other" row (remote_ip ''), so each period still adds up to the
// total. A period is built from raw Flows once its Watermark has passed, and built again
// if late Flows or reprocessing change it afterwards.

// Top Remote IPs parameters.
const (
	TopRemoteKeep = 50
	// TopRemoteHourRetention is how long hourly lists are kept. Daily lists are kept as
	// long as hour Rollups.
	TopRemoteHourRetention = 90 * 24 * time.Hour
	// rawRemoteRange is the longest range answered from raw Flows alone (exact).
	rawRemoteRange = 24 * time.Hour
)

// SourceDay is the daily Top Remote IPs list.
const SourceDay Source = "day"

type period struct {
	gran string // "hour" or "day"
	unix int64
}

func periodSize(gran string) time.Duration {
	if gran == "day" {
		return 24 * time.Hour
	}
	return time.Hour
}

func (p period) start() time.Time { return time.Unix(p.unix, 0).UTC() }

const topRemoteMigration = `CREATE TABLE top_remote (
	granularity VARCHAR NOT NULL,
	period TIMESTAMP NOT NULL,
	exporter VARCHAR NOT NULL,
	local_host VARCHAR NOT NULL,
	local_is_exporter_host BOOLEAN NOT NULL,
	remote_ip VARCHAR NOT NULL,
	peer_country VARCHAR NOT NULL,
	peer_asn UINTEGER NOT NULL,
	bytes_rx DOUBLE NOT NULL,
	bytes_tx DOUBLE NOT NULL,
	packets_rx DOUBLE NOT NULL,
	packets_tx DOUBLE NOT NULL,
	flows DOUBLE NOT NULL
)`

// Migration 12 adds estimated_rx and estimated_tx DOUBLE DEFAULT 0 to top_remote: the
// bytes that are estimates from sampled Flows.

const topRemoteBuiltMigration = `CREATE TABLE top_remote_built (granularity VARCHAR NOT NULL, period TIMESTAMP NOT NULL, built_at TIMESTAMP NOT NULL, PRIMARY KEY (granularity, period))`

func (s *Store) loadTopRemoteBuilt(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT granularity, period FROM top_remote_built`)
	if err != nil {
		return fmt.Errorf("loading Top Remote IPs: %w", err)
	}
	defer rows.Close()
	s.mu.Lock()
	defer s.mu.Unlock()
	for rows.Next() {
		var g string
		var p time.Time
		if err := rows.Scan(&g, &p); err != nil {
			return err
		}
		s.topBuilt[period{g, p.UTC().Unix()}] = true
	}
	return rows.Err()
}

// sqlArgs numbers positional parameters as they are added.
type sqlArgs struct{ args []any }

func (a *sqlArgs) add(v any) string {
	a.args = append(a.args, v)
	return fmt.Sprintf("$%d", len(a.args))
}

// remoteFilter narrows the rows that answer a Remote IPs query.
type remoteFilter struct {
	exporter, remote netip.Addr
	// hosts limits rows to these Local Hosts when byHost is set (none matches nothing).
	hosts  []string
	byHost bool
}

// where returns the filter as SQL conditions; peer is the Remote IP column.
func (f remoteFilter) where(a *sqlArgs, peer string) string {
	var w string
	if f.exporter.IsValid() {
		w += ` AND exporter = ` + a.add(f.exporter.String())
	}
	if f.remote.IsValid() {
		w += ` AND ` + peer + ` = ` + a.add(f.remote.String())
	}
	if f.byHost {
		w += ` AND ` + inList(a, "local_host", f.hosts)
	}
	return w
}

// inList is "col IN (…)" over values, or FALSE when there are none.
func inList(a *sqlArgs, col string, values []string) string {
	if len(values) == 0 {
		return `FALSE`
	}
	ps := make([]string, len(values))
	for i, v := range values {
		ps[i] = a.add(v)
	}
	return col + ` IN (` + strings.Join(ps, ", ") + `)`
}

// rawRemoteSQL selects, per raw Flow with a Remote IP overlapping [w0, w1), its share of
// bytes and packets inside the window, spread over its duration like Rollups are, and
// counts the Flow if it ended in the window. It returns "" when no raw table applies.
func (s *Store) rawRemoteSQL(a *sqlArgs, w0, w1 time.Time, f remoteFilter) string {
	// Flows are filed by end time and spread over at most rollup.MaxSpread.
	tables := s.tablesFor(w0, w1.Add(rollup.MaxSpread))
	if len(tables) == 0 {
		return ""
	}
	p0, p1 := a.add(w0.UTC()), a.add(w1.UTC())
	where := fmt.Sprintf(`direction IN (1, 2) AND end_ts >= %s AND start_ts < %s`, p0, p1) + f.where(a, "peer")
	spread := fmt.Sprintf(`INTERVAL %d SECOND`, int(rollup.MaxSpread.Seconds()))
	inWindow := fmt.Sprintf(`(end_ts >= %s AND end_ts < %s)`, p0, p1)
	share := fmt.Sprintf(`CASE WHEN end_ts <= start_ts OR end_ts - start_ts > %s
		THEN CASE WHEN %s THEN 1.0 ELSE 0.0 END
		ELSE date_diff('microsecond', greatest(start_ts, %s), least(end_ts, %s))::DOUBLE / date_diff('microsecond', start_ts, end_ts) END`,
		spread, inWindow, p0, p1)
	var parts []string
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT exporter, local_host, local_is_exporter_host, peer, peer_country, peer_asn, direction, bytes, packets,
			coalesce(sampling_rate, 0) > 1 AS sampled, %s AS share, CASE WHEN %s THEN 1.0 ELSE 0.0 END AS flows FROM %s WHERE %s`, share, inWindow, t, where))
	}
	return `SELECT exporter, local_host, local_is_exporter_host, peer, coalesce(peer_country, '') AS peer_country, coalesce(peer_asn, 0)::UINTEGER AS peer_asn,
		share * CASE WHEN direction = 1 THEN bytes ELSE 0 END AS rx, share * CASE WHEN direction = 2 THEN bytes ELSE 0 END AS tx,
		share * CASE WHEN direction = 1 THEN packets ELSE 0 END AS prx, share * CASE WHEN direction = 2 THEN packets ELSE 0 END AS ptx, flows,
		share * CASE WHEN direction = 1 AND sampled THEN bytes ELSE 0 END AS erx, share * CASE WHEN direction = 2 AND sampled THEN bytes ELSE 0 END AS etx
		FROM (` + strings.Join(parts, "\nUNION ALL\n") + `) WHERE share > 0 OR flows > 0`
}

// buildTopRemote (re)builds one period from raw Flows.
func (s *Store) buildTopRemote(ctx context.Context, p period) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	start := p.start()
	a := &sqlArgs{}
	src := s.rawRemoteSQL(a, start, start.Add(periodSize(p.gran)), remoteFilter{})

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM top_remote WHERE granularity = ? AND period = ?`, p.gran, start); err != nil {
		return fmt.Errorf("building Top Remote IPs: %w", err)
	}
	if src != "" {
		g, ps := a.add(p.gran), a.add(start)
		q := fmt.Sprintf(`INSERT INTO top_remote
WITH f AS (%[1]s),
g AS (SELECT exporter, local_host, bool_or(local_is_exporter_host) AS leh, peer, max(peer_country) AS pc, max(peer_asn) AS pa,
	sum(rx) AS rx, sum(tx) AS tx, sum(prx) AS prx, sum(ptx) AS ptx, sum(flows) AS flows, sum(erx) AS erx, sum(etx) AS etx
	FROM f GROUP BY exporter, local_host, peer),
r AS (SELECT *,
	row_number() OVER (PARTITION BY exporter, local_host ORDER BY rx + tx DESC, peer) <= %[4]d
	OR row_number() OVER (PARTITION BY exporter, local_host ORDER BY flows DESC, rx + tx DESC, peer) <= %[4]d AS keep
	FROM g),
k AS (SELECT *, CASE WHEN keep THEN peer ELSE '' END AS rip, CASE WHEN keep THEN pc ELSE '' END AS rpc,
	CASE WHEN keep THEN pa ELSE 0 END::UINTEGER AS rpa FROM r)
SELECT %[2]s, %[3]s, exporter, local_host, bool_or(leh), rip, rpc, rpa, sum(rx), sum(tx), sum(prx), sum(ptx), sum(flows), sum(erx), sum(etx)
FROM k GROUP BY exporter, local_host, rip, rpc, rpa`, src, g, ps, TopRemoteKeep)
		if _, err := tx.ExecContext(ctx, q, a.args...); err != nil {
			return fmt.Errorf("building Top Remote IPs: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO top_remote_built VALUES (?, ?, ?)
		ON CONFLICT (granularity, period) DO UPDATE SET built_at = excluded.built_at`, p.gran, start, time.Now().UTC()); err != nil {
		return fmt.Errorf("building Top Remote IPs: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.mu.Lock()
	s.topBuilt[p] = true
	delete(s.topDirty, p)
	s.mu.Unlock()
	return nil
}

// BuildTopRemote builds every hour and day of Top Remote IPs that is complete (its end
// is more than watermark before now) and not built yet, or changed since. Only periods
// with raw Flows can be built. It returns how many periods were built.
func (s *Store) BuildTopRemote(ctx context.Context, now time.Time, watermark time.Duration) (int, error) {
	days := s.RawDays(time.Time{})
	if len(days) == 0 {
		return 0, nil
	}
	complete := now.Add(-watermark).UTC()
	var due []period
	s.mu.Lock()
	first := days[0]
	if cut := now.Add(-TopRemoteHourRetention).UTC().Truncate(time.Hour); first.Before(cut) {
		first = cut
	}
	for h := first.Truncate(time.Hour); !h.Add(time.Hour).After(complete); h = h.Add(time.Hour) {
		if p := (period{"hour", h.Unix()}); !s.topBuilt[p] || s.topDirty[p] {
			due = append(due, p)
		}
	}
	for d := days[0]; !d.Add(24 * time.Hour).After(complete); d = d.Add(24 * time.Hour) {
		if p := (period{"day", d.Unix()}); !s.topBuilt[p] || s.topDirty[p] {
			due = append(due, p)
		}
	}
	s.mu.Unlock()
	for i, p := range due {
		if err := s.buildTopRemote(ctx, p); err != nil {
			return i, err
		}
	}
	return len(due), nil
}

// markTopDirty marks built periods that Flows with a Remote IP add to, so they are built
// again. Periods not built yet need nothing: they are built from raw Flows later.
func (s *Store) markTopDirty(flows []rollupSpan) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.topBuilt) == 0 {
		return
	}
	for _, f := range flows {
		for _, gran := range []string{"hour", "day"} {
			size := periodSize(gran)
			for b := f.start.Truncate(size); !b.After(f.end); b = b.Add(size) {
				if p := (period{gran, b.Unix()}); s.topBuilt[p] {
					s.topDirty[p] = true
				}
			}
		}
	}
}

// rollupSpan is the time range a Flow's bytes are spread over.
type rollupSpan struct{ start, end time.Time }

// markTopDirtyFrom marks every built period that ends after from, after reprocessing.
func (s *Store) markTopDirtyFrom(from time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for p := range s.topBuilt {
		if p.start().Add(periodSize(p.gran)).After(from) {
			s.topDirty[p] = true
		}
	}
}

// applyTopRemoteRetention drops hourly lists older than TopRemoteHourRetention and daily
// lists older than dayRetention.
func (s *Store) applyTopRemoteRetention(ctx context.Context, now time.Time, dayRetention time.Duration) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	cuts := map[string]time.Time{"hour": now.Add(-TopRemoteHourRetention).UTC(), "day": now.Add(-dayRetention).UTC()}
	for gran, cut := range cuts {
		// A period goes once all of it is older than the cut.
		before := cut.Add(-periodSize(gran))
		for _, q := range []string{`DELETE FROM top_remote WHERE granularity = ? AND period < ?`, `DELETE FROM top_remote_built WHERE granularity = ? AND period < ?`} {
			if _, err := s.db.ExecContext(ctx, q, gran, before); err != nil {
				return fmt.Errorf("Top Remote IPs retention: %w", err)
			}
		}
		s.mu.Lock()
		for p := range s.topBuilt {
			if p.gran == gran && p.start().Before(before) {
				delete(s.topBuilt, p)
				delete(s.topDirty, p)
			}
		}
		s.mu.Unlock()
	}
	return nil
}

// RemoteQuery asks for Remote IPs over a time range.
type RemoteQuery struct {
	From, To time.Time
	Exporter netip.Addr
	// LocalHosts, when not nil, counts only traffic of these Local Hosts (addresses).
	LocalHosts []string
	// ByFlows ranks by Flow count instead of bytes.
	ByFlows bool
	Limit   int
	// Now decides which lists still exist; zero means time.Now().
	Now time.Time
}

// RemoteItem is one Remote IP's traffic over the range.
type RemoteItem struct {
	RemoteIP      string  `json:"remoteIp"`
	Country       string  `json:"country,omitempty"`
	ASN           uint32  `json:"asn,omitempty"`
	ASNOrg        string  `json:"asnOrg,omitempty"`
	ReceivedBytes float64 `json:"receivedBytes"`
	SentBytes     float64 `json:"sentBytes"`
	TotalBytes    float64 `json:"totalBytes"`
	Flows         float64 `json:"flows"`
	LocalHosts    int     `json:"localHosts"`
	// EstimatedBytes is the part of TotalBytes scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes,omitempty"`
}

// Coverage says where an answer about Remote IPs came from and what it can't include.
type Coverage struct {
	From time.Time `json:"from"`
	To   time.Time `json:"to"`
	// Source is raw (exact), hour or day (Top Remote IPs lists, plus raw Flows for the most
	// recent part that has no list yet).
	Source Source `json:"source"`
	// Approximate is set when lists were used: a Remote IP outside a period's top lists
	// counts towards "other" there, so its totals are a lower bound.
	Approximate bool `json:"approximate"`
	// RawFrom is the start of the oldest raw Flows still kept; nil when there are none.
	RawFrom *time.Time `json:"rawFrom,omitempty"`
	// ListsFrom is the start of the oldest list of the chosen granularity; nil when none.
	ListsFrom *time.Time `json:"listsFrom,omitempty"`
}

// RemoteResult ranks Remote IPs.
type RemoteResult struct {
	Coverage
	Items []RemoteItem `json:"items"`
	// TotalBytes is all traffic with Remote IPs; OtherBytes the part not attributed to a
	// listed Remote IP (only with lists).
	TotalBytes float64 `json:"totalBytes"`
	OtherBytes float64 `json:"otherBytes"`
	// EstimatedBytes is the part of TotalBytes scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes"`
}

// remoteSource builds the union of list rows and raw Flows that answers a query over
// [from, to), and says how it was answered.
func (s *Store) remoteSource(a *sqlArgs, from, to, now time.Time, f remoteFilter) (string, Coverage) {
	from, to = from.UTC(), to.UTC()
	cov := Coverage{From: from, To: to, Source: SourceRaw}
	days := s.RawDays(time.Time{})
	var rawFrom time.Time
	if len(days) > 0 {
		rawFrom = days[0]
		cov.RawFrom = &rawFrom
	}
	rawCols := `exporter, local_host, local_is_exporter_host, peer, peer_country, peer_asn, rx, tx, flows, erx + etx AS est`
	if !rawFrom.IsZero() && !from.Before(rawFrom) && to.Sub(from) <= rawRemoteRange {
		raw := s.rawRemoteSQL(a, from, to, f)
		if raw == "" {
			return "", cov
		}
		return `SELECT ` + rawCols + ` FROM (` + raw + `)`, cov
	}

	gran, src := "hour", SourceHour
	if from.Before(now.Add(-TopRemoteHourRetention)) {
		gran, src = "day", SourceDay
	}
	cov.Source, cov.Approximate = src, true
	size := periodSize(gran)
	// Lists cover whole periods up to the newest one built; raw Flows cover the rest.
	var oldest, newestEnd time.Time
	s.mu.Lock()
	for p := range s.topBuilt {
		if p.gran != gran {
			continue
		}
		st := p.start()
		if oldest.IsZero() || st.Before(oldest) {
			oldest = st
		}
		if end := st.Add(size); end.After(newestEnd) && !end.After(to.Add(size)) {
			newestEnd = end
		}
	}
	s.mu.Unlock()
	var parts []string
	tail := from
	if !oldest.IsZero() {
		cov.ListsFrom = &oldest
		where := fmt.Sprintf(`granularity = %s AND period >= %s AND period < %s`, a.add(gran), a.add(from.Truncate(size)), a.add(minTime(to, newestEnd))) +
			f.where(a, "remote_ip")
		parts = append(parts, `SELECT exporter, local_host, local_is_exporter_host, remote_ip AS peer, peer_country, peer_asn, bytes_rx AS rx, bytes_tx AS tx, flows,
			estimated_rx + estimated_tx AS est FROM top_remote WHERE `+where)
		if newestEnd.After(tail) {
			tail = newestEnd
		}
	}
	if tail.Before(to) {
		if raw := s.rawRemoteSQL(a, tail, to, f); raw != "" {
			parts = append(parts, `SELECT `+rawCols+` FROM (`+raw+`)`)
		}
	}
	if len(parts) == 0 {
		return "", cov
	}
	return strings.Join(parts, "\nUNION ALL\n"), cov
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

func (q RemoteQuery) now() time.Time {
	if q.Now.IsZero() {
		return time.Now()
	}
	return q.Now
}

// TopRemoteIPs ranks Remote IPs by bytes or Flow count. Short recent ranges are exact,
// from raw Flows; others use Top Remote IPs lists and are approximate.
func (s *Store) TopRemoteIPs(ctx context.Context, q RemoteQuery) (RemoteResult, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return RemoteResult{}, ErrInvalidRange
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}
	q.Limit = min(q.Limit, MaxTopLimit)
	a := &sqlArgs{}
	src, cov := s.remoteSource(a, q.From, q.To, q.now(), remoteFilter{exporter: q.Exporter, hosts: q.LocalHosts, byHost: q.LocalHosts != nil})
	res := RemoteResult{Coverage: cov, Items: []RemoteItem{}}
	if src == "" {
		return res, nil
	}
	order := `rx + tx DESC, flows DESC`
	if q.ByFlows {
		order = `flows DESC, rx + tx DESC`
	}
	query := fmt.Sprintf(`WITH u AS (%s),
g AS (SELECT peer, max(peer_country) AS pc, max(peer_asn) AS pa, sum(rx) AS rx, sum(tx) AS tx, sum(flows) AS flows,
	count(DISTINCT local_host) AS hosts, sum(est) AS est FROM u GROUP BY peer)
SELECT peer, pc, pa, rx, tx, flows, hosts, sum(rx + tx) OVER () AS total, coalesce(sum(CASE WHEN peer = '' THEN rx + tx END) OVER (), 0) AS other,
	est, sum(est) OVER () AS total_est
FROM g ORDER BY peer = '', %s, peer LIMIT %d`, src, order, q.Limit+1)
	rows, err := s.db.QueryContext(ctx, query, a.args...)
	if err != nil {
		return RemoteResult{}, fmt.Errorf("top Remote IPs query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it RemoteItem
		if err := rows.Scan(&it.RemoteIP, &it.Country, &it.ASN, &it.ReceivedBytes, &it.SentBytes, &it.Flows, &it.LocalHosts,
			&res.TotalBytes, &res.OtherBytes, &it.EstimatedBytes, &res.EstimatedBytes); err != nil {
			return RemoteResult{}, err
		}
		if it.RemoteIP == "" || len(res.Items) == q.Limit {
			continue
		}
		it.TotalBytes = it.ReceivedBytes + it.SentBytes
		if it.ASN != 0 {
			it.ASNOrg = s.ASNName(it.ASN)
		}
		res.Items = append(res.Items, it)
	}
	return res, rows.Err()
}

// RemoteHost is one Local Host's traffic with a Remote IP.
type RemoteHost struct {
	Host                string  `json:"host"`
	LocalIsExporterHost bool    `json:"localIsExporterHost,omitempty"`
	ReceivedBytes       float64 `json:"receivedBytes"`
	SentBytes           float64 `json:"sentBytes"`
	TotalBytes          float64 `json:"totalBytes"`
	Flows               float64 `json:"flows"`
	// EstimatedBytes is the part of TotalBytes scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes,omitempty"`
}

// RemoteDetail is one Remote IP over a time range: its totals and the Local Hosts that
// talked to it.
type RemoteDetail struct {
	Coverage
	RemoteIP string `json:"remoteIp"`
	// Country and ASN are what its Flows were stored with (the most recent, if it changed).
	Country       string       `json:"country,omitempty"`
	ASN           uint32       `json:"asn,omitempty"`
	ASNOrg        string       `json:"asnOrg,omitempty"`
	ReceivedBytes float64      `json:"receivedBytes"`
	SentBytes     float64      `json:"sentBytes"`
	Flows         float64      `json:"flows"`
	LocalHosts    []RemoteHost `json:"localHosts"`
	// EstimatedBytes is the part of the bytes scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes"`
}

// RemoteIP describes one Remote IP over [from, to).
func (s *Store) RemoteIP(ctx context.Context, ip netip.Addr, q RemoteQuery) (RemoteDetail, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return RemoteDetail{}, ErrInvalidRange
	}
	a := &sqlArgs{}
	src, cov := s.remoteSource(a, q.From, q.To, q.now(), remoteFilter{exporter: q.Exporter, remote: ip})
	d := RemoteDetail{Coverage: cov, RemoteIP: ip.String(), LocalHosts: []RemoteHost{}}
	if src == "" {
		return d, nil
	}
	query := fmt.Sprintf(`WITH u AS (%s)
SELECT local_host, bool_or(local_is_exporter_host), sum(rx) AS rx, sum(tx) AS tx, sum(flows) AS flows,
	max(peer_country), max(peer_asn), sum(est)
FROM u GROUP BY local_host ORDER BY sum(rx) + sum(tx) DESC, local_host`, src)
	rows, err := s.db.QueryContext(ctx, query, a.args...)
	if err != nil {
		return RemoteDetail{}, fmt.Errorf("remote IP query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h RemoteHost
		var country string
		var asn uint32
		if err := rows.Scan(&h.Host, &h.LocalIsExporterHost, &h.ReceivedBytes, &h.SentBytes, &h.Flows, &country, &asn, &h.EstimatedBytes); err != nil {
			return RemoteDetail{}, err
		}
		h.TotalBytes = h.ReceivedBytes + h.SentBytes
		d.ReceivedBytes += h.ReceivedBytes
		d.SentBytes += h.SentBytes
		d.Flows += h.Flows
		d.EstimatedBytes += h.EstimatedBytes
		if country > d.Country {
			d.Country = country
		}
		if asn > d.ASN {
			d.ASN = asn
		}
		d.LocalHosts = append(d.LocalHosts, h)
	}
	if d.ASN != 0 {
		d.ASNOrg = s.ASNName(d.ASN)
	}
	return d, rows.Err()
}

func (s *Store) sortedPeriods(gran string) []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Time
	for p := range s.topBuilt {
		if p.gran == gran {
			out = append(out, p.start())
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}
