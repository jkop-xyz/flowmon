package flowstore

import (
	"context"
	"database/sql/driver"
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

// DNSQuery is one DNS lookup a Data Source logged (ADR 0005), stored in the table of the
// UTC day it happened on, dns_YYYYMMDD, listed in dns_days.
type DNSQuery struct {
	Time time.Time
	// Source is the Data Source's id: several sources merge into one log.
	Source string
	Client netip.Addr
	// ClientName is the name the DNS server knows the client by, or "".
	ClientName string
	// Name is the name asked for (domains.Normalize), Registrable its Registrable Domain.
	Name, Registrable string
	// Type is the query type: "A", "AAAA", "HTTPS"…
	Type string
	// Status is the response code: "NOERROR", "NXDOMAIN"…
	Status string
	// Answers are the A and AAAA addresses answered, after any CNAMEs.
	Answers []netip.Addr
	// CNAMEs are the names the answer went through.
	CNAMEs []string
	// Blocked is set when the DNS server blocked the lookup; Reason says why, Rule by
	// which rule.
	Blocked      bool
	Reason, Rule string
	Upstream     string
	Elapsed      time.Duration
	Cached       bool
	// Protocol is how the client asked: "" for plain DNS, "dot", "doh", "doq"…
	Protocol string
}

// dnsTable names the DNS table for the UTC day of t.
func dnsTable(t time.Time) string { return "dns_" + t.UTC().Format("20060102") }

// dnsDay parses a DNS table's name.
func dnsDay(name string) (time.Time, bool) {
	if !strings.HasPrefix(name, "dns_") {
		return time.Time{}, false
	}
	day, err := time.Parse("20060102", name[len("dns_"):])
	return day, err == nil
}

const dnsColumns = `(
	time TIMESTAMP NOT NULL,
	source VARCHAR NOT NULL,
	client VARCHAR NOT NULL,
	client_name VARCHAR NOT NULL,
	name VARCHAR NOT NULL,
	registrable VARCHAR NOT NULL,
	qtype VARCHAR NOT NULL,
	status VARCHAR NOT NULL,
	answers VARCHAR[] NOT NULL,
	cnames VARCHAR[] NOT NULL,
	blocked BOOLEAN NOT NULL,
	reason VARCHAR NOT NULL,
	rule VARCHAR NOT NULL,
	upstream VARCHAR NOT NULL,
	elapsed_ms DOUBLE NOT NULL,
	cached BOOLEAN NOT NULL,
	protocol VARCHAR NOT NULL
)`

const dnsSelect = `time, source, client, client_name, name, registrable, qtype, status, answers, cnames, blocked, reason, rule, upstream, elapsed_ms, cached, protocol`

func (s *Store) ensureDNSDay(ctx context.Context, t time.Time) (string, error) {
	name := dnsTable(t)
	s.mu.Lock()
	exists := s.dnsDays[name]
	s.mu.Unlock()
	if exists {
		return name, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+name+` `+dnsColumns); err != nil {
		return "", fmt.Errorf("creating %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO dns_days VALUES (?, ?) ON CONFLICT DO NOTHING`, t.UTC().Truncate(24*time.Hour), name); err != nil {
		return "", fmt.Errorf("registering %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	s.mu.Lock()
	s.dnsDays[name] = true
	s.mu.Unlock()
	return name, nil
}

// WriteDNS appends DNS Queries.
func (s *Store) WriteDNS(ctx context.Context, qs []DNSQuery) error {
	if len(qs) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	byDay := map[string][]*DNSQuery{}
	for i := range qs {
		name, err := s.ensureDNSDay(ctx, qs[i].Time)
		if err != nil {
			return err
		}
		byDay[name] = append(byDay[name], &qs[i])
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	return conn.Raw(func(dc any) error {
		for name, rows := range byDay {
			app, err := duckdb.NewAppenderFromConn(dc.(driver.Conn), "", name)
			if err != nil {
				return fmt.Errorf("appender for %s: %w", name, err)
			}
			for _, q := range rows {
				answers := make([]string, len(q.Answers))
				for i, a := range q.Answers {
					answers[i] = a.String()
				}
				cnames := q.CNAMEs
				if cnames == nil {
					cnames = []string{}
				}
				if err := app.AppendRow(q.Time.UTC(), q.Source, q.Client.String(), q.ClientName, q.Name, q.Registrable,
					q.Type, q.Status, answers, cnames, q.Blocked, q.Reason, q.Rule, q.Upstream,
					float64(q.Elapsed)/float64(time.Millisecond), q.Cached, q.Protocol); err != nil {
					_ = app.Close()
					return fmt.Errorf("appending to %s: %w", name, err)
				}
			}
			if err := app.Close(); err != nil {
				return fmt.Errorf("flushing %s: %w", name, err)
			}
		}
		return nil
	})
}

// dnsTablesNewestFirst lists the DNS day tables, newest first.
func (s *Store) dnsTablesNewestFirst() []string {
	s.mu.Lock()
	out := make([]string, 0, len(s.dnsDays))
	for name := range s.dnsDays {
		out = append(out, name)
	}
	s.mu.Unlock()
	sort.Sort(sort.Reverse(sort.StringSlice(out)))
	return out
}

// LatestDNS returns when source's newest stored DNS Query happened, and every query of
// source at exactly that time, so a poller can resume without storing one twice. It
// returns the zero time when source has none.
func (s *Store) LatestDNS(ctx context.Context, source string) (time.Time, []DNSQuery, error) {
	for _, t := range s.dnsTablesNewestFirst() {
		var latest *time.Time
		if err := s.db.QueryRowContext(ctx, `SELECT max(time) FROM `+t+` WHERE source = ?`, source).Scan(&latest); err != nil {
			return time.Time{}, nil, err
		}
		if latest == nil {
			continue
		}
		qs, err := s.queryDNS(ctx, `SELECT `+dnsSelect+` FROM `+t+` WHERE source = ? AND time = ?`, source, *latest)
		return latest.UTC(), qs, err
	}
	return time.Time{}, nil, nil
}

// DNSQueries returns the DNS Queries in [from, to), oldest first, at most limit.
func (s *Store) DNSQueries(ctx context.Context, from, to time.Time, limit int) ([]DNSQuery, error) {
	var parts []string
	for _, t := range s.dnsTablesNewestFirst() {
		day, _ := dnsDay(t)
		if day.Before(to) && day.Add(24*time.Hour).After(from) {
			parts = append(parts, `SELECT `+dnsSelect+` FROM `+t)
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	q := `SELECT * FROM (` + strings.Join(parts, ` UNION ALL `) + `) WHERE time >= ? AND time < ? ORDER BY time, source, client, name LIMIT ?`
	return s.queryDNS(ctx, q, from.UTC(), to.UTC(), limit)
}

func (s *Store) queryDNS(ctx context.Context, q string, args ...any) ([]DNSQuery, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DNSQuery
	for rows.Next() {
		var (
			d               DNSQuery
			client          string
			answers, cnames duckdb.Composite[[]string]
			elapsed         float64
		)
		if err := rows.Scan(&d.Time, &d.Source, &client, &d.ClientName, &d.Name, &d.Registrable, &d.Type, &d.Status,
			&answers, &cnames, &d.Blocked, &d.Reason, &d.Rule, &d.Upstream, &elapsed, &d.Cached, &d.Protocol); err != nil {
			return nil, err
		}
		d.Time = d.Time.UTC()
		d.Client, _ = netip.ParseAddr(client)
		for _, a := range answers.Get() {
			if addr, err := netip.ParseAddr(a); err == nil {
				d.Answers = append(d.Answers, addr)
			}
		}
		d.CNAMEs = cnames.Get()
		d.Elapsed = time.Duration(elapsed * float64(time.Millisecond))
		out = append(out, d)
	}
	return out, rows.Err()
}

// DNSDays returns the start of each day of DNS Queries kept, oldest first.
func (s *Store) DNSDays() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []time.Time
	for name := range s.dnsDays {
		if d, ok := dnsDay(name); ok {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	return out
}

// RemoteName is a name a Remote IP was looked up as.
type RemoteName struct {
	Name string `json:"name"`
	// Lookups and Clients count the lookups that answered with the Remote IP, and the
	// Local Hosts that made them.
	Lookups int       `json:"lookups"`
	Clients int       `json:"clients"`
	Last    time.Time `json:"last"`
}

// RemoteNames returns the names ip was an answer to in [from, to), most looked up first,
// at most limit.
func (s *Store) RemoteNames(ctx context.Context, ip netip.Addr, from, to time.Time, limit int) ([]RemoteName, error) {
	var parts []string
	for _, t := range s.dnsTablesNewestFirst() {
		day, _ := dnsDay(t)
		if day.Before(to) && day.Add(24*time.Hour).After(from) {
			parts = append(parts, `SELECT time, client, name, answers FROM `+t)
		}
	}
	out := []RemoteName{}
	if len(parts) == 0 {
		return out, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT name, count(*), count(DISTINCT client), max(time) FROM (`+strings.Join(parts, ` UNION ALL `)+`)
		WHERE time >= ? AND time < ? AND list_contains(answers, ?)
		GROUP BY name ORDER BY count(*) DESC, name LIMIT ?`, from.UTC(), to.UTC(), ip.Unmap().String(), limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var n RemoteName
		if err := rows.Scan(&n.Name, &n.Lookups, &n.Clients, &n.Last); err != nil {
			return nil, err
		}
		n.Last = n.Last.UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}
