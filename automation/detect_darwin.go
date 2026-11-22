//go:build darwin

package automation

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/dialog"
	"github.com/grmrgecko/karakuri/automation/internal/macos"
)

// buildSession composes the macOS session. One backend supplies every
// capability through the system's own tools; the dialog is the cross-platform
// zenity backend. A root process that opted into delegation also gets the
// per-session helper factory, which is how the session backend reaches
// sessions this process does not belong to.
func buildSession(ctx context.Context, opts Options) (*Session, error) {
	set := macos.New()
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
		Sessions:  macos.NewSessions(),
		env:       Environment{OS: "darwin", Backends: map[string]string{"sessions": "window-server"}},
	}
	if f := sessionHelperFactory(opts); f != nil {
		if sf, ok := s.Sessions.(interface {
			SetHelperFactory(func(autoapi.SessionInfo) (macos.SessionHelpers, error))
		}); ok {
			sf.SetHelperFactory(f)
		}
	}
	if lf, ok := s.Sessions.(interface{ SetLogf(func(string, ...any)) }); ok {
		lf.SetLogf(opts.Logf)
	}
	return s, nil
}
