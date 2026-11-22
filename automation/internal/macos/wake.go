//go:build darwin

package macos

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// Waking the display before the typed unlock goes through the power manager's
// user-active assertion, the one `caffeinate -u` makes. While the display is
// off the window server drops posted events, so a synthetic keypress cannot
// wake it and the wake has to come from the power manager first; the
// assertion is what brings the display back and puts the lock screen up where
// the typed password can land.

// kIOPMAssertionLevelOn is the assertion strength the headers document.
const kIOPMAssertionLevelOn = 255

var (
	ioPMCreateAssertion  func(typ uintptr, level uint32, name uintptr, id *uint32) int32
	ioPMReleaseAssertion func(id uint32) int32

	pmOnce sync.Once
	pmErr  error
)

// loadPMSymbols resolves the IOKit power-manager calls once.
func loadPMSymbols() error {
	pmOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/Frameworks/IOKit.framework/IOKit",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			pmErr = err
			return
		}
		defer func() {
			// A moved symbol lands here as an error rather than a crash.
			if r := recover(); r != nil {
				pmErr = fmt.Errorf("an IOKit power-manager symbol did not resolve: %v", r)
			}
		}()
		purego.RegisterLibFunc(&ioPMCreateAssertion, lib, "IOPMAssertionCreateWithName")
		purego.RegisterLibFunc(&ioPMReleaseAssertion, lib, "IOPMAssertionRelease")
	})
	return pmErr
}

// wakeDisplay asserts user activity, which brings the display back if it has
// gone off, and returns the assertion's release. It is best effort: a failure
// here is reported by leaving the release empty, because the display may
// already be awake and the keypress remains a second wake at it.
func wakeDisplay() (release func()) {
	if err := loadPMSymbols(); err != nil {
		return nil
	}
	typ := cfStr("UserIsActive")
	defer cfRelease(typ)
	name := cfStr("karakuri session unlock")
	defer cfRelease(name)

	var id uint32
	if rc := ioPMCreateAssertion(typ, kIOPMAssertionLevelOn, name, &id); rc != 0 || id == 0 {
		return nil
	}
	return func() { ioPMReleaseAssertion(id) }
}
