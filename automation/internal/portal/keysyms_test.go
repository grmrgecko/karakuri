package portal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestRuneKeysymControlCharacters locks typed line breaks and tabs to the
// Return and Tab keysyms. The code points below 0x20 are not keysyms, so
// sending 0x0A to the portal presses no key and the line break is lost.
func TestRuneKeysymControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		char rune
		want int32
	}{
		{"line feed", '\n', 0xFF0D},
		{"carriage return", '\r', 0xFF0D},
		{"tab", '\t', 0xFF09},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runeKeysym(tc.char),
				"A control character must map to its function keysym because its code point is not a keysym.")
		})
	}
}
