//go:build darwin

package macos

import (
	"context"
	"fmt"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ebitengine/purego"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// macOS keeps its login sessions in the window server, not in any public API:
// CGSSessionCopyAllSessionProperties is what the fast-user-switching menu reads
// and the only listing of who is logged in with a graphical session. Acting on
// one goes through the same private login framework the lock already uses —
// SACSwitchToLoginWindow is the login window, SACLOStartLogout is the logout
// the Apple menu performs, and the fast-user switch has no callable form (see
// the note on SessionActivate below).
//
// Unlocking a session means typing its password into the lock screen, because
// the password is what unlocks the keychain and FileVault and the system
// accepts it on no other surface. The typed path is the same one a person
// walks: prove the password against Directory Services first so a wrong one
// fails without ever counting as a failed unlock, bring the session's screen
// forward, wake the password field with a press that deletes nothing, type,
// press return, and watch the session's lock key clear. The typing rides the
// composed session's keyboard capability: a root process cannot deliver events
// to a console session it is not part of, so in a delegated composition the
// keys are typed by the helper running as the session's owner.

// Session dictionary keys. These are plain strings rather than exported
// CFString constants, so they are built here.
const (
	keySessionUserID    = "kCGSSessionUserIDKey"
	keySessionUserName  = "kCGSSessionUserNameKey"
	keySessionOnConsole = "kCGSSessionOnConsoleKey"
	keySessionID        = "kCGSSessionIDKey"
	keySessionLoginDone = "kCGSessionLoginDoneKey"
	// keySessionScreenLocked is present with a true value only while the
	// session's screen is locked, and absent otherwise. Verified live on Sonoma
	// by locking through SACLockScreenImmediate and watching the key appear in
	// both the current-session and all-sessions dictionaries; the "…Key"-suffix
	// spelling is not the real key and never appears.
	keySessionScreenLocked = "CGSSessionScreenIsLocked"
)

var (
	cgsSessionCopyAllSessionProperties func() uintptr
	cfBooleanGetValue                  func(uintptr) bool

	sacSwitchToLoginWindow func() int32
	sacStartLogout         func(uint32) int32

	sessionOnce sync.Once
	sessionErr  error
)

// loadSessionSymbols resolves the window-server listing and the login
// framework's session verbs once. Both are private, so a symbol that moved in
// an OS update lands here as an error rather than a crash.
func loadSessionSymbols() error {
	sessionOnce.Do(func() {
		if err := load(); err != nil {
			sessionErr = err
			return
		}
		defer func() {
			if r := recover(); r != nil {
				sessionErr = fmt.Errorf("a session symbol did not resolve: %v", r)
			}
		}()

		cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			sessionErr = fmt.Errorf("opening CoreGraphics: %w", err)
			return
		}
		purego.RegisterLibFunc(&cgsSessionCopyAllSessionProperties, cg, "CGSSessionCopyAllSessionProperties")

		cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			sessionErr = fmt.Errorf("opening CoreFoundation: %w", err)
			return
		}
		purego.RegisterLibFunc(&cfBooleanGetValue, cf, "CFBooleanGetValue")

		lib, err := purego.Dlopen("/System/Library/PrivateFrameworks/login.framework/Versions/A/login",
			purego.RTLD_NOW|purego.RTLD_GLOBAL)
		if err != nil {
			sessionErr = fmt.Errorf("opening the login framework: %w", err)
			return
		}
		purego.RegisterLibFunc(&sacSwitchToLoginWindow, lib, "SACSwitchToLoginWindow")
		purego.RegisterLibFunc(&sacStartLogout, lib, "SACLOStartLogout")
	})
	return sessionErr
}

// Pacing and limits for the typed paths. The pause after the wake press is
// how long the lock screen takes to reveal the password field; the delay
// between keystrokes is what a person types at, which some input surfaces
// drop faster sequences at; the waits bound how long the lock screen, the
// login window, and a fresh login are polled before they are reported.
const (
	wakePause      = 2 * time.Second
	keystrokeDelay = 30 * time.Millisecond
	fieldPause     = 500 * time.Millisecond
	// loginWindowPause is how long the login window takes to be ready for
	// keys after the console parks there; typing earlier drops characters.
	loginWindowPause = 2 * time.Second
	unlockPolls      = 40
	unlockPollStep   = 250 * time.Millisecond
	loginPolls       = 100
)

// SessionHelpers is a session's own actor and keyboard, spawned into that
// session's launchd domain on demand. A helper belonging to a session can
// perform that session's context verbs (the lock, the login window) and its
// synthetic keys land on whatever the console is showing while that session
// is the active one — and on the login window even after the session has been
// parked, which is what makes the login-window path work.
type SessionHelpers struct {
	Sessions autoapi.Sessions
	Keyboard autoapi.Keyboard
	Release  func()
}

// Sessions is the macOS session capability.
type Sessions struct {
	// typing is the composed session's keyboard capability, wired in after
	// composition. In a delegated composition it is the helper's, which is
	// whose session the lock screen belongs to.
	typing autoapi.Keyboard
	// actor is where the session-context verbs go when this backend lives in
	// a process outside the graphical session. The lock and the login-window
	// verb are refused to every process the session does not belong to, so a
	// delegated composition hands over an actor that runs them in the
	// helper. Nil means this process is in the session and acts natively.
	actor autoapi.Sessions
	// helpers is the per-session helper source wired by a delegated
	// composition: for a session it returns that session's own actor and
	// keyboard, spawned into the session's launchd domain on demand. Nil
	// means only this process's own session is reachable.
	helpers func(autoapi.SessionInfo) (SessionHelpers, error)
	// logf is the diagnostics sink for the credentialed session actions.
	logf func(string, ...any)
}

// NewSessions returns the macOS session backend.
func NewSessions() *Sessions { return &Sessions{} }

// SetLogf wires the diagnostics sink for the credentialed session actions.
func (s *Sessions) SetLogf(logf func(string, ...any)) {
	s.logf = logf
}

// SetKeyboard wires the composed session's keyboard into the backend, so the
// unlock can type through whatever the composition resolved it to. It is
// called after buildSession and again after a delegated composition swaps the
// keyboard for the helper's. The method is exported because the composition
// that calls it lives in another package, not because callers are expected to
// reach for it.
func (s *Sessions) SetKeyboard(kb autoapi.Keyboard) { s.typing = kb }

// SetSessions wires the actor that performs the session-context verbs on this
// backend's behalf: the helper, in a delegated composition. Nil (the default)
// keeps every verb native, which is right for a process running inside its
// own session.
func (s *Sessions) SetSessions(actor autoapi.Sessions) { s.actor = actor }

// SetHelperFactory wires the source of per-session helpers. It is what lets a
// privileged process reach sessions it does not belong to: the helper for a
// session is spawned into that session's launchd domain, where its context
// verbs work and its keys land on the console while the session is active.
func (s *Sessions) SetHelperFactory(f func(autoapi.SessionInfo) (SessionHelpers, error)) {
	s.helpers = f
}

// helpersFor returns the actor and keyboard to use for a session: its own
// helper when a factory is wired, and this process's own capabilities when it
// is not — which is right only when the session is this process's own.
func (s *Sessions) helpersFor(info autoapi.SessionInfo) (SessionHelpers, error) {
	if s.helpers != nil {
		return s.helpers(info)
	}
	if info.UID != currentUID() {
		return SessionHelpers{}, fmt.Errorf("reaching another user's session needs the agent running " +
			"as root, which spawns a helper inside that session")
	}
	return SessionHelpers{Keyboard: s.typing}, nil
}

// List reports the graphical login sessions the window server knows about. A
// session whose login has not finished is the login window itself, which is
// reported as the greeter so a caller can tell it from a person's desktop.
func (s *Sessions) List(ctx context.Context) ([]autoapi.SessionInfo, error) {
	if err := loadSessionSymbols(); err != nil {
		return nil, err
	}
	arr := cgsSessionCopyAllSessionProperties()
	if arr == 0 {
		return nil, fmt.Errorf("the window server reported no sessions")
	}
	defer cfRelease(arr)

	uidKey := cfStr(keySessionUserID)
	nameKey := cfStr(keySessionUserName)
	consoleKey := cfStr(keySessionOnConsole)
	idKey := cfStr(keySessionID)
	doneKey := cfStr(keySessionLoginDone)
	lockedKey := cfStr(keySessionScreenLocked)
	defer func() {
		for _, k := range []uintptr{uidKey, nameKey, consoleKey, idKey, doneKey, lockedKey} {
			cfRelease(k)
		}
	}()

	count := cfArrayGetCount(arr)
	out := make([]autoapi.SessionInfo, 0, count)
	for i := 0; i < count; i++ {
		dict := cfArrayGetValueAtIndex(arr, i)
		if dict == 0 {
			continue
		}
		info := autoapi.SessionInfo{
			UID:    cfNumberInt(cfDictionaryGetValue(dict, uidKey)),
			User:   cfStringGo(cfDictionaryGetValue(dict, nameKey)),
			Type:   "console",
			Class:  autoapi.SessionClassUser,
			Active: cfBool(cfDictionaryGetValue(dict, consoleKey)),
			Seat:   "console",
		}
		if id := cfNumberInt(cfDictionaryGetValue(dict, idKey)); id != 0 {
			info.ID = strconv.Itoa(id)
		} else {
			info.ID = strconv.Itoa(info.UID)
		}
		loggedIn := cfBool(cfDictionaryGetValue(dict, doneKey)) && info.User != ""
		if !loggedIn {
			info.Class = autoapi.SessionClassGreeter
			info.User = ""
		} else {
			// A logged-in session carries the screen-lock key only while
			// locked, so its absence is a reliable "unlocked" for this session
			// rather than "not known". The greeter has no screen to lock.
			info.Locked = cfBool(cfDictionaryGetValue(dict, lockedKey))
			info.LockKnown = true
		}
		info.State = "online"
		if info.Active {
			info.State = "active"
		}
		out = append(out, info)
	}
	return out, nil
}

// cfBool reads a CFBooleanRef, treating an absent value as false.
func cfBool(ref uintptr) bool {
	if ref == 0 {
		return false
	}
	return cfBooleanGetValue(ref)
}

// Act performs the action on the named session. The session-context verbs —
// lock, switch, and the login window — are refused to a process outside the
// session, so when an actor is wired (the helper, in a delegated composition)
// they run there instead; everything else stays with this process, whose
// privileges are the point.
func (s *Sessions) Act(ctx context.Context, target autoapi.SessionInfo, action autoapi.SessionAction) error {
	if err := loadSessionSymbols(); err != nil {
		return err
	}
	switch action {
	case autoapi.SessionActivate:
		// See the note above: the switch contract is not known, so there is
		// no safe call to make for any target, the session's own included.
		return fmt.Errorf("%w: macOS performs fast user switching only from its own switcher menu; "+
			"switching to a session from a process is refused", autoapi.ErrUnsupported)

	case autoapi.SessionGreeter, autoapi.SessionLock:
		if s.actor != nil {
			return s.actor.Act(ctx, target, action)
		}
		if action == autoapi.SessionGreeter {
			if rc := sacSwitchToLoginWindow(); rc != 0 {
				return fmt.Errorf("the login framework declined the switch to the login window (%d)", rc)
			}
			return nil
		}
		return lockScreenNative()

	case autoapi.SessionLogout:
		return logoutSession(ctx, target)

	case autoapi.SessionUnlock:
		return fmt.Errorf("%w: unlocking means typing the account's password into the lock screen, "+
			"which needs the credentialed session action with a stored credential", autoapi.ErrUnsupported)
	}
	return fmt.Errorf("unknown session action %q", action)
}

// logoutSession ends the session. The agent's own goes through the login
// framework, so the applications get their chance to save; another user's goes
// through launchd, which is the only thing that can tear down a GUI domain that
// is not the caller's, and needs root.
func logoutSession(ctx context.Context, target autoapi.SessionInfo) error {
	uid, err := targetUID(target)
	if err != nil {
		return err
	}
	if uid == currentUID() {
		if rc := sacStartLogout(0); rc != 0 {
			return fmt.Errorf("the login framework declined the logout (%d)", rc)
		}
		return nil
	}
	if _, err := toolsh.RunTool(ctx, "launchctl", "bootout", fmt.Sprintf("gui/%d", uid)); err != nil {
		return fmt.Errorf("logging out uid %d: %w", uid, err)
	}
	return nil
}

// Login performs a credentialed unlock or a switch-and-unlock. The password
// is proven against Directory Services before anything moves, so a wrong one
// fails the call the way a wrong password should rather than arriving as a
// failed attempt at the lock screen or the login window. Unlocked is an
// accepted no-op: there is nothing to dismiss.
//
// A session whose lock screen is on the console is unlocked by typing into
// it. A session in the background is reached through the login window
// instead: the login window accepts any account's credentials and resumes
// that account's session — authenticating and unlocking in one step — and
// synthetic keys reach it from a parked session's helper, so no switch has
// to be performed programmatically (the switch APIs are gated to Apple's own
// clients). A user with no session at all is signed in from scratch the same
// way.
func (s *Sessions) Login(ctx context.Context, req autoapi.LoginRequest) error {
	if err := loadSessionSymbols(); err != nil {
		return err
	}
	switch req.Action {
	case autoapi.SessionUnlock, autoapi.SessionLogin:
	default:
		return fmt.Errorf("unknown credentialed session action %q", req.Action)
	}
	// Resolve the uid from the target session, or from the named account when
	// there is no session — a fresh login signs an account in from scratch, so
	// the request may name a user the host has no session for.
	uid, err := targetUID(req.Target)
	if err != nil {
		if req.User == "" {
			return err
		}
		u, lookErr := user.Lookup(req.User)
		if lookErr != nil {
			return fmt.Errorf("no user named %s on this host: %w", req.User, lookErr)
		}
		uid, err = strconv.Atoi(u.Uid)
		if err != nil {
			return fmt.Errorf("resolving the uid for %s: %w", req.User, err)
		}
	}

	// Prove the password first. Verification is Directory Services' own call,
	// so the account's failed-attempt counters see exactly one attempt: the
	// successful one, when the submission lands.
	if err := verifyPassword(req.User, req.Secret); err != nil {
		return err
	}

	// Fetch the target account's current session state.
	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}
	var target autoapi.SessionInfo
	found := false
	for _, info := range sessions {
		if info.UID == uid {
			target = info
			found = true
			break
		}
	}

	// A session already on the console is either unlocked (nothing to do) or
	// unlocked in place by typing into its lock screen.
	if found && target.Active {
		if !target.Locked {
			return nil
		}
		return s.typeIntoLockScreen(ctx, target, req)
	}

	// A parked or absent session is signed in through the login window: park
	// whatever is on the console (fast user switching, so that session stays
	// alive), bring the console to the login window, and type the credentials —
	// resuming a parked session or creating a new one.
	return s.signInThroughLoginWindow(ctx, req, uid, target, found)
}

// typeIntoLockScreen dismisses the lock screen of the session that is on the
// console: wake the display, reveal the password field, type, return, and
// poll the session's lock key until it clears.
func (s *Sessions) typeIntoLockScreen(ctx context.Context, target autoapi.SessionInfo, req autoapi.LoginRequest) error {
	h, err := s.helpersFor(target)
	if err != nil {
		return err
	}
	if h.Release != nil {
		defer h.Release()
	}
	kb := h.Keyboard
	if kb == nil {
		kb = s.typing
	}
	if kb == nil {
		return fmt.Errorf("the session composition has no keyboard capability to type the password with")
	}

	// The display may have gone off since the screen locked, and a display
	// that is off drops posted events — the wake comes from the power
	// manager's user-active assertion, which is what caffeinate -u makes. A
	// failure here is not fatal: the display may be awake already.
	release := wakeDisplay()
	if release != nil {
		defer release()
	}
	select {
	case <-autoapi.RealClock{}.After(wakePause):
	case <-ctx.Done():
		return ctx.Err()
	}
	// A press that deletes nothing from an empty field, and reveals the
	// password field when the lock screen was showing the clock.
	if err := kb.PressKeys(ctx, autoapi.KeyStroke{Key: "backspace"}); err != nil {
		return err
	}
	select {
	case <-autoapi.RealClock{}.After(wakePause):
	case <-ctx.Done():
		return ctx.Err()
	}
	// Each rune is its own key event carrying its Unicode string, so the
	// password survives whatever layout the session uses.
	if err := kb.TypeText(ctx, autoapi.RealClock{}, req.Secret, keystrokeDelay); err != nil {
		return err
	}
	if err := kb.PressKeys(ctx, autoapi.KeyStroke{Key: "return"}); err != nil {
		return err
	}

	for i := 0; i < unlockPolls; i++ {
		select {
		case <-autoapi.RealClock{}.After(unlockPollStep):
		case <-ctx.Done():
			return ctx.Err()
		}
		info, err := s.sessionFor(ctx, target.UID)
		if err == nil && !info.Locked {
			return nil
		}
	}
	return fmt.Errorf("the lock screen did not accept the password for %s; if the password field was not focused, unlock once by hand and try again", req.User)
}

// clearField selects all text in the focused field and deletes it, so a typed
// value replaces whatever the login window pre-filled rather than appending to
// it. Failures are ignored: an empty field is the common case, and the type
// that follows still lands.
func clearField(ctx context.Context, kb autoapi.Keyboard) {
	_ = kb.PressKeys(ctx, autoapi.KeyStroke{Key: "a", Mods: []string{"cmd"}})
	_ = kb.PressKeys(ctx, autoapi.KeyStroke{Key: "backspace"})
}

// signInThroughLoginWindow signs a user in through the login window: bring
// the console there (the active session's helper performs the switch — the
// verb is refused from a parked session), type the credentials, and wait for
// the account's session to appear and come to the foreground. Resuming a
// parked session authenticates and unlocks it in one step; a user with no
// session is signed in from scratch.
func (s *Sessions) signInThroughLoginWindow(ctx context.Context, req autoapi.LoginRequest, uid int, target autoapi.SessionInfo, found bool) error {
	// The typed sequence assumes the login window shows name and password
	// fields. The user-list mode still works — typing a name selects the
	// account — but name+password is deterministic, so a privileged process
	// flips the setting for this sign-in and puts the old value back after.
	restore := enableNamedLoginWindow(ctx)
	if restore != nil {
		defer restore()
	}

	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}
	var active autoapi.SessionInfo
	activeFound := false
	for _, info := range sessions {
		if info.Active {
			active = info
			activeFound = true
			break
		}
	}
	atLoginWindow := activeFound && active.Class == autoapi.SessionClassGreeter

	// The keys that type the credentials: the target session's own helper
	// when it has one — its keys reach the login window even parked — and
	// the greeter step's helper or our own otherwise.
	var kb autoapi.Keyboard
	var releases []func()
	defer func() {
		for _, r := range releases {
			if r != nil {
				r()
			}
		}
	}()

	if !atLoginWindow {
		if !activeFound || active.UID <= 0 {
			return fmt.Errorf("the console is neither at the login window nor on a session to switch " +
				"away from; bring the login window up once by hand")
		}
		h, err := s.helpersFor(active)
		if err != nil {
			return fmt.Errorf("switching away from the session on the console: %w", err)
		}
		releases = append(releases, h.Release)
		if h.Sessions != nil {
			err = h.Sessions.Act(ctx, autoapi.SessionInfo{Class: autoapi.SessionClassGreeter}, autoapi.SessionGreeter)
		} else {
			err = s.Act(ctx, autoapi.SessionInfo{Class: autoapi.SessionClassGreeter}, autoapi.SessionGreeter)
		}
		if err != nil {
			return err
		}
		if err := s.waitGreeter(ctx); err != nil {
			return err
		}
		kb = h.Keyboard
		// The login window needs a moment after the session state changes
		// before its fields take keys; typing earlier drops characters.
		select {
		case <-autoapi.RealClock{}.After(loginWindowPause):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	if found && s.helpers != nil {
		// The target session's own helper types when one can be spawned;
		// without a factory our own keyboard still reaches the login window
		// from a parked session, so the credentials go out regardless.
		h, err := s.helpers(target)
		if err != nil {
			return err
		}
		releases = append(releases, h.Release)
		if h.Keyboard != nil {
			kb = h.Keyboard
		}
	}
	if kb == nil {
		kb = s.typing
	}
	if kb == nil {
		return fmt.Errorf("no keyboard capability is available to type the login credentials with")
	}

	// The login window's name field has focus when it shows name and
	// password; in the user-list mode, typing a name selects the account.
	// Either way the sequence is name, tab, password, return.
	select {
	case <-autoapi.RealClock{}.After(fieldPause):
	case <-ctx.Done():
		return ctx.Err()
	}
	// Clear anything the window pre-filled in the name field — the last account,
	// or a partial entry from a re-presented window — so the typed account is
	// not appended to a leftover one. Select all, then delete.
	clearField(ctx, kb)
	if err := kb.TypeText(ctx, autoapi.RealClock{}, req.User, keystrokeDelay); err != nil {
		return err
	}
	if err := kb.PressKeys(ctx, autoapi.KeyStroke{Key: "tab"}); err != nil {
		return err
	}
	select {
	case <-autoapi.RealClock{}.After(fieldPause):
	case <-ctx.Done():
		return ctx.Err()
	}
	clearField(ctx, kb)
	if err := kb.TypeText(ctx, autoapi.RealClock{}, req.Secret, keystrokeDelay); err != nil {
		return err
	}
	if err := kb.PressKeys(ctx, autoapi.KeyStroke{Key: "return"}); err != nil {
		return err
	}

	for i := 0; i < loginPolls; i++ {
		select {
		case <-autoapi.RealClock{}.After(unlockPollStep):
		case <-ctx.Done():
			return ctx.Err()
		}
		info, err := s.sessionFor(ctx, uid)
		if err == nil && info.Active {
			// A resumed session that was locked when parked comes back
			// unlocked; a lock key left set falls through to the lock
			// screen path, which types into the now-console lock screen.
			if info.Locked {
				return s.typeIntoLockScreen(ctx, info, req)
			}
			return nil
		}
	}
	return fmt.Errorf("the login window did not sign %s in; the credentials were typed but no session "+
		"came to the foreground", req.User)
}

// waitGreeter polls until a greeter session is the active one, after the
// switch to the login window.
func (s *Sessions) waitGreeter(ctx context.Context) error {
	for i := 0; i < unlockPolls; i++ {
		select {
		case <-autoapi.RealClock{}.After(unlockPollStep):
		case <-ctx.Done():
			return ctx.Err()
		}
		sessions, err := s.List(ctx)
		if err != nil {
			continue
		}
		for _, info := range sessions {
			if info.Active && info.Class == autoapi.SessionClassGreeter {
				return nil
			}
		}
	}
	return fmt.Errorf("the console never reached the login window")
}

// enableNamedLoginWindow switches the login window to its name-and-password
// fields (the mode network accounts need), returning the function that puts
// the previous setting back. It is a privileged change — the preference lives
// in /Library — so an unprivileged process gets a nil release and the login
// window stays whatever the operator configured. The name-and-password mode
// is what the typed sign-in assumes; the user-list mode also works through
// type-ahead, but this is the deterministic shape.
func enableNamedLoginWindow(ctx context.Context) (restore func()) {
	if currentUID() != 0 {
		return nil
	}
	const domain = "/Library/Preferences/com.apple.loginwindow"
	previous, err := toolsh.RunTool(ctx, "defaults", "read", domain, "SHOWFULLNAME")
	hadPrevious := err == nil
	if _, err := toolsh.RunTool(ctx, "defaults", "write", domain, "SHOWFULLNAME", "-bool", "true"); err != nil {
		return nil
	}
	return func() {
		if hadPrevious {
			_, _ = toolsh.RunTool(context.Background(), "defaults", "write", domain,
				"SHOWFULLNAME", "-bool", strings.TrimSpace(previous))
			return
		}
		_, _ = toolsh.RunTool(context.Background(), "defaults", "delete", domain, "SHOWFULLNAME")
	}
}

// sessionFor reports the current state of the session a uid owns, from the
// window server's own listing. A uid with no session means it ended between
// the resolution and here.
func (s *Sessions) sessionFor(ctx context.Context, uid int) (autoapi.SessionInfo, error) {
	sessions, err := s.List(ctx)
	if err != nil {
		return autoapi.SessionInfo{}, err
	}
	for _, info := range sessions {
		if info.UID == uid {
			return info, nil
		}
	}
	return autoapi.SessionInfo{}, fmt.Errorf("uid %d has no session anymore", uid)
}

// targetUID resolves the uid the action applies to, from the listing's own
// value or from the user name when a caller named one directly.
func targetUID(target autoapi.SessionInfo) (int, error) {
	if target.UID > 0 {
		return target.UID, nil
	}
	if target.User != "" {
		u, err := user.Lookup(target.User)
		if err != nil {
			return 0, fmt.Errorf("no user named %s on this host: %w", target.User, err)
		}
		return strconv.Atoi(u.Uid)
	}
	return 0, fmt.Errorf("session %q names no user to act on", target.ID)
}

// currentUID reports the uid the agent runs as.
func currentUID() int {
	if u, err := user.Current(); err == nil {
		if uid, err := strconv.Atoi(u.Uid); err == nil {
			return uid
		}
	}
	return -1
}
