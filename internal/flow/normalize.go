package flow

import (
	"encoding/binary"
	"errors"
	"net/netip"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// IANA element IDs used during normalisation.
const (
	ieOctetDeltaCount               = 1
	iePacketDeltaCount              = 2
	ieProtocolIdentifier            = 4
	ieSourceTransportPort           = 7
	ieSourceIPv4Address             = 8
	ieIngressInterface              = 10
	ieDestinationTransportPort      = 11
	ieDestinationIPv4Address        = 12
	ieEgressInterface               = 14
	ieFlowEndSysUpTime              = 21
	ieFlowStartSysUpTime            = 22
	ieSourceIPv6Address             = 27
	ieDestinationIPv6Address        = 28
	ieOctetTotalCount               = 85
	iePacketTotalCount              = 86
	ieFlowStartSeconds              = 150
	ieFlowEndSeconds                = 151
	ieFlowStartMilliseconds         = 152
	ieFlowEndMilliseconds           = 153
	ieFlowStartMicroseconds         = 154
	ieFlowEndMicroseconds           = 155
	ieFlowStartNanoseconds          = 156
	ieFlowEndNanoseconds            = 157
	ieFlowStartDeltaMicroseconds    = 158
	ieFlowEndDeltaMicroseconds      = 159
	ieSystemInitTimeMilliseconds    = 160
	ieFlowDurationMilliseconds      = 161
	ieFlowDurationMicroseconds      = 162
	iePostNATSourceIPv4Address      = 225
	iePostNATDestinationIPv4Address = 226
	iePostNAPTSourceTransportPort   = 227
	iePostNAPTDestTransportPort     = 228
	iePostNATSourceIPv6Address      = 281
	iePostNATDestinationIPv6Address = 282
	ieIngressPhysicalInterface      = 252
	ieEgressPhysicalInterface       = 253
)

// MaxClockSkew is how far a Flow's end time may be from its export time before the
// record's times are distrusted and replaced by ones derived from the export time.
const MaxClockSkew = time.Hour

// Errors returned by Normalize.
var (
	// ErrOptionsRecord means the record carried metadata, not a Flow.
	ErrOptionsRecord = errors.New("flow: options record")
	// ErrNoAddresses means the record has no source or destination address.
	ErrNoAddresses = errors.New("flow: record has no source or destination address")
)

type domainKey struct {
	exporter netip.Addr
	od       uint32
}

// uptimeWrap is when a 32-bit millisecond uptime starts again from zero: 49.7 days.
const uptimeWrap = (1 << 32) * time.Millisecond

// Normalizer turns decoded records into Flows. It remembers systemInitTimeMilliseconds
// per Exporter and Observation Domain, because exporters may send it in Options records
// rather than in every data record. It is safe for concurrent use.
type Normalizer struct {
	mu      sync.Mutex
	sysInit map[domainKey]time.Time
}

// NewNormalizer returns a Normalizer.
func NewNormalizer() *Normalizer { return &Normalizer{sysInit: map[domainKey]time.Time{}} }

// maxDomains bounds remembered system init times.
const maxDomains = 4096

// Normalize converts a record from exporter, received at received, into a Flow.
// Options records update the Normalizer's state and return ErrOptionsRecord.
func (n *Normalizer) Normalize(exporter netip.Addr, rec ipfix.Record, received time.Time) (Flow, error) {
	key := domainKey{exporter, rec.ObservationDomain}
	initTime, hasInit := timeField(rec, ieSystemInitTimeMilliseconds)

	n.mu.Lock()
	switch {
	case !rec.SystemInitTime.IsZero():
		// NetFlow v9: the packet header gives the boot time.
		initTime, hasInit = rec.SystemInitTime, true
	case hasInit:
		if _, known := n.sysInit[key]; known || len(n.sysInit) < maxDomains {
			n.sysInit[key] = initTime
		}
	default:
		initTime, hasInit = n.sysInit[key]
	}
	n.mu.Unlock()

	if rec.Options() {
		return Flow{}, ErrOptionsRecord
	}

	f := Flow{
		Exporter:          exporter,
		ObservationDomain: rec.ObservationDomain,
		Received:          received,
		ExportTime:        rec.ExportTime,
		Fields:            EncodeFields(rec.Fields),
	}
	f.Src = firstAddr(rec, ieSourceIPv4Address, ieSourceIPv6Address)
	f.Dst = firstAddr(rec, ieDestinationIPv4Address, ieDestinationIPv6Address)
	if !f.Src.IsValid() || !f.Dst.IsValid() {
		return Flow{}, ErrNoAddresses
	}
	f.PostNATSrc = translated(firstAddr(rec, iePostNATSourceIPv4Address, iePostNATSourceIPv6Address))
	f.PostNATDst = translated(firstAddr(rec, iePostNATDestinationIPv4Address, iePostNATDestinationIPv6Address))
	f.SrcPort = uint16(uintField(rec, ieSourceTransportPort))
	f.DstPort = uint16(uintField(rec, ieDestinationTransportPort))
	f.PostNATSrcPort = uint16(uintField(rec, iePostNAPTSourceTransportPort))
	f.PostNATDstPort = uint16(uintField(rec, iePostNAPTDestTransportPort))
	f.Protocol = uint8(uintField(rec, ieProtocolIdentifier))
	f.Bytes = firstUint(rec, ieOctetDeltaCount, ieOctetTotalCount)
	f.Packets = firstUint(rec, iePacketDeltaCount, iePacketTotalCount)
	f.IngressInterface = uint32(firstUint(rec, ieIngressInterface, ieIngressPhysicalInterface))
	f.EgressInterface = uint32(firstUint(rec, ieEgressInterface, ieEgressPhysicalInterface))

	f.Start, f.End, f.TimeEstimated = flowTimes(rec, initTime, hasInit)
	if f.End.Sub(rec.ExportTime).Abs() > MaxClockSkew {
		// The record's times can't be right (wrong clock, or uptime-relative times decoded
		// against the wrong base). Keep the duration, anchor the end at the export time.
		dur := f.End.Sub(f.Start)
		if dur < 0 || dur > MaxClockSkew {
			dur = 0
		}
		f.End = rec.ExportTime
		f.Start = f.End.Add(-dur)
		f.TimeEstimated = true
	}
	return f, nil
}

// flowTimes derives absolute start and end times, trying the most precise elements
// first. estimated is true when they had to be derived from the export time.
func flowTimes(rec ipfix.Record, initTime time.Time, hasInit bool) (start, end time.Time, estimated bool) {
	start, hasStart := firstTime(rec, ieFlowStartNanoseconds, ieFlowStartMicroseconds, ieFlowStartMilliseconds, ieFlowStartSeconds)
	end, hasEnd := firstTime(rec, ieFlowEndNanoseconds, ieFlowEndMicroseconds, ieFlowEndMilliseconds, ieFlowEndSeconds)

	if !hasStart || !hasEnd {
		upStart, okStart := uintFieldOK(rec, ieFlowStartSysUpTime)
		upEnd, okEnd := uintFieldOK(rec, ieFlowEndSysUpTime)
		if okStart && okEnd {
			if upEnd < upStart { // the 32-bit millisecond uptime wrapped during the Flow
				upEnd += 1 << 32
			}
			if hasInit {
				start = initTime.Add(time.Duration(upStart) * time.Millisecond)
				end = initTime.Add(time.Duration(upEnd) * time.Millisecond)
				if end.Sub(rec.ExportTime) > uptimeWrap/2 {
					// Recorded before the 32-bit uptime wrapped, exported after it: the
					// boot time already counts the wrap, these times don't yet.
					start, end = start.Add(-uptimeWrap), end.Add(-uptimeWrap)
				}
			} else {
				end = rec.ExportTime
				start = end.Add(-time.Duration(upEnd-upStart) * time.Millisecond)
				estimated = true
			}
			hasStart, hasEnd = true, true
		}
	}
	if !hasStart || !hasEnd {
		if d, ok := uintFieldOK(rec, ieFlowStartDeltaMicroseconds); ok && !hasStart {
			start, hasStart = rec.ExportTime.Add(-time.Duration(d)*time.Microsecond), true
		}
		if d, ok := uintFieldOK(rec, ieFlowEndDeltaMicroseconds); ok && !hasEnd {
			end, hasEnd = rec.ExportTime.Add(-time.Duration(d)*time.Microsecond), true
		}
	}
	if hasStart != hasEnd {
		dur, ok := uintFieldOK(rec, ieFlowDurationMilliseconds)
		unit := time.Millisecond
		if !ok {
			dur, ok = uintFieldOK(rec, ieFlowDurationMicroseconds)
			unit = time.Microsecond
		}
		switch {
		case ok && hasStart:
			end, hasEnd = start.Add(time.Duration(dur)*unit), true
		case ok && hasEnd:
			start, hasStart = end.Add(-time.Duration(dur)*unit), true
		case hasStart:
			end, hasEnd = start, true
		default:
			start, hasStart = end, true
		}
	}
	if !hasStart && !hasEnd {
		return rec.ExportTime, rec.ExportTime, true
	}
	if start.After(end) {
		start = end
	}
	return start.UTC(), end.UTC(), estimated
}

// translated treats an unspecified post-NAT address (0.0.0.0 or ::) as none: exporters
// whose Template carries the NAT fields send it for Flows that weren't translated.
func translated(a netip.Addr) netip.Addr {
	if a.IsUnspecified() {
		return netip.Addr{}
	}
	return a
}

func firstAddr(rec ipfix.Record, ids ...uint16) netip.Addr {
	for _, id := range ids {
		if f, ok := rec.Get(id); ok {
			if v, err := f.Value(); err == nil && v.Kind == ipfix.KindAddr {
				return v.Addr.Unmap()
			}
		}
	}
	return netip.Addr{}
}

func uintFieldOK(rec ipfix.Record, id uint16) (uint64, bool) {
	f, ok := rec.Get(id)
	if !ok {
		return 0, false
	}
	v, err := f.Value()
	if err != nil || v.Kind != ipfix.KindUnsigned {
		return 0, false
	}
	return v.Uint, true
}

func uintField(rec ipfix.Record, id uint16) uint64 {
	v, _ := uintFieldOK(rec, id)
	return v
}

func firstUint(rec ipfix.Record, ids ...uint16) uint64 {
	for _, id := range ids {
		if v, ok := uintFieldOK(rec, id); ok {
			return v
		}
	}
	return 0
}

func timeField(rec ipfix.Record, id uint16) (time.Time, bool) {
	f, ok := rec.Get(id)
	if !ok {
		return time.Time{}, false
	}
	v, err := f.Value()
	if err != nil || v.Kind != ipfix.KindTime {
		return time.Time{}, false
	}
	return v.Time, true
}

func firstTime(rec ipfix.Record, ids ...uint16) (time.Time, bool) {
	for _, id := range ids {
		if t, ok := timeField(rec, id); ok {
			return t, true
		}
	}
	return time.Time{}, false
}

// EncodeFields stores fields losslessly: a count, then per field the enterprise
// number, element ID, length and raw bytes, all as uvarints except the bytes.
func EncodeFields(fields []ipfix.Field) []byte {
	n := binary.MaxVarintLen64
	for _, f := range fields {
		n += 3*binary.MaxVarintLen32 + len(f.Raw)
	}
	b := make([]byte, 0, n)
	b = binary.AppendUvarint(b, uint64(len(fields)))
	for _, f := range fields {
		b = binary.AppendUvarint(b, uint64(f.Spec.Enterprise))
		b = binary.AppendUvarint(b, uint64(f.Spec.ID))
		b = binary.AppendUvarint(b, uint64(len(f.Raw)))
		b = append(b, f.Raw...)
	}
	return b
}

// DecodeFields reverses EncodeFields. Variable-length fields come back with their
// actual length; whether they were variable-length on the wire is not kept.
func DecodeFields(b []byte) ([]ipfix.Field, error) {
	errCorrupt := errors.New("flow: corrupt field encoding")
	read := func() (uint64, error) {
		v, n := binary.Uvarint(b)
		if n <= 0 {
			return 0, errCorrupt
		}
		b = b[n:]
		return v, nil
	}
	count, err := read()
	if err != nil || count > uint64(len(b)) {
		return nil, errCorrupt
	}
	fields := make([]ipfix.Field, 0, count)
	for i := uint64(0); i < count; i++ {
		pen, err1 := read()
		id, err2 := read()
		length, err3 := read()
		if err1 != nil || err2 != nil || err3 != nil || id > 0x7FFF || pen > 0xFFFFFFFF || length > uint64(len(b)) {
			return nil, errCorrupt
		}
		fields = append(fields, ipfix.Field{
			Spec: ipfix.FieldSpec{Enterprise: uint32(pen), ID: uint16(id), Length: uint16(length)},
			Raw:  b[:length:length],
		})
		b = b[length:]
	}
	return fields, nil
}
