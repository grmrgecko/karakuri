package socket

import (
	"errors"
	"net"

	"golang.org/x/sys/windows"
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
	e = raw.Control(func(fd uintptr) {
		sockErr = windows.SetsockoptInt(windows.Handle(fd), windows.SOL_SOCKET, windows.SO_BROADCAST, value)
	})
	if e != nil {
		return e
	}
	return sockErr
}

// datagramTooLarge reports whether a read failed only because the datagram
// exceeded the buffer. Windows reports WSAEMSGSIZE instead of truncating.
func datagramTooLarge(e error) bool { return errors.Is(e, windows.WSAEMSGSIZE) }
