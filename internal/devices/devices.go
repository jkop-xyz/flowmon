// Package devices finds each Flow's Devices (ADR 0005): the MAC address of its Local
// Host, and of its Peer for Internal Flows, taken from the MAC fields of the Flow's
// record.
//
// A host's MAC is reliable where it sent the record's packets: the source MAC. Where it
// received them, exporters differ: RouterOS reports its own MAC as the destination even
// after routing. So an address that received a Flow gets the MAC it was last seen
// sending from, within AddressTTL, unless the record names a usable one.
//
// A MAC that is a router hop is never taken for the Device behind it: the Exporter's own
// MACs, which every record names as the frames' next hop, and any MAC seen for more than
// HopAddresses IPv4 addresses within HopWindow, such as another router behind the
// Exporter.
package devices

import (
	"log/slog"
	"net/netip"
	"sync"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// Hop detection: a MAC seen for more than HopAddresses IPv4 addresses within HopWindow
// is a router's. A device keeps one IPv4 address for hours (a DHCP lease).
const (
	HopAddresses = 4
	HopWindow    = time.Hour
)

// AddressTTL is how long an address keeps the MAC it was last seen sending from, for
// Flows it receives: about a DHCP lease.
const AddressTTL = 24 * time.Hour

// Router is a MAC known to be a router hop.
type Router struct {
	MAC      flow.MAC
	Exporter netip.Addr
	// Reason is "exporter" for the Exporter's own MACs, "many addresses" for the rest.
	Reason string
	Since  time.Time
}

// Options configure an Assigner.
type Options struct {
	// Known are the router MACs learned before, from the Flow Store.
	Known []Router
	// Recent are addresses Devices sent from lately (the Flow Store's device addresses),
	// so after a restart a reply to one still gets its Device before it sends again.
	Recent []Sighting
	// Learned is called once for each router MAC learned; the Flow Store keeps them so
	// views can leave out Flows assigned to it before it was known.
	Learned func(Router)
	Logger  *slog.Logger
}

// Assigner sets Flows' LocalMAC and PeerMAC. It is safe for concurrent use.
type Assigner struct {
	learned func(Router)
	logger  *slog.Logger

	mu      sync.Mutex
	routers map[flow.MAC]bool
	// seen holds, per MAC, when each IPv4 address was last seen with it.
	seen map[flow.MAC]map[netip.Addr]time.Time
	// sent holds, per address, the MAC it last sent from.
	sent map[netip.Addr]sighting
}

// Sighting is a MAC an address was seen with, and when.
type Sighting struct {
	Address netip.Addr
	MAC     flow.MAC
	At      time.Time
}

type sighting struct {
	mac flow.MAC
	at  time.Time
}

// New returns an Assigner.
func New(opts Options) *Assigner {
	a := &Assigner{learned: opts.Learned, logger: opts.Logger, routers: map[flow.MAC]bool{},
		seen: map[flow.MAC]map[netip.Addr]time.Time{}, sent: map[netip.Addr]sighting{}}
	if a.logger == nil {
		a.logger = slog.Default()
	}
	for _, r := range opts.Known {
		a.routers[r.MAC] = true
	}
	for _, s := range opts.Recent {
		if prev, ok := a.sent[s.Address]; !ok || s.At.After(prev.at) {
			a.sent[s.Address.Unmap()] = sighting{s.MAC, s.At}
		}
	}
	return a
}

// Assign sets f.LocalMAC and, for Internal Flows, f.PeerMAC. f must be classified.
func (a *Assigner) Assign(f *flow.Flow) {
	f.LocalMAC, f.PeerMAC = flow.MAC{}, flow.MAC{}
	macs, ok := flow.MACsOf(f.Fields)
	if !ok {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, r := range macs.Router() {
		a.learnLocked(Router{MAC: r, Exporter: f.Exporter, Reason: "exporter", Since: f.End})
	}
	if f.Local.IsValid() && !f.LocalIsExporterHost {
		f.LocalMAC = a.deviceLocked(macs, f.Local, f)
	}
	if f.Direction == flow.DirectionInternal && f.Peer.IsValid() {
		f.PeerMAC = a.deviceLocked(macs, f.Peer, f)
	}
}

// deviceLocked returns addr's Device in the record, or zero.
func (a *Assigner) deviceLocked(macs flow.RecordMACs, addr netip.Addr, f *flow.Flow) flow.MAC {
	addr = addr.Unmap()
	m := macs.Of(addr)
	if !a.usableLocked(m) {
		if macs.IsSource(addr) {
			return flow.MAC{}
		}
		// It received the packets: the MAC it last sent from.
		s, ok := a.sent[addr]
		if !ok || f.End.Sub(s.at) > AddressTTL || !a.usableLocked(s.mac) {
			return flow.MAC{}
		}
		return s.mac
	}
	if macs.IsSource(addr) {
		if s := a.sent[addr]; !s.at.After(f.End) {
			a.sent[addr] = sighting{m, f.End}
		}
	}
	if addr.Is4() {
		addrs := a.seen[m]
		if addrs == nil {
			addrs = map[netip.Addr]time.Time{}
			a.seen[m] = addrs
		}
		addrs[addr] = f.End
		for x, t := range addrs {
			if f.End.Sub(t) > HopWindow {
				delete(addrs, x)
			}
		}
		if len(addrs) > HopAddresses {
			a.learnLocked(Router{MAC: m, Exporter: f.Exporter, Reason: "many addresses", Since: f.End})
			delete(a.seen, m)
			return flow.MAC{}
		}
	}
	return m
}

func (a *Assigner) usableLocked(m flow.MAC) bool { return !m.IsZero() && !m.Group() && !a.routers[m] }

func (a *Assigner) learnLocked(r Router) {
	if a.routers[r.MAC] {
		return
	}
	a.routers[r.MAC] = true
	a.logger.Info("router MAC learned; it is never taken for a Device", "mac", r.MAC.String(), "exporter", r.Exporter.String(), "reason", r.Reason)
	if a.learned != nil {
		a.learned(r)
	}
}

// Observe records a neighbor table's mapping of addr to mac (ADR 0005): Flows to addr
// get mac as their Device, as if addr had sent from it, until AddressTTL passes without
// either. It reports whether the mapping is a Device's: false for a router hop's MAC.
func (a *Assigner) Observe(addr netip.Addr, mac flow.MAC, at time.Time) bool {
	addr = addr.Unmap()
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.usableLocked(mac) {
		return false
	}
	if s, ok := a.sent[addr]; !ok || !s.at.After(at) {
		a.sent[addr] = sighting{mac, at}
	}
	return true
}
