//go:build linux

package automation

import (
	"context"

	"github.com/grmrgecko/karakuri/automation/internal/dialog"
	"github.com/grmrgecko/karakuri/automation/internal/klipper"
	"github.com/grmrgecko/karakuri/automation/internal/kwin"
	"github.com/grmrgecko/karakuri/automation/internal/linuxdesk"
	"github.com/grmrgecko/karakuri/automation/internal/logind"
	"github.com/grmrgecko/karakuri/automation/internal/portal"
	"github.com/grmrgecko/karakuri/automation/internal/session"
	"github.com/grmrgecko/karakuri/automation/internal/utmpsess"
	"github.com/grmrgecko/karakuri/automation/internal/wldata"
	"github.com/grmrgecko/karakuri/automation/internal/wlinput"
	"github.com/grmrgecko/karakuri/automation/internal/wlwindow"
	"github.com/grmrgecko/karakuri/automation/internal/x11clip"
)

// buildSession composes the Linux session, choosing a backend per capability
// for what the host actually is. The unix backend (X11 plus the installed
// desktop tools) is the floor every capability falls back to; on a Wayland
// session the KDE and portal backends layer over clipboard, windows, and input,
// which the X path cannot reach there.
func buildSession(ctx context.Context, opts Options) (*Session, error) {
	info := session.Probe()
	set := linuxdesk.New()

	env := Environment{
		OS:         "linux",
		Display:    info.Display,
		Compositor: info.Compositor,
		Backends:   map[string]string{},
	}

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
		env:       env,
	}

	// Sessions is logind's wherever logind runs, which is every systemd
	// distribution; the utmp listing is what is left for one without it, where
	// nothing can act on a session at all.
	if logind.Available() {
		s.Sessions = logind.New()
		env.Backends["sessions"] = "logind"
	} else {
		s.Sessions = utmpsess.New()
		env.Backends["sessions"] = "utmp"
	}

	env.Backends["screen"] = "x11/portal/helper"
	env.Backends["notifier"] = "dbus"
	env.Backends["clipboard"] = "x11/wl-clipboard"
	env.Backends["window"] = "x11/ewmh"
	// On an X11 session the linuxdesk backend synthesizes input through XTEST
	// directly; the tool path is only the floor a session reaches when neither
	// XTEST nor a Wayland route answers. A Wayland session overrides this label
	// below with the route it actually took.
	if info.Display == session.DisplayX11 {
		env.Backends["input"] = "x11-xtest"
	} else {
		env.Backends["input"] = "x11/tool"
	}

	// On a KDE session KWin's own D-Bus services answer what X and the installed
	// tools cannot on Wayland, and need nothing installed.
	if info.Compositor == session.CompositorKWin {
		if klipper.Available() {
			s.Clipboard = klipper.New()
			env.Backends["clipboard"] = "klipper"
		}
		if info.Display == session.DisplayWayland && kwin.Available() {
			s.Window = kwin.New()
			env.Backends["window"] = "kwin-script"
		}
	}

	// The wlroots family serves the foreign-toplevel protocol, which lists and
	// acts on Wayland-native windows — the ones XWayland's EWMH path never saw,
	// so a sway or niri session reported no windows at all before this. KWin has
	// its own script backend just above; GNOME serves no such interface and keeps
	// the XWayland answer.
	if env.Backends["window"] != "kwin-script" && info.Display == session.DisplayWayland {
		if wl, err := wlwindow.New(ctx); err == nil {
			s.Window = wl
			env.Backends["window"] = "wlr-foreign-toplevel"
		}
	}

	// Clipboard, for every session KWin's klipper did not already answer. A
	// Wayland compositor that serves data-control owns the session's real
	// selection, so that is asked first and needs no XWayland (wlroots — sway,
	// Hyprland, river — is the case; GNOME serves no such interface). Behind it
	// the native X11 backend owns the selection through the server directly,
	// which reaches a real X11 session and, through XWayland, a Wayland one.
	// The wl-copy/xclip floor is what is left for a session answering neither.
	if env.Backends["clipboard"] != "klipper" {
		if info.Display == session.DisplayWayland {
			if wc, err := wldata.New(ctx); err == nil {
				s.Clipboard = wc
				env.Backends["clipboard"] = "wayland-data-control"
			}
		}
		if env.Backends["clipboard"] != "wayland-data-control" {
			if xc, err := x11clip.New(); err == nil {
				s.Clipboard = xc
				s.closers = append(s.closers, func() error { xc.Close(); return nil })
				env.Backends["clipboard"] = "x11-native"
			}
		}
	}

	// Input on a Wayland session. Two routes, chosen by what the compositor
	// serves. KDE and GNOME carry the RemoteDesktop portal, the sanctioned route
	// there, and it is kept for them unconditionally so their verified path does
	// not change. Every other Wayland compositor is offered the native
	// virtual-keyboard and virtual-pointer protocols first: the wlroots family
	// (sway, Hyprland, river, Wayfire, niri, labwc) serves those but no
	// RemoteDesktop portal, so the portal path would hang there, while the native
	// path needs no portal and no per-use consent and can place the pointer at an
	// absolute coordinate. A compositor that serves neither manager — or the one
	// COSMIC case, which carries a working RemoteDesktop portal — falls through to
	// the portal, which brings the session up lazily so this never prompts here.
	if info.Display == session.DisplayWayland {
		wired := false
		portalDesktop := info.Compositor == session.CompositorKWin ||
			info.Compositor == session.CompositorGNOME ||
			info.Compositor == session.CompositorCOSMIC
		if !portalDesktop {
			// Both halves must come from the same source. The native path is
			// taken only when the compositor serves both the virtual keyboard and
			// the virtual pointer, so keyboard and mouse never split across two
			// backends — COSMIC serves the virtual keyboard but no virtual
			// pointer, and there the portal answers both rather than leaving the
			// pointer on a tool that is not there.
			if wl, caps, err := wlinput.New(ctx); err == nil && caps.Keyboard && caps.Pointer {
				s.Keyboard = wl
				s.Mouse = wl
				env.Backends["input"] = "wayland-virtual"
				wired = true
			}
		}
		if !wired {
			p := portal.New(opts.appID(), opts.TokenStore)
			s.Keyboard = p
			s.Mouse = p
			s.closers = append(s.closers, p.Close)
			env.Backends["input"] = "portal"
		}
	}

	s.env = env
	return s, nil
}
