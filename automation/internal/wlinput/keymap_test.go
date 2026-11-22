//go:build !windows && !darwin

package wlinput

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRuneKeysymName(t *testing.T) {
	// A character's keymap spelling is its Unicode codepoint in U-notation,
	// which XKB resolves back to the character's keysym on any layout.
	assert.Equal(t, "U0041", runeKeysymName('A'))
	assert.Equal(t, "U0061", runeKeysymName('a'))
	assert.Equal(t, "U0024", runeKeysymName('$'))
	assert.Equal(t, "U00E9", runeKeysymName('é'))
	assert.Equal(t, "U1F600", runeKeysymName('😀'))
}

func TestNamedKeyKeysymName(t *testing.T) {
	cases := map[string]string{
		"return":   "Return",
		"pageup":   "Prior",
		"pagedown": "Next",
		"space":    "space",
		"f5":       "F5",
		"f12":      "F12",
	}
	for key, want := range cases {
		got, err := namedKeyKeysymName(key)
		require.NoError(t, err, key)
		assert.Equal(t, want, got, key)
	}

	_, err := namedKeyKeysymName("nonsense")
	assert.Error(t, err)
}

func TestKeymapBuilderAssignsDistinctKeycodes(t *testing.T) {
	b := newKeymapBuilder()

	// The same keysym reuses its keycode; a new one takes the next.
	a1, ok := b.add("U0061")
	require.True(t, ok)
	a2, ok := b.add("U0061")
	require.True(t, ok)
	assert.Equal(t, a1, a2, "a repeated keysym keeps its keycode")

	c, ok := b.add("U0062")
	require.True(t, ok)
	assert.NotEqual(t, a1, c, "a new keysym takes a new keycode")

	// The render maps XKB keycode = evdev + 8 and places each keysym at the base
	// level, so a press produces the keysym with no shift.
	text := b.render()
	assert.Contains(t, text, "minimum = 8;")
	assert.Contains(t, text, "<K1> = 9;")
	assert.Contains(t, text, "key <K1> { [ U0061 ] };")
	assert.Contains(t, text, "key <K2> { [ U0062 ] };")
	assert.Contains(t, text, `include "complete"`)
}

func TestKeymapBuilderReportsFull(t *testing.T) {
	b := newKeymapBuilder()
	for i := 0; i < keymapCap; i++ {
		_, ok := b.add(runeKeysymName(rune('A' + i)))
		require.True(t, ok)
	}
	// One past the capacity is refused, which is the signal to flush and start a
	// fresh keymap.
	_, ok := b.add("U9999")
	assert.False(t, ok)
}

func TestModMaskCoversEveryModifier(t *testing.T) {
	// Every canonical modifier the key parser can produce must have a mask, or a
	// combination using it would fail at press time rather than parse time.
	for _, mod := range []string{"ctrl", "alt", "shift", "super"} {
		_, ok := modMask[mod]
		assert.True(t, ok, mod)
	}
	// The masks are the distinct real-modifier bits.
	seen := map[uint32]bool{}
	for _, m := range modMask {
		assert.False(t, seen[m], "mask %d is used twice", m)
		seen[m] = true
	}
}

// TestRuneKeysymNameControlCharacters locks typed line breaks and tabs to the
// keys that produce them. XKB reads a U-notation keysym below U+0020 as
// Linefeed or as no keysym at all, depending on its release, so a keymap entry
// such as U000A never presses Return and the line break is lost.
func TestRuneKeysymNameControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		char rune
		want string
	}{
		{"line feed", '\n', "Return"},
		{"carriage return", '\r', "Return"},
		{"tab", '\t', "Tab"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, runeKeysymName(tc.char),
				"A control character must be spelled as the key that types it because its U-notation is not that key.")
		})
	}
}
