//go:build unix && !darwin

// Package session probes a Unix desktop for what kind it is: X11 or Wayland,
// and which compositor family, so the parent package can compose the right
// backend per capability. The answer decides whether clipboard, windows, and
// input go through KDE's D-Bus services, the wlroots tools, the portal, or the
// X server.
package session

import (
	"os"
	"strings"
	"sync"

	"github.com/godbus/dbus/v5"

	"github.com/grmrgecko/karakuri/automation/internal/dbusconn"
)

// Display kinds.
const (
	DisplayX11     = "x11"
	DisplayWayland = "wayland"
)

// Compositor families.
const (
	CompositorKWin    = "kwin"
	CompositorGNOME   = "gnome"
	CompositorCOSMIC  = "cosmic"
	CompositorWlroots = "wlroots"
)

// Info is what the probe found.
type Info struct {
	Display    string // DisplayX11, DisplayWayland, or ""
	Compositor string // one of the Compositor* names, or ""
	X11        bool   // an X display is reachable (directly or through XWayland)
}

// Probe reports the session kind. It reads the environment for the display type
// and asks the session bus which compositor is running, since the environment
// names the socket but not the compositor.
func Probe() Info {
	info := Info{X11: strings.TrimSpace(os.Getenv("DISPLAY")) != ""}

	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), DisplayWayland) ||
		strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != "" {
		info.Display = DisplayWayland
	} else if info.X11 {
		info.Display = DisplayX11
	}

	info.Compositor = detectCompositor()
	return info
}

// detectCompositor names the compositor family, asking the session bus first
// because a name owner there is authoritative, and falling back to the desktop
// environment variable when the bus says nothing.
func detectCompositor() string {
	switch {
	case NameHasOwner("org.kde.KWin"):
		return CompositorKWin
	case NameHasOwner("org.gnome.Shell") || NameHasOwner("org.gnome.Mutter.IdleMonitor"):
		return CompositorGNOME
	}
	return CompositorFromEnvironment(map[string]string{
		"XDG_CURRENT_DESKTOP":         os.Getenv("XDG_CURRENT_DESKTOP"),
		"XDG_SESSION_DESKTOP":         os.Getenv("XDG_SESSION_DESKTOP"),
		"SWAYSOCK":                    os.Getenv("SWAYSOCK"),
		"HYPRLAND_INSTANCE_SIGNATURE": os.Getenv("HYPRLAND_INSTANCE_SIGNATURE"),
	})
}

// CompositorFromEnvironment identifies a compositor when its session bus is
// not available to the caller, as happens in a privileged parent delegating
// work to a desktop helper.
func CompositorFromEnvironment(env map[string]string) string {
	desktop := strings.ToLower(env["XDG_CURRENT_DESKTOP"] + " " + env["XDG_SESSION_DESKTOP"])
	if strings.Contains(desktop, "kde") || strings.Contains(desktop, "plasma") {
		return CompositorKWin
	}
	if strings.Contains(desktop, "gnome") {
		return CompositorGNOME
	}
	if strings.Contains(desktop, "cosmic") {
		return CompositorCOSMIC
	}
	// Wlroots and the other portal-less Wayland compositors own no common
	// service, so their desktop names and socket variables are the only stable
	// identifiers available outside the session bus.
	for _, name := range []string{"sway", "hyprland", "wayfire", "river", "niri", "labwc", "wlroots"} {
		if strings.Contains(desktop, name) {
			return CompositorWlroots
		}
	}
	if env["SWAYSOCK"] != "" || env["HYPRLAND_INSTANCE_SIGNATURE"] != "" {
		return CompositorWlroots
	}
	return ""
}

// sessionBus is the shared connection the probes and the D-Bus backends reuse,
// opened once. A session with no bus (a system service) leaves it nil, and the
// probes read that as "no owner" rather than failing.
var (
	busOnce sync.Once
	busConn *dbus.Conn
)

// SessionBus returns the shared session-bus connection, or nil if there is none.
func SessionBus() *dbus.Conn {
	busOnce.Do(func() {
		conn, err := dbusconn.Session()
		if err == nil {
			busConn = conn
		}
	})
	return busConn
}

// NameHasOwner reports whether a well-known name is claimed on the session bus.
func NameHasOwner(name string) bool {
	conn := SessionBus()
	if conn == nil {
		return false
	}
	var has bool
	err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has)
	return err == nil && has
}
