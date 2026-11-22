//go:build darwin

package macos

import (
	"fmt"
	"sync"

	"github.com/ebitengine/purego"
)

// Locking the screen has no public C entry point; SACLockScreenImmediate in
// the private login framework is what the lock-screen menu item calls, and
// the alternatives are a CGSession spawn or sending the lock keystroke with
// Accessibility permission. Private means it can move in an OS update, which
// is why both of those stay behind it and a missing symbol is only a
// fallthrough.

var (
	sacLockScreenImmediate func() int32

	loginOnce sync.Once
	loginErr  error
)

// loadLoginFramework resolves the lock call once.
func loadLoginFramework() error {
	loginOnce.Do(func() {
		lib, err := purego.Dlopen("/System/Library/PrivateFrameworks/login.framework/Versions/A/login",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			loginErr = fmt.Errorf("opening the login framework: %w", err)
			return
		}
		defer func() {
			// A moved or renamed symbol lands here rather than crashing.
			if r := recover(); r != nil {
				loginErr = fmt.Errorf("SACLockScreenImmediate did not resolve: %v", r)
			}
		}()
		purego.RegisterLibFunc(&sacLockScreenImmediate, lib, "SACLockScreenImmediate")
	})
	return loginErr
}

// lockScreenNative locks the session through the login framework.
func lockScreenNative() error {
	if err := loadLoginFramework(); err != nil {
		return err
	}
	if rc := sacLockScreenImmediate(); rc != 0 {
		return fmt.Errorf("the login framework declined the lock (%d)", rc)
	}
	return nil
}
