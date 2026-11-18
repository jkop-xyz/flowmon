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
