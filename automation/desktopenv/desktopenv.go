// Package desktopenv discovers the environment a process needs to join a user's
// live graphical session, without a display of its own.
//
// A login over SSH, a cron job, or a service starts with no DISPLAY, no
// WAYLAND_DISPLAY, and a session bus that is not the desktop's. The X server,
// the Wayland compositor, and D-Bus are all reached by their client libraries
// through environment variables, so a caller that wants to drive the desktop it
// is not sitting at has to learn those variables from a process that already is.
// Discover reads them out of the user's own running processes and hands back a
// self-contained set, with the session bus address filled in when the desktop
// left it implicit.
//
// Discover never mutates the caller's environment. A one-shot command may apply
// the result to its own process; a long-lived process hands it to a helper it
// starts in the session instead, so its own environment is never rewritten under
// the goroutines reading it.
package desktopenv

import (
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Keys are the variables that place a process in a graphical session: the two
// display sockets, the X authority, the runtime directory, and the session bus,
// plus the compositor hints the backends read to tell which desktop they are on
// and the locale a dialog renders in. Everything outside this set is dropped on
// purpose, because a detached caller's own runtime directory and bus address are
// exactly what would send a backend to the wrong session.
var Keys = []string{
	"DISPLAY",
	"WAYLAND_DISPLAY",
	"XAUTHORITY",
	"XDG_RUNTIME_DIR",
	"DBUS_SESSION_BUS_ADDRESS",
	"XDG_SESSION_ID",
	"XDG_SESSION_TYPE",
	"XDG_SESSION_DESKTOP",
	"XDG_CURRENT_DESKTOP",
	"XDG_SEAT",
	"XDG_VTNR",
	"DESKTOP_SESSION",
	"KDE_FULL_SESSION",
	"HYPRLAND_INSTANCE_SIGNATURE",
	"SWAYSOCK",
	"LANG",
}

// Result is a discovered graphical session's environment.
type Result struct {
	// Env is the self-contained set of variables that joins the session. The
	// session bus address is always present, derived from the runtime directory
	// when the desktop left it to the default.
	Env map[string]string
	// SessionID is the logind session the environment belongs to, when a session
	// manager named one; empty otherwise.
	SessionID string
}

// Display reports whether the discovered session is Wayland or X11, or "" when
// neither socket is named.
func (r Result) Display() string {
	if r.Env["WAYLAND_DISPLAY"] != "" {
		return "wayland"
	}
	if r.Env["DISPLAY"] != "" {
		return "x11"
	}
	return ""
}

// Attached reports whether this process already sits in a live graphical
// session, so a caller can tell a detached login apart from one run from a
// desktop terminal. A DISPLAY or WAYLAND_DISPLAY that names a dead server does
// not count: an ssh -X forward and a stale value both leave the variable set
// while no compositor is there to answer, and attaching to a discovered session
// is the right move in both cases.
func Attached() bool {
	env := map[string]string{}
	for _, key := range Keys {
		if v := os.Getenv(key); v != "" {
			env[key] = v
		}
	}
	return liveDisplay(env)
}

// wantedEnv pulls the session variables out of a NUL-separated environ block,
// keeping the value verbatim so a compositor hint or locale that carries spaces
// survives intact.
func wantedEnv(raw []byte) map[string]string {
	want := make(map[string]bool, len(Keys))
	for _, key := range Keys {
		want[key] = true
	}
	out := make(map[string]string, len(Keys))
	for _, entry := range strings.Split(string(raw), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if ok && want[key] && value != "" {
			out[key] = value
		}
	}
	return out
}

// parseProcstatEnv keeps the session variables out of `procstat -e` output. That
// output prints one process per line as the pid, the command, then the whole
// environment as space-separated KEY=VALUE pairs, after a header line. A value
// carrying a space would split into a token with no '=', which is dropped; every
// variable this package wants is a socket path, an id, or a short token that
// carries none, so the whitelist read is exact for them. It is here rather than
// in the FreeBSD file so it can be tested on any host.
func parseProcstatEnv(out []byte) map[string]string {
	want := make(map[string]bool, len(Keys))
	for _, key := range Keys {
		want[key] = true
	}
	env := map[string]string{}
	lines := strings.Split(string(out), "\n")
	if len(lines) > 0 {
		lines = lines[1:] // drop the "PID COMM ENVIRONMENT" header
	}
	for _, line := range lines {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		for _, entry := range fields[2:] {
			key, value, ok := strings.Cut(entry, "=")
			if ok && want[key] && value != "" {
				env[key] = value
			}
		}
	}
	return env
}

// deriveBus fills in the session bus address from the runtime directory when the
// desktop left it implicit. Many graphical processes carry only XDG_RUNTIME_DIR
// and rely on the well-known $XDG_RUNTIME_DIR/bus socket, so a set scraped from
// one of them would otherwise reach no bus at all.
func deriveBus(env map[string]string) {
	if env["DBUS_SESSION_BUS_ADDRESS"] != "" {
		return
	}
	if runtime := env["XDG_RUNTIME_DIR"]; runtime != "" {
		if _, err := os.Stat(filepath.Join(runtime, "bus")); err == nil {
			env["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + filepath.Join(runtime, "bus")
		}
	}
}

// liveDisplay reports whether the environment names a display socket that
// answers. A Wayland socket or an X11 socket that a client can connect to is the
// proof a session is really there; a variable pointing at a socket that is gone
// is treated as no display, which is what an ssh -X forward or a session that
// ended leaves behind.
func liveDisplay(env map[string]string) bool {
	var path string
	if wayland := env["WAYLAND_DISPLAY"]; wayland != "" {
		path = wayland
		if !filepath.IsAbs(path) {
			path = filepath.Join(env["XDG_RUNTIME_DIR"], path)
		}
	} else {
		display := env["DISPLAY"]
		if !strings.HasPrefix(display, ":") {
			return false
		}
		number, _, _ := strings.Cut(display[1:], ".")
		if _, err := strconv.Atoi(number); err != nil {
			return false
		}
		path = filepath.Join("/tmp/.X11-unix", "X"+number)
		if auth := env["XAUTHORITY"]; auth != "" {
			if _, err := os.Stat(auth); err != nil {
				return false
			}
		}
	}
	conn, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// score ranks one candidate environment. A Wayland session outweighs an X11 one
// because an X display can be reached through XWayland from a Wayland desktop but
// not the other way around, a session with a bus address outweighs one without,
// and more of the wanted variables outweighs fewer. A candidate whose session id
// matches the one the caller preferred wins outright, so a session manager's
// idea of the active session beats the heuristic.
func score(env map[string]string, preferSession string) int {
	n := len(env)
	if preferSession != "" && env["XDG_SESSION_ID"] == preferSession {
		n += 1000
	}
	if env["WAYLAND_DISPLAY"] != "" {
		n += 10
	}
	if env["DISPLAY"] != "" {
		n += 5
	}
	if env["DBUS_SESSION_BUS_ADDRESS"] != "" {
		n += 3
	}
	return n
}

// choose picks the best live candidate, derives its bus address, and reports
// whether one was found. preferSession is the session id a session manager named
// as the caller's active graphical session, or "" when none is known.
func choose(candidates []map[string]string, preferSession string) (Result, bool) {
	best := map[string]string(nil)
	bestScore := -1
	bestSession := ""
	for _, env := range candidates {
		if env["WAYLAND_DISPLAY"] == "" && env["DISPLAY"] == "" {
			continue
		}
		derived := make(map[string]string, len(env))
		for k, v := range env {
			derived[k] = v
		}
		deriveBus(derived)
		if !liveDisplay(derived) {
			continue
		}
		if s := score(derived, preferSession); s > bestScore {
			best, bestScore, bestSession = derived, s, derived["XDG_SESSION_ID"]
		}
	}
	if best == nil {
		return Result{}, false
	}
	return Result{Env: best, SessionID: bestSession}, true
}
