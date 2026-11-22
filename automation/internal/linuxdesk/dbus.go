//go:build !windows && !darwin

package linuxdesk

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/godbus/dbus/v5"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/dbusconn"
)

// D-Bus is the desktop's own control surface, and most of the tools this
// package used to shell out to — notify-send, playerctl, loginctl, systemctl,
// dbus-send — exist to make one bus call each. Making those calls directly
// removes the dependency on the tool being installed without changing what
// happens: the same service receives the same method call either way. godbus
// caches one shared connection per bus, so these helpers connect lazily and
// never during Session construction.

// notifyOverBus shows a notification through org.freedesktop.Notifications,
// the interface notify-send itself calls. Bus activation starts the desktop's
// notification daemon when it is not yet running, exactly as the tool would.
func notifyOverBus(ctx context.Context, n autoapi.Notification) error {
	conn, err := dbusconn.Session()
	if err != nil {
		return fmt.Errorf("connecting to the session bus: %w", err)
	}
	hints := map[string]dbus.Variant{}
	switch strings.ToLower(strings.TrimSpace(n.Urgency)) {
	case "low":
		hints["urgency"] = dbus.MakeVariant(byte(0))
	case "critical":
		hints["urgency"] = dbus.MakeVariant(byte(2))
	case "", "normal":
		// The daemon's default; saying nothing keeps it.
	default:
		return fmt.Errorf("unknown urgency %q; use low, normal, or critical", n.Urgency)
	}
	obj := conn.Object("org.freedesktop.Notifications", "/org/freedesktop/Notifications")
	call := obj.CallWithContext(ctx, "org.freedesktop.Notifications.Notify", 0,
		"karakuri", uint32(0), n.Icon, n.Title, n.Message, []string{}, hints, int32(-1))
	if call.Err != nil {
		return fmt.Errorf("no notification service answered on the session bus: %w", call.Err)
	}
	return nil
}

// mprisPrefix is the bus-name namespace every MPRIS media player claims.
const mprisPrefix = "org.mpris.MediaPlayer2."

// mprisPlayers lists the media players on the session bus, sorted so the
// choice with no player named is stable across calls.
func mprisPlayers(ctx context.Context, conn *dbus.Conn) ([]string, error) {
	var names []string
	err := conn.BusObject().CallWithContext(ctx, "org.freedesktop.DBus.ListNames", 0).Store(&names)
	if err != nil {
		return nil, fmt.Errorf("listing bus names: %w", err)
	}
	var players []string
	for _, name := range names {
		if strings.HasPrefix(name, mprisPrefix) {
			players = append(players, name)
		}
	}
	sort.Strings(players)
	return players, nil
}

// mprisPlayback drives one playback action over MPRIS, the interface playerctl
// speaks. The player is addressed by the suffix of its bus name — "spotify",
// or "firefox" matching "firefox.instance123" — so the names an author wrote
// for playerctl keep working.
func mprisPlayback(ctx context.Context, action, player string) error {
	conn, err := dbusconn.Session()
	if err != nil {
		return fmt.Errorf("connecting to the session bus: %w", err)
	}
	players, err := mprisPlayers(ctx, conn)
	if err != nil {
		return err
	}
	if len(players) == 0 {
		return fmt.Errorf("no MPRIS media player is running on this session")
	}

	target := players[0]
	if player != "" {
		target = ""
		want := strings.ToLower(player)
		for _, name := range players {
			suffix := strings.ToLower(strings.TrimPrefix(name, mprisPrefix))
			if suffix == want || strings.HasPrefix(suffix, want+".") {
				target = name
				break
			}
		}
		if target == "" {
			running := make([]string, len(players))
			for i, name := range players {
				running[i] = strings.TrimPrefix(name, mprisPrefix)
			}
			return fmt.Errorf("no media player called %q is running; found: %s",
				player, strings.Join(running, ", "))
		}
	}

	var method string
	switch action {
	case mediaPlayPause:
		method = "PlayPause"
	case mediaPlay:
		method = "Play"
	case mediaPause:
		method = "Pause"
	case mediaStop:
		method = "Stop"
	case mediaNext:
		method = "Next"
	case mediaPrevious:
		method = "Previous"
	default:
		return fmt.Errorf("unknown media action %q", action)
	}

	obj := conn.Object(target, "/org/mpris/MediaPlayer2")
	call := obj.CallWithContext(ctx, "org.mpris.MediaPlayer2.Player."+method, 0)
	if call.Err != nil {
		return fmt.Errorf("%s refused %s: %w", strings.TrimPrefix(target, mprisPrefix), method, call.Err)
	}
	return nil
}

// login1 names the logind service and its manager object on the system bus.
const (
	login1Name    = "org.freedesktop.login1"
	login1Path    = dbus.ObjectPath("/org/freedesktop/login1")
	login1Manager = "org.freedesktop.login1.Manager"
	login1Session = "org.freedesktop.login1.Session"
)

// logindSessionObject resolves the calling session's logind object: the one
// XDG_SESSION_ID names when the environment carries it, and logind's "auto"
// alias — the session the caller belongs to — when it does not.
func logindSessionObject(ctx context.Context, conn *dbus.Conn) (dbus.BusObject, error) {
	id := strings.TrimSpace(os.Getenv("XDG_SESSION_ID"))
	if id == "" {
		return conn.Object(login1Name, login1Path+"/session/auto"), nil
	}
	var path dbus.ObjectPath
	err := conn.Object(login1Name, login1Path).
		CallWithContext(ctx, login1Manager+".GetSession", 0, id).Store(&path)
	if err != nil {
		return nil, fmt.Errorf("logind does not know session %s: %w", id, err)
	}
	return conn.Object(login1Name, path), nil
}

// logindIdleHint reads the session's IdleHint and IdleSinceHint. logind
// stamps IdleSinceHint, in wall-clock microseconds, on every change of the
// hint in either direction, so the stamp measures idleness only while IdleHint
// is true.
func logindIdleHint(ctx context.Context) (bool, uint64, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return false, 0, fmt.Errorf("connecting to the system bus: %w", err)
	}
	session, err := logindSessionObject(ctx, conn)
	if err != nil {
		return false, 0, err
	}
	var value dbus.Variant
	err = session.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0,
		login1Session, "IdleHint").Store(&value)
	if err != nil {
		return false, 0, fmt.Errorf("reading IdleHint: %w", err)
	}
	idle, ok := value.Value().(bool)
	if !ok {
		return false, 0, fmt.Errorf("IdleHint is a %T, not a boolean", value.Value())
	}
	err = session.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0,
		login1Session, "IdleSinceHint").Store(&value)
	if err != nil {
		return false, 0, fmt.Errorf("reading IdleSinceHint: %w", err)
	}
	micros, ok := value.Value().(uint64)
	if !ok {
		return false, 0, fmt.Errorf("IdleSinceHint is a %T, not a timestamp", value.Value())
	}
	return idle, micros, nil
}

// logindSessionCall invokes one method on the calling session's logind object:
// Lock, Terminate.
func logindSessionCall(ctx context.Context, method string) error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("connecting to the system bus: %w", err)
	}
	session, err := logindSessionObject(ctx, conn)
	if err != nil {
		return err
	}
	if call := session.CallWithContext(ctx, login1Session+"."+method, 0); call.Err != nil {
		return fmt.Errorf("logind refused %s: %w", method, call.Err)
	}
	return nil
}

// logindManagerCall invokes one power method on logind's manager — Suspend,
// Reboot, PowerOff — the calls systemctl makes for the same verbs. The false
// argument declines interactive polkit authentication, since nobody is at the
// agent's terminal to answer a prompt.
func logindManagerCall(ctx context.Context, method string) error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("connecting to the system bus: %w", err)
	}
	obj := conn.Object(login1Name, login1Path)
	if call := obj.CallWithContext(ctx, login1Manager+"."+method, 0, false); call.Err != nil {
		return fmt.Errorf("logind refused %s: %w", method, call.Err)
	}
	return nil
}

// screenSaverLock locks the screen through org.freedesktop.ScreenSaver, the
// session-bus interface xdg-screensaver itself tries first. GNOME and KDE both
// own the name; it is the rung for a desktop session logind cannot lock —
// FreeBSD, and Linux running without systemd.
func screenSaverLock(ctx context.Context) error {
	conn, err := dbusconn.Session()
	if err != nil {
		return fmt.Errorf("connecting to the session bus: %w", err)
	}
	obj := conn.Object("org.freedesktop.ScreenSaver", "/org/freedesktop/ScreenSaver")
	if call := obj.CallWithContext(ctx, "org.freedesktop.ScreenSaver.Lock", 0); call.Err != nil {
		return fmt.Errorf("no screensaver service answered Lock: %w", call.Err)
	}
	return nil
}

// gnomeSessionLogout ends the session through GNOME's session manager, the
// call gnome-session-quit makes. Mode 1 skips the confirmation dialog, since
// nobody is at the agent's terminal to answer one. Only a GNOME session owns
// the name, so anywhere else this fails fast and the caller moves on.
func gnomeSessionLogout(ctx context.Context) error {
	conn, err := dbusconn.Session()
	if err != nil {
		return fmt.Errorf("connecting to the session bus: %w", err)
	}
	obj := conn.Object("org.gnome.SessionManager", "/org/gnome/SessionManager")
	if call := obj.CallWithContext(ctx, "org.gnome.SessionManager.Logout", 0, uint32(1)); call.Err != nil {
		return fmt.Errorf("no session manager answered Logout: %w", call.Err)
	}
	return nil
}

// mutterIdleMillis asks GNOME's idle monitor how long the session has been
// untouched. Only a GNOME session owns the name, so anywhere else this fails
// fast and the caller moves on.
func mutterIdleMillis(ctx context.Context) (uint64, error) {
	conn, err := dbusconn.Session()
	if err != nil {
		return 0, fmt.Errorf("connecting to the session bus: %w", err)
	}
	var millis uint64
	err = conn.Object("org.gnome.Mutter.IdleMonitor", "/org/gnome/Mutter/IdleMonitor/Core").
		CallWithContext(ctx, "org.gnome.Mutter.IdleMonitor.GetIdletime", 0).Store(&millis)
	if err != nil {
		return 0, err
	}
	return millis, nil
}

// openURIOverPortal hands a link to the desktop portal, the route a sandboxed
// application opens links by. The portal resolves the default handler itself,
// so this is xdg-open with nothing installed; a session without a portal
// service fails here and falls back to the tools.
func openURIOverPortal(ctx context.Context, url string) error {
	conn, err := dbusconn.Session()
	if err != nil {
		return fmt.Errorf("connecting to the session bus: %w", err)
	}
	obj := conn.Object("org.freedesktop.portal.Desktop", "/org/freedesktop/portal/desktop")
	call := obj.CallWithContext(ctx, "org.freedesktop.portal.OpenURI.OpenURI", 0,
		"", url, map[string]dbus.Variant{})
	return call.Err
}
