package flowstore

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// TopDimension is what a top-N list ranks.
type TopDimension string

// Top dimensions on the Overview.
const (
	TopLocalHosts   TopDimension = "hosts"
	TopProtocols    TopDimension = "protocols"
	TopServicePorts TopDimension = "ports"
	// TopCountries and TopASNs rank Remote IPs' countries and ASN Owners. Traffic whose
	// Remote IP had no GeoIP data is one row with an empty country or ASN 0.
	TopCountries TopDimension = "countries"
	TopASNs      TopDimension = "asns"
)

// MaxTopLimit is the most items a top-N list returns (enough for every country).
const MaxTopLimit = 300

// TopQuery asks for the busiest values of one dimension.
type TopQuery struct {
	Dimension TopDimension
	From, To  time.Time
	Exporter  netip.Addr
	Limit     int
}

// TopItem is one ranked value. For Local Hosts, Received/Sent are the host's own
// traffic (both sides of Internal traffic count). For protocols and Service Ports they
// are Network Totals: Received/Sent from and to Remote IPs, Internal counted once.
// Countries and ASN Owners only have traffic with Remote IPs.
type TopItem struct {
	Host                string  `json:"host,omitempty"`
	LocalIsExporterHost bool    `json:"localIsExporterHost,omitempty"`
	Protocol            uint8   `json:"protocol"`
	ServicePort         uint16  `json:"servicePort"`
	Country             string  `json:"country,omitempty"`
	ASN                 uint32  `json:"asn,omitempty"`
	ASNOrg              string  `json:"asnOrg,omitempty"`
	ReceivedBytes       float64 `json:"receivedBytes"`
	SentBytes           float64 `json:"sentBytes"`
	InternalBytes       float64 `json:"internalBytes"`
	TotalBytes          float64 `json:"totalBytes"`
}

// TopResult is a ranked list plus the total over every value, so the UI can show shares.
type TopResult struct {
	From       time.Time `json:"from"`
	To         time.Time `json:"to"`
	Source     Source    `json:"source"`
	Items      []TopItem `json:"items"`
	TotalBytes float64   `json:"totalBytes"`
}

// ErrUnknownDimension is returned for an unsupported TopDimension.
var ErrUnknownDimension = errors.New("flowstore: unknown top dimension")

// Top ranks Local Hosts, protocols, Service Ports, countries or ASN Owners by bytes. Ranges of a day or more are
// answered from hour Rollups, shorter ones from minute Rollups.
func (s *Store) Top(ctx context.Context, q TopQuery) (TopResult, error) {
	if !q.To.After(q.From) || q.To.Sub(q.From) > MaxRange {
		return TopResult{}, ErrInvalidRange
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
	res := TopResult{From: from, To: to, Source: source, Items: []TopItem{}}

	var group, selectKey, filter string
	received := `sum(CASE WHEN peer_kind = 1 THEN bytes_rx ELSE 0 END)`
	sent := `sum(CASE WHEN peer_kind = 1 THEN bytes_tx ELSE 0 END)`
	internal := `sum(CASE WHEN peer_kind = 2 THEN bytes_tx ELSE 0 END)`
	switch q.Dimension {
	case TopLocalHosts:
		group, selectKey = `local_host`, `local_host, bool_or(local_is_exporter_host), 0::UTINYINT, 0::USMALLINT, '', 0::UINTEGER`
		received, sent, internal = `sum(bytes_rx)`, `sum(bytes_tx)`, `0::DOUBLE`
	case TopProtocols:
		group, selectKey = `protocol`, `'', false, protocol, 0::USMALLINT, '', 0::UINTEGER`
	case TopServicePorts:
		group, selectKey = `protocol, service_port`, `'', false, protocol, service_port, '', 0::UINTEGER`
	case TopCountries:
		group, selectKey, filter = `peer_country`, `'', false, 0::UTINYINT, 0::USMALLINT, peer_country, 0::UINTEGER`, ` AND peer_kind = 1`
		received, sent, internal = `sum(bytes_rx)`, `sum(bytes_tx)`, `0::DOUBLE`
	case TopASNs:
		group, selectKey, filter = `peer_asn`, `'', false, 0::UTINYINT, 0::USMALLINT, '', peer_asn`, ` AND peer_kind = 1`
		received, sent, internal = `sum(bytes_rx)`, `sum(bytes_tx)`, `0::DOUBLE`
	default:
		return TopResult{}, ErrUnknownDimension
	}

	tables := s.rollupTablesFor(g, from, to)
	if len(tables) == 0 {
		return res, nil
	}
	where := `bucket >= $1 AND bucket < $2` + filter
	args := []any{from, to}
	if q.Exporter.IsValid() {
		where += ` AND exporter = $3`
		args = append(args, q.Exporter.String())
	}
	var parts []string
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT * FROM %s WHERE %s`, t, where))
	}
	union := strings.Join(parts, "\nUNION ALL\n")

	query := fmt.Sprintf(`
WITH r AS (%[1]s),
g AS (SELECT %[2]s, %[3]s AS rx, %[4]s AS tx, %[5]s AS internal FROM r GROUP BY %[6]s)
SELECT *, rx + tx + internal AS total, sum(rx + tx + internal) OVER () AS grand_total
FROM g ORDER BY total DESC LIMIT %[7]d`, union, selectKey, received, sent, internal, group, q.Limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return TopResult{}, fmt.Errorf("top %s query: %w", q.Dimension, err)
	}
	defer rows.Close()
	for rows.Next() {
		var it TopItem
		if err := rows.Scan(&it.Host, &it.LocalIsExporterHost, &it.Protocol, &it.ServicePort, &it.Country, &it.ASN,
			&it.ReceivedBytes, &it.SentBytes, &it.InternalBytes, &it.TotalBytes, &res.TotalBytes); err != nil {
			return TopResult{}, err
		}
		if it.ASN != 0 {
			it.ASNOrg = s.ASNName(it.ASN)
		}
		res.Items = append(res.Items, it)
	}
	return res, rows.Err()
}
