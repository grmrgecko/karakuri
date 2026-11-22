package windows

import (
	"testing"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/stretchr/testify/assert"
)

// TestDecodeVkKeyScan pins the VkKeyScanW shift-state byte: 1 is Shift, 2 is
// Ctrl, and 4 is Alt. On the German layout "@" is 0x0651, Q (0x51) with
// Ctrl+Alt (0x02|0x04), so dropping those bits presses a bare Q and types "q".
func TestDecodeVkKeyScan(t *testing.T) {
	cases := []struct {
		name string
		scan uint16
		vk   uint16
		mods []string
		ok   bool
	}{
		{"US a is the bare A key", 0x0041, 0x41, nil, true},
		{"US @ is Shift+2", 0x0132, 0x32, []string{autoapi.ModShift}, true},
		{"German @ is AltGr+Q", 0x0651, 0x51, []string{autoapi.ModControl, autoapi.ModAlt}, true},
		{"a character the layout cannot produce", 0xFFFF, 0, nil, false},
	}
	for _, tc := range cases {
		vk, mods, ok := decodeVkKeyScan(tc.scan)
		assert.Equal(t, tc.ok, ok, tc.name)
		assert.Equal(t, tc.vk, vk, tc.name)
		assert.Equal(t, tc.mods, mods, tc.name)
	}
}
