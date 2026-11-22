package autoapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestParseKeyStroke pins the key notation, which is the one part with no
// platform to check it against: what a caller writes has to mean the same thing
// on X11, macOS, and Windows, and each of those has its own spelling for the
// same key. The plus sign is the case worth locking, since it separates the
// parts of a combination and is also a key a caller may want to press, and the
// aliases matter because someone writing on a Mac types "cmd" for the key a
// Linux host calls super.
func TestParseKeyStroke(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantMods []string
		wantKey  string
		why      string
	}{
		{
			name:     "modifiers and a character",
			input:    "ctrl+alt+t",
			wantMods: []string{ModControl, ModAlt},
			wantKey:  "t",
			why:      "the last part is the key and everything before it is held down",
		},
		{
			name:     "platform names for the same modifiers",
			input:    "Command+Option+K",
			wantMods: []string{ModSuper, ModAlt},
			wantKey:  "K",
			why:      "a combination written on a Mac has to press super and alt on a Linux host",
		},
		{
			name:     "a named key on its own",
			input:    "PageDown",
			wantMods: nil,
			wantKey:  "pagedown",
			why:      "a key name is case-folded so the platform tables have one spelling to map",
		},
		{
			name:     "an aliased key name",
			input:    "Page_Up",
			wantMods: nil,
			wantKey:  "pageup",
			why:      "separators inside a key name are noise, and X11 calls this one Prior anyway",
		},
		{
			name:     "the plus key after a modifier",
			input:    "ctrl++",
			wantMods: []string{ModControl},
			wantKey:  "plus",
			why:      "the plus is both the separator and the key, so the empty part it leaves is the key",
		},
		{
			name:     "the plus key alone",
			input:    "+",
			wantMods: nil,
			wantKey:  "plus",
			why:      "a lone plus is a key, not a separator with nothing on either side",
		},
		{
			name:     "a repeated modifier",
			input:    "ctrl+control+s",
			wantMods: []string{ModControl},
			wantKey:  "s",
			why:      "holding the same key twice is once, or it would be released early",
		},
	}
	for _, tc := range tests {
		stroke, err := ParseKeyStroke(tc.input)
		require.NoErrorf(t, err, "%s: %s", tc.name, tc.why)
		assert.Equalf(t, tc.wantMods, stroke.Mods, "%s: %s", tc.name, tc.why)
		assert.Equalf(t, tc.wantKey, stroke.Key, "%s: %s", tc.name, tc.why)
	}

	// A key that is neither a single character nor a name is a typo, and a typo
	// that reached the session would press some other key instead.
	_, err := ParseKeyStroke("ctrl+enterr")
	require.Error(t, err, "an unknown key name must fail at the call rather than in the session")
	assert.Contains(t, err.Error(), "pagedown", "the error lists the vocabulary so the right name is findable")

	_, err = ParseKeyStroke("hyper+t")
	assert.Error(t, err, "a modifier no platform has must fail rather than being pressed as a key")
}
