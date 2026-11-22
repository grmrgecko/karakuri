//go:build darwin

package macos

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// The clipboard is NSPasteboard, reached over the Objective-C runtime rather
// than by spawning pbcopy and pbpaste. AppKit has to be loaded into the
// process before its classes resolve, which is the only setup this needs.

// nsPasteboardTypeString is the UTI NSPasteboardTypeString names.
const nsPasteboardTypeString = "public.utf8-plain-text"

var (
	appKitOnce sync.Once
	appKitErr  error

	selGeneralPasteboard    objc.SEL
	selStringForType        objc.SEL
	selClearContents        objc.SEL
	selSetStringForType     objc.SEL
	selStringWithUTF8String objc.SEL
	selUTF8String           objc.SEL

	classNSPasteboard objc.Class
	classNSString     objc.Class
)

// loadAppKit opens AppKit and resolves the pasteboard vocabulary once.
func loadAppKit() error {
	appKitOnce.Do(func() {
		_, err := purego.Dlopen("/System/Library/Frameworks/AppKit.framework/AppKit",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			appKitErr = fmt.Errorf("opening AppKit: %w", err)
			return
		}
		selGeneralPasteboard = objc.RegisterName("generalPasteboard")
		selStringForType = objc.RegisterName("stringForType:")
		selClearContents = objc.RegisterName("clearContents")
		selSetStringForType = objc.RegisterName("setString:forType:")
		selStringWithUTF8String = objc.RegisterName("stringWithUTF8String:")
		selUTF8String = objc.RegisterName("UTF8String")
		classNSPasteboard = objc.GetClass("NSPasteboard")
		classNSString = objc.GetClass("NSString")
		if classNSPasteboard == 0 || classNSString == 0 {
			appKitErr = fmt.Errorf("AppKit loaded but its classes did not resolve")
		}
	})
	return appKitErr
}

// nsString makes an NSString from a Go string. The runtime copies the bytes,
// so the argument's lifetime ends with the call.
func nsString(s string) objc.ID {
	return objc.ID(classNSString).Send(selStringWithUTF8String, s)
}

// goString reads an NSString back as a Go string via its UTF-8 view, whose
// bytes belong to the NSString and are copied out before it goes away.
func goString(str objc.ID) string {
	if str == 0 {
		return ""
	}
	ptr := objc.Send[*byte](str, selUTF8String)
	if ptr == nil {
		return ""
	}
	length := 0
	for *(*byte)(unsafe.Add(unsafe.Pointer(ptr), length)) != 0 {
		length++
	}
	return string(unsafe.Slice(ptr, length))
}

// pasteboardRead returns the general pasteboard's text; a pasteboard holding
// no text reads as empty rather than failing.
func pasteboardRead() (string, error) {
	if err := loadAppKit(); err != nil {
		return "", err
	}
	pasteboard := objc.ID(classNSPasteboard).Send(selGeneralPasteboard)
	if pasteboard == 0 {
		return "", fmt.Errorf("no pasteboard in this session")
	}
	return goString(pasteboard.Send(selStringForType, nsString(nsPasteboardTypeString))), nil
}

// pasteboardWrite puts text on the general pasteboard.
func pasteboardWrite(text string) error {
	if err := loadAppKit(); err != nil {
		return err
	}
	pasteboard := objc.ID(classNSPasteboard).Send(selGeneralPasteboard)
	if pasteboard == 0 {
		return fmt.Errorf("no pasteboard in this session")
	}
	pasteboard.Send(selClearContents)
	ok := pasteboard.Send(selSetStringForType, nsString(text), nsString(nsPasteboardTypeString))
	if ok == 0 {
		return fmt.Errorf("the pasteboard refused the text")
	}
	return nil
}
