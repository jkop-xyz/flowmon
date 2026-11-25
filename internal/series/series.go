// Package series is how collectors write time series (ADR 0005): through Writer, so a
// time-series database (ClickHouse, InfluxDB, Prometheus) can be added later without
// touching them. The Flow Store is the one implementation for now.
package series

import (
	"context"
	"time"
)

// Interface is what is known about one network interface of an SNMP source.
type Interface struct {
	Source  string
	IfIndex uint32
	// Name is ifName, Alias ifAlias (the description set on the device).
	Name, Alias string
	// Speed is in bits per second (ifHighSpeed), 0 when unknown.
	Speed uint64
	// Up is ifOperStatus up.
	Up bool
}

// InterfaceSample is an interface's counter deltas between two polls that ended at At,
// over Seconds.
type InterfaceSample struct {
	Source                  string
	IfIndex                 uint32
	At                      time.Time
	Seconds                 float64
	InOctets, OutOctets     uint64
	InPackets, OutPackets   uint64
	InErrors, OutErrors     uint64
	InDiscards, OutDiscards uint64
}

// Writer stores time series.
type Writer interface {
	// WriteInterfaces records interfaces as they are now and their samples.
	WriteInterfaces(ctx context.Context, now time.Time, ifaces []Interface, samples []InterfaceSample) error
}
