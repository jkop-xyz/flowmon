package flowstore

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
	"github.com/jkop-xyz/flowmon/internal/oui"
)

// Neighbors (ADR 0005, "Devices are MAC addresses") are the address-to-MAC mappings in
// SNMP sources' neighbor tables: the second way Devices are found, which also finds
// quiet ones. Each mapping is kept per source with when it was first and last seen, and
// also kept in device_addresses, so a Device's address history is current whether its
// address came from a Flow or a neighbor table.

// NeighborEntry is a mapping as polled.
type NeighborEntry struct {
	MAC     flow.MAC
	Address netip.Addr
	IfIndex uint32
}

// SaveNeighbors records the mappings source's neighbor tables held at at.
func (s *Store) SaveNeighbors(ctx context.Context, source string, at time.Time, ns []NeighborEntry) error {
	if len(ns) == 0 {
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	at = at.UTC()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	seen := map[[2]string]bool{}
	for _, n := range ns {
		mac, addr := n.MAC.String(), n.Address.Unmap().String()
		if seen[[2]string{mac, addr}] {
			continue // the same mapping on two interfaces (a bridge and its port)
		}
		seen[[2]string{mac, addr}] = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO neighbors VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT (source, mac, address) DO UPDATE SET if_index = excluded.if_index, last_seen = greatest(neighbors.last_seen, excluded.last_seen)`,
			source, mac, addr, n.IfIndex, at, at); err != nil {
			return fmt.Errorf("saving neighbors: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_addresses VALUES (?, ?, ?, ?)
			ON CONFLICT (mac, address) DO UPDATE SET first_seen = least(device_addresses.first_seen, excluded.first_seen),
				last_seen = greatest(device_addresses.last_seen, excluded.last_seen)`, mac, addr, at, at); err != nil {
			return fmt.Errorf("saving device addresses: %w", err)
		}
	}
	return tx.Commit()
}

// Neighbor is where a Device was seen in a neighbor table.
type Neighbor struct {
	Source  string     `json:"source"`
	MAC     flow.MAC   `json:"-"`
	Address netip.Addr `json:"address"`
	IfIndex uint32     `json:"ifIndex"`
	// Interface is the source's name for the interface, when it was polled.
	Interface string    `json:"interface,omitempty"`
	FirstSeen time.Time `json:"firstSeen"`
	LastSeen  time.Time `json:"lastSeen"`
}

// NeighborsOf returns where mac, and the MACs merged into it, were seen in neighbor
// tables, most recently first.
func (s *Store) NeighborsOf(ctx context.Context, mac flow.MAC) ([]Neighbor, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT n.source, n.mac, n.address, n.if_index, coalesce(i.name, ''), n.first_seen, n.last_seen
		FROM neighbors n LEFT JOIN snmp_interfaces i ON i.source = n.source AND i.if_index = n.if_index
		WHERE n.mac = $1 OR n.mac IN (SELECT mac FROM device_merges WHERE into_mac = $1)
		ORDER BY n.last_seen DESC, n.address`, mac.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Neighbor{}
	for rows.Next() {
		var n Neighbor
		var m, addr string
		if err := rows.Scan(&n.Source, &m, &addr, &n.IfIndex, &n.Interface, &n.FirstSeen, &n.LastSeen); err != nil {
			return nil, err
		}
		n.MAC, _ = flow.ParseMAC(m)
		n.Address, _ = netip.ParseAddr(addr)
		n.FirstSeen, n.LastSeen = n.FirstSeen.UTC(), n.LastSeen.UTC()
		out = append(out, n)
	}
	return out, rows.Err()
}

// QuietDevice is a Device seen only in neighbor tables in a range: no Flow had its MAC.
type QuietDevice struct {
	MAC        string    `json:"mac"`
	Vendor     string    `json:"vendor,omitempty"`
	PrivateMac bool      `json:"privateMac,omitempty"`
	Address    string    `json:"address"`
	LastSeen   time.Time `json:"lastSeen"`
}

// QuietDevices returns the Devices neighbor tables saw in [from, to) that no Flow in the
// range's hours has, by the address they used last. Router hops are left out, and a
// merged MAC counts as its Device.
func (s *Store) QuietDevices(ctx context.Context, from, to time.Time) ([]QuietDevice, error) {
	busy := `SELECT NULL AS mac WHERE false`
	if tables := s.rollupTablesFor(Hour, from.UTC().Truncate(time.Hour), to.UTC()); len(tables) > 0 {
		parts := make([]string, len(tables))
		for i, t := range tables {
			parts[i] = fmt.Sprintf(`SELECT DISTINCT local_mac AS mac FROM %s WHERE bucket >= $1 AND bucket < $2 AND local_mac <> ''`, t)
		}
		busy = strings.Join(parts, "\nUNION\n")
	}
	rows, err := s.db.QueryContext(ctx, `
WITH n AS (SELECT coalesce(dm.into_mac, n.mac) AS mac, n.address, n.last_seen FROM neighbors n LEFT JOIN device_merges dm ON dm.mac = n.mac
	WHERE n.last_seen >= $3 AND n.first_seen < $2 AND n.mac NOT IN (SELECT mac FROM router_macs)),
busy AS (SELECT coalesce(dm.into_mac, b.mac) AS mac FROM (`+busy+`) b LEFT JOIN device_merges dm ON dm.mac = b.mac)
SELECT mac, arg_max(address, last_seen), max(last_seen) FROM n WHERE mac NOT IN (SELECT mac FROM busy WHERE mac IS NOT NULL)
GROUP BY mac ORDER BY max(last_seen) DESC, mac`, from.UTC().Truncate(time.Hour), to.UTC(), from.UTC())
	if err != nil {
		return nil, fmt.Errorf("quiet devices: %w", err)
	}
	defer rows.Close()
	out := []QuietDevice{}
	for rows.Next() {
		var d QuietDevice
		if err := rows.Scan(&d.MAC, &d.Address, &d.LastSeen); err != nil {
			return nil, err
		}
		d.LastSeen = d.LastSeen.UTC()
		if hw, err := net.ParseMAC(d.MAC); err == nil {
			d.Vendor, d.PrivateMac = oui.Vendor(hw), oui.Private(hw)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
