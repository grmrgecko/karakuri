package automation

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// Delegation exists because a desktop refuses a privileged process. The session
// bus checks the peer's uid and answers nobody else — root included, which is
// not a policy that can be configured away — so an agent running as root can
// open the Wayland socket and read the X authority and still not post a
// notification, drive a media player, or reach a portal. Half a working palette
// is worse than none, because the half that fails does so at the node rather
// than at the agent.
//
// The way to be the user is to start a process as them. A privileged Session
// therefore keeps every capability but Sessions as a stub over a long-lived
// child running as the session's owner, with that session's environment. The
// child is this same binary re-executed with the argv the caller names in
// Options.HelperCommand, and it is only started by the first call that needs
// it: an agent whose flows never touch the desktop never spawns one and never
// prompts anybody for portal consent.
//
// Sessions is the one capability that stays in the parent, since logging a
// user out or locking their screen is what the unprivileged child could not
// do — with macOS as the exception that proves the shape: there the parent is
// the one who cannot reach the desktop, and the unlock's typing rides the
// helper's keyboard through the capability the composition wires in.
//
// The desktop to delegate to is chosen the same way on every platform: the
// graphical session a person is actually at, the active one first, then the
// most recently started, and never one owned by root — delegating to root
// would be the process it already is.
func pickDesktopSession(sessions []SessionInfo) (SessionInfo, bool) {
	var candidates []SessionInfo
	for _, s := range sessions {
		if s.UID <= 0 || !s.Graphical() {
			continue
		}
		candidates = append(candidates, s)
	}
	if len(candidates) == 0 {
		return SessionInfo{}, false
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Active != candidates[j].Active {
			return candidates[i].Active
		}
		return candidates[i].Since.After(candidates[j].Since)
	})
	return candidates[0], true
}

// DelegateTarget names the desktop session a privileged process delegates to,
// and everything a child needs to join it.
type DelegateTarget struct {
	UID    int
	GID    int
	Groups []uint32
	User   string
	Home   string
	// Env is the child's whole environment, not an addition to the parent's: a
	// privileged parent's environment names its own runtime directory and its
	// own bus, and inheriting either is how a helper ends up talking to the
	// wrong session.
	Env []string
	// SessionID, Display, and Compositor are carried for the description a
	// caller logs.
	SessionID  string
	Display    string
	Compositor string
	// pending marks a target chosen while nobody was at a desktop. Delegation
	// is wanted, and the session is found when the helper is first started.
	pending bool
}

// pendingTarget is the target New returns when the caller delegates but no
// user is at a desktop yet. A service starts at boot, before anyone logs in;
// composing locally then would leave it in a dead end it never leaves, so the
// composition delegates and the first desktop call looks for the session.
func pendingTarget() DelegateTarget {
	return DelegateTarget{User: "the console session", pending: true}
}

// String renders the target for a log line or the Environment description.
func (t DelegateTarget) String() string {
	if t.pending {
		return "the console session (no user signed in yet)"
	}
	if t.SessionID != "" {
		return fmt.Sprintf("%s (uid %d, session %s)", t.User, t.UID, t.SessionID)
	}
	return fmt.Sprintf("%s (uid %d)", t.User, t.UID)
}

// errNoDesktopSession is what a desktop call gets while nobody is at a desktop
// to run the helper in.
var errNoDesktopSession = errors.New("no user is signed in at a desktop to run the session helper in; desktop calls work once one is")

// spawnTarget picks the session a helper is started in. The target is found
// again at each spawn rather than carried from New: a helper that restarts
// after the user logged out and back in must join the new session, whose
// socket names, X authority, and compositor instance are not the old ones,
// and a session that did not exist at startup has to be found by the first
// call that needs it. The startup target is kept as the fallback when the
// lookup fails outright, so a transient session-manager error does not turn a
// working helper into no helper.
func spawnTarget(fresh DelegateTarget, found bool, startup DelegateTarget) (DelegateTarget, error) {
	if found && !fresh.pending {
		return fresh, nil
	}
	if !found && !startup.pending {
		return startup, nil
	}
	return DelegateTarget{}, errNoDesktopSession
}

// newDelegated composes a Session whose desktop capabilities run in a helper
// process owned by the target user. The local composition still happens: it
// supplies Sessions, which must stay privileged, and it is what a caller's
// Describe reports the host as.
func newDelegated(ctx context.Context, opts Options, target DelegateTarget) (*Session, error) {
	s, err := buildSession(ctx, opts)
	if err != nil {
		return nil, err
	}

	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	h := newHelper(func(ctx context.Context) (*hrpc.Conn, func(), error) {
		// The lookup repeats the one New already logged, so it runs quietly and
		// only a changed outcome is reported.
		quiet := opts
		quiet.Logf = nil
		fresh, found := findDelegateTarget(ctx, quiet)
		current, err := spawnTarget(fresh, found, target)
		if err != nil {
			return nil, nil, err
		}
		if !found {
			logf("finding the desktop session again failed; starting the helper in %s, found at startup", current)
		} else if current.String() != target.String() {
			logf("starting the session helper in %s", current)
		}
		return spawnHelper(ctx, opts, current)
	}, opts.Logf)

	s.Inspection = helperInspection{h}
	s.Desktop = helperDesktop{h}
	s.env.Backends["inspection"] = "session-helper"
	s.Keyboard = helperKeyboard{h}
	s.Mouse = helperMouse{h}
	s.Screen = helperScreen{h}
	s.Window = helperWindow{h}
	s.Clipboard = helperClipboard{h}
	s.Notifier = helperNotifier{h}
	s.Dialog = helperDialog{h}
	s.Speaker = helperSpeaker{h}
	s.Media = helperMedia{h}
	s.Power = helperPower{h}
	s.Idle = helperIdle{h}
	s.Opener = helperOpener{h}
	s.closers = append(s.closers, h.Close)
	wireUnlockTyping(s)
	// The session-context verbs go with the typing: a privileged process is
	// outside the session, and on macOS that is exactly what the lock and the
	// login window refuse.
	if sa, ok := s.Sessions.(interface{ SetSessions(autoapi.Sessions) }); ok {
		sa.SetSessions(helperSessions{h})
	}
	// Per-session helpers: the startup delegate's own session reuses the
	// helper above, and any other session gets a fresh one on demand. The
	// wiring is platform-specific — on macOS it spawns helpers into other
	// sessions' launchd domains, which is how the login-window path reaches
	// them.
	wirePerSessionHelpers(s, opts, target, h)

	s.env.Delegate = target.String()
	if s.env.Display == "" {
		s.env.Display = target.Display
	}
	if s.env.Compositor == "" {
		s.env.Compositor = target.Compositor
	}
	for name := range s.env.Backends {
		if name == "sessions" {
			continue
		}
		s.env.Backends[name] = "session-helper"
	}
	return s, nil
}
