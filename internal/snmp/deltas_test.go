package snmp

import (
	"testing"
	"time"
)

func TestDeltas(t *testing.T) {
	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	row := func(in uint64, errs uint32) IfRow {
		return IfRow{Name: "ether1", InOctets: in, OutOctets: in / 2, InPackets: in / 1000, OutPackets: in / 2000, InErrors: errs}
	}
	prev := Poll{At: t0, Table: Table{Uptime: time.Hour, Rows: map[uint32]IfRow{1: row(1_000_000, 4_294_967_290), 2: row(500, 0)}}}
	cur := Poll{At: t0.Add(time.Minute), Table: Table{Uptime: time.Hour + time.Minute,
		Rows: map[uint32]IfRow{1: row(8_500_000, 5), 2: row(100, 0), 3: row(9, 0)}}}

	got := Deltas("sw1", prev, cur)
	if len(got) != 1 {
		t.Fatalf("samples %+v; want only interface 1: 2 reset, 3 new", got)
	}
	s := got[0]
	if s.IfIndex != 1 || s.InOctets != 7_500_000 || s.OutOctets != 3_750_000 || s.Seconds != 60 || s.InErrors != 11 {
		t.Errorf("sample %+v; want 7.5 MB in over 60 s, and 11 errors across the 32-bit wrap", s)
	}

	restarted := cur
	restarted.Table.Uptime = time.Minute
	if got := Deltas("sw1", prev, restarted); got != nil {
		t.Errorf("after a restart: %+v", got)
	}
	late := cur
	late.At = t0.Add(MaxGap + time.Second)
	if got := Deltas("sw1", prev, late); got != nil {
		t.Errorf("after a gap: %+v", got)
	}
	if ifs := Interfaces("sw1", Table{Rows: map[uint32]IfRow{1: {Name: "ether1", SpeedMbps: 1000, Up: true}}}); len(ifs) != 1 || ifs[0].Speed != 1_000_000_000 {
		t.Errorf("interfaces %+v", ifs)
	}
}
