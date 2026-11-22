package windows

import "github.com/grmrgecko/karakuri/automation/autoapi"

// decodeVkKeyScan splits a VkKeyScanW result into the virtual key and the
// modifiers the layout needs held to produce the character. The low byte is
// the virtual key; the high byte sets 1 for Shift, 2 for Ctrl, and 4 for Alt,
// and Ctrl with Alt is how Windows reaches the AltGr level. A result of -1
// means the layout cannot produce the character, which ok reports as false.
func decodeVkKeyScan(scan uint16) (vk uint16, mods []string, ok bool) {
	if int16(scan) == -1 {
		return 0, nil, false
	}
	if scan&0x100 != 0 {
		mods = append(mods, autoapi.ModShift)
	}
	if scan&0x200 != 0 {
		mods = append(mods, autoapi.ModControl)
	}
	if scan&0x400 != 0 {
		mods = append(mods, autoapi.ModAlt)
	}
	return scan & 0xFF, mods, true
}
