//go:build !windows && !darwin

package wlinput

import (
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// The virtual keyboard delivers keycodes, and the compositor interprets them
// through a keymap this client uploads. Rather than carry a fixed layout and
// hunt for the keycode that produces a character, the keymap is built to order:
// each keysym the operation needs is assigned its own keycode at the base
// level, so pressing that keycode produces exactly that keysym with no shift.
// The standard modifier vocabulary is pulled in by including the "complete"
// types and compat rules, so a modifier mask sent through the modifiers request
// means what it does on any keyboard.

// Real modifier masks, the X core values a "complete" compat section binds the
// modifiers to. Alt is Mod1 and Super is Mod4.
const (
	maskShift   = 1 << 0
	maskControl = 1 << 2
	maskMod1    = 1 << 3 // Alt
	maskMod4    = 1 << 6 // Super
)

// modMask maps a canonical modifier onto its keymap mask bit.
var modMask = map[string]uint32{
	autoapi.ModShift:   maskShift,
	autoapi.ModControl: maskControl,
	autoapi.ModAlt:     maskMod1,
	autoapi.ModSuper:   maskMod4,
}

// namedKeysymNames maps the canonical named keys onto their XKB keysym names,
// the spelling the keymap text uses. The character keys are not here: a
// character is written as its Unicode keysym instead (keysymName).
var namedKeysymNames = map[string]string{
	"return": "Return", "escape": "Escape", "tab": "Tab", "space": "space",
	"backspace": "BackSpace", "delete": "Delete", "insert": "Insert",
	"home": "Home", "end": "End", "pageup": "Prior", "pagedown": "Next",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"printscreen": "Print", "menu": "Menu", "pause": "Pause",
	"capslock": "Caps_Lock", "numlock": "Num_Lock", "scrolllock": "Scroll_Lock",
	"plus": "plus",
}

// runeKeysymName returns the XKB keymap spelling of a single character: its
// Unicode keysym in U-notation, which XKB resolves to the character's keysym
// (the codepoint itself below 0x100, the 0x01000000+codepoint range above).
// A line break or tab is spelled as the key that types it: U-notation below
// U+0020 resolves to Linefeed or to no keysym at all, depending on the
// compositor's XKB release, and neither is the Return key.
func runeKeysymName(r rune) string {
	switch r {
	case '\n', '\r':
		return "Return"
	case '\t':
		return "Tab"
	}
	return fmt.Sprintf("U%04X", r)
}

// namedKeyKeysymName resolves a named key (Return, F5, …) to its XKB keysym
// name.
func namedKeyKeysymName(key string) (string, error) {
	if name, ok := namedKeysymNames[key]; ok {
		return name, nil
	}
	if n, ok := autoapi.FunctionKeyNumber(key); ok {
		return fmt.Sprintf("F%d", n), nil
	}
	return "", fmt.Errorf("no keysym for key %q", key)
}

// keymapCap is the number of assignable keycodes: evdev codes 1..cap, which are
// XKB keycodes 9..cap+8. Kept well under 255 so a long, glyph-rich string still
// fits one upload.
const keymapCap = 240

// keymapBuilder assigns keysym names to keycodes and renders the XKB keymap.
type keymapBuilder struct {
	order []string       // keysym names in assignment order
	code  map[string]int // keysym name -> evdev keycode (1-based)
}

func newKeymapBuilder() *keymapBuilder {
	return &keymapBuilder{code: map[string]int{}}
}

// add assigns a keycode to a keysym name if it has none, returning the evdev
// keycode and whether the keymap still has room. A full keymap returns ok=false
// and the caller flushes and starts a fresh one.
func (b *keymapBuilder) add(name string) (code int, ok bool) {
	if c, seen := b.code[name]; seen {
		return c, true
	}
	if len(b.order) >= keymapCap {
		return 0, false
	}
	c := len(b.order) + 1 // evdev keycode, XKB keycode c+8
	b.order = append(b.order, name)
	b.code[name] = c
	return c, true
}

// render produces the XKB keymap text for the assigned keysyms.
func (b *keymapBuilder) render() string {
	var sb strings.Builder
	sb.WriteString("xkb_keymap {\n")
	sb.WriteString("xkb_keycodes \"karakuri\" {\n")
	fmt.Fprintf(&sb, "minimum = 8;\nmaximum = %d;\n", keymapCap+8)
	for i := range b.order {
		fmt.Fprintf(&sb, "<K%d> = %d;\n", i+1, i+1+8) // XKB keycode = evdev + 8
	}
	sb.WriteString("};\n")
	sb.WriteString("xkb_types \"karakuri\" { include \"complete\" };\n")
	sb.WriteString("xkb_compatibility \"karakuri\" { include \"complete\" };\n")
	sb.WriteString("xkb_symbols \"karakuri\" {\n")
	for i, name := range b.order {
		fmt.Fprintf(&sb, "key <K%d> { [ %s ] };\n", i+1, name)
	}
	sb.WriteString("};\n")
	sb.WriteString("};\n")
	return sb.String()
}
