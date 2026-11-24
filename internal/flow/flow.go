// Package flow defines a Flow after normalisation: absolute times, typed addresses and
// counters, its classification, and every original field kept losslessly.
package flow

import (
	"net/netip"
	"time"
)

// Direction is the way a Flow crosses the boundary between the local side and the
// internet (see GLOSSARY.md). The numeric values are stored in the Flow Store.
type Direction uint8

// Directions.
const (
	// DirectionTransit is a Flow with no local end: neither address is in a Local
	// Network or belongs to an Exporter Host.
	DirectionTransit Direction = 0
	DirectionInbound Direction = 1
	// DirectionOutbound is a Flow from a Local Host to a Remote IP.
	DirectionOutbound Direction = 2
	// DirectionInternal is a Flow between two Local Hosts.
	DirectionInternal Direction = 3
	// DirectionUncounted is a Flow an Internal Exporter saw that isn't between two hosts
	// in one of its subnets: an Edge Exporter counts that traffic, so this copy is kept
	// raw but left out of every total.
	DirectionUncounted Direction = 4
)

func (d Direction) String() string {
	switch d {
	case DirectionInbound:
		return "inbound"
	case DirectionOutbound:
		return "outbound"
	case DirectionInternal:
		return "internal"
	case DirectionUncounted:
		return "uncounted"
	}
	return "transit"
}

// Flow is one normalised flow record.
type Flow struct {
	Exporter          netip.Addr
	ObservationDomain uint32
	// Received is when the collector received the datagram.
	Received time.Time
	// ExportTime is the export time from the message header.
	ExportTime time.Time
	Start      time.Time
	End        time.Time
	// TimeEstimated is set when Start/End couldn't be taken from the record and were
	// derived from the export time instead.
	TimeEstimated bool

	Src, Dst         netip.Addr
	SrcPort, DstPort uint16
	Protocol         uint8
	// PostNATSrc/PostNATDst are the addresses after NAT, if the exporter sends them.
	PostNATSrc, PostNATDst         netip.Addr
	PostNATSrcPort, PostNATDstPort uint16

	// Bytes and Packets are estimates of the real traffic when SamplingRate is above 1:
	// the counts the Exporter reported for its sample, multiplied by the rate.
	Bytes   uint64
	Packets uint64
	// SamplingRate is N when the Exporter counted 1 in N packets. From the Normalizer it
	// is what the Exporter announced: 0 for nothing, 1 for "every packet". After
	// ApplySampling it is 0 for counted in full, or the rate the counts were scaled by.
	SamplingRate uint32
	// IngressInterface and EgressInterface are interface numbers, 0 when absent.
	IngressInterface uint32
	EgressInterface  uint32

	// Classification, set by the classifier.
	Direction Direction
	// Local is the Local Host; invalid for transit Flows.
	Local netip.Addr
	// LocalIsExporterHost is set when Local is one of the Exporter's own addresses.
	LocalIsExporterHost bool
	// Peer is the other end: a Remote IP, or another Local Host for Internal Flows.
	Peer                netip.Addr
	LocalPort, PeerPort uint16

	// PeerCountry (ISO alpha-2) and PeerASN are the Remote IP's GeoIP enrichment, fixed
	// when the Flow is stored; "" and 0 when unknown, and for Internal and transit Flows.
	PeerCountry string
	PeerASN     uint32
	// PeerASNOrg is the ASN Owner's name at that time. The Flow Store keeps it per ASN.
	PeerASNOrg string

	// LocalMAC is the Local Host's Device, and PeerMAC the Peer's for Internal Flows:
	// the MAC address of that end, fixed when the Flow is stored (see package devices).
	// Zero when unknown.
	LocalMAC, PeerMAC MAC

	// Fields holds every original field, encoded with EncodeFields.
	Fields []byte
}
