// Package dbusconn opens the session bus without ever starting one.
//
// godbus falls back to running dbus-launch when it cannot find an address,
// which is right for a desktop program and wrong for everything here: a process
// with no session — a system service, or the privileged half of a delegating
// agent — would get a private bus of its own that no desktop service is on.
// Every probe against it answers "nobody owns that name", so the capability
// fails anyway, and a stray dbus-daemon is left running under the unit for as
// long as it lives. Refusing to connect says the same thing without the
// daemon.
package dbusconn

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/godbus/dbus/v5"
)

// ErrNoSessionBus reports that this process is not in a session with a bus,
// which is a different failure from a bus that refused the connection.
var ErrNoSessionBus = errors.New("automation: this process is not in a desktop session with a message bus")

// Available reports whether an address to connect to exists, checking the two
// places godbus itself looks before it resorts to launching one.
func Available() bool {
	if strings.TrimSpace(os.Getenv("DBUS_SESSION_BUS_ADDRESS")) != "" {
		return true
	}
	dir := strings.TrimSpace(os.Getenv("XDG_RUNTIME_DIR"))
	if dir == "" {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "bus"))
	return err == nil
}

// Session returns the shared session-bus connection, or ErrNoSessionBus when
// there is no session to connect to.
func Session() (*dbus.Conn, error) {
	if !Available() {
		return nil, ErrNoSessionBus
	}
	return dbus.SessionBus()
}

// SessionPrivate returns a connection of the caller's own, for a backend that
// needs its own signal stream rather than the shared one.
func SessionPrivate() (*dbus.Conn, error) {
	if !Available() {
		return nil, ErrNoSessionBus
	}
	return dbus.SessionBusPrivate()
}
