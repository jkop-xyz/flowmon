package flowstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"time"
)

// SNMPConfig returns an Exporter's stored SNMP settings (JSON), or "" if none.
func (s *Store) SNMPConfig(ctx context.Context, exporter netip.Addr) (string, error) {
	var cfg string
	err := s.db.QueryRowContext(ctx, `SELECT config FROM snmp_configs WHERE exporter = ?`, exporter.String()).Scan(&cfg)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return cfg, err
}

// SNMPConfigs returns every stored SNMP setting by Exporter.
func (s *Store) SNMPConfigs(ctx context.Context) (map[netip.Addr]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT exporter, config FROM snmp_configs`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[netip.Addr]string{}
	for rows.Next() {
		var exp, cfg string
		if err := rows.Scan(&exp, &cfg); err != nil {
			return nil, err
		}
		if a, err := netip.ParseAddr(exp); err == nil {
			out[a] = cfg
		}
	}
	return out, rows.Err()
}

// SetSNMPConfig stores an Exporter's SNMP settings; "" deletes them.
func (s *Store) SetSNMPConfig(ctx context.Context, exporter netip.Addr, cfg string) error {
	if cfg == "" {
		_, err := s.db.ExecContext(ctx, `DELETE FROM snmp_configs WHERE exporter = ?`, exporter.String())
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT OR REPLACE INTO snmp_configs VALUES (?, ?)`, exporter.String(), cfg)
	return err
}

// SNMPHour is one hour of SNMP counter deltas for an interface.
type SNMPHour struct {
	Hour                  time.Time
	InOctets, OutOctets   uint64
	InPackets, OutPackets uint64
	// Complete is false when polling didn't cover the whole hour (start-up, counter
	// reset, missed polls); such hours aren't reconciled.
	Complete bool
}

// AddSNMPDelta adds counter deltas to an interface's hour. complete=false marks the
// hour incomplete for good.
func (s *Store) AddSNMPDelta(ctx context.Context, exporter netip.Addr, ifName string, d SNMPHour) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO snmp_hours VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (exporter, if_name, hour) DO UPDATE SET
			in_octets = snmp_hours.in_octets + excluded.in_octets,
			out_octets = snmp_hours.out_octets + excluded.out_octets,
			in_packets = snmp_hours.in_packets + excluded.in_packets,
			out_packets = snmp_hours.out_packets + excluded.out_packets,
			complete = snmp_hours.complete AND excluded.complete`,
		exporter.String(), ifName, d.Hour.UTC().Truncate(time.Hour), d.InOctets, d.OutOctets, d.InPackets, d.OutPackets, d.Complete)
	return err
}

// SNMPHours returns an interface's hours in [from, to), oldest first.
func (s *Store) SNMPHours(ctx context.Context, exporter netip.Addr, ifName string, from, to time.Time) ([]SNMPHour, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT hour, in_octets, out_octets, in_packets, out_packets, complete FROM snmp_hours
		WHERE exporter = ? AND if_name = ? AND hour >= ? AND hour < ? ORDER BY hour`, exporter.String(), ifName, from.UTC(), to.UTC())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SNMPHour
	for rows.Next() {
		var h SNMPHour
		if err := rows.Scan(&h.Hour, &h.InOctets, &h.OutOctets, &h.InPackets, &h.OutPackets, &h.Complete); err != nil {
			return nil, err
		}
		h.Hour = h.Hour.UTC()
		out = append(out, h)
	}
	return out, rows.Err()
}

// InterfaceHour is Flow bytes through one interface number in one hour.
type InterfaceHour struct {
	Hour     time.Time
	InBytes  float64 // Flows that entered through the interface
	OutBytes float64 // Flows that left through it
}

// FlowBytesByInterface sums raw Flow bytes per hour that entered or left through an
// interface number, by flow end time.
func (s *Store) FlowBytesByInterface(ctx context.Context, exporter netip.Addr, index uint32, from, to time.Time) (map[time.Time]InterfaceHour, error) {
	out := map[time.Time]InterfaceHour{}
	tables := s.tablesFor(from, to)
	if len(tables) == 0 {
		return out, nil
	}
	var parts []string
	for _, t := range tables {
		parts = append(parts, fmt.Sprintf(`SELECT end_ts, bytes, ingress_interface, egress_interface FROM %s WHERE exporter = $1 AND end_ts >= $2 AND end_ts < $3`, t))
	}
	q := `SELECT time_bucket(INTERVAL 1 HOUR, end_ts) AS h,
		sum(CASE WHEN ingress_interface = $4 THEN bytes ELSE 0 END)::DOUBLE,
		sum(CASE WHEN egress_interface = $4 THEN bytes ELSE 0 END)::DOUBLE
		FROM (` + strings.Join(parts, " UNION ALL ") + `) GROUP BY h`
	rows, err := s.db.QueryContext(ctx, q, exporter.String(), from.UTC(), to.UTC(), index)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var h InterfaceHour
		if err := rows.Scan(&h.Hour, &h.InBytes, &h.OutBytes); err != nil {
			return nil, err
		}
		h.Hour = h.Hour.UTC()
		out[h.Hour] = h
	}
	return out, rows.Err()
}
