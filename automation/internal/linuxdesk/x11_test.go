//go:build !windows && !darwin

package linuxdesk

import (
	"context"
	"testing"
	"time"

	"github.com/jezek/xgb/xproto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestX11KeysymControlCharacters locks typed line breaks and tabs to the
// Return and Tab keysyms. The code points below 0x20 are not keysyms, so no
// layout has a key for 0x0A and binding it to a spare keycode types nothing.
func TestX11KeysymControlCharacters(t *testing.T) {
	cases := []struct {
		name string
		char rune
		want xproto.Keysym
	}{
		{"line feed", '\n', 0xFF0D},
		{"carriage return", '\r', 0xFF0D},
		{"tab", '\t', 0xFF09},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, x11Keysym(tc.char),
				"A control character must map to its function keysym because its code point is not a keysym.")
		})
	}
}

// countingClock counts the pauses a typing loop asks for and ends each at once.
type countingClock struct{ waits int }

func (c *countingClock) Now() time.Time { return time.Time{} }

func (c *countingClock) After(time.Duration) <-chan time.Time {
	c.waits++
	fired := make(chan time.Time, 1)
	fired <- time.Time{}
	return fired
}

// TestTypePacedWaitsOnlyBetweenCharacters locks the keystroke delay to the
// gaps between characters. The last character was found by comparing a byte
// offset with the byte length, so text ending in a multi-byte character such
// as "é" (two bytes in UTF-8) waited once more after everything was typed.
func TestTypePacedWaitsOnlyBetweenCharacters(t *testing.T) {
	cases := []struct {
		name string
		text string
		want int
	}{
		{"ASCII text", "abc", 2},
		{"text ending in a two-byte character", "né", 1},
		{"one four-byte character", "😀", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clock := &countingClock{}
			var typed []rune
			err := typePaced(context.Background(), clock, tc.text, time.Millisecond, func(char rune) error {
				typed = append(typed, char)
				return nil
			})
			require.NoError(t, err, "Typing through a press that succeeds must succeed.")
			assert.Equal(t, []rune(tc.text), typed, "Every character must be pressed once, in order.")
			assert.Equal(t, tc.want, clock.waits, "The delay belongs between characters, one fewer than the characters typed.")
		})
	}
}
