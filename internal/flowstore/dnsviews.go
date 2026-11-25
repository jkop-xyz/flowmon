package flowstore

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// DNSFilter narrows DNS Queries for the DNS views (#78).
type DNSFilter struct {
	From, To time.Time
	// Clients, when not nil, keeps lookups by these addresses (a Local Host's).
	Clients []string
	// Domain keeps lookups of this name and the names under it ("bbc.co.uk" keeps
	// "www.bbc.co.uk"). Top domains are then full names, and answers are listed.
	Domain string
}

// DNSPoint is one step of the DNS chart: lookups per minute.
type DNSPoint struct {
	T       time.Time `json:"t"`
	Queries float64   `json:"queries"`
	Blocked float64   `json:"blocked"`
}

// DNSName is how often a name (a Registrable Domain, or a full name under a Domain
// filter) was looked up.
type DNSName struct {
	Name    string    `json:"name"`
	Queries int       `json:"queries"`
	Blocked int       `json:"blocked"`
	Clients int       `json:"clients"`
	Last    time.Time `json:"last"`
}

// DNSClient is how often a client looked names up.
type DNSClient struct {
	Client     string    `json:"client"`
	ClientName string    `json:"clientName,omitempty"`
	Queries    int       `json:"queries"`
	Blocked    int       `json:"blocked"`
	Last       time.Time `json:"last"`
}

// DNSUpstream is how a DNS server's upstream answered lookups it didn't answer from
// its cache.
type DNSUpstream struct {
	Upstream string  `json:"upstream"`
	Queries  int     `json:"queries"`
	AvgMs    float64 `json:"avgMs"`
	P95Ms    float64 `json:"p95Ms"`
}

// DNSAnswer is an address a Domain filter's names were answered with.
type DNSAnswer struct {
	Address string    `json:"address"`
	Queries int       `json:"queries"`
	Last    time.Time `json:"last"`
}

// DNSSummary is what the DNS views show for a range.
type DNSSummary struct {
	From        time.Time     `json:"from"`
	To          time.Time     `json:"to"`
	StepSeconds int           `json:"stepSeconds"`
	Queries     int           `json:"queries"`
	Blocked     int           `json:"blocked"`
	Clients     int           `json:"clients"`
	Points      []DNSPoint    `json:"points"`
	TopNames    []DNSName     `json:"topNames"`
	TopBlocked  []DNSName     `json:"topBlocked"`
	TopClients  []DNSClient   `json:"topClients"`
	Upstreams   []DNSUpstream `json:"upstreams"`
	Answers     []DNSAnswer   `json:"answers,omitempty"`
	// Since is the start of the oldest day of DNS Queries kept; nil when none are.
	Since *time.Time `json:"since,omitempty"`
}

// dnsTopLimit is how many names, clients and answers a summary lists.
const dnsTopLimit = 15

// dnsStep picks a chart step of whole minutes, about 120 points over the range.
func dnsStep(r time.Duration) time.Duration {
	step := (r / 120).Truncate(time.Minute)
	if step < time.Minute {
		step = time.Minute
	}
	return step
}

// DNSSummary counts the DNS Queries in f's range.
func (s *Store) DNSSummary(ctx context.Context, f DNSFilter) (DNSSummary, error) {
	if !f.To.After(f.From) || f.To.Sub(f.From) > MaxRange {
		return DNSSummary{}, ErrInvalidRange
	}
	step := dnsStep(f.To.Sub(f.From))
	from, to := f.From.UTC().Truncate(step), f.To.UTC()
	res := DNSSummary{From: from, To: to, StepSeconds: int(step.Seconds()), Points: []DNSPoint{}, TopNames: []DNSName{},
		TopBlocked: []DNSName{}, TopClients: []DNSClient{}, Upstreams: []DNSUpstream{}}
	if days := s.DNSDays(); len(days) > 0 {
		res.Since = &days[0]
	}
	var parts []string
	for _, t := range s.dnsTablesNewestFirst() {
		day, _ := dnsDay(t)
		if day.Before(to) && day.Add(24*time.Hour).After(from) {
			parts = append(parts, `SELECT * FROM `+t)
		}
	}
	if len(parts) == 0 {
		return res, nil
	}
	sort.Strings(parts)
	a := &sqlArgs{}
	where := fmt.Sprintf(`time >= %s AND time < %s`, a.add(from), a.add(to))
	if f.Clients != nil {
		where += ` AND ` + inList(a, "client", f.Clients)
	}
	if f.Domain != "" {
		where += fmt.Sprintf(` AND (name = %[1]s OR ends_with(name, '.' || %[1]s))`, a.add(f.Domain))
	}
	base := `WITH q AS (SELECT * FROM (` + strings.Join(parts, ` UNION ALL `) + `) WHERE ` + where + `) `
	query := func(sel string, scan func(scan func(...any) error) error) error {
		rows, err := s.db.QueryContext(ctx, base+sel, a.args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			if err := scan(rows.Scan); err != nil {
				return err
			}
		}
		return rows.Err()
	}

	if err := query(`SELECT count(*), count(*) FILTER (WHERE blocked), count(DISTINCT client) FROM q`, func(scan func(...any) error) error {
		return scan(&res.Queries, &res.Blocked, &res.Clients)
	}); err != nil {
		return res, err
	}
	perMinute := step.Minutes()
	counts := map[int64][2]int{}
	if err := query(fmt.Sprintf(`SELECT time_bucket(INTERVAL '%d seconds', time, TIMESTAMP '2000-01-01') AS b, count(*), count(*) FILTER (WHERE blocked)
		FROM q GROUP BY b`, int(step.Seconds())), func(scan func(...any) error) error {
		var b time.Time
		var n, blocked int
		if err := scan(&b, &n, &blocked); err != nil {
			return err
		}
		counts[b.UTC().Unix()] = [2]int{n, blocked}
		return nil
	}); err != nil {
		return res, err
	}
	for t := from; t.Before(to); t = t.Add(step) {
		c := counts[t.Unix()]
		res.Points = append(res.Points, DNSPoint{T: t, Queries: float64(c[0]) / perMinute, Blocked: float64(c[1]) / perMinute})
	}

	nameCol := `registrable`
	if f.Domain != "" {
		nameCol = `name`
	}
	scanName := func(into *[]DNSName) func(func(...any) error) error {
		return func(scan func(...any) error) error {
			var n DNSName
			if err := scan(&n.Name, &n.Queries, &n.Blocked, &n.Clients, &n.Last); err != nil {
				return err
			}
			n.Last = n.Last.UTC()
			*into = append(*into, n)
			return nil
		}
	}
	names := `SELECT ` + nameCol + `, count(*), count(*) FILTER (WHERE blocked), count(DISTINCT client), max(time) FROM q `
	if err := query(names+`GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT `+fmt.Sprint(dnsTopLimit), scanName(&res.TopNames)); err != nil {
		return res, err
	}
	if err := query(names+`WHERE blocked GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT `+fmt.Sprint(dnsTopLimit), scanName(&res.TopBlocked)); err != nil {
		return res, err
	}
	if err := query(`SELECT client, max(client_name), count(*), count(*) FILTER (WHERE blocked), max(time) FROM q
		GROUP BY client ORDER BY 3 DESC, 1 LIMIT `+fmt.Sprint(dnsTopLimit), func(scan func(...any) error) error {
		var c DNSClient
		if err := scan(&c.Client, &c.ClientName, &c.Queries, &c.Blocked, &c.Last); err != nil {
			return err
		}
		c.Last = c.Last.UTC()
		res.TopClients = append(res.TopClients, c)
		return nil
	}); err != nil {
		return res, err
	}
	if err := query(`SELECT upstream, count(*), avg(elapsed_ms), quantile_cont(elapsed_ms, 0.95) FROM q
		WHERE NOT cached AND NOT blocked AND upstream <> '' GROUP BY upstream ORDER BY 2 DESC, 1 LIMIT 10`, func(scan func(...any) error) error {
		var u DNSUpstream
		if err := scan(&u.Upstream, &u.Queries, &u.AvgMs, &u.P95Ms); err != nil {
			return err
		}
		res.Upstreams = append(res.Upstreams, u)
		return nil
	}); err != nil {
		return res, err
	}
	if f.Domain != "" {
		res.Answers = []DNSAnswer{}
		if err := query(`SELECT a, count(*), max(time) FROM (SELECT unnest(answers) AS a, time FROM q WHERE NOT blocked)
			GROUP BY a ORDER BY 2 DESC, 1 LIMIT `+fmt.Sprint(dnsTopLimit), func(scan func(...any) error) error {
			var an DNSAnswer
			if err := scan(&an.Address, &an.Queries, &an.Last); err != nil {
				return err
			}
			an.Last = an.Last.UTC()
			res.Answers = append(res.Answers, an)
			return nil
		}); err != nil {
			return res, err
		}
	}
	return res, nil
}
