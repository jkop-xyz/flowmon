package flowstore

import (
	"context"
	"fmt"
	"net/netip"
	"strings"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// RouterMAC is a MAC that is a router hop, never a Device (package devices).
type RouterMAC struct {
	MAC      flow.MAC
	Exporter netip.Addr
	Reason   string
	Since    time.Time
}

// RouterMACs returns every router MAC learned.
func (s *Store) RouterMACs(ctx context.Context) ([]RouterMAC, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, exporter, reason, since FROM router_macs ORDER BY since, mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RouterMAC
	for rows.Next() {
		var r RouterMAC
		var mac, exporter string
		if err := rows.Scan(&mac, &exporter, &r.Reason, &r.Since); err != nil {
			return nil, err
		}
		r.MAC, _ = flow.ParseMAC(mac)
		r.Exporter, _ = netip.ParseAddr(exporter)
		r.Since = r.Since.UTC()
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddRouterMAC records a router MAC. One already recorded keeps its first record.
func (s *Store) AddRouterMAC(ctx context.Context, r RouterMAC) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO router_macs VALUES (?, ?, ?, ?) ON CONFLICT DO NOTHING`,
		r.MAC.String(), r.Exporter.String(), r.Reason, r.Since.UTC())
	return err
}

// DeviceAddress is an address a Device used, and when.
type DeviceAddress struct {
	MAC                 flow.MAC
	Address             netip.Addr
	FirstSeen, LastSeen time.Time
}

// DeviceAddresses returns every address each Device used, by MAC then first seen.
func (s *Store) DeviceAddresses(ctx context.Context) ([]DeviceAddress, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, address, first_seen, last_seen FROM device_addresses ORDER BY mac, first_seen, address`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DeviceAddress
	for rows.Next() {
		var d DeviceAddress
		var mac, addr string
		if err := rows.Scan(&mac, &addr, &d.FirstSeen, &d.LastSeen); err != nil {
			return nil, err
		}
		d.MAC, _ = flow.ParseMAC(mac)
		d.Address, _ = netip.ParseAddr(addr)
		d.FirstSeen, d.LastSeen = d.FirstSeen.UTC(), d.LastSeen.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}

// saveDeviceAddresses records the addresses flows show their Devices using.
func (s *Store) saveDeviceAddresses(ctx context.Context, flows []flow.Flow) error {
	type key struct {
		mac  flow.MAC
		addr netip.Addr
	}
	span := map[key][2]time.Time{}
	note := func(m flow.MAC, a netip.Addr, t time.Time) {
		if m.IsZero() || !a.IsValid() {
			return
		}
		k := key{m, a}
		sp, ok := span[k]
		if !ok {
			span[k] = [2]time.Time{t, t}
			return
		}
		if t.Before(sp[0]) {
			sp[0] = t
		}
		if t.After(sp[1]) {
			sp[1] = t
		}
		span[k] = sp
	}
	for i := range flows {
		f := &flows[i]
		note(f.LocalMAC, f.Local, f.End.UTC())
		note(f.PeerMAC, f.Peer, f.End.UTC())
	}
	if len(span) == 0 {
		return nil
	}
	values := make([]string, 0, len(span))
	args := make([]any, 0, 4*len(span))
	for k, sp := range span {
		values = append(values, "(?, ?, ?, ?)")
		args = append(args, k.mac.String(), k.addr.String(), sp[0], sp[1])
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO device_addresses VALUES `+strings.Join(values, ", ")+`
		ON CONFLICT (mac, address) DO UPDATE SET first_seen = least(device_addresses.first_seen, excluded.first_seen),
			last_seen = greatest(device_addresses.last_seen, excluded.last_seen)`, args...)
	if err != nil {
		return fmt.Errorf("saving device addresses: %w", err)
	}
	return nil
}

// deviceAddresses lists the addresses mac used in the Rollup tables' [from, to).
func (s *Store) deviceAddresses(ctx context.Context, tables []string, from, to time.Time, mac string) ([]string, error) {
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT DISTINCT local_host FROM %s WHERE bucket >= $1 AND bucket < $2 AND local_mac = $3`, t)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT DISTINCT local_host FROM (`+strings.Join(parts, "\nUNION ALL\n")+`) ORDER BY 1`, from, to, mac)
	if err != nil {
		return nil, fmt.Errorf("device addresses: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// DeviceAddressesOf returns the addresses mac used, most recently seen first.
func (s *Store) DeviceAddressesOf(ctx context.Context, mac flow.MAC) ([]DeviceAddress, error) {
	return s.queryDeviceAddresses(ctx, `WHERE mac = ? ORDER BY last_seen DESC, address`, mac.String())
}

// DevicesAt returns the Devices that used addr, most recently first.
func (s *Store) DevicesAt(ctx context.Context, addr netip.Addr) ([]DeviceAddress, error) {
	return s.queryDeviceAddresses(ctx, `WHERE address = ? ORDER BY last_seen DESC, mac`, addr.Unmap().String())
}

// IsRouterMAC reports whether mac is a known router hop.
func (s *Store) IsRouterMAC(ctx context.Context, mac flow.MAC) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM router_macs WHERE mac = ?`, mac.String()).Scan(&n)
	return n > 0, err
}

func (s *Store) queryDeviceAddresses(ctx context.Context, tail string, args ...any) ([]DeviceAddress, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, address, first_seen, last_seen FROM device_addresses `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceAddress{}
	for rows.Next() {
		var d DeviceAddress
		var mac, addr string
		if err := rows.Scan(&mac, &addr, &d.FirstSeen, &d.LastSeen); err != nil {
			return nil, err
		}
		d.MAC, _ = flow.ParseMAC(mac)
		d.Address, _ = netip.ParseAddr(addr)
		d.FirstSeen, d.LastSeen = d.FirstSeen.UTC(), d.LastSeen.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}
