//go:build !windows && !darwin

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestMergeDesktopEnvironmentDropsLoginSessionKeys locks that a resident
// started from an SSH login joins the desktop's session and nothing of the
// login's. pam_systemd gives the login its own XDG_SESSION_ID and ssh -X a
// forwarded DISPLAY. A desktop process that carries neither variable, as on a
// Wayland compositor with no XWayland, replaced nothing, so the resident kept
// both: its X fallbacks reached the SSH client's display and its logind calls
// named the SSH session.
func TestMergeDesktopEnvironmentDropsLoginSessionKeys(t *testing.T) {
	for key := range desktopEnvKeys {
		t.Setenv(key, "")
		os.Unsetenv(key)
	}
	t.Setenv("DISPLAY", "localhost:10.0")
	t.Setenv("XDG_SESSION_ID", "42")
	t.Setenv("XDG_SESSION_TYPE", "tty")
	t.Setenv("SSH_CONNECTION", "10.0.0.5 51000 10.0.0.9 22")
	t.Setenv("LANG", "en_US.UTF-8")

	env := mergeDesktopEnvironment(map[string]string{
		"WAYLAND_DISPLAY":          "wayland-1",
		"XDG_RUNTIME_DIR":          "/run/user/1000",
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=/run/user/1000/bus",
		"XDG_SESSION_TYPE":         "wayland",
	}, desktopEnvKeys)

	assert.Contains(t, env, "WAYLAND_DISPLAY=wayland-1", "The desktop's display must be adopted.")
	assert.Contains(t, env, "XDG_SESSION_TYPE=wayland", "A key both sides carry takes the desktop's value.")
	assert.NotContains(t, env, "XDG_SESSION_TYPE=tty", "The login's value must not survive beside the desktop's.")
	assert.NotContains(t, env, "DISPLAY=localhost:10.0", "A forwarded display belongs to the SSH client, not this desktop.")
	assert.NotContains(t, env, "XDG_SESSION_ID=42", "The SSH login's session is not the desktop session.")
	assert.NotContains(t, env, "SSH_CONNECTION=10.0.0.5 51000 10.0.0.9 22", "The resident must not look like an SSH command.")
	assert.Contains(t, env, "LANG=en_US.UTF-8", "Variables that place no session are inherited.")
}
