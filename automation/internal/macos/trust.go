//go:build darwin

package macos

import (
	"fmt"
	"sync"
)

// Every native input and window call needs the process trusted for
// Accessibility. macOS raises no prompt on its own for a background agent, so
// the agent asks for the grant itself the first time it needs it — which is
// what makes the permission appear in System Settings rather than leaving the
// operator to find it.

var promptOnce sync.Once

// requestAccessibility raises the Accessibility prompt once, when the process
// is not yet trusted. The grant only takes effect after a restart, so the call
// that triggered this still fails; the prompt is what lets the next run succeed.
func requestAccessibility() {
	promptOnce.Do(func() {
		if axIsProcessTrusted == nil || axIsProcessTrusted() {
			return
		}
		if axIsProcessTrustedWithOptions == nil || axTrustedCheckOptionPrompt == 0 ||
			cfDictionaryCreate == nil || kCFBooleanTrueRef == 0 {
			return
		}
		keys := []uintptr{axTrustedCheckOptionPrompt}
		values := []uintptr{kCFBooleanTrueRef}
		options := cfDictionaryCreate(0, &keys[0], &values[0], 1, 0, 0)
		if options == 0 {
			return
		}
		defer cfRelease(options)
		axIsProcessTrustedWithOptions(options)
	})
}

// ensureTrusted reports a clear error, and asks for the grant, when the process
// is not trusted. Input is otherwise dropped in silence and the window calls
// return a bare -25211, so every native entry point checks this first.
func ensureTrusted() error {
	if axIsProcessTrusted != nil && axIsProcessTrusted() {
		return nil
	}
	requestAccessibility()
	return fmt.Errorf("the agent is not trusted for Accessibility; grant it in " +
		"System Settings > Privacy & Security > Accessibility, then restart the agent")
}
