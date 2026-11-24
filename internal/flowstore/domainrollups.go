package flowstore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"

	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/rollup"
)

// Domain Rollups (ADR 0005) are traffic totals by Local Host, its Device and Registrable
// Domain, per minute and per hour, kept apart from the Rollups so domains don't multiply
// their size. Like Rollups, minute tables hold a UTC day and hour tables a UTC month,
// under the same retentions. Traffic with no domain is counted under "".
//
// They are built from raw Flows DomainRollupDelay after the Flows were received, once
// Domain Attribution has named Flows that arrived before their DNS lookup was polled,
// so a built total never changes. Each Flow is counted once: by when it was received.

// DomainRollupDelay is how long after a Flow is received it is counted in Domain
// Rollups.
const DomainRollupDelay = 5 * time.Minute

// domainBuildChunk bounds how much one build reads, after Flowmon was off for a while.
const domainBuildChunk = time.Hour

const domainRollupColumns = `(
	bucket TIMESTAMP NOT NULL,
	exporter VARCHAR NOT NULL,
	local_host VARCHAR NOT NULL,
	local_mac VARCHAR NOT NULL,
	registrable VARCHAR NOT NULL,
	bytes_rx DOUBLE NOT NULL,
	bytes_tx DOUBLE NOT NULL,
	packets_rx DOUBLE NOT NULL,
	packets_tx DOUBLE NOT NULL,
	flows UBIGINT NOT NULL
)`

const domainKeyColumns = `bucket, exporter, local_host, local_mac, registrable`

// keyDomainsBuilt is the settings key that records how far Domain Rollups are built: every
// Flow received before it is counted.
const keyDomainsBuilt = "domain_rollups_built"

func domainTable(g Granularity, p time.Time) string {
	if g == Hour {
		return "domain_hour_" + p.Format("200601")
	}
	return "domain_minute_" + p.Format("20060102")
}

// parseDomainTable reads a Domain Rollup table's name.
func parseDomainTable(name string) (Granularity, time.Time, bool) {
	if rest, ok := strings.CutPrefix(name, "domain_hour_"); ok {
		p, err := time.Parse("200601", rest)
		return Hour, p, err == nil
	}
	if rest, ok := strings.CutPrefix(name, "domain_minute_"); ok {
		p, err := time.Parse("20060102", rest)
		return Minute, p, err == nil
	}
	return "", time.Time{}, false
}

func (s *Store) ensureDomainRollup(ctx context.Context, g Granularity, period time.Time) (string, error) {
	name := domainTable(g, period)
	s.mu.Lock()
	exists := s.domainRollups[name]
	s.mu.Unlock()
	if exists {
		return name, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+name+` `+domainRollupColumns); err != nil {
		return "", fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO domain_rollup_partitions VALUES (?, ?, ?) ON CONFLICT DO NOTHING`, string(g), period, name); err != nil {
		return "", fmt.Errorf("registering %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.domainRollups[name] = true
	s.mu.Unlock()
	return name, nil
}

// DomainRollupsBuilt returns how far Domain Rollups are built: every Flow received before
// it is counted. Zero until the first build.
func (s *Store) DomainRollupsBuilt(ctx context.Context) (time.Time, error) {
	v, ok, err := s.Setting(ctx, keyDomainsBuilt)
	if err != nil || !ok {
		return time.Time{}, err
	}
	return time.Parse(time.RFC3339Nano, v)
}

// BuildDomainRollups counts the Flows received up to now minus DomainRollupDelay that
// aren't counted yet. The first build starts at that point: domains start when this
// Flowmon starts counting them. It returns how many Flows it counted.
func (s *Store) BuildDomainRollups(ctx context.Context, now time.Time) (int, error) {
	through := now.Add(-DomainRollupDelay).UTC().Truncate(time.Minute)
	built, err := s.DomainRollupsBuilt(ctx)
	if err != nil {
		return 0, err
	}
	if built.IsZero() {
		return 0, s.SetSettings(ctx, map[string]string{keyDomainsBuilt: through.Format(time.RFC3339Nano)})
	}
	total := 0
	for built.Before(through) {
		end := built.Add(domainBuildChunk)
		if end.After(through) {
			end = through
		}
		n, err := s.buildDomainRollups(ctx, built, end)
		total += n
		if err != nil {
			return total, err
		}
		built = end
	}
	return total, nil
}

// buildDomainRollups counts the Flows received in [from, to) and records to as built,
// under the write lock so reprocessing can't run in between.
func (s *Store) buildDomainRollups(ctx context.Context, from, to time.Time) (int, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	flows, err := s.flowsForDomains(ctx, `received >= ? AND received < ?`, from, to)
	if err != nil {
		return 0, err
	}
	if err := s.appendDomainRollups(ctx, flows, time.Time{}); err != nil {
		return 0, err
	}
	return len(flows), s.SetSettings(ctx, map[string]string{keyDomainsBuilt: to.Format(time.RFC3339Nano)})
}

// flowsForDomains reads the Inbound and Outbound Flows matching where, with what Domain
// Rollups need. Flows are filed by end time, which follows received time closely, so the
// day before the first argument's day is read too.
func (s *Store) flowsForDomains(ctx context.Context, where string, from, to time.Time, args ...any) ([]flow.Flow, error) {
	var out []flow.Flow
	for _, t := range s.tablesFor(from.Add(-24*time.Hour), to) {
		q := `SELECT exporter, local_host, local_mac, start_ts, end_ts, bytes, packets, direction, domain FROM ` + t +
			` WHERE ` + where + ` AND direction IN (?, ?) AND local_host IS NOT NULL`
		rows, err := s.db.QueryContext(ctx, q, append(append([]any{from.UTC(), to.UTC()}, args...), uint8(flow.DirectionInbound), uint8(flow.DirectionOutbound))...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var f flow.Flow
			var exporter, local, mac string
			var dir uint8
			if err := rows.Scan(&exporter, &local, &mac, &f.Start, &f.End, &f.Bytes, &f.Packets, &dir, &f.Domain); err != nil {
				rows.Close()
				return nil, err
			}
			f.Exporter, _ = netip.ParseAddr(exporter)
			f.Local, _ = netip.ParseAddr(local)
			f.LocalMAC, _ = flow.ParseMAC(mac)
			f.Direction = flow.Direction(dir)
			out = append(out, f)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

type domainKey struct {
	bucket                                time.Time
	exporter, localHost, mac, registrable string
}

type domainMeasures struct {
	bytesRx, bytesTx, packetsRx, packetsTx float64
	flows                                  uint64
}

// appendDomainRollups adds flows' contributions, spread over buckets as Rollups spread
// them, skipping buckets before minBucket. s.writeMu must be held.
func (s *Store) appendDomainRollups(ctx context.Context, flows []flow.Flow, minBucket time.Time) error {
	if len(flows) == 0 {
		return nil
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		for _, g := range []Granularity{Minute, Hour} {
			agg := map[domainKey]*domainMeasures{}
			for i := range flows {
				f := &flows[i]
				reg := domains.Registrable(f.Domain)
				for _, r := range rollup.Contributions(f, g.bucket()) {
					if r.Key.Bucket.Before(minBucket) {
						continue
					}
					k := domainKey{r.Key.Bucket, r.Key.Exporter, r.Key.LocalHost, r.Key.LocalMAC, reg}
					m := agg[k]
					if m == nil {
						m = &domainMeasures{}
						agg[k] = m
					}
					m.bytesRx += r.BytesRx
					m.bytesTx += r.BytesTx
					m.packetsRx += r.PacketsRx
					m.packetsTx += r.PacketsTx
					m.flows += r.Flows
				}
			}
			byTable := map[string][]domainKey{}
			for k := range agg {
				name, err := s.ensureDomainRollup(ctx, g, g.period(k.bucket))
				if err != nil {
					return err
				}
				byTable[name] = append(byTable[name], k)
			}
			for name, keys := range byTable {
				app, err := duckdb.NewAppenderFromConn(dc.(driver.Conn), "", name)
				if err != nil {
					return fmt.Errorf("appender for %s: %w", name, err)
				}
				for _, k := range keys {
					m := agg[k]
					if err := app.AppendRow(k.bucket, k.exporter, k.localHost, k.mac, k.registrable,
						m.bytesRx, m.bytesTx, m.packetsRx, m.packetsTx, m.flows); err != nil {
						_ = app.Close()
						return fmt.Errorf("appending to %s: %w", name, err)
					}
				}
				if err := app.Close(); err != nil {
					return fmt.Errorf("flushing %s: %w", name, err)
				}
				s.mu.Lock()
				s.dirty[name] = true
				s.mu.Unlock()
			}
		}
		return nil
	})
}

// rebuildDomainRollups recounts exporter's Flows from window on, after reprocessing
// rewrote them: their totals are removed and the Flows already counted are counted
// again. s.writeMu must be held.
func (s *Store) rebuildDomainRollups(ctx context.Context, exporter netip.Addr, window time.Time) error {
	built, err := s.DomainRollupsBuilt(ctx)
	if err != nil || built.IsZero() {
		return err
	}
	s.mu.Lock()
	var tables []string
	for name := range s.domainRollups {
		if g, p, ok := parseDomainTable(name); ok && g.periodEnd(p).After(window) {
			tables = append(tables, name)
		}
	}
	s.mu.Unlock()
	sort.Strings(tables)
	for _, t := range tables {
		if _, err := s.db.ExecContext(ctx, `DELETE FROM `+t+` WHERE exporter = ? AND bucket >= ?`, exporter.String(), window.UTC()); err != nil {
			return fmt.Errorf("clearing %s: %w", t, err)
		}
	}
	flows, err := s.flowsForDomains(ctx, `end_ts >= ? AND received < ? AND exporter = ?`, window, built, exporter.String())
	if err != nil {
		return err
	}
	return s.appendDomainRollups(ctx, flows, window)
}

func (s *Store) compactDomainTable(ctx context.Context, name string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	tmp := name + "_compact"
	for _, q := range []string{
		`DROP TABLE IF EXISTS ` + tmp,
		`CREATE TABLE ` + tmp + ` ` + domainRollupColumns,
		`INSERT INTO ` + tmp + ` SELECT ` + domainKeyColumns + `, sum(bytes_rx), sum(bytes_tx), sum(packets_rx), sum(packets_tx), sum(flows)::UBIGINT
			FROM ` + name + ` GROUP BY ` + domainKeyColumns,
		`DROP TABLE ` + name,
		`ALTER TABLE ` + tmp + ` RENAME TO ` + name,
	} {
		if _, err := tx.ExecContext(ctx, q); err != nil {
			return fmt.Errorf("compacting %s: %w", name, err)
		}
	}
	return tx.Commit()
}

// DomainQuery asks for the busiest Registrable Domains.
type DomainQuery struct {
	From, To time.Time
	Exporter netip.Addr
	// LocalHosts, when not nil, keeps the traffic of these Local Hosts (addresses).
	LocalHosts []string
	Limit      int
}

// DomainItem is one Registrable Domain's traffic; Registrable is "" for traffic whose
// Remote IP no Local Host looked up.
type DomainItem struct {
	Registrable   string  `json:"registrable"`
	ReceivedBytes float64 `json:"receivedBytes"`
	SentBytes     float64 `json:"sentBytes"`
	TotalBytes    float64 `json:"totalBytes"`
	Flows         uint64  `json:"flows"`
	// LocalHosts and Devices count who used it.
	LocalHosts int `json:"localHosts"`
	Devices    int `json:"devices"`
}

// DomainResult is a ranked list of Registrable Domains.
type DomainResult struct {
	From   time.Time    `json:"from"`
	To     time.Time    `json:"to"`
	Source Source       `json:"source"`
	Items  []DomainItem `json:"items"`
	// TotalBytes is every byte counted in the range, with or without a domain.
	TotalBytes float64 `json:"totalBytes"`
	// Count is how many Registrable Domains had traffic, "" not counted.
	Count int `json:"count"`
	// CountedThrough is how far Domain Rollups are built: later traffic isn't counted yet.
	// Since is the first bucket counted. Both are nil before anything was counted.
	CountedThrough *time.Time `json:"countedThrough,omitempty"`
	Since          *time.Time `json:"since,omitempty"`
}

// TopDomains ranks Registrable Domains by bytes. Ranges of a day or more are answered
// from hour Domain Rollups, shorter ones from minute ones.
func (s *Store) TopDomains(ctx context.Context, q DomainQuery) (DomainResult, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return DomainResult{}, ErrInvalidRange
	}
	if q.Limit <= 0 {
		q.Limit = 10
	}
	q.Limit = min(q.Limit, MaxTopLimit)
	g, source := Minute, SourceMinute
	if q.To.Sub(q.From) >= 24*time.Hour {
		g, source = Hour, SourceHour
	}
	from, to := q.From.UTC().Truncate(g.bucket()), q.To.UTC()
	res := DomainResult{From: from, To: to, Source: source, Items: []DomainItem{}}
	if built, err := s.DomainRollupsBuilt(ctx); err != nil {
		return res, err
	} else if !built.IsZero() {
		res.CountedThrough = &built
	}

	s.mu.Lock()
	var parts, all []string
	for name := range s.domainRollups {
		tg, p, ok := parseDomainTable(name)
		if !ok || tg != g {
			continue
		}
		all = append(all, name)
		if p.Before(to) && g.periodEnd(p).After(from) {
			parts = append(parts, `SELECT * FROM `+name)
		}
	}
	s.mu.Unlock()
	sort.Strings(all)
	if len(all) > 0 {
		var since time.Time
		if err := s.db.QueryRowContext(ctx, `SELECT min(bucket) FROM `+all[0]).Scan(&since); err == nil && !since.IsZero() {
			since = since.UTC()
			res.Since = &since
		}
	}
	if len(parts) == 0 {
		return res, nil
	}
	sort.Strings(parts)
	a := &sqlArgs{}
	where := fmt.Sprintf(`bucket >= %s AND bucket < %s`, a.add(from), a.add(to))
	if q.Exporter.IsValid() {
		where += ` AND exporter = ` + a.add(q.Exporter.String())
	}
	if q.LocalHosts != nil {
		where += ` AND ` + inList(a, "local_host", q.LocalHosts)
	}
	rows, err := s.db.QueryContext(ctx, `WITH d AS (SELECT * FROM (`+strings.Join(parts, ` UNION ALL `)+`) WHERE `+where+`),
		g AS (SELECT registrable, sum(bytes_rx) AS rx, sum(bytes_tx) AS tx, sum(flows)::UBIGINT AS flows,
			count(DISTINCT local_host) AS hosts, count(DISTINCT local_mac) FILTER (WHERE local_mac <> '') AS devices
			FROM d GROUP BY registrable)
		SELECT registrable, rx, tx, flows, hosts, devices, sum(rx + tx) OVER (), count(*) FILTER (WHERE registrable <> '') OVER ()
		FROM g ORDER BY rx + tx DESC, registrable LIMIT `+a.add(q.Limit), a.args...)
	if err != nil {
		return res, err
	}
	defer rows.Close()
	for rows.Next() {
		var it DomainItem
		var total float64
		var count int
		if err := rows.Scan(&it.Registrable, &it.ReceivedBytes, &it.SentBytes, &it.Flows, &it.LocalHosts, &it.Devices, &total, &count); err != nil {
			return res, err
		}
		it.TotalBytes = it.ReceivedBytes + it.SentBytes
		res.Items = append(res.Items, it)
		res.TotalBytes, res.Count = total, count
	}
	return res, rows.Err()
}
