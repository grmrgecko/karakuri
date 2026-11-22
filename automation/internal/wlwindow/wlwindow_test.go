//go:build !windows && !darwin

package wlwindow

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestToplevelHandleRequestOpcodes locks the zwlr_foreign_toplevel_handle_v1
// request numbers to the order wlr-foreign-toplevel-management-unstable-v1
// declares them in: set_maximized 0, unset_maximized 1, set_minimized 2,
// unset_minimized 3, activate 4, close 5, set_rectangle 6, destroy 7,
// set_fullscreen 8, unset_fullscreen 9. Restore once sent request 7 to leave
// fullscreen, which destroyed the handle and left the window fullscreen.
func TestToplevelHandleRequestOpcodes(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	require.NoError(t, err, "the test needs a socket pair to stand in for the compositor")
	defer unix.Close(pair[1])
	c := &conn{fd: pair[0], nextID: 2}
	defer c.close()

	const handle = uint32(0xff000001)
	tests := []struct {
		name   string
		opcode int
		want   uint32
	}{
		{"set_maximized", handleRequestSetMaximized, 0},
		{"unset_maximized", handleRequestUnsetMaximized, 1},
		{"set_minimized", handleRequestSetMinimized, 2},
		{"unset_minimized", handleRequestUnsetMinimized, 3},
		{"activate", handleRequestActivate, 4},
		{"close", handleRequestClose, 5},
		{"unset_fullscreen", handleRequestUnsetFullscreen, 9},
	}
	for _, tc := range tests {
		require.NoError(t, c.request(handle, tc.opcode), "sending %s must succeed", tc.name)

		wire := make([]byte, 64)
		n, err := unix.Read(pair[1], wire)
		require.NoError(t, err, "reading back %s must succeed", tc.name)
		require.Equal(t, 8, n, "%s without arguments is the 8-byte header alone", tc.name)

		assert.Equal(t, handle, binary.LittleEndian.Uint32(wire), "%s must address the toplevel handle", tc.name)
		assert.Equal(t, tc.want, binary.LittleEndian.Uint32(wire[4:])&0xffff,
			"%s must use the opcode the protocol declares", tc.name)
	}
}
