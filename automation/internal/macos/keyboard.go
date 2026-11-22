//go:build darwin

package macos

import (
	"context"
	"fmt"
	"time"
	"unicode/utf16"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Keyboard input is synthesized through CoreGraphics events rather than
// System Events over osascript. Typed text carries its own Unicode string,
// which Cocoa applications read whatever the layout. Some applications read the
// virtual key code instead: the iOS Simulator forwards key positions to the
// device, and remote desktop clients send them on. So a typed character also
// carries its US key position and shift where it has one. A key combination
// needs a virtual key code too, which is a position on the keyboard and so
// depends on the layout — the tables below are the US layout, the same
// assumption every synthetic-input tool makes.

// namedKeyCodes are the virtual key codes for the named keys, positions on the
// original Macintosh keyboard rather than characters.
var namedKeyCodes = map[string]int{
	"return": 36, "tab": 48, "space": 49, "backspace": 51, "escape": 53,
	"delete": 117, "home": 115, "end": 119, "pageup": 116, "pagedown": 121,
	"left": 123, "right": 124, "down": 125, "up": 126, "capslock": 57,
	"numlock": 71, "insert": 114, "menu": 110,
}

// functionKeyCodes are the function keys, which are not a contiguous range.
var functionKeyCodes = map[int]int{
	1: 122, 2: 120, 3: 99, 4: 118, 5: 96, 6: 97, 7: 98, 8: 100,
	9: 101, 10: 109, 11: 103, 12: 111, 13: 105, 14: 107, 15: 113,
	16: 106, 17: 64, 18: 79, 19: 80, 20: 90,
}

// usKeyCodes are the virtual key codes for the printable keys of the US layout,
// used when a key combination names a character. The shifted characters share a
// key with their base, so only the base is listed and the caller adds shift.
var usKeyCodes = map[rune]int{
	'a': 0, 's': 1, 'd': 2, 'f': 3, 'h': 4, 'g': 5, 'z': 6, 'x': 7, 'c': 8,
	'v': 9, 'b': 11, 'q': 12, 'w': 13, 'e': 14, 'r': 15, 'y': 16, 't': 17,
	'1': 18, '2': 19, '3': 20, '4': 21, '6': 22, '5': 23, '=': 24, '9': 25,
	'7': 26, '-': 27, '8': 28, '0': 29, ']': 30, 'o': 31, 'u': 32, '[': 33,
	'i': 34, 'p': 35, 'l': 37, 'j': 38, '\'': 39, 'k': 40, ';': 41, '\\': 42,
	',': 43, '/': 44, 'n': 45, 'm': 46, '.': 47, '`': 50,
}

// usShiftedKeys are the characters the US layout types with shift, by the
// unshifted character on the same key.
var usShiftedKeys = map[rune]rune{
	'!': '1', '@': '2', '#': '3', '$': '4', '%': '5', '^': '6', '&': '7',
	'*': '8', '(': '9', ')': '0', '_': '-', '+': '=', '{': '[', '}': ']',
	'|': '\\', ':': ';', '"': '\'', '<': ',', '>': '.', '?': '/', '~': '`',
}

// typedKeyCodes are the keys typed text reaches by name rather than by
// character.
var typedKeyCodes = map[rune]int{' ': 49, '\t': 48, '\n': 36, '\r': 36}

// usKeyFor finds the US key position that types r and whether it takes
// shift. ok is false for a character the US layout has no key for.
func usKeyFor(r rune) (code int, shift, ok bool) {
	if c, found := typedKeyCodes[r]; found {
		return c, false, true
	}
	if c, found := usKeyCodes[r]; found {
		return c, false, true
	}
	if r >= 'A' && r <= 'Z' {
		return usKeyCodes[r-'A'+'a'], true, true
	}
	if base, found := usShiftedKeys[r]; found {
		return usKeyCodes[base], true, true
	}
	return 0, false, false
}

// modifierMasks maps a canonical modifier onto its CGEventFlags bit.
var modifierMasks = map[string]uint64{
	autoapi.ModControl: kCGEventFlagMaskControl,
	autoapi.ModAlt:     kCGEventFlagMaskAlternate,
	autoapi.ModShift:   kCGEventFlagMaskShift,
	autoapi.ModSuper:   kCGEventFlagMaskCommand,
}

// typeText types a string one rune at a time, each as its own key event
// carrying the rune's UTF-16 as a Unicode string and, where the US layout has
// one, the key position that types it.
func typeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}
	source := cgEventSourceCreate(kCGEventSourceStateHIDSystemState)
	if source != 0 {
		defer cfRelease(source)
	}

	runes := []rune(text)
	for i, r := range runes {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := typeRune(ctx, source, r); err != nil {
			return err
		}
		if delay > 0 && i < len(runes)-1 {
			select {
			case <-clock.After(delay):
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	return nil
}

// typeRune sends one rune as a keystroke carrying its Unicode string and its
// US key position, with shift held for a shifted character. A character with
// no US key keeps key code 0 and relies on the string, which applications
// reading key codes type as "a".
func typeRune(ctx context.Context, source uintptr, r rune) error {
	units := utf16.Encode([]rune{r})
	if len(units) == 0 {
		return nil
	}
	code, shift, _ := usKeyFor(r)
	var flags uint64
	if shift {
		flags = kCGEventFlagMaskShift
	}
	return strike(ctx, source, uint16(code), flags, units)
}

// keyHold is how long a synthesized key stays down. A key down and up posted
// in the same instant reach the iOS Simulator, which follows the keyboard's
// state rather than its events, as nothing at all, and it then drops the keys
// that follow for a while. A physical keystroke is held for tens of
// milliseconds.
const keyHold = 15 * time.Millisecond

// modifierKeys are the modifier keys a stroke holds, in the order they go
// down; they come up in reverse.
var modifierKeys = []struct {
	mask uint64
	code uint16
}{
	{kCGEventFlagMaskControl, 59},
	{kCGEventFlagMaskAlternate, 58},
	{kCGEventFlagMaskShift, 56},
	{kCGEventFlagMaskCommand, 55},
}

// strike presses one key with the modifiers in flags held around it, as a
// person does. Applications that read the modifier keys' own events, as the
// iOS Simulator does, otherwise ignore a flag set on the key alone. units,
// when set, is the text the key types.
func strike(ctx context.Context, source uintptr, code uint16, flags uint64, units []uint16) error {
	var held uint64
	release := func() {
		for i := len(modifierKeys) - 1; i >= 0; i-- {
			m := modifierKeys[i]
			if held&m.mask == 0 {
				continue
			}
			held &^= m.mask
			_ = postModifier(source, m.code, held)
		}
	}
	defer release()
	for _, m := range modifierKeys {
		if flags&m.mask == 0 {
			continue
		}
		held |= m.mask
		if err := postModifier(source, m.code, held); err != nil {
			return err
		}
	}
	if err := postKey(source, code, true, flags, units); err != nil {
		return err
	}
	if err := sleepCtx(ctx, keyHold); err != nil {
		_ = postKey(source, code, false, flags, units)
		return err
	}
	return postKey(source, code, false, flags, units)
}

// postKey posts one key event with its modifier flags and, when set, its text.
func postKey(source uintptr, code uint16, down bool, flags uint64, units []uint16) error {
	event := cgEventCreateKeyboardEvent(source, code, down)
	if event == 0 {
		return fmt.Errorf("macOS refused to create a keyboard event")
	}
	defer cfRelease(event)
	if flags != 0 {
		cgEventSetFlags(event, flags)
	}
	if len(units) > 0 {
		cgEventKeyboardSetUnicodeString(event, len(units), &units[0])
	}
	cgEventPost(kCGHIDEventTap, event)
	return nil
}

// postModifier posts a modifier key going down or up as the flags-changed
// event a keyboard sends, with flags the modifiers held afterwards.
func postModifier(source uintptr, code uint16, flags uint64) error {
	event := cgEventCreateKeyboardEvent(source, code, true)
	if event == 0 {
		return fmt.Errorf("macOS refused to create a keyboard event")
	}
	defer cfRelease(event)
	cgEventSetType(event, kCGEventFlagsChanged)
	cgEventSetFlags(event, flags)
	cgEventPost(kCGHIDEventTap, event)
	return nil
}

// pressKeys presses one key combination: a modifier set plus one key, named or
// a single character. A bare character with no known key code falls back to
// typing it, so a printable key still does something on a non-US layout.
func pressKeys(ctx context.Context, stroke autoapi.KeyStroke) error {
	if err := load(); err != nil {
		return err
	}
	if err := ensureTrusted(); err != nil {
		return err
	}

	var flags uint64
	for _, mod := range stroke.Mods {
		flags |= modifierMasks[mod]
	}

	code, extraShift, ok, err := keyCodeFor(stroke)
	if err != nil {
		return err
	}
	if !ok {
		// No key code for this character. With no modifiers it is still typed;
		// with modifiers a shortcut needs a key position that is not known.
		if len(stroke.Mods) == 0 {
			source := cgEventSourceCreate(kCGEventSourceStateHIDSystemState)
			if source != 0 {
				defer cfRelease(source)
			}
			return typeRune(ctx, source, []rune(stroke.Key)[0])
		}
		return fmt.Errorf("macOS has no key position for %q on the US layout", stroke.Key)
	}
	if extraShift {
		flags |= kCGEventFlagMaskShift
	}

	source := cgEventSourceCreate(kCGEventSourceStateHIDSystemState)
	if source != 0 {
		defer cfRelease(source)
	}
	return strike(ctx, source, uint16(code), flags, nil)
}

// keyCodeFor resolves a stroke's key to a virtual key code. It reports whether
// a code was found and whether the code needs shift added (the '+' written
// "plus", which shares the '=' key).
func keyCodeFor(stroke autoapi.KeyStroke) (code int, extraShift, ok bool, err error) {
	if stroke.Named() && stroke.Key != "plus" {
		if c, found := namedKeyCodes[stroke.Key]; found {
			return c, false, true, nil
		}
		if number, isFn := autoapi.FunctionKeyNumber(stroke.Key); isFn {
			if c, found := functionKeyCodes[number]; found {
				return c, false, true, nil
			}
			return 0, false, false, fmt.Errorf("macOS keyboards stop at F20, so there is no %s key", stroke.Key)
		}
		return 0, false, false, fmt.Errorf("macOS has no key called %q", stroke.Key)
	}

	key := stroke.Key
	if key == "plus" {
		return usKeyCodes['='], true, true, nil
	}
	r := []rune(key)
	if len(r) != 1 {
		return 0, false, false, nil
	}
	if c, found := usKeyCodes[r[0]]; found {
		return c, false, true, nil
	}
	return 0, false, false, nil
}
