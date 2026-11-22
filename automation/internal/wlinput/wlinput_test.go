//go:build !windows && !darwin

package wlinput

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestVirtualPointerRequestOpcodes locks the zwlr_virtual_pointer_v1 request
// numbers to the order wlr-virtual-pointer-unstable-v1 declares them in:
// motion 0, motion_absolute 1, button 2, axis 3, frame 4, axis_source 5,
// axis_stop 6, axis_discrete 7, destroy 8. Scroll once sent its
// (time, axis, value, steps) body as request 5, which is axis_source, so the
// compositor never scrolled.
func TestVirtualPointerRequestOpcodes(t *testing.T) {
	pair, err := unix.Socketpair(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	require.NoError(t, err, "the test needs a socket pair to stand in for the compositor")
	defer unix.Close(pair[1])
	c := &conn{fd: pair[0], nextID: 2}
	defer c.close()

	const vp = uint32(7)
	tests := []struct {
		name   string
		opcode int
		args   []any
		want   uint32
	}{
		{"motion", vpRequestMotion, []any{uint32(1), int32(256), int32(256)}, 0},
		{"motion_absolute", vpRequestMotionAbsolute, []any{uint32(1), uint32(10), uint32(10), uint32(100), uint32(100)}, 1},
		{"button", vpRequestButton, []any{uint32(1), uint32(btnLeft), uint32(statePressed)}, 2},
		{"frame", vpRequestFrame, nil, 4},
		// One notch down: 15 units per step in 24.8 fixed point, then the step count.
		{"axis_discrete", vpRequestAxisDiscrete, []any{uint32(1), uint32(0), int32(15 * 256), int32(1)}, 7},
	}
	for _, tc := range tests {
		require.NoError(t, c.request(vp, tc.opcode, tc.args...), "sending %s must succeed", tc.name)

		wire := make([]byte, 64)
		n, err := unix.Read(pair[1], wire)
		require.NoError(t, err, "reading back %s must succeed", tc.name)
		// 8 header bytes plus one 4-byte word per argument.
		require.Equal(t, 8+4*len(tc.args), n, "%s must be a header plus one word per argument", tc.name)

		word := binary.LittleEndian.Uint32(wire[4:])
		assert.Equal(t, vp, binary.LittleEndian.Uint32(wire), "%s must address the virtual pointer", tc.name)
		assert.Equal(t, uint32(n), word>>16, "%s must carry its own length", tc.name)
		assert.Equal(t, tc.want, word&0xffff, "%s must use the opcode the protocol declares", tc.name)
	}
}
