package snmp

import (
	"context"
	"math"
	"net/netip"
	"sort"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flowstore"
)

// Reconciliation thresholds (ADR 0003, #10): an hour counts only with enough traffic,
// and a warning needs the gap to stay large for several consecutive hours.
const (
	MinBytesPerHour = 10 << 20
	GapThreshold    = 0.15
	SustainedHours  = 3
	ReconcileWindow = 24 * time.Hour
)

// Store is what reconciliation reads. *flowstore.Store implements it.
type Store interface {
	SNMPHours(ctx context.Context, exporter netip.Addr, ifName string, from, to time.Time) ([]flowstore.SNMPHour, error)
	FlowBytesByInterface(ctx context.Context, exporter netip.Addr, index uint32, from, to time.Time) (map[time.Time]flowstore.InterfaceHour, error)
}

// Direction compares one direction of one hour.
type Direction struct {
	// InterfaceBytes is the counter delta minus layer-2 overhead.
	InterfaceBytes float64 `json:"interfaceBytes"`
	FlowBytes      float64 `json:"flowBytes"`
	// Gap is (interface - flows) / interface, or null when there was too little traffic.
	Gap *float64 `json:"gap"`
}

// Hour is one reconciled hour of a WAN Interface.
type Hour struct {
	Hour     time.Time `json:"hour"`
	Complete bool      `json:"complete"`
	In       Direction `json:"in"`
	Out      Direction `json:"out"`
}

// InterfaceReport is the reconciliation of one WAN Interface.
type InterfaceReport struct {
	Name  string `json:"name"`
	Index uint32 `json:"index"`
	Hours []Hour `json:"hours"`
	// Warning is set when the gap stayed above the threshold for SustainedHours.
	Warning bool `json:"warning"`
}

func compare(ifBytes, flowBytes float64) Direction {
	d := Direction{InterfaceBytes: math.Max(ifBytes, 0), FlowBytes: flowBytes}
	if d.InterfaceBytes >= MinBytesPerHour {
		g := (d.InterfaceBytes - flowBytes) / d.InterfaceBytes
		d.Gap = &g
	}
	return d
}

func large(d Direction) bool { return d.Gap != nil && math.Abs(*d.Gap) > GapThreshold }

// Reconcile compares SNMP counters with Flow bytes per complete hour of the last
// ReconcileWindow, for each WAN Interface (name → current interface number).
func Reconcile(ctx context.Context, s Store, exporter netip.Addr, wan map[string]uint32, overhead int, now time.Time) ([]InterfaceReport, error) {
	to := now.UTC().Truncate(time.Hour) // the current hour is still running
	from := to.Add(-ReconcileWindow)
	var out []InterfaceReport
	for name, idx := range wan {
		hours, err := s.SNMPHours(ctx, exporter, name, from, to)
		if err != nil {
			return nil, err
		}
		flows, err := s.FlowBytesByInterface(ctx, exporter, idx, from, to)
		if err != nil {
			return nil, err
		}
		rep := InterfaceReport{Name: name, Index: idx, Hours: []Hour{}}
		for _, h := range hours {
			f := flows[h.Hour]
			rep.Hours = append(rep.Hours, Hour{
				Hour: h.Hour, Complete: h.Complete,
				In:  compare(float64(h.InOctets)-float64(h.InPackets)*float64(overhead), f.InBytes),
				Out: compare(float64(h.OutOctets)-float64(h.OutPackets)*float64(overhead), f.OutBytes),
			})
		}
		// Warn when the newest SustainedHours complete, measurable hours all have a large gap.
		run, seen := 0, 0
		for i := len(rep.Hours) - 1; i >= 0 && seen < SustainedHours; i-- {
			h := rep.Hours[i]
			if !h.Complete || (h.In.Gap == nil && h.Out.Gap == nil) {
				continue
			}
			seen++
			if large(h.In) || large(h.Out) {
				run++
			}
		}
		rep.Warning = seen == SustainedHours && run == SustainedHours
		out = append(out, rep)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
