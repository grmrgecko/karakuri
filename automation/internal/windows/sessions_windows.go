//go:build windows

package windows

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// providerOnly reports whether the typed secure-input fallback is disabled, so
// the credential provider is the only path a credentialed unlock, sign-in, or
// switch takes and its failure surfaces as the failure. It is the stance for
// bringing the provider up — a masked failure looks like success — and a
// standing choice for an operator who does not want passwords delivered as
// synthetic keystrokes on the secure desktop.
func providerOnly() bool {
	return os.Getenv("KARAKURI_UNLOCK_PROVIDER_ONLY") == "1"
}

// Terminal Services is the whole session story on Windows: WTSEnumerateSessions
// is the only listing of who is logged on, and logging one off or dropping it
// back to the sign-in screen are the only two of the five actions the platform
// offers. Both need SYSTEM or an administrator for a session other than the
// caller's, which is what a service running as LocalSystem already is.
var (
	wtsapi32                     = windows.NewLazySystemDLL("wtsapi32.dll")
	procWTSEnumerateSessionsW    = wtsapi32.NewProc("WTSEnumerateSessionsW")
	procWTSQuerySessionInfoW     = wtsapi32.NewProc("WTSQuerySessionInformationW")
	procWTSFreeMemory            = wtsapi32.NewProc("WTSFreeMemory")
	procWTSLogoffSession         = wtsapi32.NewProc("WTSLogoffSession")
	procWTSDisconnectSession     = wtsapi32.NewProc("WTSDisconnectSession")
	procWTSConnectSessionW       = wtsapi32.NewProc("WTSConnectSessionW")
	procProcessIDToSessionID     = kernel32.NewProc("ProcessIdToSessionId")
	procWTSGetActiveConsoleSesID = kernel32.NewProc("WTSGetActiveConsoleSessionId")
)

// WTS_CURRENT_SERVER_HANDLE is the local machine.
const wtsCurrentServer = 0

// WTS_INFO_CLASS values this file reads.
const (
	wtsUserName       = 5
	wtsDomainName     = 7
	wtsClientProtocol = 16
	wtsSessionInfo    = 24
	wtsSessionInfoEx  = 25
)

// WTSINFOEX_LEVEL1.SessionFlags values. Windows 8 and Server 2012 onward report
// them this way; Windows 7 and Server 2008 R2 swap the two values.
const (
	wtsSessionStateLock    = 0
	wtsSessionStateUnlock  = 1
	wtsSessionStateUnknown = -1
)

// WTS_CONNECTSTATE_CLASS, the states a session can be in.
const (
	wtsActive = iota
	wtsConnected
	wtsConnectQuery
	wtsShadow
	wtsDisconnected
	wtsIdle
	wtsListen
	wtsReset
	wtsDown
	wtsInit
)

// wtsSessionInfoW is one row of the enumeration.
type wtsSessionInfoW struct {
	SessionID  uint32
	WinStation *uint16
	State      uint32
}

// wtsInfoW is the WTSINFOW struct WTSSessionInfo returns. Only the leading
// fields and the logon time are read; the rest is carried so the layout is
// right.
type wtsInfoW struct {
	State                   uint32
	SessionID               uint32
	IncomingBytes           uint32
	OutgoingBytes           uint32
	IncomingFrames          uint32
	OutgoingFrames          uint32
	IncomingCompressedBytes uint32
	OutgoingCompressedBytes uint32
	WinStationName          [32]uint16
	Domain                  [17]uint16
	UserName                [21]uint16
	ConnectTime             int64
	DisconnectTime          int64
	LastInputTime           int64
	LogonTime               int64
	CurrentTime             int64
}

// Sessions is the Terminal Services session capability.
type Sessions struct {
	// secureInput is the argv the in-session helper is started with, wired in
	// by the composition that owns this backend. Empty means credentialed
	// unlock reports what is missing instead of pretending to type.
	secureInput []string
	// logf is where the helper's diagnostics go.
	logf func(string, ...any)
	// provider tracks whether arming the credential provider is worth its
	// timeout: a Windows build that re-enumerates an armed tile without
	// submitting it backs the provider off instead of taxing every unlock.
	provider struct {
		mu            sync.Mutex
		unusableUntil time.Time
	}
}

// NewSessions returns the Windows session backend.
func NewSessions() *Sessions { return &Sessions{} }

// SetSecureInput wires the helper argv and the log sink the credentialed
// unlock types through. It is called by the composition after building the
// backend, the way the macOS backend's typing and actor are wired in. The
// method is exported because the composition that calls it lives in another
// package.
func (s *Sessions) SetSecureInput(argv []string, logf func(string, ...any)) {
	s.secureInput = argv
	s.logf = logf
}

// List reports the machine's Terminal Services sessions. The listener and
// down sessions are dropped: they are stations with nobody on them, and a
// caller looking for who is logged in would have to filter them out anyway.
func (s *Sessions) List(ctx context.Context) ([]autoapi.SessionInfo, error) {
	// The out-parameters are typed pointers rather than uintptr so the address
	// the API wrote is never held as an integer, which is what keeps the
	// garbage collector and vet satisfied about memory this package does not
	// own.
	var raw *wtsSessionInfoW
	var count uint32
	r, _, err := procWTSEnumerateSessionsW.Call(wtsCurrentServer, 0, 1,
		uintptr(unsafe.Pointer(&raw)), uintptr(unsafe.Pointer(&count)))
	if r == 0 || raw == nil {
		return nil, fmt.Errorf("enumerating sessions: %w", err)
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(raw)))

	console := activeConsoleSession()
	rows := unsafe.Slice(raw, count)
	out := make([]autoapi.SessionInfo, 0, len(rows))
	for _, row := range rows {
		if row.State == wtsListen || row.State == wtsDown || row.State == wtsInit {
			continue
		}
		info := autoapi.SessionInfo{
			ID: fmt.Sprint(row.SessionID),
			// Windows names an account by SID, so there is no uid to report
			// and a zero would read as root to everything downstream.
			UID:    -1,
			TTY:    utf16PtrToString(row.WinStation),
			State:  connectStateName(row.State),
			Active: row.State == wtsActive,
			Class:  autoapi.SessionClassUser,
			Type:   "console",
		}
		info.Locked, info.LockKnown = sessionLocked(row.SessionID)
		if row.SessionID == console {
			info.Seat = "console"
		}
		// Session 0 is the service session on every supported Windows: it has
		// no desktop and nobody is signed into it.
		if row.SessionID == 0 {
			info.Class = autoapi.SessionClassManager
			info.Type = ""
		}
		fillSessionDetail(row.SessionID, &info)
		out = append(out, info)
	}
	return out, nil
}

// fillSessionDetail adds the user, the protocol, and the logon time, each from
// its own query because a failure on one should not lose the others.
func fillSessionDetail(id uint32, info *autoapi.SessionInfo) {
	user := querySessionString(id, wtsUserName)
	domain := querySessionString(id, wtsDomainName)
	switch {
	case user != "" && domain != "":
		info.User = domain + `\` + user
	case user != "":
		info.User = user
	}
	if info.User == "" && info.Class == autoapi.SessionClassUser {
		// A session with no user signed in is the sign-in screen itself, which
		// is what the greeter class means everywhere else.
		info.Class = autoapi.SessionClassGreeter
	}
	if proto := querySessionUint16(id, wtsClientProtocol); proto == 2 {
		info.Type = "rdp"
		info.Remote = true
	}
	if buf, ok := querySessionBytes(id, wtsSessionInfo); ok && len(buf) >= int(unsafe.Sizeof(wtsInfoW{})) {
		wi := (*wtsInfoW)(unsafe.Pointer(&buf[0]))
		if wi.LogonTime > 0 {
			info.Since = time.Unix(0, (wi.LogonTime-116444736000000000)*100)
		}
	}
}

// wtsInfoExLevel1 is the head of WTSINFOEX_LEVEL1, up to the field this reads.
// The union in WTSINFOEX is 8-byte aligned because the full struct carries
// LARGE_INTEGERs, so Level1 begins at offset 8 and SessionFlags — a LONG — sits
// three DWORDs into it. Only the head is declared, since nothing past it is
// read; the query allocates the whole thing regardless.
type wtsInfoExLevel1 struct {
	SessionID    uint32
	SessionState uint32
	SessionFlags int32
}

// sessionLocked reports whether the session's interactive desktop is locked,
// and whether the answer is known at all. It reads WTSINFOEX, whose SessionFlags
// carries the lock state that no other WTS query exposes; a session type that
// does not track it reports WTS_SESSIONSTATE_UNKNOWN, which is the "not known"
// case a caller has to tell apart from "not locked".
func sessionLocked(id uint32) (locked bool, known bool) {
	buf, ok := querySessionBytes(id, wtsSessionInfoEx)
	if !ok {
		return false, false
	}
	version := windows.RtlGetVersion()
	return decodeSessionLock(buf, version.MajorVersion, version.MinorVersion)
}

// decodeSessionLock reads WTSINFOEX and corrects the reversed lock flags on
// Windows 7 and Server 2008 R2 (NT 6.1), as documented by Microsoft.
func decodeSessionLock(buf []byte, major, minor uint32) (locked bool, known bool) {
	// WTSINFOEX: DWORD Level, then the level union at offset 8.
	const level1Offset = 8
	if len(buf) < level1Offset+int(unsafe.Sizeof(wtsInfoExLevel1{})) {
		return false, false
	}
	if level := *(*uint32)(unsafe.Pointer(&buf[0])); level != 1 {
		return false, false
	}
	l1 := (*wtsInfoExLevel1)(unsafe.Pointer(&buf[level1Offset]))
	switch l1.SessionFlags {
	case wtsSessionStateLock:
		return !(major == 6 && minor == 1), true
	case wtsSessionStateUnlock:
		return major == 6 && minor == 1, true
	case wtsSessionStateUnknown:
		return false, false
	}
	// Anything a future release adds is treated as not knowable rather than
	// guessed.
	return false, false
}

// Act performs the action on the named session. Windows answers four of the
// five from here: unlock is the credentialed action, and the other four are
// WTS calls the agent's privileges already cover. Locking a session this
// process is not in rides the same in-session helper the unlock types
// through, because LockWorkStation acts on the caller's session only.
func (s *Sessions) Act(ctx context.Context, target autoapi.SessionInfo, action autoapi.SessionAction) error {
	switch action {
	case autoapi.SessionLogout:
		id, err := sessionID(target)
		if err != nil {
			return err
		}
		r, _, callErr := procWTSLogoffSession.Call(wtsCurrentServer, uintptr(id), 1)
		if r == 0 {
			return fmt.Errorf("logging off session %d: %w", id, privilegeHint(callErr))
		}
		return nil

	case autoapi.SessionGreeter:
		// The login screen is what the console shows once its session is
		// parked, so the action is on whoever is sitting there: the target a
		// caller names is the login screen's own session, and the session to
		// end is the console's. With nobody on the console the screen is
		// already up and there is nothing to do.
		id, err := consoleSessionID()
		if err != nil {
			return err
		}
		r, _, callErr := procWTSDisconnectSession.Call(wtsCurrentServer, uintptr(id), 1)
		if r == 0 {
			return fmt.Errorf("disconnecting session %d: %w", id, privilegeHint(callErr))
		}
		return nil

	case autoapi.SessionActivate:
		// Only a disconnected session can be reconnected, and only to a
		// station the caller owns. A session already on the console is
		// already where it would be moved to.
		if target.Active {
			return nil
		}
		id, err := sessionID(target)
		if err != nil {
			return err
		}
		console := activeConsoleSession()
		r, _, callErr := procWTSConnectSessionW.Call(uintptr(id), uintptr(console),
			uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(""))), 1)
		if r == 0 {
			return fmt.Errorf("connecting session %d to the console: %w", id, privilegeHint(callErr))
		}
		return nil

	case autoapi.SessionLock:
		id, err := sessionID(target)
		if err != nil {
			return err
		}
		if id == currentSessionID() {
			return powerControl(ctx, powerLock)
		}
		// Another session's screen is locked from inside it, by the same
		// helper the credentialed unlock starts — there, LockWorkStation is
		// the session's own rather than this process's.
		_, err = secureInput(ctx, s.secureInput, s.logf, id, secureInputRequest{Mode: "lock"})
		return err

	case autoapi.SessionUnlock:
		return fmt.Errorf("%w: unlocking means typing the account's password into the lock screen, "+
			"which needs the credentialed session action with a stored credential", autoapi.ErrUnsupported)
	}
	return fmt.Errorf("unknown session action %q", action)
}

// consoleSessionID names the session a person is sitting at, for the actions
// that mean the console rather than a named session. The greeter itself and
// the service session both answer "nobody here".
func consoleSessionID() (uint32, error) {
	id, ok := consoleAttached()
	if !ok {
		return 0, fmt.Errorf("this machine reports no active console session")
	}
	return id, nil
}

// Login performs a credentialed unlock or a switch-and-unlock. The password
// is proven with LogonUser before anything is submitted, so a wrong one fails
// the call the way a wrong password should and the lock screen never counts
// a failed attempt.
//
// The credential reaches the screen through the karakuri credential provider
// when it is installed: a COM DLL LogonUI loads on the secure desktop, which
// arms an auto-logon tile that winlogon submits — no keystrokes, no pacing,
// no dependence on which sign-in option the screen was last showing. The
// provider only exists while the screen it serves is up, so an answer that
// names its pipe falls back to the typed path: this binary re-executed once
// as SYSTEM inside the target session, which wakes the console, waits for
// the desktop receiving input to become the secure one, and types. A session
// in the background is brought to the console first; when Windows refuses
// the switch, the credentials are typed at the sign-in screen instead, which
// resumes the session — authenticating and unlocking in one step. An account
// with no session is signed in from scratch the same way, parking whoever is
// on the console and flipping the logon screen to its name-and-password
// fields for the duration, the way the macOS login window is flipped.
func (s *Sessions) Login(ctx context.Context, req autoapi.LoginRequest) error {
	switch req.Action {
	case autoapi.SessionUnlock, autoapi.SessionLogin:
	default:
		return fmt.Errorf("unknown credentialed session action %q", req.Action)
	}
	if err := verifyPassword(req.User, req.Secret); err != nil {
		return err
	}

	// The listing the caller resolved the target from may be stale; the id in
	// it is the only thing carried forward, and its absence is what a login
	// for an account with no session looks like.
	id, err := sessionID(req.Target)
	if err != nil {
		if req.Action == autoapi.SessionLogin {
			return s.signIn(ctx, req)
		}
		return err
	}
	return s.unlockSession(ctx, req, id)
}

// unlockSession dismisses the lock of one known session: on the console it
// types into the lock screen, in the background it is brought forward first.
// A session that comes forward unlocked had nothing to dismiss.
func (s *Sessions) unlockSession(ctx context.Context, req autoapi.LoginRequest, id uint32) error {
	sessions, err := s.List(ctx)
	if err != nil {
		return err
	}
	info, found := findSession(sessions, id)
	if !found {
		// A session the caller resolved moments ago is gone; a login can
		// still reach its account through the sign-in screen.
		if req.Action == autoapi.SessionLogin {
			return s.signIn(ctx, req)
		}
		return fmt.Errorf("session %d is gone; %s", id, summarizeSessions(sessions))
	}
	if info.Active {
		if !info.Locked {
			return nil
		}
		return s.typeIntoLockScreen(ctx, req, id)
	}

	// A disconnected session is moved to the console, the fast-user-switching
	// move — it stays locked there, and the typing then happens as usual.
	if err := connectSessionToConsole(ctx, id); err != nil {
		if req.Action == autoapi.SessionLogin {
			return s.signIn(ctx, req)
		}
		return err
	}
	if err := waitSessionActive(ctx, id); err != nil {
		return err
	}
	if locked, known := sessionLocked(id); known && locked {
		return s.typeIntoLockScreen(ctx, req, id)
	}
	return nil
}

// connectSessionToConsole moves a disconnected session onto the console,
// which is the fast-user-switching swap: whoever was there stays signed in
// and parks in the background.
func connectSessionToConsole(ctx context.Context, id uint32) error {
	console := activeConsoleSession()
	r, _, callErr := procWTSConnectSessionW.Call(uintptr(id), uintptr(console),
		uintptr(unsafe.Pointer(windows.StringToUTF16Ptr(""))), 1)
	if r == 0 {
		return fmt.Errorf("connecting session %d to the console: %w", id, privilegeHint(callErr))
	}
	return nil
}

// waitSessionActive polls until the session's WTS state is Active, which is
// how long reconnecting a disconnected session to the console takes.
func waitSessionActive(ctx context.Context, id uint32) error {
	for i := 0; i < unlockPolls; i++ {
		if state := sessionState(id); state == wtsActive {
			return nil
		}
		if err := pause(ctx, unlockPollStep); err != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("session %d never reached the console", id)
}

// sessionState reads one session's WTS connect state, naming the unknown the
// way the enumeration does when the session is gone.
func sessionState(id uint32) uint32 {
	buf, ok := querySessionBytes(id, wtsSessionInfo)
	if !ok || len(buf) < 4 {
		return wtsDown
	}
	return *(*uint32)(unsafe.Pointer(&buf[0]))
}

// The provider's arm windows. LogonUI submits an armed tile when it first
// enumerates the screen's tiles, and (observed on 26200) a tile armed into a
// screen that was already showing only gets its count re-read — the tile is
// never rebuilt. So the sequence is: arm with a short window (the refresh
// will not submit), restart LogonUI — winlogon replaces the screen at once —
// and re-arm, because the armed state died with the LogonUI that just did;
// the fresh enumeration submits. A Windows build that declines even then
// backs the provider off, and the typed helper — which works everywhere —
// carries the unlock.
const (
	providerArmWindow     = 4 * time.Second  // first try: the refresh will not submit
	providerRetryWindow   = 12 * time.Second // after the restart: enumeration + submission
	providerRetryDeadline = 30 * time.Second
	providerBackoff       = 10 * time.Minute
)

// submitViaProvider queues the credential at the session's credential
// provider and sees the submission through, restarting the logon screen when
// the refresh path declines. The answer is the provider's: submitted,
// refused (the NTSTATUS naming why), or not submitted before the deadline.
func (s *Sessions) submitViaProvider(ctx context.Context, id uint32, user, secret string) error {
	deadline := time.Now().Add(providerRetryDeadline)
	attempt := providerArmWindow
	for {
		err := armCredentialProvider(id, user, secret, attempt)
		if err == nil {
			return nil
		}
		if strings.Contains(err.Error(), "refused") {
			return err // winlogon tried it and said no: a retry fails the same way
		}
		if strings.Contains(err.Error(), "not answering") {
			return err // no provider on this host at all
		}
		if time.Now().After(deadline) {
			return err
		}
		if strings.Contains(err.Error(), "EOF") {
			// The logon screen's provider died under us — a restart is
			// already happening; give the fresh one a longer window and
			// re-queue into it.
			attempt = providerRetryWindow
			if lerr := pause(ctx, time.Second); lerr != nil {
				return lerr
			}
			continue
		}
		// Connected but not submitted: the screen was showing before the
		// arm, and a refreshed tile list is never rebuilt. The restart is
		// what puts a fresh enumeration behind the queue.
		s.logf("credential provider armed in session %d without submitting; restarting the logon screen", id)
		restartLogonUI()
		attempt = providerRetryWindow
		if lerr := pause(ctx, time.Second); lerr != nil {
			return lerr
		}
	}
}

// typeIntoLockScreen dismisses the lock of a session that is on the console.
// The credential provider carries the password when it is installed: the
// credential is queued over its pipe and winlogon submits it on the secure
// desktop — no keystroke ever carries the password. The typed helper — which
// works everywhere — is right behind it; the backstop keeps a host whose
// provider cannot submit from taxing every unlock with the arm's timeout.
//
// With the typed fallback disabled (KARAKURI_UNLOCK_PROVIDER_ONLY), the
// provider is always tried and its failure is returned as the failure, rather
// than being masked by a typed unlock — the stance for bringing the provider
// up and for an operator who does not want passwords synthesized as keystrokes.
func (s *Sessions) typeIntoLockScreen(ctx context.Context, req autoapi.LoginRequest, id uint32) error {
	if providerOnly() || s.providerUsable() {
		err := s.submitViaProvider(ctx, id, autoapi.AccountName(req.User), req.Secret)
		if err == nil {
			return s.waitLockCleared(ctx, req.User, id)
		}
		if providerOnly() {
			return fmt.Errorf("the credential provider did not unlock session %d: %w", id, err)
		}
		s.logf("credential provider did not submit in session %d (%v); typing the password instead", id, err)
		s.deferProvider(providerBackoff)
	}
	if _, err := secureInput(ctx, s.secureInput, s.logf, id,
		secureInputRequest{Mode: "unlock", Secret: req.Secret}); err != nil {
		return err
	}
	return s.waitLockCleared(ctx, req.User, id)
}

// providerUsable reports whether the credential provider is worth arming:
// it is installed, and a recent attempt did not end in the OS declining to
// submit.
func (s *Sessions) providerUsable() bool {
	s.provider.mu.Lock()
	defer s.provider.mu.Unlock()
	return s.provider.unusableUntil.IsZero() || time.Now().After(s.provider.unusableUntil)
}

// deferProvider backs the provider off until the given time, so a host whose
// Windows build declines the auto-submit pays the arm's timeout once, not on
// every unlock.
func (s *Sessions) deferProvider(d time.Duration) {
	s.provider.mu.Lock()
	s.provider.unusableUntil = time.Now().Add(d)
	s.provider.mu.Unlock()
}

// waitLockCleared polls the session's lock state until the submitted
// credential has been accepted.
func (s *Sessions) waitLockCleared(ctx context.Context, user string, id uint32) error {
	for i := 0; i < unlockPolls; i++ {
		locked, known := sessionLocked(id)
		if known && !locked {
			return nil
		}
		if err := pause(ctx, unlockPollStep); err != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("the lock screen did not accept the password for %s; if it is not showing the "+
		"password option, unlock once by hand so Windows remembers the choice", user)
}

// signIn signs the account in at the console: park whoever is sitting there,
// queue the credential at the provider the logon screen hosts, and wait for
// the account's session to come forward. A logon screen that was already
// showing is restarted after the arm, which puts a fresh tile enumeration
// behind the queued credential; a host without the provider falls back to
// the typed sign-in, which flips the screen to its name-and-password fields
// for the sequence and undoes the flip before returning.
func (s *Sessions) signIn(ctx context.Context, req autoapi.LoginRequest) error {
	console, ok := consoleAttached()
	if !ok {
		return fmt.Errorf("this machine reports no active console session to sign in on")
	}
	// The logon screen is what the console shows when nobody is signed into
	// its session or the session sitting there is not on the console
	// anymore. Anything else is parked first, which is also what signing in
	// fresh wants.
	if !consoleAtLogon() {
		if r, _, callErr := procWTSDisconnectSession.Call(wtsCurrentServer, uintptr(console), 1); r == 0 {
			return fmt.Errorf("parking the session on the console: %w", privilegeHint(callErr))
		}
		if err := s.waitConsoleAtLogon(ctx); err != nil {
			return err
		}
	}
	// Parking the console does not draw the logon screen over the session that
	// left — Windows brings a fresh greeter session to the console, with its
	// own id and its own credential-provider pipe. Re-resolve the console so
	// the credential is armed at the session now showing the logon screen
	// rather than the one that was parked. waitConsoleAtLogon has already held
	// out for a valid greeter, so the console is settled here.
	console, ok = consoleAttached()
	if !ok {
		return fmt.Errorf("the console reports no session at the sign-in screen")
	}

	// The provider submits the credential the same way it dismisses a lock:
	// queue, refresh, and one restart to put the queued credential behind a
	// fresh tile enumeration. The typed path — with the name-and-password
	// flip — stays behind it for a host without the provider or whose
	// Windows build declines the submit.
	var armErr error
	if providerOnly() || s.providerUsable() {
		armErr = s.submitViaProvider(ctx, console, autoapi.AccountName(req.User), req.Secret)
		if armErr != nil && !providerOnly() {
			s.logf("credential provider did not submit in session %d (%v); typing the sign-in instead", console, armErr)
			s.deferProvider(providerBackoff)
		}
	} else {
		armErr = fmt.Errorf("the credential provider is backed off")
	}
	if armErr != nil && providerOnly() {
		return fmt.Errorf("the credential provider did not sign %s in: %w", req.User, armErr)
	}
	if armErr != nil && strings.Contains(armErr.Error(), "refused") {
		return armErr
	}
	if armErr != nil {
		// The typed path signs the account in from scratch, flipping the
		// logon screen to its name-and-password fields and restarting it so
		// the flip is read.
		restore, flipped := namePasswordLogon()
		if flipped {
			defer restore()
			restartLogonUI()
		}
		if err := pause(ctx, signinformPause); err != nil {
			return err
		}
		if _, err := secureInput(ctx, s.secureInput, s.logf, console,
			secureInputRequest{Mode: "signin", Username: autoapi.AccountName(req.User), Secret: req.Secret}); err != nil {
			return err
		}
	}

	// A first sign-in creates the profile and can take a while; the
	// credentials were submitted, so the answer is the session's arrival,
	// not the typing.
	for i := 0; i < loginPolls; i++ {
		if err := pause(ctx, unlockPollStep); err != nil {
			return ctx.Err()
		}
		sessions, err := s.List(ctx)
		if err != nil {
			continue
		}
		for _, info := range sessions {
			if !info.Active || autoapi.AccountName(info.User) != autoapi.AccountName(req.User) {
				continue
			}
			id, err := sessionID(info)
			if err != nil {
				continue
			}
			if locked, known := sessionLocked(id); known && locked {
				return s.typeIntoLockScreen(ctx, req, id)
			}
			return nil
		}
	}
	return fmt.Errorf("the sign-in screen did not sign %s in; the credentials were submitted but no session "+
		"came to the console", req.User)
}

// waitConsoleAtLogon polls until the console is showing the logon screen —
// either a fresh greeter session brought to the console after a disconnect
// (its own id, nobody signed in), or the console id naming a session that is
// no longer active on it.
func (s *Sessions) waitConsoleAtLogon(ctx context.Context) error {
	for i := 0; i < logonScreenPolls; i++ {
		if consoleAtLogon() {
			return nil
		}
		if err := pause(ctx, unlockPollStep); err != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("the console never reached the sign-in screen")
}

// consoleAtLogon reports whether the console is settled on the logon screen:
// a valid session owns the physical console and either nobody is signed into
// it (an active greeter, the logon screen at rest after a logoff or at boot)
// or it is a session parked in the background with the logon screen drawn over
// it. It is deliberately strict about the transient states a fast-user-switch
// passes through — no console attached, or a session still coming up — because
// a sign-in reads the console id right after this to arm the credential
// provider, and a transient answer would name the wrong pipe.
func consoleAtLogon() bool {
	console, ok := consoleAttached()
	if !ok {
		// No session owns the console — the brief gap a fast-user-switch passes
		// through before the next session attaches. Keep waiting.
		return false
	}
	// A session that owns the console but is parked shows the logon screen
	// drawn over it.
	if state := sessionState(console); state == wtsDisconnected || state == wtsListen {
		return true
	}
	// Otherwise the console is at the logon screen exactly when nobody is
	// signed into the session that owns it — a greeter, whether it reports
	// active or is still coming up as connected. A session with a user on it is
	// that person's desktop.
	return querySessionString(console, wtsUserName) == ""
}

// wtsInvalidSession is what WTSGetActiveConsoleSessionId returns when no
// session is attached to the physical console, which happens transiently while
// a fast-user-switch brings the next session forward.
const wtsInvalidSession = 0xFFFFFFFF

// consoleAttached reports the session that owns the physical console, and
// whether one is attached at all. Session 0 (services) and the invalid marker
// both mean "no console to act on".
func consoleAttached() (uint32, bool) {
	id := activeConsoleSession()
	if id == 0 || id == wtsInvalidSession {
		return 0, false
	}
	return id, true
}

// findSession reports the listed session with the given id.
func findSession(sessions []autoapi.SessionInfo, id uint32) (autoapi.SessionInfo, bool) {
	for _, info := range sessions {
		if parsed, err := sessionID(info); err == nil && parsed == id {
			return info, true
		}
	}
	return autoapi.SessionInfo{}, false
}

// summarizeSessions names what was on the host, so a session that vanished
// between two calls says what was there instead.
func summarizeSessions(sessions []autoapi.SessionInfo) string {
	names := make([]string, 0, len(sessions))
	for _, info := range sessions {
		who := info.User
		if who == "" {
			who = info.Class
		}
		names = append(names, fmt.Sprintf("%s (%s)", info.ID, who))
	}
	return "the host now has " + strings.Join(names, ", ")
}

// sessionID parses the numeric session id back out of the info.
func sessionID(target autoapi.SessionInfo) (uint32, error) {
	var id uint32
	if _, err := fmt.Sscanf(target.ID, "%d", &id); err != nil {
		return 0, fmt.Errorf("%q is not a Windows session id", target.ID)
	}
	return id, nil
}

// currentSessionID reports the session the agent process runs in.
func currentSessionID() uint32 {
	var id uint32
	pid, _, _ := kernel32.NewProc("GetCurrentProcessId").Call()
	if r, _, _ := procProcessIDToSessionID.Call(pid, uintptr(unsafe.Pointer(&id))); r == 0 {
		return 0
	}
	return id
}

// activeConsoleSession reports which session owns the physical console.
func activeConsoleSession() uint32 {
	r, _, _ := procWTSGetActiveConsoleSesID.Call()
	return uint32(r)
}

// querySessionBytes reads one WTS info class as raw bytes, copying them out
// before the API's buffer is freed.
func querySessionBytes(id uint32, class uint32) ([]byte, bool) {
	var buf *byte
	var size uint32
	r, _, _ := procWTSQuerySessionInfoW.Call(wtsCurrentServer, uintptr(id), uintptr(class),
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&size)))
	if r == 0 || buf == nil {
		return nil, false
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	out := make([]byte, size)
	copy(out, unsafe.Slice(buf, size))
	return out, true
}

// querySessionString reads one WTS info class that holds a wide string.
func querySessionString(id uint32, class uint32) string {
	var buf *uint16
	var size uint32
	r, _, _ := procWTSQuerySessionInfoW.Call(wtsCurrentServer, uintptr(id), uintptr(class),
		uintptr(unsafe.Pointer(&buf)), uintptr(unsafe.Pointer(&size)))
	if r == 0 || buf == nil {
		return ""
	}
	defer procWTSFreeMemory.Call(uintptr(unsafe.Pointer(buf)))
	return strings.TrimSpace(windows.UTF16PtrToString(buf))
}

// querySessionUint16 reads one WTS info class that holds a USHORT.
func querySessionUint16(id uint32, class uint32) uint16 {
	buf, ok := querySessionBytes(id, class)
	if !ok || len(buf) < 2 {
		return 0
	}
	return *(*uint16)(unsafe.Pointer(&buf[0]))
}

// utf16PtrToString reads a wide string the enumeration allocated, tolerating
// the nil a station with no name leaves.
func utf16PtrToString(p *uint16) string {
	if p == nil {
		return ""
	}
	return strings.TrimSpace(windows.UTF16PtrToString(p))
}

// connectStateName maps a WTS_CONNECTSTATE_CLASS onto the shared vocabulary.
func connectStateName(state uint32) string {
	switch state {
	case wtsActive:
		return "active"
	case wtsConnected, wtsConnectQuery:
		return "online"
	case wtsDisconnected:
		return "disconnected"
	case wtsIdle:
		return "online"
	case wtsShadow:
		return "online"
	case wtsReset, wtsDown:
		return "closing"
	}
	return "online"
}

// privilegeHint names the cause an operator can act on. Acting on another
// session fails with access denied when the agent is not SYSTEM, which reads
// like a bug until you know why.
func privilegeHint(err error) error {
	if err == windows.ERROR_ACCESS_DENIED {
		return fmt.Errorf("%w: acting on another user's session needs an agent running as a system service", err)
	}
	return err
}
