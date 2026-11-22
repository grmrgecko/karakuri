//go:build windows

package windows

import (
	"encoding/binary"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestDecodeSessionLock pins Microsoft's WTSINFOEX layout and NT 6.1's
// reversed lock flags, including unknown and truncated responses.
func TestDecodeSessionLock(t *testing.T) {
	for _, tc := range []struct {
		name                      string
		major, minor, level, flag uint32
		size                      int
		locked, known             bool
	}{
		{"windows 7 unlocked", 6, 1, 1, 0, 24, false, true},
		{"windows 7 locked", 6, 1, 1, 1, 24, true, true},
		{"windows 8 locked", 6, 2, 1, 0, 24, true, true},
		{"windows 11 unlocked", 10, 0, 1, 1, 24, false, true},
		{"windows 11 locked", 10, 0, 1, 0, 24, true, true},
		{"windows 7 unknown", 6, 1, 1, 0xffffffff, 24, false, false},
		{"unknown flag", 10, 0, 1, 2, 24, false, false},
		{"unknown level", 10, 0, 2, 0, 24, false, false},
		{"truncated flags", 10, 0, 1, 0, 19, false, false},
		{"empty", 10, 0, 1, 0, 0, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Synthetic WTSINFOEX starts its level-1 union at byte 8; flags follow
			// the session ID and connection state at byte 16.
			buf := make([]byte, 24)
			binary.LittleEndian.PutUint32(buf, tc.level)
			binary.LittleEndian.PutUint32(buf[16:], tc.flag)
			locked, known := decodeSessionLock(buf[:tc.size], tc.major, tc.minor)
			assert.Equal(t, tc.locked, locked, "lock state must match %s", tc.name)
			assert.Equal(t, tc.known, known, "lock-state availability must match %s", tc.name)
		})
	}
}
