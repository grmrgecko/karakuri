//go:build linux

package automation

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation/desktopenv"
	"github.com/grmrgecko/karakuri/automation/internal/logind"
	"github.com/grmrgecko/karakuri/automation/internal/nssuser"
	"github.com/grmrgecko/karakuri/automation/internal/session"
)

// findDelegateTarget picks the desktop a privileged process should act on, or
// reports that there is nothing to delegate to. Delegation is opt-in: a caller
// that named no helper command gets none, because re-executing somebody's
// binary with arguments they did not choose is not a decision this module makes
// for them.
func findDelegateTarget(ctx context.Context, opts Options) (DelegateTarget, bool) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if len(opts.HelperCommand) == 0 {
		return DelegateTarget{}, false
	}
	// A non-root caller can reach only its own session, and only needs to when
	// it was started away from one. Attaching to it takes no privilege and no
	// helper of somebody else's, so it is handled separately from the root path
	// that has to become another user to touch their desktop.
	if os.Geteuid() != 0 {
		return findSameUserAttach(ctx, logf)
	}
	if !logind.Available() {
		logf("running as root with no logind to name the desktop session; desktop capabilities will fail")
		return DelegateTarget{}, false
	}

	sessions, err := logind.New().List(ctx)
	if err != nil {
		logf("listing sessions to find a desktop to delegate to: %v", err)
		return DelegateTarget{}, false
	}
	chosen, ok := pickDesktopSession(sessions)
	if !ok {
		logf("running as root and no user is logged in to a desktop yet; the session helper starts when one is")
		return pendingTarget(), true
	}

	target, err := buildDelegateTarget(chosen.ID, chosen.UID, chosen.Type)
	if err != nil {
		logf("preparing the session helper for %s: %v", chosen.User, err)
		return DelegateTarget{}, false
	}
	logf("delegating desktop capabilities to %s", target)
	return target, true
}

// pickDesktopSession lives in delegate.go, shared with the macOS side, which
// picks from the window server's session table the same way.

// wirePerSessionHelpers is a no-op on Linux: logind serves every session's
// verbs to the privileged parent directly, so no per-session helpers exist.
func wirePerSessionHelpers(s *Session, opts Options, target DelegateTarget, h *helper) {}

// buildDelegateTarget assembles what the child needs to be that user in that
// session. The base comes from the account and from logind's own runtime path,
// which are always right; the display settings are scraped from a process
// already in the session, because the compositor's socket name and the X
// authority file are known to it and to nothing else.
func buildDelegateTarget(sessionID string, uid int, sessionType string) (DelegateTarget, error) {
	u, err := nssuser.LookupID(uint32(uid))
	if err != nil {
		return DelegateTarget{}, err
	}

	runtimeDir := filepath.Join("/run/user", strconv.Itoa(uid))
	env := map[string]string{
		"HOME":                     u.HomeDir,
		"USER":                     u.Name,
		"LOGNAME":                  u.Name,
		"SHELL":                    "/bin/sh",
		"PATH":                     "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"XDG_RUNTIME_DIR":          runtimeDir,
		"DBUS_SESSION_BUS_ADDRESS": "unix:path=" + filepath.Join(runtimeDir, "bus"),
		"XDG_SESSION_ID":           sessionID,
		"XDG_SESSION_TYPE":         sessionType,
		// The portal's restore token has to live somewhere the delegated user
		// owns, which a privileged agent's own state directory is not. Keep it
		// under the user's config directory so logout does not discard consent.
		EnvHelperTokenDir: filepath.Join(u.HomeDir, ".config", "karakuri", "automation"),
	}
	for key, value := range scrapeSessionEnv(uid, sessionID) {
		env[key] = value
	}

	target := DelegateTarget{
		UID:        uid,
		GID:        int(u.GID),
		User:       u.Name,
		Home:       u.HomeDir,
		SessionID:  sessionID,
		Display:    sessionType,
		Compositor: session.CompositorFromEnvironment(env),
	}
	target.Groups = append(target.Groups, u.GroupIDs...)
	target.Env = make([]string, 0, len(env))
	for key, value := range env {
		if value != "" {
			target.Env = append(target.Env, key+"="+value)
		}
	}
	sort.Strings(target.Env)
	return target, nil
}

// lookupSessionUser resolves the account fields a helper needs to be uid,
// through NSS so a homed or SSSD user is found the same as one in /etc/passwd.
func lookupSessionUser(uid int) (name, home string, gid int, groups []uint32, err error) {
	u, err := nssuser.LookupID(uint32(uid))
	if err != nil {
		return "", "", 0, nil, err
	}
	return u.Name, u.HomeDir, int(u.GID), u.GroupIDs, nil
}

// scrapeSessionEnv reads the display settings out of a process already in the
// user's session. logind names the runtime directory and the session type but
// not which Wayland socket the compositor opened or where the X authority file
// went, and those are what a helper cannot join the session without.
func scrapeSessionEnv(uid int, sessionID string) map[string]string {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var matching, fallback map[string]string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		if !ownedBy(filepath.Join("/proc", entry.Name()), uid) {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "environ"))
		if err != nil {
			continue
		}
		found := wantedEnv(raw)
		if found["WAYLAND_DISPLAY"] == "" && found["DISPLAY"] == "" {
			continue
		}
		// One user can have old or concurrent graphical sessions. Prefer a
		// process that logind assigned to the session selected above instead of
		// taking the first process with a display, which may describe a desktop
		// that has already been replaced.
		if found["XDG_SESSION_ID"] == sessionID {
			if found["DBUS_SESSION_BUS_ADDRESS"] != "" {
				return found
			}
			if matching == nil {
				matching = found
			}
			continue
		}
		if fallback == nil || (fallback["DBUS_SESSION_BUS_ADDRESS"] == "" && found["DBUS_SESSION_BUS_ADDRESS"] != "") {
			fallback = found
		}
	}
	if matching != nil {
		return matching
	}
	return fallback
}

// ownedBy reports whether a /proc entry belongs to the uid.
func ownedBy(dir string, uid int) bool {
	info, err := os.Stat(dir)
	if err != nil {
		return false
	}
	stat, ok := statUID(info)
	return ok && stat == uid
}

// wantedEnv pulls the session settings out of a NUL-separated environ block.
// The keys are desktopenv's: everything else about the parent's environment is
// deliberately dropped, because a privileged agent's own runtime directory and
// bus address are exactly what would send the child to the wrong session.
func wantedEnv(raw []byte) map[string]string {
	want := make(map[string]bool, len(desktopenv.Keys))
	for _, key := range desktopenv.Keys {
		want[key] = true
	}
	out := make(map[string]string, len(desktopenv.Keys))
	for _, entry := range strings.Split(string(raw), "\x00") {
		key, value, ok := strings.Cut(entry, "=")
		if ok && want[key] && value != "" {
			out[key] = value
		}
	}
	return out
}
