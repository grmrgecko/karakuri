//go:build darwin

package macos

import (
	"fmt"
	"sync"
	"unsafe"

	"github.com/ebitengine/purego"
)

// IOKit is where the HID system lives, and HIDIdleTime — nanoseconds since
// the last keyboard or mouse event — is the one idle measurement macOS
// exposes without a signed helper. Reading the registry property directly
// replaces spawning ioreg and regex-matching its dump.

var (
	ioServiceMatching             func(string) uintptr
	ioServiceGetMatchingService   func(uint32, uintptr) uint32
	ioRegistryEntryCreateProperty func(uint32, uintptr, uintptr, uint32) uintptr
	ioObjectRelease               func(uint32) int32
)

var (
	iokitOnce sync.Once
	iokitErr  error
)

// loadIOKit binds the registry functions once, on top of the CoreFoundation
// bindings the main loader owns.
func loadIOKit() error {
	if err := load(); err != nil {
		return err
	}
	iokitOnce.Do(func() {
		iokit, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			iokitErr = fmt.Errorf("opening IOKit: %w", err)
			return
		}
		purego.RegisterLibFunc(&ioServiceMatching, iokit, "IOServiceMatching")
		purego.RegisterLibFunc(&ioServiceGetMatchingService, iokit, "IOServiceGetMatchingService")
		purego.RegisterLibFunc(&ioRegistryEntryCreateProperty, iokit, "IORegistryEntryCreateCFProperty")
		purego.RegisterLibFunc(&ioObjectRelease, iokit, "IOObjectRelease")
	})
	return iokitErr
}

// hidIdleNanos reads HIDIdleTime from the IOHIDSystem registry entry.
func hidIdleNanos() (int64, error) {
	if err := loadIOKit(); err != nil {
		return 0, err
	}

	// The matching dictionary is consumed by the lookup, so only the service
	// handle needs releasing. Master port 0 is the default port.
	service := ioServiceGetMatchingService(0, ioServiceMatching("IOHIDSystem"))
	if service == 0 {
		return 0, fmt.Errorf("the IO registry has no IOHIDSystem entry")
	}
	defer ioObjectRelease(service)

	key := cfStr("HIDIdleTime")
	defer cfRelease(key)
	value := ioRegistryEntryCreateProperty(service, key, 0, 0)
	if value == 0 {
		return 0, fmt.Errorf("IOHIDSystem reports no HIDIdleTime")
	}
	defer cfRelease(value)

	var nanos int64
	if !cfNumberGetValue(value, kCFNumberSInt64Type, unsafe.Pointer(&nanos)) {
		return 0, fmt.Errorf("HIDIdleTime is not a number")
	}
	return nanos, nil
}
