package flowstore

import (
	"context"
	"database/sql"
	"errors"
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
		parts[i] = fmt.Sprintf(`SELECT DISTINCT local_host FROM %s WHERE bucket >= $1 AND bucket < $2 AND (local_mac = $3 OR local_mac IN (SELECT mac FROM device_merges WHERE into_mac = $3))`, t)
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

// DeviceAddressesOf returns the addresses mac, and the MACs merged into it, used, most
// recently seen first.
func (s *Store) DeviceAddressesOf(ctx context.Context, mac flow.MAC) ([]DeviceAddress, error) {
	return s.queryDeviceAddresses(ctx, `WHERE mac = ? OR mac IN (SELECT mac FROM device_merges WHERE into_mac = ?) ORDER BY last_seen DESC, address`, mac.String(), mac.String())
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

// DeviceMerge is a MAC counted as another Device.
type DeviceMerge struct {
	MAC, Into flow.MAC
	// Reason is "auto" (Private Addresses with the same name) or "manual".
	Reason   string
	MergedAt time.Time
}

// DeviceMerges returns every merge.
func (s *Store) DeviceMerges(ctx context.Context) ([]DeviceMerge, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, into_mac, reason, merged_at FROM device_merges ORDER BY merged_at, mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []DeviceMerge{}
	for rows.Next() {
		var m DeviceMerge
		var mac, into string
		if err := rows.Scan(&mac, &into, &m.Reason, &m.MergedAt); err != nil {
			return nil, err
		}
		m.MAC, _ = flow.ParseMAC(mac)
		m.Into, _ = flow.ParseMAC(into)
		m.MergedAt = m.MergedAt.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// ErrMergeSelf is a merge of a Device into itself.
var ErrMergeSelf = errors.New("flowstore: a Device can't be merged into itself")

// MergeDevice counts mac as into, and every MAC merged into mac with it. When into is
// itself merged, mac goes to the Device into belongs to, so merges stay one step deep.
func (s *Store) MergeDevice(ctx context.Context, mac, into flow.MAC, reason string, at time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var root string
	err = tx.QueryRowContext(ctx, `SELECT into_mac FROM device_merges WHERE mac = ?`, into.String()).Scan(&root)
	if errors.Is(err, sql.ErrNoRows) {
		root, err = into.String(), nil
	}
	if err != nil {
		return err
	}
	if root == mac.String() {
		return ErrMergeSelf
	}
	for _, q := range []struct {
		sql  string
		args []any
	}{
		{`UPDATE device_merges SET into_mac = ? WHERE into_mac = ?`, []any{root, mac.String()}},
		{`INSERT INTO device_merges VALUES (?, ?, ?, ?) ON CONFLICT (mac) DO UPDATE SET into_mac = excluded.into_mac, reason = excluded.reason, merged_at = excluded.merged_at`,
			[]any{mac.String(), root, reason, at.UTC()}},
	} {
		if _, err := tx.ExecContext(ctx, q.sql, q.args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// SplitDevice undoes mac's merge: it counts as its own Device again. MACs merged into
// mac's Device stay merged.
func (s *Store) SplitDevice(ctx context.Context, mac flow.MAC) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM device_merges WHERE mac = ?`, mac.String())
	return err
}

// MACSpan is when a MAC was first and last seen, and the address it used last.
type MACSpan struct {
	MAC                 flow.MAC
	FirstSeen, LastSeen time.Time
	Latest              netip.Addr
}

// MACSpans returns every MAC's span, from the addresses Devices used.
func (s *Store) MACSpans(ctx context.Context) ([]MACSpan, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT mac, min(first_seen), max(last_seen), arg_max(address, last_seen) FROM device_addresses GROUP BY mac`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MACSpan
	for rows.Next() {
		var m MACSpan
		var mac, addr string
		if err := rows.Scan(&mac, &m.FirstSeen, &m.LastSeen, &addr); err != nil {
			return nil, err
		}
		m.MAC, _ = flow.ParseMAC(mac)
		m.Latest, _ = netip.ParseAddr(addr)
		m.FirstSeen, m.LastSeen = m.FirstSeen.UTC(), m.LastSeen.UTC()
		out = append(out, m)
	}
	return out, rows.Err()
}

// DNSServer is a server a Local Host sent DNS to (port 53, or 853 for DNS over TLS),
// from raw Flows.
type DNSServer struct {
	Server string    `json:"server"`
	Port   uint16    `json:"port"`
	Flows  int       `json:"flows"`
	Bytes  uint64    `json:"bytes"`
	Last   time.Time `json:"last"`
}

// DNSServers lists the DNS servers addrs sent to in [from, to), busiest first. Raw Flows
// only: older ranges have none.
func (s *Store) DNSServers(ctx context.Context, addrs []string, from, to time.Time) ([]DNSServer, error) {
	out := []DNSServer{}
	tables := s.tablesFor(from, to)
	if len(tables) == 0 || len(addrs) == 0 {
		return out, nil
	}
	a := &sqlArgs{}
	where := fmt.Sprintf(`end_ts >= %s AND start_ts < %s AND direction IN (2, 3) AND peer_port IN (53, 853) AND %s`,
		a.add(from.UTC()), a.add(to.UTC()), inList(a, "local_host", addrs))
	parts := make([]string, len(tables))
	for i, t := range tables {
		parts[i] = fmt.Sprintf(`SELECT peer, peer_port, bytes, end_ts FROM %s WHERE %s`, t, where)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT peer, peer_port, count(*), sum(bytes), max(end_ts) FROM (`+strings.Join(parts, " UNION ALL ")+`)
		GROUP BY peer, peer_port ORDER BY 3 DESC, 1 LIMIT 20`, a.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var d DNSServer
		if err := rows.Scan(&d.Server, &d.Port, &d.Flows, &d.Bytes, &d.Last); err != nil {
			return nil, err
		}
		d.Last = d.Last.UTC()
		out = append(out, d)
	}
	return out, rows.Err()
}
