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

// Health is an SNMP device's Device Health at one poll (ADR 0005).
type Health struct {
	Source string
	At     time.Time
	// Boot is when the device started: At less its uptime.
	Boot time.Time
	// Metrics are the readings by name: "uptime" (seconds), "cpu" (percent),
	// "memory_used" and "memory_total" (bytes), "wifi_clients", and sensors as
	// "<kind>:<sensor name>", with kind temperature (°C), voltage (V), current (A),
	// power (W) or fan (rpm). A reading the device doesn't offer is absent.
	Metrics map[string]float64
	// Wifi are its Wi-Fi registrations now; nil when it has no registration table.
	Wifi []WifiClient
}

// WifiClient is a station registered to one of an access point's interfaces.
type WifiClient struct {
	MAC     string
	IfIndex uint32
	// Signal is in dBm; rates in bits per second.
	Signal         int
	TxRate, RxRate uint64
	Connected      time.Duration
}

// HealthWriter stores Device Health.
type HealthWriter interface {
	// WriteHealth records a poll's readings. restart is the boot time when the device
	// restarted since the last poll (or while nothing polled it), else zero.
	WriteHealth(ctx context.Context, h Health) (restart time.Time, err error)
}
