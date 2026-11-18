package collector

import (
	"encoding/binary"
	"net"

	"golang.org/x/sys/unix"
)

// tuneSocket asks for a receive buffer of want bytes and turns on kernel drop counting
// (SO_RXQ_OVFL). SO_RCVBUFFORCE can exceed net.core.rmem_max but needs CAP_NET_ADMIN;
// without it the kernel caps SO_RCVBUF at rmem_max. It returns the buffer granted.
func tuneSocket(c *net.UDPConn, want int) (granted int, dropsCounted bool) {
	rc, err := c.SyscallConn()
	if err != nil {
		return 0, false
	}
	_ = rc.Control(func(fd uintptr) {
		if unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUFFORCE, want) != nil {
			_ = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF, want)
		}
		// Linux reports twice what it grants: the other half is for its bookkeeping.
		if v, err := unix.GetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RCVBUF); err == nil {
			granted = v / 2
		}
		dropsCounted = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_RXQ_OVFL, 1) == nil
	})
	return granted, dropsCounted
}

// oobSize fits the SO_RXQ_OVFL control message.
var oobSize = unix.CmsgSpace(4)

// socketDrops reads the kernel's count of datagrams dropped on the socket so far from a
// received message's control data. The kernel only sends it once there are drops.
func socketDrops(oob []byte) (uint32, bool) {
	msgs, err := unix.ParseSocketControlMessage(oob)
	if err != nil {
		return 0, false
	}
	for _, m := range msgs {
		if m.Header.Level == unix.SOL_SOCKET && m.Header.Type == unix.SO_RXQ_OVFL && len(m.Data) >= 4 {
			return binary.NativeEndian.Uint32(m.Data), true
		}
	}
	return 0, false
}
