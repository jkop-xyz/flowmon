package flowstore

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/domains"
	"github.com/jkop-xyz/flowmon/internal/oui"
)

// Usage now answers "who is using the internet right now?" for the Overview: the
// Devices and domains behind the last few minutes of traffic with Remote IPs. It reads
// raw Flows, which carry their domain as soon as they are stored, because Domain
// Rollups run a few minutes behind.

// NowDevice is a Device's, or an address's, share of traffic with Remote IPs.
type NowDevice struct {
	// MAC is empty for traffic without a known MAC, counted by Host.
	MAC        string  `json:"mac,omitempty"`
	Vendor     string  `json:"vendor,omitempty"`
	PrivateMac bool    `json:"privateMac,omitempty"`
	Host       string  `json:"host"`
	InBps      float64 `json:"inBps"`
	OutBps     float64 `json:"outBps"`
}

// NowDomain is a Registrable Domain's share, with the Device or address using it most.
type NowDomain struct {
	Domain string  `json:"domain"`
	InBps  float64 `json:"inBps"`
	OutBps float64 `json:"outBps"`
	// TopMAC and TopHost name who used it most.
	TopMAC  string `json:"topMac,omitempty"`
	TopHost string `json:"topHost"`
}

// NowResult is UsageNow's answer.
type NowResult struct {
	From    time.Time   `json:"from"`
	To      time.Time   `json:"to"`
	Devices []NowDevice `json:"devices"`
	Domains []NowDomain `json:"domains"`
}

// UsageNow ranks the Devices and domains by their traffic with Remote IPs in [from, to),
// as average bits per second, limit of each. A router hop's MAC counts as none, and a
// merged MAC as its Device. Traffic without a MAC counts by address, merged into
// groups (Host Labels' prefixes) as in Top.
func (s *Store) UsageNow(ctx context.Context, from, to time.Time, groups []netip.Prefix, limit int) (NowResult, error) {
	res := NowResult{From: from.UTC(), To: to.UTC(), Devices: []NowDevice{}, Domains: []NowDomain{}}
	secs := to.Sub(from).Seconds()
	if secs <= 0 || to.Sub(from) > 24*time.Hour {
		return res, ErrInvalidRange
	}
	tables := s.tablesFor(from, to)
	if len(tables) == 0 {
		return res, nil
	}
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT local_host, local_mac, domain, direction, bytes FROM %s WHERE end_ts >= $1 AND end_ts < $2 AND direction IN (1, 2)`, t)
	}
	rows, err := s.db.QueryContext(ctx, `
WITH r AS (`+strings.Join(parts, "\nUNION ALL\n")+`)
SELECT coalesce(r.local_host, ''), CASE WHEN r.local_mac <> '' AND r.local_mac NOT IN (SELECT mac FROM router_macs)
		THEN coalesce(dm.into_mac, r.local_mac) ELSE '' END, r.domain, r.direction, sum(r.bytes)
FROM r LEFT JOIN device_merges dm ON dm.mac = r.local_mac GROUP BY 1, 2, 3, 4`, from.UTC(), to.UTC())
	if err != nil {
		return res, fmt.Errorf("usage now: %w", err)
	}
	defer rows.Close()
	type bytes struct{ in, out float64 }
	devs := map[string]*NowDevice{}
	doms := map[string]*bytes{}
	// domainBy holds each domain's bytes per Device key, for its top user.
	domainBy := map[string]map[string]float64{}
	for rows.Next() {
		var host, mac, domain string
		var dir uint8
		var n float64
		if err := rows.Scan(&host, &mac, &domain, &dir, &n); err != nil {
			return res, err
		}
		key := mac
		if key == "" {
			host, _ = hostKey(host, groups)
			key = "host:" + host
		}
		d := devs[key]
		if d == nil {
			d = &NowDevice{MAC: mac, Host: host}
			devs[key] = d
		}
		bps := n * 8 / secs
		if dir == 1 {
			d.InBps += bps
		} else {
			d.OutBps += bps
		}
		if reg := domains.Registrable(domain); reg != "" {
			b := doms[reg]
			if b == nil {
				b = &bytes{}
				doms[reg] = b
				domainBy[reg] = map[string]float64{}
			}
			if dir == 1 {
				b.in += bps
			} else {
				b.out += bps
			}
			domainBy[reg][key] += bps
		}
	}
	if err := rows.Err(); err != nil {
		return res, err
	}
	for _, d := range devs {
		if hw, err := net.ParseMAC(d.MAC); err == nil {
			d.Vendor, d.PrivateMac = oui.Vendor(hw), oui.Private(hw)
		}
		res.Devices = append(res.Devices, *d)
	}
	sort.Slice(res.Devices, func(i, j int) bool {
		a, b := res.Devices[i], res.Devices[j]
		return a.InBps+a.OutBps > b.InBps+b.OutBps || a.InBps+a.OutBps == b.InBps+b.OutBps && a.Host < b.Host
	})
	for name, b := range doms {
		top, best := "", -1.0
		for key, v := range domainBy[name] {
			if v > best || v == best && key < top {
				top, best = key, v
			}
		}
		res.Domains = append(res.Domains, NowDomain{Domain: name, InBps: b.in, OutBps: b.out, TopMAC: devs[top].MAC, TopHost: devs[top].Host})
	}
	sort.Slice(res.Domains, func(i, j int) bool {
		a, b := res.Domains[i], res.Domains[j]
		return a.InBps+a.OutBps > b.InBps+b.OutBps || a.InBps+a.OutBps == b.InBps+b.OutBps && a.Domain < b.Domain
	})
	if len(res.Devices) > limit {
		res.Devices = res.Devices[:limit]
	}
	if len(res.Domains) > limit {
		res.Domains = res.Domains[:limit]
	}
	return res, nil
}
