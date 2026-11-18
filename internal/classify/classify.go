// Package classify decides a Flow's Direction, Local Host and Peer by address: Local
// Networks plus each Exporter's own addresses (Exporter Hosts). Classification by
// interface (ADR 0003) builds on this and replaces it for Exporters with WAN Interfaces.
package classify

import (
	"fmt"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jkop-xyz/flowmon/internal/flow"
)

// DefaultLocalNetworks are private (RFC 1918), ULA and link-local ranges. CGNAT
// (100.64.0.0/10) is deliberately absent: it is also used by overlay networks and by
// ISPs for WAN addresses (ADR 0003).
func DefaultLocalNetworks() []netip.Prefix {
	return []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("fe80::/10"),
	}
}

// ParseLocalNetworks parses a comma-separated list of CIDR prefixes.
func ParseLocalNetworks(s string) ([]netip.Prefix, error) {
	var out []netip.Prefix
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		p, err := netip.ParsePrefix(part)
		if err != nil {
			return nil, fmt.Errorf("invalid prefix %q: %w", part, err)
		}
		out = append(out, p.Masked())
	}
	return out, nil
}

// ExporterHostTTL is how long a learned Exporter Host address counts as local after it
// was last seen. A DHCP WAN address changes; the old one must stop being local.
const ExporterHostTTL = 24 * time.Hour

// maxLearnedPerExporter bounds learned addresses per Exporter.
const maxLearnedPerExporter = 16

// InterfaceRole is the role of an interface a Flow crossed, as the classifier sees it.
type InterfaceRole uint8

// Interface roles. IfSelf is interface number 0: the Exporter itself sent or received
// the Flow. IfUnknown means no role is set, and the Flow is classified by address.
const (
	IfUnknown InterfaceRole = iota
	IfSelf
	IfWAN
	IfLAN
	IfTunnel
)

// Interfaces tells the classifier which Exporters classify by interface and what each
// interface's role was at a given time.
type Interfaces interface {
	ByInterface(exporter netip.Addr) bool
	Role(exporter netip.Addr, ifIndex uint32, at time.Time) InterfaceRole
}

// Classifier is safe for concurrent use.
type Classifier struct {
	local  atomic.Pointer[[]netip.Prefix]
	ifaces atomic.Pointer[Interfaces]

	mu      sync.Mutex
	learned map[netip.Addr]map[netip.Addr]time.Time // Exporter → address → last seen
}

// New returns a Classifier for the given Local Networks.
func New(local []netip.Prefix) *Classifier {
	c := &Classifier{learned: map[netip.Addr]map[netip.Addr]time.Time{}}
	c.SetLocalNetworks(local)
	return c
}

// SetLocalNetworks replaces the Local Networks; Flows classified from now on use them.
func (c *Classifier) SetLocalNetworks(local []netip.Prefix) {
	local = append([]netip.Prefix(nil), local...)
	c.local.Store(&local)
}

// SetInterfaces enables classification by interface for the Exporters that ask for it.
func (c *Classifier) SetInterfaces(i Interfaces) { c.ifaces.Store(&i) }

// LocalNetworks returns the configured Local Networks.
func (c *Classifier) LocalNetworks() []netip.Prefix {
	return append([]netip.Prefix(nil), *c.local.Load()...)
}

// InLocalNetwork reports whether an address is in a Local Network.
func (c *Classifier) InLocalNetwork(a netip.Addr) bool { return c.inLocalNetwork(a) }

func (c *Classifier) inLocalNetwork(a netip.Addr) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap()
	for _, p := range *c.local.Load() {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// localScope is an address that never leaves the local side even though no one owns
// it: multicast and the limited broadcast address.
func localScope(a netip.Addr) bool {
	a = a.Unmap()
	return a.IsMulticast() || a == netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

func (c *Classifier) isExporterHost(exporter, a netip.Addr, now time.Time) bool {
	if !a.IsValid() {
		return false
	}
	a = a.Unmap()
	if a == exporter.Unmap() {
		return true
	}
	seen, ok := c.learned[exporter][a]
	return ok && now.Sub(seen) <= ExporterHostTTL
}

// learn records post-NAT source addresses of NATed outbound Flows: the router's own
// (usually WAN) addresses.
func (c *Classifier) learn(f *flow.Flow, now time.Time) {
	if !c.inLocalNetwork(f.Src) || !f.PostNATSrc.IsValid() || f.PostNATSrc == f.Src || c.inLocalNetwork(f.PostNATSrc) {
		return
	}
	m := c.learned[f.Exporter]
	if m == nil {
		m = map[netip.Addr]time.Time{}
		c.learned[f.Exporter] = m
	}
	addr := f.PostNATSrc.Unmap()
	if _, ok := m[addr]; !ok && len(m) >= maxLearnedPerExporter {
		var oldest netip.Addr
		var oldestSeen time.Time
		for a, seen := range m {
			if oldestSeen.IsZero() || seen.Before(oldestSeen) {
				oldest, oldestSeen = a, seen
			}
		}
		delete(m, oldest)
	}
	m[addr] = now
}

// end is one side of a Flow as the classifier sees it.
type end struct {
	addr         netip.Addr
	port         uint16
	local        bool
	exporterHost bool
}

// pick chooses the address that identifies one end. Addresses in a Local Network win
// over Exporter Host addresses, so traffic NATed to a LAN host is attributed to the
// host rather than the router. preferred is tried first among equals.
func (c *Classifier) pick(exporter netip.Addr, now time.Time, candidates ...end) end {
	for _, e := range candidates {
		if c.inLocalNetwork(e.addr) || (e.addr.IsValid() && localScope(e.addr)) {
			e.local = true
			return e
		}
	}
	for _, e := range candidates {
		if c.isExporterHost(exporter, e.addr, now) {
			e.local, e.exporterHost = true, true
			return e
		}
	}
	return candidates[0]
}

// Classify sets f's Direction, Local, Peer and ports. now is used to learn and expire
// Exporter Host addresses.
func (c *Classifier) Classify(f *flow.Flow, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.learn(f, now)

	postSrcPort, postDstPort := f.PostNATSrcPort, f.PostNATDstPort
	if postSrcPort == 0 {
		postSrcPort = f.SrcPort
	}
	if postDstPort == 0 {
		postDstPort = f.DstPort
	}
	// Source: the pre-NAT address is the host that sent it. Destination: the post-NAT
	// address is the host it was delivered to (port forwards, download replies).
	src := c.pick(f.Exporter, now, end{addr: f.Src, port: f.SrcPort}, end{addr: f.PostNATSrc, port: postSrcPort})
	dst := c.pick(f.Exporter, now, end{addr: f.PostNATDst, port: postDstPort}, end{addr: f.Dst, port: f.DstPort})
	if !dst.addr.IsValid() {
		dst = end{addr: f.Dst, port: f.DstPort}
	}

	f.Local, f.Peer, f.LocalPort, f.PeerPort, f.LocalIsExporterHost = netip.Addr{}, netip.Addr{}, 0, 0, false
	if c.classifyByInterface(f, src, dst) {
		c.stableExporterHost(f)
		return
	}
	switch {
	case src.local && dst.local:
		f.Direction = flow.DirectionInternal
		f.Local, f.LocalPort, f.LocalIsExporterHost = src.addr, src.port, src.exporterHost
		f.Peer, f.PeerPort = dst.addr, dst.port
	case src.local:
		f.Direction = flow.DirectionOutbound
		f.Local, f.LocalPort, f.LocalIsExporterHost = src.addr, src.port, src.exporterHost
		f.Peer, f.PeerPort = f.Dst, f.DstPort
	case dst.local:
		f.Direction = flow.DirectionInbound
		f.Local, f.LocalPort, f.LocalIsExporterHost = dst.addr, dst.port, dst.exporterHost
		f.Peer, f.PeerPort = f.Src, f.SrcPort
	default:
		f.Direction = flow.DirectionTransit
	}
	f.Local, f.Peer = f.Local.Unmap(), f.Peer.Unmap()
	c.stableExporterHost(f)
}

// stableExporterHost attributes the Exporter's own traffic to the Exporter's address
// rather than its (DHCP) WAN address, so its history doesn't split when that changes.
func (c *Classifier) stableExporterHost(f *flow.Flow) {
	if f.LocalIsExporterHost {
		f.Local = f.Exporter.Unmap()
	}
}

// classifyByInterface applies Interface Roles (ADR 0003). It reports false when the
// Exporter classifies by address or a crossed interface has no role, so the caller
// falls back to Local Networks.
func (c *Classifier) classifyByInterface(f *flow.Flow, src, dst end) bool {
	p := c.ifaces.Load()
	if p == nil || !(*p).ByInterface(f.Exporter) || (f.IngressInterface == 0 && f.EgressInterface == 0) {
		return false
	}
	in := (*p).Role(f.Exporter, f.IngressInterface, f.End)
	out := (*p).Role(f.Exporter, f.EgressInterface, f.End)
	if in == IfUnknown || out == IfUnknown {
		return false
	}
	srcEnd := end{addr: f.Src, port: f.SrcPort}
	if src.local && !src.exporterHost {
		srcEnd = src
	}
	dstEnd := end{addr: f.Dst, port: f.DstPort}
	if f.PostNATDst.IsValid() {
		dstEnd = end{addr: f.PostNATDst, port: dst.port}
	}
	switch {
	case in == IfWAN && out == IfWAN:
		f.Direction = flow.DirectionTransit
	case in == IfWAN:
		f.Direction = flow.DirectionInbound
		f.Local, f.LocalPort, f.LocalIsExporterHost = dstEnd.addr, dstEnd.port, out == IfSelf
		f.Peer, f.PeerPort = f.Src, f.SrcPort
	case out == IfWAN:
		f.Direction = flow.DirectionOutbound
		f.Local, f.LocalPort, f.LocalIsExporterHost = srcEnd.addr, srcEnd.port, in == IfSelf
		f.Peer, f.PeerPort = f.Dst, f.DstPort
	default:
		f.Direction = flow.DirectionInternal
		f.Local, f.LocalPort, f.LocalIsExporterHost = srcEnd.addr, srcEnd.port, in == IfSelf
		f.Peer, f.PeerPort = dstEnd.addr, dstEnd.port
	}
	f.Local, f.Peer = f.Local.Unmap(), f.Peer.Unmap()
	return true
}

// ExporterHosts returns the learned addresses of an Exporter that still count as local.
func (c *Classifier) ExporterHosts(exporter netip.Addr, now time.Time) []netip.Addr {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []netip.Addr
	for a, seen := range c.learned[exporter] {
		if now.Sub(seen) <= ExporterHostTTL {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Less(out[j]) })
	return out
}
