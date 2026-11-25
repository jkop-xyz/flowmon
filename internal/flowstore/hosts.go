package flowstore

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"time"
)

// Local Hosts are stored by address. A Host Label on a prefix makes its addresses one
// Local Host, identified by the prefix; queries take every Host Label's prefix as "host
// groups" and leave naming to the caller. An address counts towards the longest group
// that contains it, so an address with a label of its own stays a Local Host of its own.

// hostKey is the Local Host an address counts towards: the longest group that contains
// it, unless that is the address alone; then, or without a group, the address itself.
func hostKey(addr string, groups []netip.Prefix) (string, bool) {
	if len(groups) == 0 {
		return addr, false
	}
	a, err := netip.ParseAddr(addr)
	if err != nil {
		return addr, false
	}
	best, ok := longestGroup(a, groups)
	if !ok || best.IsSingleIP() {
		return addr, false
	}
	return best.String(), true
}

func longestGroup(a netip.Addr, groups []netip.Prefix) (netip.Prefix, bool) {
	best := -1
	for i, g := range groups {
		if g.Contains(a) && (best < 0 || g.Bits() > groups[best].Bits()) {
			best = i
		}
	}
	if best < 0 {
		return netip.Prefix{}, false
	}
	return groups[best], true
}

// groupTopHosts merges Local Hosts in the same group, sums them, ranks them by total
// and keeps limit.
func groupTopHosts(items []TopItem, groups []netip.Prefix, limit int) []TopItem {
	byKey := map[string]int{}
	out := []TopItem{}
	for _, it := range items {
		key, grouped := hostKey(it.Host, groups)
		i, ok := byKey[key]
		if !ok {
			i = len(out)
			byKey[key] = i
			out = append(out, TopItem{Host: key})
		}
		g := &out[i]
		if grouped {
			g.Addresses++
		}
		g.LocalIsExporterHost = g.LocalIsExporterHost || it.LocalIsExporterHost
		g.ReceivedBytes += it.ReceivedBytes
		g.SentBytes += it.SentBytes
		g.InternalBytes += it.InternalBytes
		g.TotalBytes += it.TotalBytes
		g.Flows += it.Flows
		g.EstimatedBytes += it.EstimatedBytes
	}
	slices.SortStableFunc(out, func(a, b TopItem) int {
		switch {
		case a.TotalBytes > b.TotalBytes:
			return -1
		case a.TotalBytes < b.TotalBytes:
			return 1
		}
		return strings.Compare(a.Host, b.Host)
	})
	return out[:min(limit, len(out))]
}

// HostQuery asks about one Local Host: an address, or a prefix whose addresses count as
// one Local Host.
type HostQuery struct {
	Host netip.Prefix
	// MAC, when set, asks about a Device instead: the addresses it used in the range,
	// with all their traffic. Host is then ignored.
	MAC      string
	From, To time.Time
	Exporter netip.Addr
	// HostGroups are Host Labels' prefixes, as in TopQuery. They group Local Peers, and
	// when Host is one of them, only the addresses that count towards it are included.
	HostGroups []netip.Prefix
	// MaxPoints bounds the traffic series; 0 means 300.
	MaxPoints int
	// Limit bounds each ranked list; 0 means 20.
	Limit int
}

func (q HostQuery) valid() error {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return ErrInvalidRange
	}
	return nil
}

// granularity answers ranges of a day or more from hour Rollups, like Top.
func (q HostQuery) granularity() (Granularity, Source) {
	if q.To.Sub(q.From) >= 24*time.Hour {
		return Hour, SourceHour
	}
	return Minute, SourceMinute
}

// hostRollups returns a union over the Rollups of g in [from, to) restricted to the
// Local Host's addresses, and those addresses. It is "" when there is nothing to read.
func (s *Store) hostRollups(ctx context.Context, a *sqlArgs, g Granularity, from, to time.Time, q HostQuery) (string, []string, error) {
	tables := s.rollupTablesFor(g, from, to)
	if len(tables) == 0 {
		return "", nil, nil
	}
	addrs, err := s.hostAddresses(ctx, tables, from, to, q)
	if err != nil || len(addrs) == 0 {
		return "", addrs, err
	}
	where := fmt.Sprintf(`bucket >= %s AND bucket < %s AND %s`, a.add(from), a.add(to), inList(a, "local_host", addrs))
	if q.Exporter.IsValid() {
		where += ` AND exporter = ` + a.add(q.Exporter.String())
	}
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT * FROM %s WHERE %s`, t, where)
	}
	return strings.Join(parts, "\nUNION ALL\n"), addrs, nil
}

// hostAddresses lists the addresses of q.Host that have traffic in the tables. When
// q.Host is a host group, that is the addresses that count towards it, as in Top.
func (s *Store) hostAddresses(ctx context.Context, tables []string, from, to time.Time, q HostQuery) ([]string, error) {
	if q.MAC != "" {
		return s.deviceAddresses(ctx, tables, from, to, q.MAC)
	}
	if q.Host.IsSingleIP() {
		return []string{q.Host.Addr().String()}, nil
	}
	isGroup := slices.Contains(q.HostGroups, q.Host)
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT DISTINCT local_host FROM %s WHERE bucket >= $1 AND bucket < $2`, t)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT local_host FROM (`+strings.Join(parts, "\nUNION ALL\n")+`)`, from, to)
	if err != nil {
		return nil, fmt.Errorf("local host addresses: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		a, err := netip.ParseAddr(h)
		if err != nil || !q.Host.Contains(a) {
			continue
		}
		if isGroup {
			if g, _ := longestGroup(a, q.HostGroups); g != q.Host {
				continue
			}
		}
		out = append(out, h)
	}
	slices.Sort(out)
	return out, rows.Err()
}

// HostAddresses returns the addresses of a Local Host that had traffic in the range, for
// filtering other queries (such as Top Remote IPs) to it.
func (s *Store) HostAddresses(ctx context.Context, q HostQuery) ([]string, error) {
	if err := q.valid(); err != nil {
		return nil, err
	}
	if q.Host.IsSingleIP() && q.MAC == "" {
		return []string{q.Host.Addr().String()}, nil
	}
	g, _ := q.granularity()
	from := q.From.UTC().Truncate(g.bucket())
	tables := s.rollupTablesFor(g, from, q.To.UTC())
	if len(tables) == 0 {
		return []string{}, nil
	}
	return s.hostAddresses(ctx, tables, from, q.To.UTC(), q)
}

// HostPoint is one time bucket of a Local Host's traffic, in bits per second.
type HostPoint struct {
	Time        time.Time `json:"t"`
	ReceivedBps float64   `json:"receivedBps"`
	SentBps     float64   `json:"sentBps"`
	// EstimatedBps is the part of both scaled up from sampled Flows.
	EstimatedBps float64 `json:"estimatedBps,omitempty"`
}

// HostTrafficResult is a Local Host's traffic over time.
type HostTrafficResult struct {
	From   time.Time   `json:"from"`
	To     time.Time   `json:"to"`
	StepS  float64     `json:"stepSeconds"`
	Source Source      `json:"source"`
	Points []HostPoint `json:"points"`
	// EstimatedBytes is how many of the bytes shown were scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes"`
}

// HostTraffic returns what a Local Host received and sent per time bucket, from every
// Peer. It is answered from Rollups, so the finest step is a minute.
func (s *Store) HostTraffic(ctx context.Context, q HostQuery) (HostTrafficResult, error) {
	if err := q.valid(); err != nil {
		return HostTrafficResult{}, err
	}
	if q.MaxPoints <= 0 {
		q.MaxPoints = 300
	}
	step := max(StepFor(q.To.Sub(q.From), q.MaxPoints), time.Minute)
	g, source := Minute, SourceMinute
	if step >= time.Hour {
		g, source = Hour, SourceHour
	}
	from, to := q.From.UTC().Truncate(step), q.To.UTC()
	res := HostTrafficResult{From: from, To: to, StepS: step.Seconds(), Source: source, Points: []HostPoint{}}
	buckets := map[int64]int{}
	for t := from; t.Before(to); t = t.Add(step) {
		buckets[t.Unix()] = len(res.Points)
		res.Points = append(res.Points, HostPoint{Time: t})
	}
	a := &sqlArgs{}
	union, _, err := s.hostRollups(ctx, a, g, from, to, q)
	if err != nil || union == "" {
		return res, err
	}
	rows, err := s.db.QueryContext(ctx, fmt.Sprintf(`SELECT time_bucket(INTERVAL %d SECOND, bucket) AS b, sum(bytes_rx), sum(bytes_tx),
	sum(estimated_rx + estimated_tx)
FROM (%s) GROUP BY b`, int64(step.Seconds()), union), a.args...)
	if err != nil {
		return HostTrafficResult{}, fmt.Errorf("local host traffic query: %w", err)
	}
	defer rows.Close()
	perSecond := 8 / step.Seconds()
	for rows.Next() {
		var b time.Time
		var rx, tx, est float64
		if err := rows.Scan(&b, &rx, &tx, &est); err != nil {
			return HostTrafficResult{}, err
		}
		if i, ok := buckets[b.UTC().Unix()]; ok {
			res.Points[i].ReceivedBps += rx * perSecond
			res.Points[i].SentBps += tx * perSecond
			res.Points[i].EstimatedBps += est * perSecond
			res.EstimatedBytes += est
		}
	}
	return res, rows.Err()
}

// HostSummary is a Local Host's traffic over a range, broken down by Peer and port.
// Received and Sent are the Local Host's own: from and to Remote IPs and other Local
// Hosts.
type HostSummary struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Source Source    `json:"source"`
	// Addresses are the Local Host's addresses with traffic (one, unless it is a prefix).
	Addresses           []HostAddress `json:"addresses"`
	LocalIsExporterHost bool          `json:"localIsExporterHost,omitempty"`
	ReceivedBytes       float64       `json:"receivedBytes"`
	SentBytes           float64       `json:"sentBytes"`
	// RemoteBytes is the part with Remote IPs, InternalBytes the part with Local Hosts.
	RemoteBytes   float64 `json:"remoteBytes"`
	InternalBytes float64 `json:"internalBytes"`
	// Flows counts the Flows the Local Host took part in, except Internal ones it
	// received: Rollups count those for the Local Host that sent them.
	Flows float64 `json:"flows"`
	// EstimatedBytes is the part of Received and Sent scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes"`
	// Ports are Service Ports; LocalPeers other Local Hosts (grouped by HostGroups);
	// Countries and ASNs the Remote IPs' countries and ASN Owners.
	Ports      []HostItem `json:"ports"`
	LocalPeers []HostItem `json:"localPeers"`
	Countries  []HostItem `json:"countries"`
	ASNs       []HostItem `json:"asns"`
}

// HostAddress is one address of a Local Host.
type HostAddress struct {
	Address       string  `json:"address"`
	ReceivedBytes float64 `json:"receivedBytes"`
	SentBytes     float64 `json:"sentBytes"`
	TotalBytes    float64 `json:"totalBytes"`
}

// HostItem is one ranked value in a HostSummary.
type HostItem struct {
	Protocol    uint8  `json:"protocol,omitempty"`
	ServicePort uint16 `json:"servicePort,omitempty"`
	// Host is a Local Peer: an address, or a host group's prefix with Addresses.
	Host          string  `json:"host,omitempty"`
	Addresses     int     `json:"addresses,omitempty"`
	Country       string  `json:"country,omitempty"`
	ASN           uint32  `json:"asn,omitempty"`
	ASNOrg        string  `json:"asnOrg,omitempty"`
	ReceivedBytes float64 `json:"receivedBytes"`
	SentBytes     float64 `json:"sentBytes"`
	TotalBytes    float64 `json:"totalBytes"`
	Flows         float64 `json:"flows"`
	// EstimatedBytes is the part of TotalBytes scaled up from sampled Flows.
	EstimatedBytes float64 `json:"estimatedBytes,omitempty"`
}

// HostDetail summarises a Local Host's traffic over a range.
func (s *Store) HostDetail(ctx context.Context, q HostQuery) (HostSummary, error) {
	if err := q.valid(); err != nil {
		return HostSummary{}, err
	}
	if q.Limit <= 0 {
		q.Limit = 20
	}
	g, source := q.granularity()
	from, to := q.From.UTC().Truncate(g.bucket()), q.To.UTC()
	res := HostSummary{From: from, To: to, Source: source, Addresses: []HostAddress{},
		Ports: []HostItem{}, LocalPeers: []HostItem{}, Countries: []HostItem{}, ASNs: []HostItem{}}
	a := &sqlArgs{}
	union, _, err := s.hostRollups(ctx, a, g, from, to, q)
	if err != nil || union == "" {
		return res, err
	}

	// One scan, grouped several ways: k says which grouping a row belongs to.
	query := fmt.Sprintf(`WITH r AS (%s)
SELECT CASE
		WHEN GROUPING(local_host) = 0 THEN 'address'
		WHEN GROUPING(protocol, service_port) = 0 THEN 'port'
		WHEN GROUPING(peer_local_host) = 0 THEN 'peer'
		WHEN GROUPING(peer_country) = 0 THEN 'country'
		WHEN GROUPING(peer_asn) = 0 THEN 'asn'
		ELSE 'total' END AS k,
	coalesce(local_host, ''), coalesce(protocol, 0), coalesce(service_port, 0), coalesce(peer_local_host, ''),
	coalesce(peer_country, ''), coalesce(peer_asn, 0), coalesce(peer_kind, 0),
	coalesce(sum(bytes_rx), 0), coalesce(sum(bytes_tx), 0), coalesce(sum(flows), 0)::DOUBLE, coalesce(bool_or(local_is_exporter_host), false),
	coalesce(sum(estimated_rx + estimated_tx), 0)
FROM r
GROUP BY GROUPING SETS ((local_host), (protocol, service_port), (peer_kind, peer_local_host), (peer_kind, peer_country), (peer_kind, peer_asn), (peer_kind), ())`, union)
	rows, err := s.db.QueryContext(ctx, query, a.args...)
	if err != nil {
		return HostSummary{}, fmt.Errorf("local host query: %w", err)
	}
	defer rows.Close()
	var peers []TopItem
	for rows.Next() {
		var k, host, peer, country string
		var proto, kind uint8
		var port uint16
		var asn uint32
		var rx, tx, flows, est float64
		var leh bool
		if err := rows.Scan(&k, &host, &proto, &port, &peer, &country, &asn, &kind, &rx, &tx, &flows, &leh, &est); err != nil {
			return HostSummary{}, err
		}
		it := HostItem{ReceivedBytes: rx, SentBytes: tx, TotalBytes: rx + tx, Flows: flows, EstimatedBytes: est}
		switch {
		case k == "address":
			res.Addresses = append(res.Addresses, HostAddress{Address: host, ReceivedBytes: rx, SentBytes: tx, TotalBytes: rx + tx})
		case k == "port":
			it.Protocol, it.ServicePort = proto, port
			res.Ports = append(res.Ports, it)
		case k == "peer" && kind == 2:
			peers = append(peers, TopItem{Host: peer, ReceivedBytes: rx, SentBytes: tx, TotalBytes: rx + tx, Flows: flows, EstimatedBytes: est})
		case k == "country" && kind == 1:
			it.Country = country
			res.Countries = append(res.Countries, it)
		case k == "asn" && kind == 1:
			it.ASN = asn
			res.ASNs = append(res.ASNs, it)
		case k == "total" && kind == 1:
			res.RemoteBytes = rx + tx
		case k == "total" && kind == 2:
			res.InternalBytes = rx + tx
		case k == "total" && kind == 0:
			res.ReceivedBytes, res.SentBytes, res.Flows, res.LocalIsExporterHost, res.EstimatedBytes = rx, tx, flows, leh, est
		}
	}
	if err := rows.Err(); err != nil {
		return HostSummary{}, err
	}
	for _, p := range groupTopHosts(peers, q.HostGroups, q.Limit) {
		res.LocalPeers = append(res.LocalPeers, HostItem{Host: p.Host, Addresses: p.Addresses,
			ReceivedBytes: p.ReceivedBytes, SentBytes: p.SentBytes, TotalBytes: p.TotalBytes, Flows: p.Flows, EstimatedBytes: p.EstimatedBytes})
	}
	byTotal := func(a, b HostItem) int {
		switch {
		case a.TotalBytes > b.TotalBytes:
			return -1
		case a.TotalBytes < b.TotalBytes:
			return 1
		}
		return 0
	}
	slices.SortStableFunc(res.Addresses, func(a, b HostAddress) int {
		return byTotal(HostItem{TotalBytes: a.TotalBytes}, HostItem{TotalBytes: b.TotalBytes})
	})
	for _, l := range []*[]HostItem{&res.Ports, &res.Countries, &res.ASNs} {
		slices.SortStableFunc(*l, byTotal)
		*l = (*l)[:min(q.Limit, len(*l))]
	}
	for i := range res.ASNs {
		if res.ASNs[i].ASN != 0 {
			res.ASNs[i].ASNOrg = s.ASNName(res.ASNs[i].ASN)
		}
	}
	return res, nil
}
