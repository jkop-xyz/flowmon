package snmp

import (
	"context"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"
)

// Counters are an interface's 64-bit IF-MIB counters.
type Counters struct {
	InOctets, OutOctets   uint64
	InPackets, OutPackets uint64
}

// Agent is an SNMP agent on an Exporter.
type Agent interface {
	// InterfaceNames walks ifName.
	InterfaceNames(ctx context.Context) (map[uint32]string, error)
	// Counters gets ifHC* counters for the given interface numbers.
	Counters(ctx context.Context, indexes []uint32) (map[uint32]Counters, error)
	Close() error
}

// Dialer opens an Agent. Tests replace it.
type Dialer func(c Config, exporter netip.Addr) (Agent, error)

const (
	oidIfName           = ".1.3.6.1.2.1.31.1.1.1.1"
	oidIfHCInOctets     = ".1.3.6.1.2.1.31.1.1.1.6"
	oidIfHCInUcastPkts  = ".1.3.6.1.2.1.31.1.1.1.7"
	oidIfHCInMcastPkts  = ".1.3.6.1.2.1.31.1.1.1.8"
	oidIfHCInBcastPkts  = ".1.3.6.1.2.1.31.1.1.1.9"
	oidIfHCOutOctets    = ".1.3.6.1.2.1.31.1.1.1.10"
	oidIfHCOutUcastPkts = ".1.3.6.1.2.1.31.1.1.1.11"
	oidIfHCOutMcastPkts = ".1.3.6.1.2.1.31.1.1.1.12"
	oidIfHCOutBcastPkts = ".1.3.6.1.2.1.31.1.1.1.13"
)

var authProtocols = map[string]gosnmp.SnmpV3AuthProtocol{
	"MD5": gosnmp.MD5, "SHA": gosnmp.SHA, "SHA224": gosnmp.SHA224, "SHA256": gosnmp.SHA256, "SHA384": gosnmp.SHA384, "SHA512": gosnmp.SHA512,
}

var privProtocols = map[string]gosnmp.SnmpV3PrivProtocol{
	"DES": gosnmp.DES, "AES": gosnmp.AES, "AES192": gosnmp.AES192, "AES256": gosnmp.AES256,
}

// Dial connects to an Exporter's agent with gosnmp.
func Dial(c Config, exporter netip.Addr) (Agent, error) {
	host, port := exporter.String(), uint16(161)
	if c.Target != "" {
		var err error
		if host, port, err = splitTarget(c.Target); err != nil {
			return nil, err
		}
	}
	g := &gosnmp.GoSNMP{Target: host, Port: port, Timeout: 5 * time.Second, Retries: 1, MaxRepetitions: 25, MaxOids: 60}
	switch c.Version {
	case "v3":
		g.Version = gosnmp.Version3
		g.SecurityModel = gosnmp.UserSecurityModel
		usm := &gosnmp.UsmSecurityParameters{UserName: c.User}
		g.MsgFlags = gosnmp.NoAuthNoPriv
		if c.AuthProtocol != "" {
			usm.AuthenticationProtocol, usm.AuthenticationPassphrase = authProtocols[c.AuthProtocol], c.AuthPassword
			g.MsgFlags = gosnmp.AuthNoPriv
		}
		if c.PrivProtocol != "" {
			usm.PrivacyProtocol, usm.PrivacyPassphrase = privProtocols[c.PrivProtocol], c.PrivPassword
			g.MsgFlags = gosnmp.AuthPriv
		}
		g.SecurityParameters = usm
	default:
		g.Version = gosnmp.Version2c
		g.Community = c.Community
	}
	if err := g.Connect(); err != nil {
		return nil, fmt.Errorf("snmp: connecting to %s:%d: %w", host, port, err)
	}
	return &gosnmpAgent{g: g}, nil
}

type gosnmpAgent struct{ g *gosnmp.GoSNMP }

func (a *gosnmpAgent) Close() error { return a.g.Conn.Close() }

func indexOf(oid, base string) (uint32, bool) {
	rest, ok := strings.CutPrefix(oid, base+".")
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(rest, 10, 32)
	return uint32(n), err == nil
}

func (a *gosnmpAgent) InterfaceNames(ctx context.Context) (map[uint32]string, error) {
	a.g.Context = ctx
	pdus, err := a.g.BulkWalkAll(oidIfName)
	if err != nil {
		return nil, fmt.Errorf("snmp: walking ifName: %w", err)
	}
	out := map[uint32]string{}
	for _, p := range pdus {
		idx, ok := indexOf(p.Name, oidIfName)
		if b, isBytes := p.Value.([]byte); ok && isBytes && len(b) > 0 {
			out[idx] = string(b)
		}
	}
	return out, nil
}

func (a *gosnmpAgent) Counters(ctx context.Context, indexes []uint32) (map[uint32]Counters, error) {
	a.g.Context = ctx
	out := map[uint32]Counters{}
	bases := []string{oidIfHCInOctets, oidIfHCOutOctets, oidIfHCInUcastPkts, oidIfHCInMcastPkts, oidIfHCInBcastPkts,
		oidIfHCOutUcastPkts, oidIfHCOutMcastPkts, oidIfHCOutBcastPkts}
	for _, idx := range indexes {
		var oids []string
		for _, b := range bases {
			oids = append(oids, b+"."+strconv.FormatUint(uint64(idx), 10))
		}
		res, err := a.g.Get(oids)
		if err != nil {
			return nil, fmt.Errorf("snmp: getting counters of interface %d: %w", idx, err)
		}
		var c Counters
		for _, p := range res.Variables {
			if p.Type != gosnmp.Counter64 {
				continue // only 64-bit counters are used (ADR 0003)
			}
			v := gosnmp.ToBigInt(p.Value).Uint64()
			switch {
			case strings.HasPrefix(p.Name, oidIfHCInOctets+"."):
				c.InOctets = v
			case strings.HasPrefix(p.Name, oidIfHCOutOctets+"."):
				c.OutOctets = v
			case strings.HasPrefix(p.Name, oidIfHCInUcastPkts+"."), strings.HasPrefix(p.Name, oidIfHCInMcastPkts+"."), strings.HasPrefix(p.Name, oidIfHCInBcastPkts+"."):
				c.InPackets += v
			default:
				c.OutPackets += v
			}
		}
		out[idx] = c
	}
	return out, nil
}

// IF-MIB and SNMPv2-MIB columns read for Interface Counters.
const (
	oidSysUpTime     = ".1.3.6.1.2.1.1.3.0"
	oidIfInDiscards  = ".1.3.6.1.2.1.2.2.1.13"
	oidIfInErrors    = ".1.3.6.1.2.1.2.2.1.14"
	oidIfOutDiscards = ".1.3.6.1.2.1.2.2.1.19"
	oidIfOutErrors   = ".1.3.6.1.2.1.2.2.1.20"
	oidIfOperStatus  = ".1.3.6.1.2.1.2.2.1.8"
	oidIfHighSpeed   = ".1.3.6.1.2.1.31.1.1.1.15"
	oidIfAlias       = ".1.3.6.1.2.1.31.1.1.1.18"
)

// IfRow is one interface's row of IF-MIB, as read.
type IfRow struct {
	Name, Alias string
	// SpeedMbps is ifHighSpeed.
	SpeedMbps uint64
	Up        bool
	// 64-bit counters.
	InOctets, OutOctets, InPackets, OutPackets uint64
	// 32-bit counters, which wrap at 2^32.
	InErrors, OutErrors, InDiscards, OutDiscards uint32
}

// Table is an agent's interfaces at one moment.
type Table struct {
	// Uptime is sysUpTime: it goes back when the device restarted.
	Uptime time.Duration
	Rows   map[uint32]IfRow
}

// TableReader reads a device's whole interface table.
type TableReader interface {
	Table(ctx context.Context) (Table, error)
	Close() error
}

// Table walks IF-MIB's interface columns.
func (a *gosnmpAgent) Table(ctx context.Context) (Table, error) {
	a.g.Context = ctx
	t := Table{Rows: map[uint32]IfRow{}}
	res, err := a.g.Get([]string{oidSysUpTime})
	if err != nil {
		return t, fmt.Errorf("snmp: getting sysUpTime: %w", err)
	}
	for _, v := range res.Variables {
		if v.Type == gosnmp.TimeTicks {
			t.Uptime = time.Duration(gosnmp.ToBigInt(v.Value).Uint64()) * 10 * time.Millisecond
		}
	}
	row := func(idx uint32) IfRow { return t.Rows[idx] }
	walk := func(base string, set func(r *IfRow, v gosnmp.SnmpPDU)) error {
		pdus, err := a.g.BulkWalkAll(base)
		if err != nil {
			return fmt.Errorf("snmp: walking %s: %w", base, err)
		}
		for _, p := range pdus {
			idx, ok := indexOf(p.Name, base)
			if !ok {
				continue
			}
			r := row(idx)
			set(&r, p)
			t.Rows[idx] = r
		}
		return nil
	}
	num := func(p gosnmp.SnmpPDU) uint64 { return gosnmp.ToBigInt(p.Value).Uint64() }
	str := func(p gosnmp.SnmpPDU) string {
		if b, ok := p.Value.([]byte); ok {
			return string(b)
		}
		return ""
	}
	for _, c := range []struct {
		oid string
		set func(r *IfRow, p gosnmp.SnmpPDU)
	}{
		{oidIfName, func(r *IfRow, p gosnmp.SnmpPDU) { r.Name = str(p) }},
		{oidIfAlias, func(r *IfRow, p gosnmp.SnmpPDU) { r.Alias = str(p) }},
		{oidIfHighSpeed, func(r *IfRow, p gosnmp.SnmpPDU) { r.SpeedMbps = num(p) }},
		{oidIfOperStatus, func(r *IfRow, p gosnmp.SnmpPDU) { r.Up = num(p) == 1 }},
		{oidIfHCInOctets, func(r *IfRow, p gosnmp.SnmpPDU) { r.InOctets = num(p) }},
		{oidIfHCOutOctets, func(r *IfRow, p gosnmp.SnmpPDU) { r.OutOctets = num(p) }},
		{oidIfHCInUcastPkts, func(r *IfRow, p gosnmp.SnmpPDU) { r.InPackets += num(p) }},
		{oidIfHCInMcastPkts, func(r *IfRow, p gosnmp.SnmpPDU) { r.InPackets += num(p) }},
		{oidIfHCInBcastPkts, func(r *IfRow, p gosnmp.SnmpPDU) { r.InPackets += num(p) }},
		{oidIfHCOutUcastPkts, func(r *IfRow, p gosnmp.SnmpPDU) { r.OutPackets += num(p) }},
		{oidIfHCOutMcastPkts, func(r *IfRow, p gosnmp.SnmpPDU) { r.OutPackets += num(p) }},
		{oidIfHCOutBcastPkts, func(r *IfRow, p gosnmp.SnmpPDU) { r.OutPackets += num(p) }},
		{oidIfInErrors, func(r *IfRow, p gosnmp.SnmpPDU) { r.InErrors = uint32(num(p)) }},
		{oidIfOutErrors, func(r *IfRow, p gosnmp.SnmpPDU) { r.OutErrors = uint32(num(p)) }},
		{oidIfInDiscards, func(r *IfRow, p gosnmp.SnmpPDU) { r.InDiscards = uint32(num(p)) }},
		{oidIfOutDiscards, func(r *IfRow, p gosnmp.SnmpPDU) { r.OutDiscards = uint32(num(p)) }},
	} {
		if err := walk(c.oid, c.set); err != nil {
			return t, err
		}
	}
	return t, nil
}
