//go:build windows

package automation

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/internal/dialog"
	"github.com/grmrgecko/karakuri/automation/internal/windows"
)

// buildSession composes the Windows session. One backend supplies every
// capability through the Win32 API; the dialog is the
// cross-platform zenity backend, which uses native Win32 there.
func buildSession(ctx context.Context, opts Options) (*Session, error) {
	set := windows.New()
	s := &Session{
		Keyboard:  set.Keyboard,
		Mouse:     set.Mouse,
		Screen:    set.Screen,
		Window:    set.Window,
		Clipboard: set.Clipboard,
		Notifier:  set.Notifier,
		Dialog:    dialog.New(),
		Speaker:   set.Speaker,
		Media:     set.Media,
		Power:     set.Power,
		Idle:      set.Idle,
		Opener:    set.Opener,
		Sessions:  windows.NewSessions(),
		env: Environment{OS: "windows", Backends: map[string]string{
			"input": "sendinput", "screen": "gdi", "window": "user32",
			"clipboard": "win32", "notifier": "winrt/shell-notify-icon",
			"dialog": "win32", "speaker": "sapi", "media": "media-keys",
			"power": "win32", "idle": "getlastinputinfo", "opener": "shell-execute",
			"sessions": "terminal-services",
		}},
	}
	// The credentialed unlock types through this binary re-executed inside
	// the locked session, so the backend needs the argv it would start it
	// with — the same opt-in shape the delegation helper takes.
	if si, ok := s.Sessions.(interface {
		SetSecureInput(argv []string, logf func(string, ...any))
	}); ok {
		si.SetSecureInput(opts.SecureInputCommand, opts.Logf)
	}
	return s, nil
}
