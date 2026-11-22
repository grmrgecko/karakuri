//go:build !windows

package main

import (
	"os"
	"testing"

	"github.com/grmrgecko/karakuri/automation/desktopenv"
	"github.com/stretchr/testify/assert"
)

// TestDrivesDesktop pins which commands attach to a graphical session over SSH.
// The session verbs and the hidden helpers must not, or a detached login would
// scan for a desktop before locking a screen or serving a helper that never
// needed one. File-based image-match and sleep must also run in process, or an
// SSH login on a host with no graphical session forwards them to a resident
// that does not exist and fails.
func TestDrivesDesktop(t *testing.T) {
	for _, cmd := range []string{"type", "screenshot", "windows", "run", "click", "script", "snapshot", "copy"} {
		assert.True(t, drivesDesktop(cmd), "%q should attach to the desktop", cmd)
		assert.False(t, runsInProcess(cmd), "%q should forward to a resident when no session is attached", cmd)
	}
	for _, cmd := range []string{"version", "sessions", "lock", "unlock", "login", "logout", "activate", "greeter", "serve-helper", "secure-input", "resident", "__resident-stop", "image-match", "sleep"} {
		assert.False(t, drivesDesktop(cmd), "%q must not attach to the desktop", cmd)
	}
	for _, cmd := range []string{"version", "sessions", "unlock", "serve-helper", "image-match", "sleep"} {
		assert.True(t, runsInProcess(cmd), "%q must never forward to a resident", cmd)
	}
}

// TestApplyDesktopEnv adopts the session's variables and drops the SSH ones, so
// the command drives the desktop and a program it launches does not inherit the
// forwarding session.
func TestApplyDesktopEnv(t *testing.T) {
	// applyDesktopEnv rewrites every session key, so each one is registered
	// for restore or the rest of the package's tests would run in the session
	// this test invents.
	for _, key := range desktopenv.Keys {
		t.Setenv(key, os.Getenv(key))
	}
	t.Setenv("SSH_CONNECTION", "10.0.0.5 51000 10.0.0.9 22")
	t.Setenv("SSH_TTY", "/dev/pts/9")
	t.Setenv("SSH_CLIENT", "10.0.0.5 51000 22")
	t.Setenv("DISPLAY", "localhost:10.0") // an ssh -X forward the session must replace
	t.Setenv("XAUTHORITY", "/tmp/stale-xauth")
	t.Setenv("LANG", "en_US.UTF-8")

	applyDesktopEnv(desktopenv.Result{Env: map[string]string{
		"WAYLAND_DISPLAY":          "wayland-0",
		"XDG_RUNTIME_DIR":          "/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
	}})

	for _, key := range []string{"SSH_CONNECTION", "SSH_TTY", "SSH_CLIENT", "DISPLAY", "XAUTHORITY"} {
		_, ok := os.LookupEnv(key)
		assert.False(t, ok, "%s must be dropped", key)
	}
	assert.Equal(t, "wayland-0", os.Getenv("WAYLAND_DISPLAY"))
	assert.Equal(t, "/run/user/1000", os.Getenv("XDG_RUNTIME_DIR"))
	assert.Equal(t, "unix:path=/run/user/1000/bus", os.Getenv("DBUS_SESSION_BUS_ADDRESS"))
	assert.Equal(t, "en_US.UTF-8", os.Getenv("LANG"), "the login's locale stands when the session names none")
}
