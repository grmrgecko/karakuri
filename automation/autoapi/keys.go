package autoapi

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// One key vocabulary is parsed here and mapped three ways by the platform
// backends, because the names a caller writes should not change with the host:
// X11 calls Page Up "Prior", macOS calls Return key code 36, and Windows calls
// it 0x0D. What the caller writes is "pageup" and "return" on all of them.

// Canonical modifier names. Everything a caller is likely to write for one of
// these folds onto these four.
const (
	ModControl = "ctrl"
	ModAlt     = "alt"
	ModShift   = "shift"
	ModSuper   = "super"
)

// modifierAliases maps what a caller writes onto the canonical modifier. The
// platform keys have several names each and none of them is wrong.
var modifierAliases = map[string]string{
	"ctrl": ModControl, "control": ModControl,
	"alt": ModAlt, "option": ModAlt, "opt": ModAlt, "meta": ModAlt,
	"shift": ModShift,
	"super": ModSuper, "cmd": ModSuper, "command": ModSuper, "win": ModSuper, "windows": ModSuper,
}

// namedKeys is the set of keys that have a name rather than a character. A key
// outside it has to be a single character, which is what makes a typo fail at
// the call instead of arriving as some other keystroke.
var namedKeys = map[string]bool{
	"return": true, "escape": true, "tab": true, "space": true, "backspace": true,
	"delete": true, "insert": true, "home": true, "end": true,
	"pageup": true, "pagedown": true,
	"up": true, "down": true, "left": true, "right": true,
	"printscreen": true, "menu": true, "pause": true,
	"capslock": true, "numlock": true, "scrolllock": true,
	"plus": true,
}

// keyAliases folds the other spellings of a named key onto the canonical one.
var keyAliases = map[string]string{
	"enter": "return", "esc": "escape", "del": "delete", "ins": "insert",
	"pgup": "pageup", "pgdn": "pagedown", "pagedn": "pagedown",
	"prtsc": "printscreen", "print": "printscreen", "sysrq": "printscreen",
	"arrowup": "up", "arrowdown": "down", "arrowleft": "left", "arrowright": "right",
	"spacebar": "space", "bksp": "backspace",
}

func init() {
	// Function keys are a range rather than a list, and every platform table
	// indexes them by number.
	for i := 1; i <= 24; i++ {
		namedKeys[fmt.Sprintf("f%d", i)] = true
	}
}

// KeyStroke is one key with the modifiers held while it is pressed.
type KeyStroke struct {
	Mods []string
	// Key is a canonical name from namedKeys, or a single character to be
	// pressed as the session's layout produces it.
	Key string
}

// String renders the stroke back in the notation it was written in, for the
// metadata a caller reports.
func (k KeyStroke) String() string {
	return strings.Join(append(append([]string{}, k.Mods...), k.Key), "+")
}

// Named reports whether the key is one of the named ones rather than a
// character the layout has to produce.
func (k KeyStroke) Named() bool {
	return namedKeys[k.Key]
}

// HasMod reports whether the stroke holds a modifier.
func (k KeyStroke) HasMod(mod string) bool {
	for _, held := range k.Mods {
		if held == mod {
			return true
		}
	}
	return false
}

// ParseKeyStroke reads "ctrl+alt+t" into modifiers and one key. The plus that
// separates the parts is also a key, so a trailing empty segment is read as
// the plus key rather than as a missing one.
func ParseKeyStroke(raw string) (KeyStroke, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return KeyStroke{}, errors.New("no keys configured")
	}

	// "ctrl++" splits into ctrl and two empty parts, because the plus is both
	// the separator and the key. Every trailing empty part is that one key.
	parts := strings.Split(text, "+")
	if n := len(parts); n > 1 && parts[n-1] == "" {
		kept := parts[:n-1]
		for len(kept) > 0 && kept[len(kept)-1] == "" {
			kept = kept[:len(kept)-1]
		}
		parts = append(kept, "plus")
	}

	stroke := KeyStroke{}
	for i, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return KeyStroke{}, fmt.Errorf("%q has an empty key between its plus signs", raw)
		}
		if i < len(parts)-1 {
			mod, ok := modifierAliases[strings.ToLower(part)]
			if !ok {
				return KeyStroke{}, fmt.Errorf("%q is not a modifier; use ctrl, alt, shift, or super", part)
			}
			if !stroke.HasMod(mod) {
				stroke.Mods = append(stroke.Mods, mod)
			}
			continue
		}
		key, err := canonicalKey(part)
		if err != nil {
			return KeyStroke{}, err
		}
		stroke.Key = key
	}
	return stroke, nil
}

// canonicalKey folds one written key onto the vocabulary, leaving a single
// character as itself so the layout decides what it takes to produce it.
func canonicalKey(part string) (string, error) {
	if utf8.RuneCountInString(part) == 1 {
		return part, nil
	}
	folded := strings.ToLower(part)
	folded = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(folded)
	if alias, ok := keyAliases[folded]; ok {
		folded = alias
	}
	if !namedKeys[folded] {
		return "", fmt.Errorf("%q is not a key name; use a single character or one of %s", part, keyNameList())
	}
	return folded, nil
}

// keyNameList renders the vocabulary for an error message, with the function
// keys collapsed: listing f1 through f24 buries the names worth reading.
func keyNameList() string {
	names := make([]string, 0, len(namedKeys))
	for name := range namedKeys {
		if strings.HasPrefix(name, "f") && len(name) > 1 && name[1] >= '0' && name[1] <= '9' {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ") + ", f1 to f24"
}

// FunctionKeyNumber reports the number of a function key name, and whether it
// is one at all. Every platform table maps the range rather than the names.
func FunctionKeyNumber(key string) (int, bool) {
	if len(key) < 2 || key[0] != 'f' {
		return 0, false
	}
	number := 0
	for _, digit := range key[1:] {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		number = number*10 + int(digit-'0')
	}
	if number < 1 || number > 24 {
		return 0, false
	}
	return number, true
}
