//go:build !linux

package collector

import "net"

// tuneSocket asks for a receive buffer; drop counting is Linux-only.
func tuneSocket(c *net.UDPConn, want int) (granted int, dropsCounted bool) {
	_ = c.SetReadBuffer(want)
	return 0, false
}

var oobSize = 0

func socketDrops([]byte) (uint32, bool) { return 0, false }
