package collector

import (
	"fmt"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// Severity of a Finding.
type Severity string

// Severities.
const (
	SeverityWarning Severity = "warning"
	SeverityInfo    Severity = "info"
)

// Finding is something about an Exporter or Template that the user should know: a field
// Flowmon needs that is missing or too short, or a reason data isn't decoding.
type Finding struct {
	Severity Severity `json:"severity"`
	Code     string   `json:"code"`
	Message  string   `json:"message"`
}

// IANA element IDs Flowmon looks for in data Templates.
const (
	ieOctetDeltaCount            = 1
	iePacketDeltaCount           = 2
	ieProtocolIdentifier         = 4
	ieSourceIPv4Address          = 8
	ieIngressInterface           = 10
	ieDestinationIPv4Address     = 12
	ieEgressInterface            = 14
	ieFlowEndSysUpTime           = 21
	ieFlowStartSysUpTime         = 22
	ieSourceIPv6Address          = 27
	ieDestinationIPv6Address     = 28
	ieOctetTotalCount            = 85
	iePacketTotalCount           = 86
	ieSystemInitTimeMilliseconds = 160
	ieIngressPhysicalInterface   = 252
	ieEgressPhysicalInterface    = 253
)

var (
	byteCounters   = []uint16{ieOctetDeltaCount, ieOctetTotalCount}
	packetCounters = []uint16{iePacketDeltaCount, iePacketTotalCount}
	sourceAddrs    = []uint16{ieSourceIPv4Address, ieSourceIPv6Address}
	destAddrs      = []uint16{ieDestinationIPv4Address, ieDestinationIPv6Address}
	interfaceIDs   = []uint16{ieIngressInterface, ieEgressInterface, ieIngressPhysicalInterface, ieEgressPhysicalInterface}
	// flowStart*/flowEnd* in every unit, the SysUpTime variants and the delta/duration forms.
	startTimes = []uint16{150, 152, 154, 156, ieFlowStartSysUpTime, 158}
	endTimes   = []uint16{151, 153, 155, 157, ieFlowEndSysUpTime, 159, 161, 162}
)

// AssessTemplate reports what Flowmon can and can't do with the data a Template describes.
// Options Templates carry metadata, not Flows, and get no findings.
func AssessTemplate(t *ipfix.Template) []Finding {
	if t.Options() {
		return nil
	}
	if natEventTemplate(t) {
		return []Finding{{SeverityInfo, "nat_events", "NAT events: NAT sessions being created and deleted, not traffic. Flowmon counts them apart from Flows and learns the router's WAN address from them."}}
	}
	has := map[uint16]ipfix.FieldSpec{}
	for _, f := range t.Fields {
		if f.Enterprise == 0 {
			if _, dup := has[f.ID]; !dup {
				has[f.ID] = f
			}
		}
	}
	first := func(ids []uint16) (ipfix.FieldSpec, bool) {
		for _, id := range ids {
			if f, ok := has[id]; ok {
				return f, true
			}
		}
		return ipfix.FieldSpec{}, false
	}

	var out []Finding
	add := func(sev Severity, code, msg string) { out = append(out, Finding{sev, code, msg}) }

	if f, ok := first(byteCounters); !ok {
		add(SeverityWarning, "no_byte_counter", "No byte counter (octetDeltaCount or octetTotalCount): traffic volume can't be measured from these Flows.")
	} else if !f.Variable() && f.Length < 8 {
		add(SeverityWarning, "short_byte_counter", fmt.Sprintf(
			"%s is sent in %d bytes, so it wraps after %s per export. Keep the exporter's active timeout short (15 s on a 1 Gbit link).",
			f.Name(), f.Length, wrapSize(f.Length)))
	}
	if _, ok := first(packetCounters); !ok {
		add(SeverityInfo, "no_packet_counter", "No packet counter (packetDeltaCount or packetTotalCount).")
	}
	_, hasSrc := first(sourceAddrs)
	_, hasDst := first(destAddrs)
	if !hasSrc || !hasDst {
		add(SeverityWarning, "no_addresses", "No source or destination IP address: Flowmon can't tell which hosts these Flows belong to.")
	}
	if _, ok := has[ieProtocolIdentifier]; !ok {
		add(SeverityWarning, "no_protocol", "No protocolIdentifier: protocols and Service Ports can't be shown.")
	}
	_, hasStart := first(startTimes)
	_, hasEnd := first(endTimes)
	if !hasStart || !hasEnd {
		add(SeverityWarning, "no_flow_times", "No flow start or end time: Flowmon has to fall back on the export time.")
	}
	_, upStart := has[ieFlowStartSysUpTime]
	_, upEnd := has[ieFlowEndSysUpTime]
	_, initTime := has[ieSystemInitTimeMilliseconds]
	// NetFlow v9 and v5 packets say how long the Exporter has been up, which is all it takes.
	if (upStart || upEnd) && !initTime && t.Version == ipfix.Version {
		add(SeverityInfo, "uptime_relative_times", "Flow times are relative to the exporter's uptime. They are converted using systemInitTimeMilliseconds, which this Template doesn't carry; it may come in an Options Template.")
	}
	for _, f := range t.Fields {
		if f.Enterprise == ipfix.EnterpriseReverse && (f.ID == 1 || f.ID == 2 || f.ID == 85 || f.ID == 86) {
			add(SeverityInfo, "biflow", "Biflow records (RFC 5103): each describes both directions of a connection, and Flowmon stores it as two Flows, one per direction.")
			break
		}
	}
	if _, ok := first(interfaceIDs); !ok {
		add(SeverityInfo, "no_interfaces", "No interface numbers: classification by interface isn't possible for this Exporter, so Flowmon classifies by address.")
	}
	return out
}

func wrapSize(length uint16) string {
	switch length {
	case 1:
		return "256 bytes"
	case 2:
		return "64 KiB"
	case 3:
		return "16 MiB"
	case 4:
		return "4 GiB"
	case 5:
		return "1 TiB"
	case 6:
		return "256 TiB"
	case 7:
		return "64 PiB"
	}
	return "16 EiB"
}

// natEventTemplate reports whether a Template describes NAT event records: natEvent
// (IE 230) and no byte or packet counter.
func natEventTemplate(t *ipfix.Template) bool {
	nat := false
	for _, f := range t.Fields {
		if f.Enterprise != 0 {
			continue
		}
		switch f.ID {
		case 230:
			nat = true
		case 1, 2, 85, 86:
			return false
		}
	}
	return nat
}
