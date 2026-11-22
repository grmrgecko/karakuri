//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package socket

import (
	"golang.org/x/sys/unix"
	"net"
)

func setBroadcast(c *net.UDPConn, enabled bool) error {
	raw, e := c.SyscallConn()
	if e != nil {
		return e
	}
	value := 0
	if enabled {
		value = 1
	}
	var sockErr error
	e = raw.Control(func(fd uintptr) { sockErr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_BROADCAST, value) })
	if e != nil {
		return e
	}
	return sockErr
}

// datagramTooLarge reports whether a read failed only because the datagram
// exceeded the buffer. Unix truncates such datagrams without an error.
func datagramTooLarge(error) bool { return false }
