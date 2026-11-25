package flowstore

import (
	"context"
	"fmt"
	"math"
	"net"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/oui"
)

// topDevices ranks Local Hosts by Device (TopDevices). A MAC later found to be a router
// hop (router_macs) counts as no MAC. Traffic without a MAC counts toward the Device its
// address had in the range, when it had exactly one (replies that arrived before the
// address sent anything); otherwise by address, merged into host groups as in Top.
func (s *Store) topDevices(ctx context.Context, q TopQuery, g Granularity, res TopResult) (TopResult, error) {
	tables := s.rollupTablesFor(g, res.From, res.To)
	if len(tables) == 0 {
		return res, nil
	}
	where := `bucket >= $1 AND bucket < $2`
	args := []any{res.From, res.To}
	if q.Exporter.IsValid() {
		where += ` AND exporter = $3`
		args = append(args, q.Exporter.String())
	}
	var parts []string
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT bucket, local_host, local_is_exporter_host, local_mac, bytes_rx, bytes_tx, estimated_rx, estimated_tx FROM %s WHERE %s`, t, where))
	}
	// Host groups are merged below, so every row is read.
	limit := fmt.Sprintf("LIMIT %d", q.Limit)
	if len(q.HostGroups) > 0 {
		limit = ""
	}
	query := fmt.Sprintf(`
WITH r AS (%s),
m AS (SELECT r.* EXCLUDE (local_mac), CASE WHEN r.local_mac <> '' AND r.local_mac NOT IN (SELECT mac FROM router_macs)
	THEN coalesce(dm.into_mac, r.local_mac) ELSE '' END AS mac FROM r LEFT JOIN device_merges dm ON dm.mac = r.local_mac),
one AS (SELECT local_host, any_value(mac) AS only_mac FROM m WHERE mac <> '' GROUP BY local_host HAVING count(DISTINCT mac) = 1),
k AS (SELECT m.*, CASE WHEN mac <> '' THEN mac ELSE coalesce(one.only_mac, '') END AS dev FROM m LEFT JOIN one USING (local_host)),
g AS (SELECT dev, arg_max(local_host, bucket) AS host, count(DISTINCT local_host) AS addrs, bool_or(local_is_exporter_host) AS eh,
		sum(bytes_rx) AS rx, sum(bytes_tx) AS tx, sum(estimated_rx + estimated_tx) AS est, max(bucket) AS last
	FROM k GROUP BY dev, CASE WHEN dev = '' THEN local_host ELSE '' END)
SELECT dev, host, addrs, eh, rx, tx, est, last, rx + tx AS total, sum(rx + tx) OVER (), sum(est) OVER (), count(*) OVER ()
FROM g ORDER BY total DESC, host %s`, strings.Join(parts, "\nUNION ALL\n"), limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return TopResult{}, fmt.Errorf("top devices query: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it TopItem
		var addrs int
		var last time.Time
		if err := rows.Scan(&it.Mac, &it.Host, &addrs, &it.LocalIsExporterHost, &it.ReceivedBytes, &it.SentBytes, &it.EstimatedBytes,
			&last, &it.TotalBytes, &res.TotalBytes, &res.EstimatedBytes, &res.Count); err != nil {
			return TopResult{}, err
		}
		last = last.UTC()
		it.LastSeen = &last
		if it.Mac != "" {
			if addrs > 1 {
				it.Addresses = addrs
			}
			if hw, err := net.ParseMAC(it.Mac); err == nil {
				it.Vendor, it.PrivateMac = oui.Vendor(hw), oui.Private(hw)
			}
		}
		res.Items = append(res.Items, it)
	}
	if err := rows.Err(); err != nil {
		return TopResult{}, err
	}
	if len(q.HostGroups) > 0 {
		all := groupTopHosts(res.Items, q.HostGroups, math.MaxInt)
		res.Count = len(all)
		res.Items = all[:min(q.Limit, len(all))]
	}
	return res, nil
}
