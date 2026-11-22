package portal

import (
	"fmt"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// The portal takes XKB keysyms, so the key vocabulary maps onto the same keysym
// values the X backend uses. A character is its Unicode keysym and a named key
// is its function keysym, which is what lets the same combination written on any
// keyboard press the right key here.

// modifierKeycodes names the evdev keycode held for each canonical modifier.
// Modifiers go over the wire as keycodes rather than keysyms: a keycode is a
// physical key press the compositor runs through its own keymap, so the
// modifier state is tracked the way a real keyboard's would be — COSMIC
// applies no modifier state at all from a keysym press. The keycodes are the
// fixed evdev values, the same on every layout.
var modifierKeycodes = map[string]int32{
	autoapi.ModShift:   42,  // KEY_LEFTSHIFT
	autoapi.ModControl: 29,  // KEY_LEFTCTRL
	autoapi.ModAlt:     56,  // KEY_LEFTALT
	autoapi.ModSuper:   125, // KEY_LEFTMETA
}

// namedKeysyms maps the named keys onto their keysyms.
var namedKeysyms = map[string]int32{
	"return": 0xFF0D, "escape": 0xFF1B, "tab": 0xFF09, "space": 0x0020,
	"backspace": 0xFF08, "delete": 0xFFFF, "insert": 0xFF63,
	"home": 0xFF50, "end": 0xFF57, "pageup": 0xFF55, "pagedown": 0xFF56,
	"up": 0xFF52, "down": 0xFF54, "left": 0xFF51, "right": 0xFF53,
	"printscreen": 0xFF61, "menu": 0xFF67, "pause": 0xFF13,
	"capslock": 0xFFE5, "numlock": 0xFF7F, "scrolllock": 0xFF14,
	"plus": 0x002B,
}

// runeKeysym maps a character onto the keysym that produces it. Latin-1 is its
// own value and everything else is the Unicode keysym range. A line break or
// tab is the Return or Tab key, because a code point below 0x20 is not a
// keysym and pressing it types nothing.
func runeKeysym(r rune) int32 {
	switch r {
	case '\n', '\r':
		return namedKeysyms["return"]
	case '\t':
		return namedKeysyms["tab"]
	}
	if r < 0x100 {
		return int32(r)
	}
	return int32(0x01000000 + r)
}

// strokeKeysym resolves the key half of a combination.
func strokeKeysym(k autoapi.KeyStroke) (int32, error) {
	if k.Named() {
		if sym, ok := namedKeysyms[k.Key]; ok {
			return sym, nil
		}
		if number, ok := autoapi.FunctionKeyNumber(k.Key); ok {
			// F1 is 0xFFBE and the function keys run consecutively from there.
			return int32(0xFFBE + number - 1), nil
		}
		return 0, fmt.Errorf("no keysym for key %q", k.Key)
	}
	runes := []rune(k.Key)
	if len(runes) != 1 {
		return 0, fmt.Errorf("%q is not a single key", k.Key)
	}
	return runeKeysym(runes[0]), nil
}
