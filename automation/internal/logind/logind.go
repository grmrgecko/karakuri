//go:build linux

// Package logind lists and acts on a Linux host's login sessions through
// systemd-logind on the system bus. logind is the one interface every desktop
// shares here: the display managers disagree about everything else, but they
// all register their sessions and their greeter with it, so activating,
// locking, unlocking, and ending a session needs nothing installed and no
// knowledge of which desktop is running.
//
// Acting on a session that is not the caller's own is an administrator's
// action, which logind puts behind polkit. Running as root skips it — systemd
// short-circuits the check for uid 0 — so a privileged agent unlocks and ends
// sessions without a prompt, and an unprivileged one reaches only its own.
package logind

import (
	"context"
	"fmt"
	"os"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/godbus/dbus/v5"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/unixauth"
)

// login1 names the logind service and the interfaces this package calls.
const (
	login1Name    = "org.freedesktop.login1"
	login1Path    = dbus.ObjectPath("/org/freedesktop/login1")
	login1Manager = "org.freedesktop.login1.Manager"
	login1Session = "org.freedesktop.login1.Session"
)

// displayManager names the freedesktop display-manager interface SDDM and
// LightDM both serve. It is only ever the fallback for showing the greeter on a
// seat whose greeter session logind cannot see.
const (
	dmName      = "org.freedesktop.DisplayManager"
	dmSeatIface = "org.freedesktop.DisplayManager.Seat"
	dmPathBase  = "/org/freedesktop/DisplayManager/"

	gdmName         = "org.gnome.DisplayManager"
	gdmFactoryPath  = dbus.ObjectPath("/org/gnome/DisplayManager/LocalDisplayFactory")
	gdmFactoryIface = "org.gnome.DisplayManager.LocalDisplayFactory"
)

// Sessions is the logind-backed session capability.
type Sessions struct {
	keyboard autoapi.Keyboard
}

// New returns the logind session backend. It opens nothing here: the system bus
// connection is shared and cached by godbus, and a host without logind should
// fail at the call that needed it rather than at construction.
func New() *Sessions { return &Sessions{} }

// SetKeyboard supplies the target session's input path for lock clients that
// do not implement logind's Unlock signal. Hyprlock accepts credentials only
// through its focused PAM prompt.
func (s *Sessions) SetKeyboard(keyboard autoapi.Keyboard) { s.keyboard = keyboard }

// Available reports whether logind is answering on the system bus, which is how
// the parent package decides between this backend and the utmp one a host
// without systemd gets.
func Available() bool {
	conn, err := dbus.SystemBus()
	if err != nil {
		return false
	}
	var has bool
	err = conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, login1Name).Store(&has)
	return err == nil && has
}

// sessionEntry is one row of logind's ListSessions reply.
type sessionEntry struct {
	ID   string
	UID  uint32
	User string
	Seat string
	Path dbus.ObjectPath
}

// List reports every session logind knows about, greeter and service-manager
// sessions included: a caller asking to show the login screen needs to see the
// greeter, and one deciding where the desktop is needs to tell it apart from
// the rest.
func (s *Sessions) List(ctx context.Context) ([]autoapi.SessionInfo, error) {
	conn, err := dbus.SystemBus()
	if err != nil {
		return nil, fmt.Errorf("connecting to the system bus: %w", err)
	}
	var entries []sessionEntry
	err = conn.Object(login1Name, login1Path).
		CallWithContext(ctx, login1Manager+".ListSessions", 0).Store(&entries)
	if err != nil {
		return nil, fmt.Errorf("logind refused ListSessions: %w", err)
	}

	out := make([]autoapi.SessionInfo, 0, len(entries))
	for _, e := range entries {
		info := autoapi.SessionInfo{ID: e.ID, UID: int(e.UID), User: e.User, Seat: e.Seat}
		// A session that vanished between the listing and the property read is
		// not an error: it ended, which is exactly what the caller wanted to
		// know, so it is dropped rather than failing the whole listing.
		if !readProperties(ctx, conn.Object(login1Name, e.Path), &info) {
			continue
		}
		out = append(out, info)
	}
	return out, nil
}

// readProperties fills in what only the session object reports. It returns
// false when the session is gone.
func readProperties(ctx context.Context, obj dbus.BusObject, info *autoapi.SessionInfo) bool {
	var props map[string]dbus.Variant
	err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.GetAll", 0, login1Session).Store(&props)
	if err != nil {
		return false
	}
	str := func(key string) string {
		v, ok := props[key]
		if !ok {
			return ""
		}
		s, _ := v.Value().(string)
		return s
	}
	boolean := func(key string) (bool, bool) {
		v, ok := props[key]
		if !ok {
			return false, false
		}
		b, ok := v.Value().(bool)
		return b, ok
	}

	info.TTY = str("TTY")
	info.Display = str("Display")
	info.Type = str("Type")
	info.Class = str("Class")
	info.State = str("State")
	info.Active, _ = boolean("Active")
	info.Remote, _ = boolean("Remote")
	info.Locked, info.LockKnown = boolean("LockedHint")
	if v, ok := props["Timestamp"]; ok {
		if micros, ok := v.Value().(uint64); ok && micros > 0 {
			info.Since = time.UnixMicro(int64(micros))
		}
	}
	return true
}

// Act performs the action on the named session. Greeter is the one that is not
// a method on the session: logind registers the login screen as a session of
// its own, so putting it on the seat is activating that one, and only a seat
// whose greeter logind cannot see falls through to the display manager.
func (s *Sessions) Act(ctx context.Context, target autoapi.SessionInfo, action autoapi.SessionAction) error {
	conn, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("connecting to the system bus: %w", err)
	}

	var method string
	switch action {
	case autoapi.SessionActivate:
		method = "Activate"
	case autoapi.SessionLock:
		method = "Lock"
	case autoapi.SessionUnlock:
		method = "Unlock"
	case autoapi.SessionLogout:
		method = "Terminate"
	case autoapi.SessionGreeter:
		return s.showGreeter(ctx, conn, target)
	default:
		return fmt.Errorf("unknown session action %q", action)
	}

	var path dbus.ObjectPath
	err = conn.Object(login1Name, login1Path).
		CallWithContext(ctx, login1Manager+".GetSession", 0, target.ID).Store(&path)
	if err != nil {
		return fmt.Errorf("logind does not know session %s: %w", target.ID, err)
	}
	if call := conn.Object(login1Name, path).CallWithContext(ctx, login1Session+"."+method, 0); call.Err != nil {
		return fmt.Errorf("logind refused %s on session %s: %w", strings.ToLower(method), target.ID, permissionHint(call.Err))
	}
	return nil
}

// Login passes the account's password through the host's PAM login stack, then
// dismisses the target session's lock, switching to it first when the action is
// a login. PAM gives encrypted-home, network-identity, and lockout modules the
// original credential before the privileged logind action runs.
func (s *Sessions) Login(ctx context.Context, req autoapi.LoginRequest) error {
	if req.User == "" {
		return fmt.Errorf("no account to authenticate")
	}
	ok, err := unixauth.AuthenticateUser(req.User, req.Secret)
	if err != nil {
		return fmt.Errorf("authenticating %s: %w", req.User, err)
	}
	if !ok {
		return fmt.Errorf("the password for %s is incorrect", req.User)
	}
	if req.Target.ID == "" {
		return fmt.Errorf("%w: Linux can switch and unlock only an existing graphical session for %s",
			autoapi.ErrUnsupported, req.User)
	}
	if err := refuseParkedHyprlock(req, lockerRunning(req.Target.UID, "hyprlock")); err != nil {
		return err
	}

	// A login switches to the session first; an unlock acts on it in place. A
	// session already in the foreground needs no switch.
	if req.Action == autoapi.SessionLogin && !req.Target.Active {
		if err := s.Act(ctx, req.Target, autoapi.SessionActivate); err != nil {
			return fmt.Errorf("switching to %s's session: %w", req.User, err)
		}
	}
	if lockerRunning(req.Target.UID, "hyprlock") {
		if err := s.typeIntoHyprlock(ctx, req.Secret, req.Target); err != nil {
			return err
		}
	}
	if err := s.Act(ctx, req.Target, autoapi.SessionUnlock); err != nil {
		return fmt.Errorf("unlocking %s's session: %w", req.User, err)
	}
	return nil
}

// refuseParkedHyprlock refuses a credentialed action on a session whose
// hyprlock is running while the session is not in the foreground. The
// password reaches hyprlock only as typed input, and a parked session takes no
// input; an unlock of a parked session would type into the foreground one.
func refuseParkedHyprlock(req autoapi.LoginRequest, hyprlock bool) error {
	if !req.Target.Active && hyprlock {
		return fmt.Errorf("%w: hyprlock cannot accept automation input while its session is parked", autoapi.ErrUnsupported)
	}
	return nil
}

func (s *Sessions) typeIntoHyprlock(ctx context.Context, secret string, target autoapi.SessionInfo) error {
	keyboard := s.keyboard
	if keyboard == nil {
		return fmt.Errorf("unlocking hyprlock for uid %d: the session has no keyboard input path", target.UID)
	}
	if err := keyboard.PressKeys(ctx, autoapi.KeyStroke{Key: "backspace"}); err != nil {
		return fmt.Errorf("focusing hyprlock's password field: %w", err)
	}
	if err := waitForInputFocus(ctx, 250*time.Millisecond); err != nil {
		return err
	}
	if err := keyboard.PressKeys(ctx, autoapi.KeyStroke{Mods: []string{"ctrl"}, Key: "u"}); err != nil {
		return fmt.Errorf("clearing hyprlock's password field: %w", err)
	}
	if err := keyboard.TypeText(ctx, autoapi.RealClock{}, secret, 0); err != nil {
		return fmt.Errorf("typing into hyprlock: %w", err)
	}
	if err := keyboard.PressKeys(ctx, autoapi.KeyStroke{Key: "return"}); err != nil {
		return fmt.Errorf("submitting to hyprlock: %w", err)
	}

	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for lockerRunning(target.UID, "hyprlock") {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return fmt.Errorf("hyprlock did not dismiss after credential submission")
		case <-ticker.C:
		}
	}
	return nil
}

func waitForInputFocus(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func lockerRunning(uid int, name string) bool {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := "/proc/" + entry.Name()
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || int(stat.Uid) != uid {
			continue
		}
		comm, err := os.ReadFile(path + "/comm")
		if err == nil && strings.TrimSpace(string(comm)) == name {
			return true
		}
	}
	return false
}

// showGreeter puts the login screen on the target's seat, preferring the
// greeter session logind already has over asking the display manager: the
// session is there on every desktop, while the display-manager interface is
// SDDM's and LightDM's and GDM serves an incompatible one of its own.
func (s *Sessions) showGreeter(ctx context.Context, conn *dbus.Conn, target autoapi.SessionInfo) error {
	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.Class != autoapi.SessionClassGreeter {
			continue
		}
		if target.Seat != "" && sess.Seat != target.Seat {
			continue
		}
		return s.Act(ctx, sess, autoapi.SessionActivate)
	}

	seat := target.Seat
	if seat == "" {
		seat = "seat0"
	}
	obj := conn.Object(dmName, dbus.ObjectPath(dmPathBase+seatObjectName(seat)))
	if call := obj.CallWithContext(ctx, dmSeatIface+".SwitchToGreeter", 0); call.Err != nil {
		standardErr := permissionHint(call.Err)
		if seat == "seat0" {
			var display dbus.ObjectPath
			gdmCall := conn.Object(gdmName, gdmFactoryPath).
				CallWithContext(ctx, gdmFactoryIface+".CreateTransientDisplay", 0)
			if gdmCall.Err == nil {
				if err := gdmCall.Store(&display); err == nil && display.IsValid() {
					return nil
				}
			}
			if gdmCall.Err != nil {
				return fmt.Errorf("no greeter session on %s: the standard display-manager interface returned %v; GDM returned %w",
					seat, standardErr, permissionHint(gdmCall.Err))
			}
		}
		return fmt.Errorf("no greeter session on %s and the display manager refused to show one: %w",
			seat, standardErr)
	}
	return nil
}

// seatObjectName maps a seat name onto the display manager's object name for
// it: "seat0" is published at Seat0, the capitalization LightDM established and
// SDDM copied.
func seatObjectName(seat string) string {
	var b strings.Builder
	for i, r := range seat {
		switch {
		case i == 0:
			b.WriteRune(unicode.ToUpper(r))
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

// permissionHint rewrites polkit's refusal into the thing an operator can act
// on. logind reports it as an interactive-authorization failure, which reads
// like a bug until you know the agent simply is not privileged.
func permissionHint(err error) error {
	var dbusErr dbus.Error
	if !asDBusError(err, &dbusErr) {
		return err
	}
	switch dbusErr.Name {
	case "org.freedesktop.DBus.Error.InteractiveAuthorizationRequired",
		"org.freedesktop.DBus.Error.AccessDenied":
		return fmt.Errorf("%w: acting on another user's session needs an agent running as root", err)
	}
	return err
}

// asDBusError unwraps a D-Bus error, which godbus returns by value.
func asDBusError(err error, out *dbus.Error) bool {
	for err != nil {
		if e, ok := err.(dbus.Error); ok {
			*out = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		u, ok := err.(unwrapper)
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}
