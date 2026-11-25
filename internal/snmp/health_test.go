package snmp

import (
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gosnmp/gosnmp"
)

// fakeMIB answers Get and BulkWalkAll from a fixed set of objects, as an agent does:
// a missing scalar is noSuchObject, a missing column walks to nothing.
type fakeMIB map[string]gosnmp.SnmpPDU

func (m fakeMIB) Get(oids []string) (*gosnmp.SnmpPacket, error) {
	p := &gosnmp.SnmpPacket{}
	for _, o := range oids {
		v, ok := m[o]
		if !ok {
			v = gosnmp.SnmpPDU{Name: o, Type: gosnmp.NoSuchObject}
		}
		p.Variables = append(p.Variables, v)
	}
	return p, nil
}

func (m fakeMIB) BulkWalkAll(root string) ([]gosnmp.SnmpPDU, error) {
	var out []gosnmp.SnmpPDU
	for oid, v := range m {
		if strings.HasPrefix(oid, root+".") {
			out = append(out, v)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m fakeMIB) set(oid string, t gosnmp.Asn1BER, v any) {
	m[oid] = gosnmp.SnmpPDU{Name: oid, Type: t, Value: v}
}

// standard is a device with HOST-RESOURCES: two processors and RAM plus a disk.
func standard(vendor string) fakeMIB {
	m := fakeMIB{}
	m.set(oidSysObjectID, gosnmp.ObjectIdentifier, vendor)
	m.set(oidSysUpTime, gosnmp.TimeTicks, uint32(100*60)) // the agent: up a minute
	m.set(oidHrSystemUptime, gosnmp.TimeTicks, uint32(100*86400))
	m.set(oidHrProcessorLoad+".1", gosnmp.Integer, 10)
	m.set(oidHrProcessorLoad+".2", gosnmp.Integer, 30)
	for idx, s := range map[string]struct {
		typ               string
		units, size, used int
	}{"65536": {hrStorageRAM, 1024, 262144, 65536}, "131072": {".1.3.6.1.2.1.25.2.1.4", 4096, 1 << 20, 1 << 19}} {
		m.set(oidHrStorageType+"."+idx, gosnmp.ObjectIdentifier, s.typ)
		m.set(oidHrStorageUnits+"."+idx, gosnmp.Integer, s.units)
		m.set(oidHrStorageSize+"."+idx, gosnmp.Integer, s.size)
		m.set(oidHrStorageUsed+"."+idx, gosnmp.Integer, s.used)
	}
	return m
}

func TestReadHealthMikroTik(t *testing.T) {
	m := standard(mikrotikEnterprise + ".1")
	for i, g := range []struct {
		name  string
		value int
		unit  int
	}{{"cpu-temperature", 45, 1}, {"board-temperature1", 38, 1}, {"voltage", 245, 3}, {"fan1-speed", 3000, 2}, {"psu1-state", 1, 6}} {
		idx := "." + string(rune('1'+i))
		m.set(oidMtxrGaugeName+idx, gosnmp.OctetString, []byte(g.name))
		m.set(oidMtxrGaugeValue+idx, gosnmp.Integer, g.value)
		m.set(oidMtxrGaugeUnit+idx, gosnmp.Integer, g.unit)
	}
	// A phone on interface 5, MAC 00:11:22:33:44:55.
	station := ".0.17.34.51.68.85.5"
	m.set(oidMtxrWlRtabStrength+station, gosnmp.Integer, -58)
	m.set(oidMtxrWlRtabTxRate+station, gosnmp.Gauge32, uint(144_400_000))
	m.set(oidMtxrWlRtabRxRate+station, gosnmp.Gauge32, uint(130_000_000))
	m.set(oidMtxrWlRtabUptime+station, gosnmp.TimeTicks, uint32(100*3600))

	h, err := ReadHealth(m)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		"uptime": 86400, "cpu": 20, "memory_total": 256 << 20, "memory_used": 64 << 20,
		"temperature:cpu-temperature": 45, "temperature:board-temperature1": 38, "voltage:voltage": 24.5, "fan:fan1-speed": 3000,
		"wifi_clients": 1,
	}
	if len(h.Metrics) != len(want) {
		t.Errorf("metrics %v, want %v", h.Metrics, want)
	}
	for k, v := range want {
		if h.Metrics[k] != v {
			t.Errorf("%s = %v, want %v", k, h.Metrics[k], v)
		}
	}
	if h.Uptime != 24*time.Hour {
		t.Errorf("uptime %v: the host's, not the agent's", h.Uptime)
	}
	if len(h.Wifi) != 1 || h.Wifi[0].MAC != "00:11:22:33:44:55" || h.Wifi[0].IfIndex != 5 || h.Wifi[0].Signal != -58 ||
		h.Wifi[0].TxRate != 144_400_000 || h.Wifi[0].Connected != time.Hour {
		t.Errorf("wifi %+v", h.Wifi)
	}
}

// Older RouterOS has no gauge table, only fixed readings in tenths; no wireless
// package means no registration table.
func TestReadHealthOldRouterOS(t *testing.T) {
	m := standard(mikrotikEnterprise + ".1")
	m.set(oidMtxrHlTemperature, gosnmp.Integer, 410)
	m.set(oidMtxrHlVoltage, gosnmp.Integer, 240)
	h, err := ReadHealth(m)
	if err != nil {
		t.Fatal(err)
	}
	if h.Metrics["temperature:temperature"] != 41 || h.Metrics["voltage:voltage"] != 24 || h.Wifi != nil {
		t.Errorf("metrics %v wifi %v", h.Metrics, h.Wifi)
	}
	if _, ok := h.Metrics["temperature:cpu-temperature"]; ok {
		t.Error("a reading the device doesn't offer")
	}
}

// A device without HOST-RESOURCES has only its uptime; MIKROTIK-MIB is only read on
// MikroTik devices.
func TestReadHealthWithoutHostResources(t *testing.T) {
	m := fakeMIB{}
	m.set(oidSysObjectID, gosnmp.ObjectIdentifier, ".1.3.6.1.4.1.4526.100") // some switch
	m.set(oidSysUpTime, gosnmp.TimeTicks, uint32(100*600))
	m.set(oidMtxrHlTemperature, gosnmp.Integer, 410)
	h, err := ReadHealth(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(h.Metrics) != 1 || h.Metrics["uptime"] != 600 || h.Uptime != 10*time.Minute || h.Wifi != nil {
		t.Errorf("%+v", h)
	}
	if got := describeHealth(h); !strings.Contains(got, "no health readings") {
		t.Errorf("describe: %s", got)
	}
}
