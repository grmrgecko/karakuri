//go:build linux

package desktopenv

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"syscall"

	"github.com/grmrgecko/karakuri/automation/internal/logind"
)

// Discover finds the live graphical session belonging to uid by reading the
// environment of that user's own running processes out of /proc. It reads no
// display and starts no process, so a caller that only wants to know whether a
// desktop is reachable pays nothing but the scan. ok is false when the user has
// no live graphical session.
func Discover(ctx context.Context, uid int) (Result, bool) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return Result{}, false
	}
	var candidates []map[string]string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		dir := filepath.Join("/proc", entry.Name())
		if !ownedBy(dir, uid) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, "environ"))
		if err != nil {
			continue
		}
		env := wantedEnv(raw)
		if env["WAYLAND_DISPLAY"] == "" && env["DISPLAY"] == "" {
			continue
		}
		candidates = append(candidates, env)
	}
	return choose(candidates, activeSessionID(ctx, uid))
}

// ownedBy reports whether a /proc entry belongs to the uid.
func ownedBy(dir string, uid int) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(stat.Uid) == uid
}

// activeSessionID asks logind which graphical session the user is actually at,
// so a nested Xephyr, a VNC server, or a leftover from a fast-user switch does
// not outrank the desktop the person is sitting in front of. It returns "" when
// logind is not running or names no active graphical session, and the caller
// falls back to ranking the candidates on their own merits.
func activeSessionID(ctx context.Context, uid int) string {
	if !logind.Available() {
		return ""
	}
	sessions, err := logind.New().List(ctx)
	if err != nil {
		return ""
	}
	fallback := ""
	for _, s := range sessions {
		if s.UID != uid || !s.Graphical() {
			continue
		}
		if s.Active {
			return s.ID
		}
		if fallback == "" {
			fallback = s.ID
		}
	}
	return fallback
}
