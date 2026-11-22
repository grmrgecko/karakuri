//go:build darwin

package macos

import "unsafe"

// CoreFoundation value helpers. The window list and the Accessibility API hand
// back CFString and CFNumber objects, and the AX calls take CFString attribute
// names; these convert between those and Go values.

// cfStringGo reads a CFStringRef as a Go string. A nil ref, or a string that
// will not encode, reads as empty rather than failing the whole listing.
func cfStringGo(ref uintptr) string {
	if ref == 0 {
		return ""
	}
	length := cfStringGetLength(ref)
	if length <= 0 {
		return ""
	}
	// A UTF-8 code point is at most four bytes; the extra one is the NUL that
	// CFStringGetCString writes and the loop below trims.
	buf := make([]byte, length*4+1)
	if !cfStringGetCString(ref, buf, len(buf), kCFStringEncodingUTF8) {
		return ""
	}
	n := 0
	for n < len(buf) && buf[n] != 0 {
		n++
	}
	return string(buf[:n])
}

// cfNumberInt reads a CFNumberRef as an int.
func cfNumberInt(ref uintptr) int {
	if ref == 0 {
		return 0
	}
	var value int64
	if !cfNumberGetValue(ref, kCFNumberSInt64Type, unsafe.Pointer(&value)) {
		return 0
	}
	return int(value)
}

// cfStr makes a CFStringRef the caller owns and must release. It is used for
// the AX attribute and action names, which are passed as CFStrings.
func cfStr(s string) uintptr {
	return cfStringCreateWithCString(0, s, kCFStringEncodingUTF8)
}
