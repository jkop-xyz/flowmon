package flow

import (
	"net/netip"
	"time"

	"github.com/jkop-xyz/flowmon/internal/ipfix"
)

// A biflow record (RFC 5103) describes both directions of a connection: its usual fields
// are the forward direction, from source to destination, and reverse fields (enterprise
// 29305) count the packets going back. Flowmon stores it as two Flows, one per
// direction, so a biflow Exporter's traffic is classified and rolled up exactly like a
// uniflow Exporter's, which sends the two directions as separate records anyway.

// reverseField returns a record's reverse field for an IANA element.
func reverseField(rec ipfix.Record, id uint16) (ipfix.Field, bool) {
	for _, f := range rec.Fields {
		if f.Spec.Enterprise == ipfix.EnterpriseReverse && f.Spec.ID == id {
			return f, true
		}
	}
	return ipfix.Field{}, false
}

func reverseUint(rec ipfix.Record, ids ...uint16) (uint64, bool) {
	for _, id := range ids {
		if f, ok := reverseField(rec, id); ok {
			if v, err := f.Value(); err == nil && v.Kind == ipfix.KindUnsigned {
				return v.Uint, true
			}
		}
	}
	return 0, false
}

func reverseTime(rec ipfix.Record, ids ...uint16) (time.Time, bool) {
	for _, id := range ids {
		if f, ok := reverseField(rec, id); ok {
			if v, err := f.Value(); err == nil && v.Kind == ipfix.KindTime {
				return v.Time.UTC(), true
			}
		}
	}
	return time.Time{}, false
}

// IsBiflow reports whether a record carries reverse counters (RFC 5103).
func IsBiflow(rec ipfix.Record) bool {
	_, bytes := reverseUint(rec, ieOctetDeltaCount, ieOctetTotalCount)
	_, packets := reverseUint(rec, iePacketDeltaCount, iePacketTotalCount)
	return bytes || packets
}

// Reverse returns the reverse direction of a biflow record whose forward direction
// Normalize made into f: from the destination back to the source, with the reverse
// counters. ok is false for a uniflow record, and for a biflow whose reverse direction
// carried nothing (no answer came back).
//
// The reverse packets pass the same NAT the other way: they arrive addressed to what
// the forward packets were translated to, and leave addressed to where those came from.
// So the reverse direction's pre-NAT ends are the forward post-NAT ends swapped, and its
// post-NAT ends the forward pre-NAT ends swapped.
func Reverse(f Flow, rec ipfix.Record) (Flow, bool) {
	bytes, _ := reverseUint(rec, ieOctetDeltaCount, ieOctetTotalCount)
	packets, _ := reverseUint(rec, iePacketDeltaCount, iePacketTotalCount)
	if bytes == 0 && packets == 0 {
		return Flow{}, false
	}
	r := f
	r.Bytes, r.Packets = bytes, packets

	preSrc, preSrcPort := f.Dst, f.DstPort
	if f.PostNATDst.IsValid() {
		preSrc = f.PostNATDst
	}
	if f.PostNATDstPort != 0 {
		preSrcPort = f.PostNATDstPort
	}
	preDst, preDstPort := f.Src, f.SrcPort
	if f.PostNATSrc.IsValid() {
		preDst = f.PostNATSrc
	}
	if f.PostNATSrcPort != 0 {
		preDstPort = f.PostNATSrcPort
	}
	r.Src, r.SrcPort, r.Dst, r.DstPort = preSrc, preSrcPort, preDst, preDstPort
	r.PostNATSrc, r.PostNATSrcPort, r.PostNATDst, r.PostNATDstPort = netip.Addr{}, 0, netip.Addr{}, 0
	if f.PostNATDst.IsValid() || f.PostNATDstPort != 0 {
		r.PostNATSrc, r.PostNATSrcPort = f.Dst, f.DstPort
	}
	if f.PostNATSrc.IsValid() || f.PostNATSrcPort != 0 {
		r.PostNATDst, r.PostNATDstPort = f.Src, f.SrcPort
	}
	r.IngressInterface, r.EgressInterface = f.EgressInterface, f.IngressInterface

	// Reverse times, when the Exporter sends them; otherwise the connection's.
	start, okStart := reverseTime(rec, ieFlowStartNanoseconds, ieFlowStartMicroseconds, ieFlowStartMilliseconds, ieFlowStartSeconds)
	end, okEnd := reverseTime(rec, ieFlowEndNanoseconds, ieFlowEndMicroseconds, ieFlowEndMilliseconds, ieFlowEndSeconds)
	if okStart && okEnd && !end.Before(start) && end.Sub(rec.ExportTime).Abs() <= MaxClockSkew {
		r.Start, r.End = start, end
	}
	return r, true
}
