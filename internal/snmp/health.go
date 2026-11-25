package snmp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/gosnmp/gosnmp"

	"github.com/jkop-xyz/flowmon/internal/series"
)

// Device Health (ADR 0005): the standard MIBs every device may offer, plus MIKROTIK-MIB
// on MikroTik devices. Other vendors get the standard set until their MIB is added.
const (
	oidSysObjectID     = ".1.3.6.1.2.1.1.2.0"
	oidHrSystemUptime  = ".1.3.6.1.2.1.25.1.1.0"
	oidHrProcessorLoad = ".1.3.6.1.2.1.25.3.3.1.2"
	oidHrStorageType   = ".1.3.6.1.2.1.25.2.3.1.2"
	oidHrStorageUnits  = ".1.3.6.1.2.1.25.2.3.1.4"
	oidHrStorageSize   = ".1.3.6.1.2.1.25.2.3.1.5"
	oidHrStorageUsed   = ".1.3.6.1.2.1.25.2.3.1.6"
	hrStorageRAM       = ".1.3.6.1.2.1.25.2.1.2"

	mikrotikEnterprise = ".1.3.6.1.4.1.14988"
	// mtxrGaugeTable: name, value and unit of each sensor (RouterOS 6.4x and 7).
	oidMtxrGaugeName  = ".1.3.6.1.4.1.14988.1.1.3.100.1.2"
	oidMtxrGaugeValue = ".1.3.6.1.4.1.14988.1.1.3.100.1.3"
	oidMtxrGaugeUnit  = ".1.3.6.1.4.1.14988.1.1.3.100.1.4"
	// Older RouterOS: single readings in tenths.
	oidMtxrHlVoltage              = ".1.3.6.1.4.1.14988.1.1.3.8.0"
	oidMtxrHlTemperature          = ".1.3.6.1.4.1.14988.1.1.3.10.0"
	oidMtxrHlProcessorTemperature = ".1.3.6.1.4.1.14988.1.1.3.11.0"
	// mtxrWlRtabTable, indexed by the station's MAC (6 sub-ids) and the ifIndex.
	oidMtxrWlRtabStrength = ".1.3.6.1.4.1.14988.1.1.1.2.1.3"
	oidMtxrWlRtabTxRate   = ".1.3.6.1.4.1.14988.1.1.1.2.1.8"
	oidMtxrWlRtabRxRate   = ".1.3.6.1.4.1.14988.1.1.1.2.1.9"
	oidMtxrWlRtabUptime   = ".1.3.6.1.4.1.14988.1.1.1.2.1.11"
)

// mtxrGaugeUnit values, and the metric kind and scale each maps to.
var gaugeUnits = map[uint64]struct {
	kind  string
	scale float64
}{
	1: {"temperature", 1}, // celsius
	2: {"fan", 1},         // rpm
	3: {"voltage", 0.1},   // dV
	4: {"current", 0.1},   // dA
	5: {"power", 0.1},     // dW
	// 6 is status (a PSU ok or not), not a reading.
}

// PDUReader is the part of an SNMP session Device Health uses. *gosnmp.GoSNMP is one;
// tests use a fake MIB.
type PDUReader interface {
	Get(oids []string) (*gosnmp.SnmpPacket, error)
	BulkWalkAll(rootOid string) ([]gosnmp.SnmpPDU, error)
}

// Health is what a device says about itself.
type Health struct {
	// Uptime is the host's (hrSystemUptime), else the agent's (sysUpTime); 0 when
	// neither was read here.
	Uptime time.Duration
	// Metrics as series.Health names them.
	Metrics map[string]float64
	// Wifi is nil when the device has no registration table.
	Wifi []series.WifiClient
}

// ReadHealth reads Device Health. A device without HOST-RESOURCES or a vendor MIB just
// has fewer readings; only a failed request is an error.
func ReadHealth(r PDUReader) (Health, error) {
	h := Health{Metrics: map[string]float64{}}
	res, err := r.Get([]string{oidSysObjectID, oidHrSystemUptime, oidSysUpTime})
	if err != nil {
		return h, fmt.Errorf("snmp: reading the system group: %w", err)
	}
	var vendor string
	var sysUp, hrUp time.Duration
	for _, v := range res.Variables {
		switch v.Name {
		case oidSysObjectID:
			if s, ok := v.Value.(string); ok {
				vendor = s
			}
		case oidHrSystemUptime:
			if v.Type == gosnmp.TimeTicks {
				hrUp = ticks(v)
			}
		case oidSysUpTime:
			if v.Type == gosnmp.TimeTicks {
				sysUp = ticks(v)
			}
		}
	}
	h.Uptime = hrUp
	if h.Uptime == 0 {
		h.Uptime = sysUp
	}
	if h.Uptime > 0 {
		h.Metrics["uptime"] = h.Uptime.Seconds()
	}

	loads, err := walk(r, oidHrProcessorLoad)
	if err != nil {
		return h, err
	}
	if len(loads) > 0 {
		var sum float64
		for _, p := range loads {
			sum += float64(num(p))
		}
		h.Metrics["cpu"] = sum / float64(len(loads))
	}
	if err := readMemory(r, h.Metrics); err != nil {
		return h, err
	}
	if strings.HasPrefix(vendor, mikrotikEnterprise+".") {
		if err := readMikroTik(r, &h); err != nil {
			return h, err
		}
	}
	return h, nil
}

func ticks(p gosnmp.SnmpPDU) time.Duration {
	return time.Duration(gosnmp.ToBigInt(p.Value).Uint64()) * 10 * time.Millisecond
}

func num(p gosnmp.SnmpPDU) int64 { return gosnmp.ToBigInt(p.Value).Int64() }

// walk reads a column; a device without it gives none.
func walk(r PDUReader, oid string) ([]gosnmp.SnmpPDU, error) {
	pdus, err := r.BulkWalkAll(oid)
	if err != nil {
		return nil, fmt.Errorf("snmp: walking %s: %w", oid, err)
	}
	out := pdus[:0]
	for _, p := range pdus {
		if strings.HasPrefix(p.Name, oid+".") && p.Type != gosnmp.NoSuchObject && p.Type != gosnmp.NoSuchInstance && p.Type != gosnmp.EndOfMibView {
			out = append(out, p)
		}
	}
	return out, nil
}

// column walks a column into values by the rest of their OID after it.
func column(r PDUReader, oid string) (map[string]gosnmp.SnmpPDU, error) {
	pdus, err := walk(r, oid)
	out := make(map[string]gosnmp.SnmpPDU, len(pdus))
	for _, p := range pdus {
		out[strings.TrimPrefix(p.Name, oid+".")] = p
	}
	return out, err
}

// readMemory sums the RAM entries of hrStorageTable (usually one).
func readMemory(r PDUReader, m map[string]float64) error {
	types, err := column(r, oidHrStorageType)
	if err != nil {
		return err
	}
	var ram []string
	for idx, p := range types {
		if s, ok := p.Value.(string); ok && s == hrStorageRAM {
			ram = append(ram, idx)
		}
	}
	if len(ram) == 0 {
		return nil
	}
	cols := map[string]map[string]gosnmp.SnmpPDU{}
	for _, oid := range []string{oidHrStorageUnits, oidHrStorageSize, oidHrStorageUsed} {
		if cols[oid], err = column(r, oid); err != nil {
			return err
		}
	}
	var used, total float64
	for _, idx := range ram {
		units := float64(num(cols[oidHrStorageUnits][idx]))
		total += units * float64(num(cols[oidHrStorageSize][idx]))
		used += units * float64(num(cols[oidHrStorageUsed][idx]))
	}
	if total > 0 {
		m["memory_used"], m["memory_total"] = used, total
	}
	return nil
}

func readMikroTik(r PDUReader, h *Health) error {
	names, err := column(r, oidMtxrGaugeName)
	if err != nil {
		return err
	}
	if len(names) > 0 {
		values, err := column(r, oidMtxrGaugeValue)
		if err != nil {
			return err
		}
		units, err := column(r, oidMtxrGaugeUnit)
		if err != nil {
			return err
		}
		for idx, n := range names {
			name, _ := n.Value.([]byte)
			v, ok := values[idx]
			u, known := gaugeUnits[uint64(num(units[idx]))]
			if len(name) == 0 || !ok || !known {
				continue
			}
			h.Metrics[u.kind+":"+string(name)] = float64(num(v)) * u.scale
		}
	} else {
		// Before the gauge table: three fixed readings, in tenths.
		res, err := r.Get([]string{oidMtxrHlTemperature, oidMtxrHlProcessorTemperature, oidMtxrHlVoltage})
		if err != nil {
			return fmt.Errorf("snmp: reading MikroTik health: %w", err)
		}
		names := map[string]string{oidMtxrHlTemperature: "temperature:temperature", oidMtxrHlProcessorTemperature: "temperature:cpu-temperature", oidMtxrHlVoltage: "voltage:voltage"}
		for _, v := range res.Variables {
			if key, ok := names[v.Name]; ok && (v.Type == gosnmp.Integer || v.Type == gosnmp.Gauge32) {
				h.Metrics[key] = float64(num(v)) / 10
			}
		}
	}
	return readRegistrations(r, h)
}

// readRegistrations reads the wireless registration table, when the device has one.
func readRegistrations(r PDUReader, h *Health) error {
	strength, err := column(r, oidMtxrWlRtabStrength)
	if err != nil || len(strength) == 0 {
		return err
	}
	cols := map[string]map[string]gosnmp.SnmpPDU{}
	for _, oid := range []string{oidMtxrWlRtabTxRate, oidMtxrWlRtabRxRate, oidMtxrWlRtabUptime} {
		if cols[oid], err = column(r, oid); err != nil {
			return err
		}
	}
	h.Wifi = []series.WifiClient{}
	for idx, s := range strength {
		mac, ifIndex, ok := registrationIndex(idx)
		if !ok {
			continue
		}
		c := series.WifiClient{MAC: mac, IfIndex: ifIndex, Signal: int(num(s))}
		if p, ok := cols[oidMtxrWlRtabTxRate][idx]; ok {
			c.TxRate = uint64(num(p))
		}
		if p, ok := cols[oidMtxrWlRtabRxRate][idx]; ok {
			c.RxRate = uint64(num(p))
		}
		if p, ok := cols[oidMtxrWlRtabUptime][idx]; ok && p.Type == gosnmp.TimeTicks {
			c.Connected = ticks(p)
		}
		h.Wifi = append(h.Wifi, c)
	}
	h.Metrics["wifi_clients"] = float64(len(h.Wifi))
	return nil
}

// registrationIndex parses "a.b.c.d.e.f.ifIndex": the station's MAC, then its interface.
func registrationIndex(idx string) (string, uint32, bool) {
	parts := strings.Split(idx, ".")
	if len(parts) != 7 {
		return "", 0, false
	}
	mac := make(net.HardwareAddr, 6)
	for i := range 6 {
		b, err := strconv.ParseUint(parts[i], 10, 8)
		if err != nil {
			return "", 0, false
		}
		mac[i] = byte(b)
	}
	n, err := strconv.ParseUint(parts[6], 10, 32)
	if err != nil {
		return "", 0, false
	}
	return mac.String(), uint32(n), true
}

// Health reads Device Health over the agent's session.
func (a *gosnmpAgent) Health(ctx context.Context) (Health, error) {
	a.g.Context = ctx
	return ReadHealth(a.g)
}

// HealthReader is an agent that reads Device Health.
type HealthReader interface {
	Health(ctx context.Context) (Health, error)
}
