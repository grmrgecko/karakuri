//go:build linux || freebsd

package automation

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/grmrgecko/karakuri/automation/desktopenv"
	"github.com/grmrgecko/karakuri/automation/internal/session"
)

// Attaching to one's own session is the unprivileged half of delegation. A root
// process must become the session's owner to reach the desktop, which takes a
// helper started as that user; a process that already is the owner but was
// started away from the display — over SSH, from a service, from cron — only
// lacks the environment that names the session. It scrapes that from a process
// already in the session and starts the helper with it, so a person's own
// automation over SSH needs no resident and no install: the session is theirs to
// join, and joining it is a matter of the right DISPLAY, Wayland socket, X
// authority, and bus address rather than of privilege.

// defaultAttachPath is the search path handed to a session helper. A login shell
// would set one, but the helper is not started through one, so it carries the
// base-system path the desktop tools live on.
const defaultAttachPath = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

// findSameUserAttach returns a delegate target for this user's own graphical
// session. It fires only when the process is not already sitting in a live
// session, so a caller run from a desktop terminal keeps composing locally and
// never spawns a helper. A detached caller with no session yet — a user service
// started at boot, before its owner logged in — gets a pending target, so the
// desktop is looked for again when the first call needs it.
func findSameUserAttach(ctx context.Context, logf func(string, ...any)) (DelegateTarget, bool) {
	if desktopenv.Attached() {
		return DelegateTarget{}, false
	}
	res, ok := desktopenv.Discover(ctx, os.Geteuid())
	if !ok {
		logf("no live graphical session for this user yet; the session helper starts when one is")
		return pendingTarget(), true
	}
	target, err := buildAttachTarget(os.Geteuid(), res)
	if err != nil {
		logf("preparing the session helper for the current user: %v", err)
		return DelegateTarget{}, false
	}
	logf("attaching desktop capabilities to %s", target)
	return target, true
}

// buildAttachTarget assembles what a helper needs to join uid's graphical
// session from a discovered environment. The display settings come from the
// session itself; the account basics come from the password database. It serves
// both the same-user attach and the FreeBSD root delegate, which reaches an
// arbitrary uid the same way once it has picked one.
func buildAttachTarget(uid int, res desktopenv.Result) (DelegateTarget, error) {
	name, home, gid, groups, err := lookupSessionUser(uid)
	if err != nil {
		return DelegateTarget{}, err
	}
	env := map[string]string{
		"HOME":    home,
		"USER":    name,
		"LOGNAME": name,
		"SHELL":   "/bin/sh",
		"PATH":    defaultAttachPath,
		// The portal's restore token has to live somewhere the delegated user
		// owns, so consent survives logout rather than landing in a privileged
		// caller's state directory.
		EnvHelperTokenDir: filepath.Join(home, ".config", "karakuri", "automation"),
	}
	for key, value := range res.Env {
		env[key] = value
	}

	target := DelegateTarget{
		UID:        uid,
		GID:        gid,
		User:       name,
		Home:       home,
		SessionID:  res.SessionID,
		Display:    res.Display(),
		Compositor: session.CompositorFromEnvironment(env),
	}
	target.Groups = append(target.Groups, groups...)
	target.Env = make([]string, 0, len(env))
	for key, value := range env {
		if value != "" {
			target.Env = append(target.Env, key+"="+value)
		}
	}
	sort.Strings(target.Env)
	return target, nil
}
