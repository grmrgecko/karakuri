//go:build !windows && !darwin && !linux

package automation

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/internal/dialog"
	"github.com/grmrgecko/karakuri/automation/internal/klipper"
	"github.com/grmrgecko/karakuri/automation/internal/kwin"
	"github.com/grmrgecko/karakuri/automation/internal/linuxdesk"
	"github.com/grmrgecko/karakuri/automation/internal/portal"
	"github.com/grmrgecko/karakuri/automation/internal/session"
	"github.com/grmrgecko/karakuri/automation/internal/utmpsess"
	"github.com/grmrgecko/karakuri/automation/internal/wldata"
	"github.com/grmrgecko/karakuri/automation/internal/wlinput"
	"github.com/grmrgecko/karakuri/automation/internal/wlwindow"
	"github.com/grmrgecko/karakuri/automation/internal/x11clip"
)

// buildSession composes the session for FreeBSD and the other Unixes. They
// share the Unix desktop protocols with Linux, while their host session manager
// remains platform-specific.
func buildSession(ctx context.Context, opts Options) (*Session, error) {
	info := session.Probe()
	set := linuxdesk.New()

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
		Sessions:  utmpsess.New(),
		env: Environment{
			OS:         "freebsd",
			Display:    info.Display,
			Compositor: info.Compositor,
			Backends:   map[string]string{"sessions": "utmp"},
		},
	}
	s.env.Backends["screen"] = "x11/helper"
	s.env.Backends["notifier"] = "dbus"
	s.env.Backends["window"] = "x11/ewmh"
	if info.Display == session.DisplayX11 {
		s.env.Backends["input"] = "x11-xtest"
	} else {
		s.env.Backends["input"] = "tool"
	}

	// Plasma exposes its native clipboard and windows through D-Bus on every
	// supported Unix. These are the same services used on Linux.
	if info.Compositor == session.CompositorKWin {
		if klipper.Available() {
			s.Clipboard = klipper.New()
			s.env.Backends["clipboard"] = "klipper"
		}
		if info.Display == session.DisplayWayland && kwin.Available() {
			s.Window = kwin.New()
			s.env.Backends["window"] = "kwin-script"
		}
	}

	// Wayland protocol clients are pure Go and work on any Unix compositor that
	// advertises the corresponding interfaces.
	if info.Display == session.DisplayWayland && s.env.Backends["window"] != "kwin-script" {
		if wl, err := wlwindow.New(ctx); err == nil {
			s.Window = wl
			s.env.Backends["window"] = "wlr-foreign-toplevel"
		}
	}

	// Own the selection natively rather than shelling out to the clipboard
	// tools the base set needs installed: a Wayland compositor serving
	// data-control answers first, then the X selection owner, which is the
	// whole answer on the X11 desktops FreeBSD usually runs.
	if info.Display == session.DisplayWayland && s.env.Backends["clipboard"] != "klipper" {
		if wc, err := wldata.New(ctx); err == nil {
			s.Clipboard = wc
			s.env.Backends["clipboard"] = "wayland-data-control"
		}
	}
	if s.env.Backends["clipboard"] != "klipper" &&
		s.env.Backends["clipboard"] != "wayland-data-control" {
		if xc, err := x11clip.New(); err == nil {
			s.Clipboard = xc
			s.closers = append(s.closers, func() error { xc.Close(); return nil })
			s.env.Backends["clipboard"] = "x11-native"
		}
	}

	// KDE, GNOME, and COSMIC use the standard RemoteDesktop portal. Other
	// compositors get native virtual input when they advertise both halves of
	// the protocol, then fall back to the portal if they do not.
	if info.Display == session.DisplayWayland {
		wired := false
		portalDesktop := info.Compositor == session.CompositorKWin ||
			info.Compositor == session.CompositorGNOME ||
			info.Compositor == session.CompositorCOSMIC
		if !portalDesktop {
			if wl, caps, err := wlinput.New(ctx); err == nil && caps.Keyboard && caps.Pointer {
				s.Keyboard = wl
				s.Mouse = wl
				s.env.Backends["input"] = "wayland-virtual"
				wired = true
			}
		}
		if !wired {
			p := portal.New(opts.appID(), opts.TokenStore)
			s.Keyboard = p
			s.Mouse = p
			s.closers = append(s.closers, p.Close)
			s.env.Backends["input"] = "portal"
		}
	}

	return s, nil
}
