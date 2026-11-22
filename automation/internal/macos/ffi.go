//go:build darwin

package macos

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// This is the cgo-free bridge to the macOS frameworks. Rather than link Cocoa
// and CoreGraphics through cgo — which would tie the agent's cross-compilation
// to a macOS toolchain and break the CGO_ENABLED=0 build the rest of the agent
// depends on — purego opens the frameworks at run time and calls their C
// functions directly. The input, window list, and window actions that used to
// go through osascript and cliclick are native CoreGraphics and Accessibility
// calls here.

// cgPoint mirrors CoreGraphics' CGPoint: two doubles passed and returned by
// value, which purego marshals directly.
type cgPoint struct{ X, Y float64 }

// CoreGraphics event and window functions.
var (
	cgEventSourceCreate             func(int32) uintptr
	cgEventCreateMouseEvent         func(uintptr, uint32, cgPoint, uint32) uintptr
	cgEventCreateKeyboardEvent      func(uintptr, uint16, bool) uintptr
	cgEventKeyboardSetUnicodeString func(uintptr, int, *uint16)
	cgEventCreateScrollWheelEvent   func(uintptr, uint32, uint32, int32, int32) uintptr
	cgEventSetFlags                 func(uintptr, uint64)
	cgEventSetIntegerValueField     func(uintptr, uint32, int64)
	cgEventSetType                  func(uintptr, uint32)
	cgEventPost                     func(uint32, uintptr)
	cgEventCreate                   func(uintptr) uintptr
	cgEventGetLocation              func(uintptr) cgPoint
	cgWindowListCopyWindowInfo      func(uint32, uint32) uintptr
)

// CoreFoundation functions for reading the values the window list returns.
var (
	cfRelease                 func(uintptr)
	cfArrayGetCount           func(uintptr) int
	cfArrayGetValueAtIndex    func(uintptr, int) uintptr
	cfDictionaryGetValue      func(uintptr, uintptr) uintptr
	cfStringCreateWithCString func(uintptr, string, uint32) uintptr
	cfStringGetCString        func(uintptr, []byte, int, uint32) bool
	cfStringGetLength         func(uintptr) int
	cfNumberGetValue          func(uintptr, int32, unsafe.Pointer) bool
)

// Accessibility functions for acting on a window and for the trust check that
// requests the permission the whole backend depends on.
var (
	axUIElementCreateApplication  func(int32) uintptr
	axUIElementCopyAttributeValue func(uintptr, uintptr, *uintptr) int32
	axUIElementSetAttributeValue  func(uintptr, uintptr, uintptr) int32
	axUIElementPerformAction      func(uintptr, uintptr) int32
	axIsProcessTrusted            func() bool
	axIsProcessTrustedWithOptions func(uintptr) bool
	cfDictionaryCreate            func(uintptr, *uintptr, *uintptr, int, uintptr, uintptr) uintptr
)

// axTrustedCheckOptionPrompt is the CFString key that asks the trust check to
// raise the Accessibility prompt when the process is not yet trusted.
var axTrustedCheckOptionPrompt uintptr

// Exported CFString constants naming the window-dictionary keys, resolved once.
var (
	keyWindowNumber    uintptr
	keyWindowOwnerName uintptr
	keyWindowName      uintptr
	keyWindowOwnerPID  uintptr
	keyWindowLayer     uintptr
)

// kCFBooleanTrueRef is CoreFoundation's shared true value, used to set the
// boolean Accessibility attributes like AXMinimized and AXFrontmost.
var kCFBooleanTrueRef uintptr

const (
	// CFString UTF-8 encoding.
	kCFStringEncodingUTF8 = 0x08000100
	// CFNumber signed 64-bit type.
	kCFNumberSInt64Type = 4

	// CGEvent tap and source.
	kCGHIDEventTap                    = 0
	kCGEventSourceStateHIDSystemState = 1

	// CGEvent mouse types and buttons.
	kCGEventLeftMouseDown  = 1
	kCGEventLeftMouseUp    = 2
	kCGEventRightMouseDown = 3
	kCGEventRightMouseUp   = 4
	kCGEventMouseMoved     = 5
	kCGEventOtherMouseDown = 25
	kCGEventOtherMouseUp   = 26
	kCGEventFlagsChanged   = 12

	// kCGMouseEventClickState is the event field holding the click count: 1
	// for a single click, 2 for the second click of a double click.
	kCGMouseEventClickState = 1

	kCGMouseButtonLeft   = 0
	kCGMouseButtonRight  = 1
	kCGMouseButtonCenter = 2

	// CGEvent scroll unit.
	kCGScrollEventUnitLine = 1

	// CGEventFlags modifier masks.
	kCGEventFlagMaskShift     = 0x00020000
	kCGEventFlagMaskControl   = 0x00040000
	kCGEventFlagMaskAlternate = 0x00080000
	kCGEventFlagMaskCommand   = 0x00100000

	// CGWindowList options.
	kCGWindowListOptionOnScreenOnly = 1
	kCGNullWindowID                 = 0
)

var (
	loadOnce sync.Once
	loadErr  error
)

// load opens the frameworks and binds every function once. A failure here is
// what a caller reports instead of a nil call, and it should never happen on a
// real macOS host: these frameworks are always present.
func load() error {
	loadOnce.Do(func() {
		const flags = purego.RTLD_NOW | purego.RTLD_GLOBAL
		app, err := purego.Dlopen("/System/Library/Frameworks/ApplicationServices.framework/ApplicationServices", flags)
		if err != nil {
			loadErr = fmt.Errorf("opening ApplicationServices: %w", err)
			return
		}
		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", flags)
		if err != nil {
			loadErr = fmt.Errorf("opening CoreFoundation: %w", err)
			return
		}

		purego.RegisterLibFunc(&cgEventSourceCreate, app, "CGEventSourceCreate")
		purego.RegisterLibFunc(&cgEventCreateMouseEvent, app, "CGEventCreateMouseEvent")
		purego.RegisterLibFunc(&cgEventCreateKeyboardEvent, app, "CGEventCreateKeyboardEvent")
		purego.RegisterLibFunc(&cgEventKeyboardSetUnicodeString, app, "CGEventKeyboardSetUnicodeString")
		purego.RegisterLibFunc(&cgEventCreateScrollWheelEvent, app, "CGEventCreateScrollWheelEvent")
		purego.RegisterLibFunc(&cgEventSetFlags, app, "CGEventSetFlags")
		purego.RegisterLibFunc(&cgEventSetIntegerValueField, app, "CGEventSetIntegerValueField")
		purego.RegisterLibFunc(&cgEventSetType, app, "CGEventSetType")
		purego.RegisterLibFunc(&cgEventPost, app, "CGEventPost")
		purego.RegisterLibFunc(&cgEventCreate, app, "CGEventCreate")
		purego.RegisterLibFunc(&cgEventGetLocation, app, "CGEventGetLocation")
		purego.RegisterLibFunc(&cgWindowListCopyWindowInfo, app, "CGWindowListCopyWindowInfo")

		purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
		purego.RegisterLibFunc(&cfArrayGetCount, cf, "CFArrayGetCount")
		purego.RegisterLibFunc(&cfArrayGetValueAtIndex, cf, "CFArrayGetValueAtIndex")
		purego.RegisterLibFunc(&cfDictionaryGetValue, cf, "CFDictionaryGetValue")
		purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
		purego.RegisterLibFunc(&cfStringGetCString, cf, "CFStringGetCString")
		purego.RegisterLibFunc(&cfStringGetLength, cf, "CFStringGetLength")
		purego.RegisterLibFunc(&cfNumberGetValue, cf, "CFNumberGetValue")

		purego.RegisterLibFunc(&axUIElementCreateApplication, app, "AXUIElementCreateApplication")
		purego.RegisterLibFunc(&axUIElementCopyAttributeValue, app, "AXUIElementCopyAttributeValue")
		purego.RegisterLibFunc(&axUIElementSetAttributeValue, app, "AXUIElementSetAttributeValue")
		purego.RegisterLibFunc(&axUIElementPerformAction, app, "AXUIElementPerformAction")
		purego.RegisterLibFunc(&axIsProcessTrusted, app, "AXIsProcessTrusted")
		purego.RegisterLibFunc(&axIsProcessTrustedWithOptions, app, "AXIsProcessTrustedWithOptions")
		purego.RegisterLibFunc(&cfDictionaryCreate, cf, "CFDictionaryCreate")

		// The window-dictionary keys are exported CFStringRef globals; a symbol
		// lookup yields a pointer to the ref, which is dereferenced once.
		keyWindowNumber = derefStringSym(app, "kCGWindowNumber")
		keyWindowOwnerName = derefStringSym(app, "kCGWindowOwnerName")
		keyWindowName = derefStringSym(app, "kCGWindowName")
		keyWindowOwnerPID = derefStringSym(app, "kCGWindowOwnerPID")
		keyWindowLayer = derefStringSym(app, "kCGWindowLayer")

		kCFBooleanTrueRef = derefStringSym(cf, "kCFBooleanTrue")
		axTrustedCheckOptionPrompt = derefStringSym(app, "kAXTrustedCheckOptionPrompt")
	})
	return loadErr
}

// derefStringSym resolves an exported CFStringRef constant to its value.
func derefStringSym(lib uintptr, name string) uintptr {
	p, err := purego.Dlsym(lib, name)
	if err != nil || p == 0 {
		return 0
	}
	return *(*uintptr)(unsafe.Pointer(p))
}
