package auth

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Proxies describes the reverse proxies whose X-Forwarded-* headers are trusted.
type Proxies struct {
	Trusted []netip.Prefix
}

func (p Proxies) trusted(a netip.Addr) bool {
	a = a.Unmap()
	for _, pfx := range p.Trusted {
		if pfx.Contains(a) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) netip.Addr {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	a, _ := netip.ParseAddr(host)
	return a.Unmap()
}

// ClientIP is the address of the browser. When the request came from a trusted proxy,
// X-Forwarded-For is walked from the right, skipping trusted proxies, so a client can't
// spoof its address by sending the header itself.
func (p Proxies) ClientIP(r *http.Request) netip.Addr {
	addr := remoteAddr(r)
	if !p.trusted(addr) {
		return addr
	}
	hops := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(hops) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(strings.TrimSpace(hops[i]))
		if err != nil {
			break
		}
		a = a.Unmap()
		if !p.trusted(a) {
			return a
		}
		addr = a
	}
	return addr
}

// IsHTTPS reports whether the browser reached Flowmon over HTTPS: directly, or through a
// trusted proxy that says so in X-Forwarded-Proto.
func (p Proxies) IsHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	if p.trusted(remoteAddr(r)) {
		proto := r.Header.Get("X-Forwarded-Proto")
		if i := strings.IndexByte(proto, ','); i >= 0 {
			proto = proto[:i]
		}
		return strings.EqualFold(strings.TrimSpace(proto), "https")
	}
	return false
}
