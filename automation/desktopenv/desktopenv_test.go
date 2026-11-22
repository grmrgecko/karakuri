package desktopenv

import (
	"net"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// liveWayland binds a Wayland-style socket in a runtime dir and returns the
// environment that reaches it, so a test can exercise the liveness gate against
// a socket that really answers rather than one that only exists as a name.
func liveWayland(t *testing.T) map[string]string {
	t.Helper()
	dir := t.TempDir()
	ln, err := net.Listen("unix", filepath.Join(dir, "wayland-1"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })
	return map[string]string{"WAYLAND_DISPLAY": "wayland-1", "XDG_RUNTIME_DIR": dir}
}

// TestWantedEnv keeps only the session variables and preserves values that carry
// a space, which a compositor hint or a locale can.
func TestWantedEnv(t *testing.T) {
	raw := []byte("HOME=/home/x\x00DISPLAY=:0\x00XDG_CURRENT_DESKTOP=ubuntu:GNOME\x00LANG=en_US.UTF-8\x00SECRET=drop-me\x00EMPTY=\x00")
	got := wantedEnv(raw)
	assert.Equal(t, ":0", got["DISPLAY"])
	assert.Equal(t, "ubuntu:GNOME", got["XDG_CURRENT_DESKTOP"])
	assert.Equal(t, "en_US.UTF-8", got["LANG"])
	assert.NotContains(t, got, "HOME", "a variable outside the wanted set must be dropped")
	assert.NotContains(t, got, "SECRET")
	assert.NotContains(t, got, "EMPTY", "an empty value must not be carried")
}

// TestDeriveBus fills the session bus address from the runtime directory only
// when the desktop left it implicit and the socket is really there.
func TestDeriveBus(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "bus"), nil, 0600))
	env := map[string]string{"XDG_RUNTIME_DIR": dir}
	deriveBus(env)
	assert.Equal(t, "unix:path="+filepath.Join(dir, "bus"), env["DBUS_SESSION_BUS_ADDRESS"])

	explicit := map[string]string{"XDG_RUNTIME_DIR": dir, "DBUS_SESSION_BUS_ADDRESS": "unix:path=/kept"}
	deriveBus(explicit)
	assert.Equal(t, "unix:path=/kept", explicit["DBUS_SESSION_BUS_ADDRESS"], "an explicit address must be left alone")

	missing := map[string]string{"XDG_RUNTIME_DIR": filepath.Join(dir, "nope")}
	deriveBus(missing)
	assert.NotContains(t, missing, "DBUS_SESSION_BUS_ADDRESS", "no socket means no address is invented")
}

// TestLiveDisplay treats a named-but-dead socket as no display and a socket that
// answers as live, which is what tells an ssh -X forward or a stale value apart
// from a real session.
func TestLiveDisplay(t *testing.T) {
	assert.True(t, liveDisplay(liveWayland(t)))
	assert.False(t, liveDisplay(map[string]string{"WAYLAND_DISPLAY": "wayland-9", "XDG_RUNTIME_DIR": t.TempDir()}),
		"a Wayland socket that is not there is not a live display")
	assert.False(t, liveDisplay(map[string]string{"DISPLAY": "localhost:10.0"}),
		"an ssh -X style forward names no local socket and must read as detached")
	assert.False(t, liveDisplay(map[string]string{"DISPLAY": ":42"}),
		"an X display whose socket is absent is not live")
}

// TestChoosePrefersActiveSession picks the session a manager named as active over
// one that merely scores higher on its own, so a leftover or nested session does
// not outrank the desktop the person is at.
func TestChoosePrefersActiveSession(t *testing.T) {
	active := liveWayland(t)
	active["XDG_SESSION_ID"] = "2"
	// A second live session that would win the heuristic (also Wayland) but is
	// not the one logind calls active.
	other := liveWayland(t)
	other["XDG_SESSION_ID"] = "7"
	other["EXTRA"] = "x" // one more key, so it outscores active without the preference

	got, ok := choose([]map[string]string{other, active}, "2")
	require.True(t, ok)
	assert.Equal(t, "2", got.SessionID, "the manager's active session must win")

	// With no preference, the higher-scoring candidate wins.
	got, ok = choose([]map[string]string{active, other}, "")
	require.True(t, ok)
	assert.Equal(t, "7", got.SessionID)
}

// TestChooseSkipsDeadCandidates ignores a candidate whose display socket does not
// answer, even when it carries more variables than a live one.
func TestChooseSkipsDeadCandidates(t *testing.T) {
	dead := map[string]string{"WAYLAND_DISPLAY": "wayland-dead", "XDG_RUNTIME_DIR": t.TempDir(), "XDG_SESSION_ID": "dead", "LANG": "C"}
	live := liveWayland(t)
	live["XDG_SESSION_ID"] = "live"
	got, ok := choose([]map[string]string{dead, live}, "")
	require.True(t, ok)
	assert.Equal(t, "live", got.SessionID)

	_, ok = choose([]map[string]string{dead}, "")
	assert.False(t, ok, "no live candidate means nothing to attach to")
}

// TestChooseDerivesBus makes the chosen environment self-contained: the bus
// address is present even when the winning process carried only the runtime dir.
func TestChooseDerivesBus(t *testing.T) {
	env := liveWayland(t)
	require.NoError(t, os.WriteFile(filepath.Join(env["XDG_RUNTIME_DIR"], "bus"), nil, 0600))
	got, ok := choose([]map[string]string{env}, "")
	require.True(t, ok)
	assert.Equal(t, "unix:path="+filepath.Join(env["XDG_RUNTIME_DIR"], "bus"), got.Env["DBUS_SESSION_BUS_ADDRESS"])
	assert.Equal(t, "wayland", got.Display())
}

// TestParseProcstatEnv reads the FreeBSD procstat output shape: a header line,
// then pid, command, and space-separated KEY=VALUE environment. Only the session
// variables are kept, and a command name with a space does not derail the read.
func TestParseProcstatEnv(t *testing.T) {
	out := []byte("  PID COMM             ENVIRONMENT\n" +
		" 1234 kwin_wayland     HOME=/home/x WAYLAND_DISPLAY=wayland-0 XDG_RUNTIME_DIR=/var/run/user/1001 DBUS_SESSION_BUS_ADDRESS=unix:path=/var/run/user/1001/bus XDG_CURRENT_DESKTOP=KDE SHLVL=1\n")
	env := parseProcstatEnv(out)
	assert.Equal(t, "wayland-0", env["WAYLAND_DISPLAY"])
	assert.Equal(t, "/var/run/user/1001", env["XDG_RUNTIME_DIR"])
	assert.Equal(t, "unix:path=/var/run/user/1001/bus", env["DBUS_SESSION_BUS_ADDRESS"])
	assert.Equal(t, "KDE", env["XDG_CURRENT_DESKTOP"])
	assert.NotContains(t, env, "HOME")
	assert.NotContains(t, env, "SHLVL")

	// No graphical process: nothing wanted survives.
	assert.Empty(t, parseProcstatEnv([]byte("  PID COMM ENVIRONMENT\n 5 sh HOME=/root PATH=/bin\n")))
	// Header only, or empty, yields nothing rather than panicking.
	assert.Empty(t, parseProcstatEnv([]byte("  PID COMM ENVIRONMENT\n")))
	assert.Empty(t, parseProcstatEnv(nil))
}
