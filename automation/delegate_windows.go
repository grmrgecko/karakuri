//go:build windows

package automation

import (
	"context"
	"strconv"

	win "github.com/grmrgecko/karakuri/automation/internal/windows"
)

// Delegation on Windows exists because a system service runs in session 0,
// which has no desktop and no way to reach one from its own process: the
// window station is not interactive, and a service cannot open another
// session's desktop. The one supported crossing is to start a process inside
// the target session, which needs the session's user token, which only SYSTEM
// may mint. So a SYSTEM caller keeps every session verb — the Terminal
// Services listing, lock, switch, and the credentialed unlock all run in the
// parent — and hands its desktop capabilities to a helper started as the
// console user, on the console's visible desktop.
//
// This is the inverse of the resident the CLI installs. The resident is a
// separately installed, autostarted process an out-of-session login forwards
// to; the helper is an ephemeral child of the agent, spawned on the first
// desktop call and gone when the session closes. A SYSTEM agent therefore
// drives the desktop with nothing installed beyond the service itself.

// findDelegateTarget decides whether a Windows caller delegates its desktop
// work to an in-session helper. A SYSTEM caller that opted into delegation
// does; every other caller — an agent installed as a user logon task, a CLI
// running inside the session — is already where the desktop is and composes
// locally.
//
// The decision does not wait for a user to sign in. A service starts at boot,
// before anyone logs in, and the helper is spawned lazily on the first desktop
// call, by which time the console usually has a user. Until it does, those
// calls report that no one is at the console rather than the session composing
// itself into a local dead end it can never leave.
func findDelegateTarget(ctx context.Context, opts Options) (DelegateTarget, bool) {
	if len(opts.HelperCommand) == 0 || !win.RunningAsSystem() {
		return DelegateTarget{}, false
	}
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	id, user, ok := win.ActiveConsoleUser()
	if !ok {
		logf("no user is signed in at the console yet; the desktop helper starts when one is")
		return pendingTarget(), true
	}
	logf("delegating desktop capabilities to console session %d (%s)", id, user)
	return DelegateTarget{
		UID:       int(id),
		User:      user,
		SessionID: strconv.Itoa(int(id)),
		Display:   "windows",
	}, true
}

// wirePerSessionHelpers is a no-op on Windows. The session verbs a privileged
// parent cannot perform in its own process are the macOS problem; on Windows
// the lock, the switch, and the credentialed unlock run in the SYSTEM parent
// through Terminal Services and the credential provider, and never need a
// helper belonging to another session.
func wirePerSessionHelpers(s *Session, opts Options, target DelegateTarget, h *helper) {}
