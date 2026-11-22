//go:build windows

package windows

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/lxn/win"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// LogonUser lives in advapi32 and the desktop attachment calls in user32 are
// beyond what x/sys wraps; everything else rides the procs the other files
// already declare.
var (
	advapi32                      = windows.NewLazySystemDLL("advapi32.dll")
	procLogonUserW                = advapi32.NewProc("LogonUserW")
	procOpenInputDesktop          = user32.NewProc("OpenInputDesktop")
	procOpenDesktopW              = user32.NewProc("OpenDesktopW")
	procSetThreadDesktop          = user32.NewProc("SetThreadDesktop")
	procGetUserObjectInformationW = user32.NewProc("GetUserObjectInformationW")
	procOpenWindowStationW        = user32.NewProc("OpenWindowStationW")
	procSetProcessWindowStation   = user32.NewProc("SetProcessWindowStation")
)

// UOI_NAME, the object-information class a desktop handle's name is read with.
const uoiName = 2

// The logon type and provider a password check wants.
const (
	logon32Network         = 3
	logon32ProviderDefault = 0
)

// Windows dismisses a lock screen only for a credential handed in on the
// secure desktop itself. The typed path is what a SYSTEM agent does instead of
// a credential provider: it starts this binary once as SYSTEM inside the
// target session, and that child joins the session's interactive window
// station, dismisses the lock screen's clock with a press that deletes
// nothing, waits for the desktop receiving input to become the secure one —
// the clock phase runs on Default and the fields on Winlogon — and types the
// password as Unicode events followed by return, the same sequence the macOS
// unlock walks. The password is verified with LogonUser in the parent first,
// so a wrong one fails here and never counts as a failed unlock at the screen.
//
// A sign-in for an account with no session goes through the same screen: the
// console is parked at the logon screen, which a SYSTEM process flips to its
// name-and-password fields (the dontdisplaylastusername policy, restored
// after) and the credentials are typed as name, tab, password, return. The
// sign-in resumes a disconnected session of the same account, authenticating
// and unlocking in one step, exactly as the macOS login window does.

// Pacing for the typed paths, the same numbers the macOS unlock uses: the
// pause after the wake is how long the lock screen takes to reveal the
// password field, the delay between keystrokes is what a person types at, and
// the waits bound how long the sign-in screen is given before it is reported.
const (
	wakePause      = 2 * time.Second
	keystrokeDelay = 30 * time.Millisecond
	fieldPause     = 500 * time.Millisecond
	unlockPolls    = 40
	unlockPollStep = 250 * time.Millisecond
	// logonScreenPolls bounds the wait for a fresh greeter to attach to the
	// console after the current session is parked — a cold first switch spins
	// a new session up and starts LogonUI in it, which is slower than an
	// unlock.
	logonScreenPolls = 160
	loginPolls       = 400
	// signinformPause is how long a restarted LogonUI takes to be ready for
	// keys after the console reaches the sign-in screen.
	signinformPause = 6 * time.Second
)

// secureInputRequest is one line the parent hands the helper. Secret rides
// standard input rather than the command line, which every user on the host
// can read.
type secureInputRequest struct {
	Mode     string `json:"mode"`               // "unlock", "signin", "lock"
	Username string `json:"username,omitempty"` // signin: the account to type
	Secret   string `json:"secret,omitempty"`   // unlock, signin
	Capture  string `json:"capture,omitempty"`  // debug: save PNGs here before typing
}

// secureInputResult is the helper's one line back to the parent. OK means the
// keys were sent; whether the screen accepted them is the parent's poll to
// answer. Detail names the desktop the helper typed on, which is the first
// thing to check when the keys went nowhere.
type secureInputResult struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

// ServeSecureInput is the helper end: one request on standard input, one
// result line on standard output, then exit. The process was started as
// SYSTEM inside one session, which is the whole of its authority. The
// composition calls it through ServeSecureInputCommand.
func ServeSecureInput(ctx context.Context, logf func(string, ...any)) error {
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("reading the secure-input request: %w", err)
	}
	var req secureInputRequest
	if err := json.Unmarshal(raw, &req); err != nil {
		return fmt.Errorf("decoding the secure-input request: %w", err)
	}
	result := runSecureInput(ctx, req, logf)
	out, err := json.Marshal(result)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, string(out))
	return nil
}

// runSecureInput performs the sequence the request names. The lock needs no
// desktop; the typed modes pin the thread to the session's secure desktop
// first, which is where both the lock screen's password field and the
// sign-in screen live.
func runSecureInput(ctx context.Context, req secureInputRequest, logf func(string, ...any)) secureInputResult {
	// The debug capture comes from the environment when the request does not
	// name it, which is how a scheduled-task test asks for one without a
	// second wire through the protocol.
	if req.Capture == "" {
		req.Capture = os.Getenv("KARAKURI_SECURE_CAPTURE")
	}
	if req.Mode == "lock" {
		// The lock lands on the session the helper runs in, whatever desktop
		// its thread is on; no typing is involved.
		if err := powerControl(ctx, powerLock); err != nil {
			return secureInputResult{Error: err.Error()}
		}
		return secureInputResult{OK: true}
	}
	if req.Mode != "unlock" && req.Mode != "signin" {
		return secureInputResult{Error: fmt.Sprintf("unknown secure-input mode %q", req.Mode)}
	}
	detail, release, err := pinSecureDesktop(ctx)
	if err != nil {
		return secureInputResult{Error: err.Error(), Detail: detail}
	}
	defer release()
	if req.Mode == "unlock" {
		if err := typeUnlockSequence(ctx, req); err != nil {
			return secureInputResult{Error: err.Error(), Detail: detail}
		}
		return secureInputResult{OK: true, Detail: detail}
	}
	if err := typeSigninSequence(ctx, req); err != nil {
		return secureInputResult{Error: err.Error(), Detail: detail}
	}
	return secureInputResult{OK: true, Detail: detail}
}

// pinSecureDesktop puts the calling thread on the session's secure desktop.
// It joins the interactive window station — a service-started process
// inherits its parent's station, which is never the one to type on — pins
// whatever desktop is receiving input, dismisses the lock screen's clock
// with a press that deletes nothing, and re-pins when the input desktop
// changes: the clock phase of a lock screen and of the logon screen run on
// the Default desktop, and the switch to Winlogon is what brings the fields
// the password is typed into. The returned description names where the
// typing will land.
func pinSecureDesktop(ctx context.Context) (detail string, release func(), err error) {
	runtime.LockOSThread()
	release = func() { runtime.UnlockOSThread() }
	fail := func(format string, args ...any) (string, func(), error) {
		return fmt.Sprintf("session %d, %s", currentSessionID(), fmt.Sprintf(format, args...)), release, fmt.Errorf(format, args...)
	}
	station, err := openInteractiveStation()
	if err != nil {
		return fail("no interactive window station: %v", err)
	}
	defer windows.CloseHandle(station)
	if !setProcessStation(windows.Handle(station)) {
		return fail("could not join the interactive window station")
	}
	pinned, err := pinInputDesktop()
	if err != nil {
		return fail("%v", err)
	}
	if err := wakeConsoleInput(ctx); err != nil {
		windows.CloseHandle(pinned)
		return fail("waking the console: %v", err)
	}
	if err := pause(ctx, wakePause); err != nil {
		windows.CloseHandle(pinned)
		return fail("waiting for the console: %v", err)
	}
	// The press is what advances a clock-only lock screen to its password
	// field — a pointer move does not — and deletes nothing from a field
	// that is already showing. It is typed on whatever desktop is pinned,
	// which is the one receiving input at this moment.
	if err := pressKeys(ctx, autoapi.KeyStroke{Key: "backspace"}); err != nil {
		windows.CloseHandle(pinned)
		return fail("%v", err)
	}
	if err := pause(ctx, wakePause); err != nil {
		windows.CloseHandle(pinned)
		return fail("waiting for the secure desktop: %v", err)
	}
	for i := 0; i < 20; i++ {
		if name := desktopName(pinned); strings.EqualFold(name, "Winlogon") {
			return fmt.Sprintf("session %d, typing on the %s desktop", currentSessionID(), name), func() {
				windows.CloseHandle(pinned)
				runtime.UnlockOSThread()
			}, nil
		}
		// The wake moved the console off the clock phase; the input desktop
		// follows it to the secure one, where the fields are.
		windows.CloseHandle(pinned)
		if pinned, err = pinInputDesktop(); err != nil {
			return fail("%v", err)
		}
		if err := pause(ctx, 500*time.Millisecond); err != nil {
			windows.CloseHandle(pinned)
			return fail("waiting for the secure desktop: %v", err)
		}
	}
	windows.CloseHandle(pinned)
	return fail("the console never reached the secure desktop; it may be showing a screen no key press advances")
}

// pinInputDesktop pins the calling thread to the desktop receiving input.
func pinInputDesktop() (windows.Handle, error) {
	h, err := openInputDesktop()
	if err != nil {
		// The console may not be locked, in which case the default desktop
		// is the one to pin; the await loop re-pins when it changes.
		h, err = openDesktop("Default")
		if err != nil {
			return 0, fmt.Errorf("no desktop to pin: %w", err)
		}
	}
	if !setThreadDesktop(h) {
		windows.CloseHandle(h)
		return 0, fmt.Errorf("the input desktop refused the calling thread")
	}
	return h, nil
}

// typeUnlockSequence types the password and presses return. The console is
// awake and on the secure desktop; the pin dismissed the clock and awaited
// the password field.
func typeUnlockSequence(ctx context.Context, req secureInputRequest) error {
	if err := captureScreens(ctx, req.Capture); err != nil {
		return err
	}
	if err := pause(ctx, fieldPause); err != nil {
		return err
	}
	if err := typeText(ctx, autoapi.RealClock{}, req.Secret, keystrokeDelay); err != nil {
		return err
	}
	return pressKeys(ctx, autoapi.KeyStroke{Key: "return"})
}

// typeSigninSequence types an account name, a tab, and the password into the
// name-and-password fields of the sign-in screen. The console is already
// there; parking it and flipping the fields on are the parent's doing.
func typeSigninSequence(ctx context.Context, req secureInputRequest) error {
	if err := captureScreens(ctx, req.Capture); err != nil {
		return err
	}
	if req.Username == "" {
		return fmt.Errorf("no account name for the sign-in screen")
	}
	if err := pause(ctx, fieldPause); err != nil {
		return err
	}
	if err := typeText(ctx, autoapi.RealClock{}, req.Username, keystrokeDelay); err != nil {
		return err
	}
	if err := pressKeys(ctx, autoapi.KeyStroke{Key: "tab"}); err != nil {
		return err
	}
	if err := pause(ctx, fieldPause); err != nil {
		return err
	}
	if err := typeText(ctx, autoapi.RealClock{}, req.Secret, keystrokeDelay); err != nil {
		return err
	}
	return pressKeys(ctx, autoapi.KeyStroke{Key: "return"})
}

// wakeConsoleInput moves the pointer a little, which is what turns a display
// that has gone off back on and counts as the first input the lock screen
// waits for.
func wakeConsoleInput(ctx context.Context) error {
	for i := 0; i < 3; i++ {
		if err := sendMouseInputs(
			win.MOUSE_INPUT{Type: win.INPUT_MOUSE, Mi: win.MOUSEINPUT{Dx: 1, Dy: 1, DwFlags: win.MOUSEEVENTF_MOVE}},
			win.MOUSE_INPUT{Type: win.INPUT_MOUSE, Mi: win.MOUSEINPUT{Dx: -1, Dy: -1, DwFlags: win.MOUSEEVENTF_MOVE}},
		); err != nil {
			return fmt.Errorf("waking the console: %w", err)
		}
		if err := pause(ctx, 200*time.Millisecond); err != nil {
			return err
		}
	}
	return nil
}

// captureScreens writes a PNG of the console to the path the request names,
// the state the typed keys are about to land on. Absent or failing, it is
// skipped: it is a diagnostic, not part of the unlock.
func captureScreens(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	img, err := captureScreen(ctx, autoapi.CaptureRequest{})
	if err != nil {
		return fmt.Errorf("capturing the console before typing: %w", err)
	}
	_ = writePNG(img, path)
	return nil
}

// writePNG encodes one image to a file, for the helper's debug capture.
func writePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

// pause waits through the injected clock, so the helper's pacing is
// interruptible the way the engine's waits are.
func pause(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-autoapi.RealClock{}.After(d):
		return nil
	}
}

// openInteractiveStation opens this session's interactive window station, the
// one every desktop in the session belongs to. A service-started process
// inherits its parent's station instead, which is never the one to type on.
func openInteractiveStation() (windows.Handle, error) {
	ptr, err := windows.UTF16PtrFromString("WinSta0")
	if err != nil {
		return 0, err
	}
	h, _, callErr := procOpenWindowStationW.Call(uintptr(unsafe.Pointer(ptr)), 0, windows.MAXIMUM_ALLOWED)
	if h == 0 {
		return 0, fmt.Errorf("opening WinSta0: %v", callErr)
	}
	return windows.Handle(h), nil
}

// desktopName reads a desktop handle's name, for the diagnostics a typed
// sequence that landed nowhere needs.
func desktopName(h windows.Handle) string {
	var need uint32
	procGetUserObjectInformationW.Call(uintptr(h), uoiName, 0, 0, uintptr(unsafe.Pointer(&need)))
	if need == 0 {
		return "unnamed"
	}
	buf := make([]uint16, need/2+1)
	var got uint32
	if r, _, _ := procGetUserObjectInformationW.Call(uintptr(h), uoiName,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(need), uintptr(unsafe.Pointer(&got))); r == 0 {
		return "unnamed"
	}
	return windows.UTF16ToString(buf)
}

// secureInput runs the helper inside the named session and reports what it
// sent. The child is this binary re-executed with the argv the caller named,
// as SYSTEM in the session whose lock is being dismissed — the one identity
// the secure desktop accepts input from. The request crosses a pipe, never
// the command line.
func secureInput(ctx context.Context, argv []string, logf func(string, ...any), sessionID uint32, req secureInputRequest) (secureInputResult, error) {
	if len(argv) == 0 {
		return secureInputResult{}, fmt.Errorf("%w: credentialed unlock is not configured: "+
			"the caller must name its secure-input command for the in-session helper", autoapi.ErrUnsupported)
	}
	if !RunningAsSystem() {
		return secureInputResult{}, fmt.Errorf("%w: dismissing a Windows lock screen needs the agent running "+
			"as a system service, which starts the helper that types the password", autoapi.ErrUnsupported)
	}
	exe, err := os.Executable()
	if err != nil {
		return secureInputResult{}, fmt.Errorf("finding this binary to start the secure-input helper: %w", err)
	}
	tok, err := sessionToken(sessionID)
	if err != nil {
		return secureInputResult{}, fmt.Errorf("minting a SYSTEM token for session %d: %w", sessionID, err)
	}
	defer tok.Close()

	cmd := exec.CommandContext(ctx, exe, argv...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Token:         syscall.Token(tok),
		HideWindow:    true,
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	cmd.Stderr = secureInputLog{logf: logf}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return secureInputResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return secureInputResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return secureInputResult{}, fmt.Errorf("starting the secure-input helper in session %d: %w", sessionID, err)
	}

	request, err := json.Marshal(req)
	if err == nil {
		_, err = stdin.Write(append(request, '\n'))
	}
	stdin.Close()
	out, readErr := io.ReadAll(stdout)
	waitErr := cmd.Wait()

	if err != nil {
		return secureInputResult{}, fmt.Errorf("handing the request to the secure-input helper: %w", err)
	}
	if readErr != nil && len(out) == 0 {
		return secureInputResult{}, fmt.Errorf("reading the secure-input helper's result: %w", readErr)
	}
	result, ok := parseSecureInputResult(out)
	if !ok {
		// A helper that produced no result line died before it could send
		// anything; the wait error, if any, is the cause worth naming.
		if waitErr != nil {
			return secureInputResult{}, fmt.Errorf("the secure-input helper failed: %w", waitErr)
		}
		return secureInputResult{}, fmt.Errorf("the secure-input helper produced no result")
	}
	if !result.OK {
		if result.Detail != "" {
			return secureInputResult{}, fmt.Errorf("%s (%s)", result.Error, result.Detail)
		}
		return secureInputResult{}, errors.New(result.Error)
	}
	if result.Detail != "" && logf != nil {
		logf("%s", result.Detail)
	}
	return result, nil
}

// parseSecureInputResult reads the helper's JSON line out of its output,
// tolerating anything else the process printed first.
func parseSecureInputResult(out []byte) (secureInputResult, bool) {
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var result secureInputResult
		if err := json.Unmarshal([]byte(line), &result); err == nil {
			return result, true
		}
	}
	return secureInputResult{}, false
}

// secureInputLog forwards the helper's standard error to the caller's log
// sink, so a child that died before answering names why.
type secureInputLog struct{ logf func(string, ...any) }

// Write logs one chunk of the helper's standard error.
func (s secureInputLog) Write(p []byte) (int, error) {
	if s.logf != nil && len(p) > 0 {
		s.logf("secure-input helper: %s", string(p))
	}
	return len(p), nil
}

var _ io.Writer = secureInputLog{}

// openInputDesktop opens the desktop of this session that is receiving input
// right now, which is the secure one while the console is locked.
func openInputDesktop() (windows.Handle, error) {
	h, _, err := procOpenInputDesktop.Call(0, 0, windows.MAXIMUM_ALLOWED)
	if h == 0 {
		return 0, fmt.Errorf("opening the input desktop: %w", err)
	}
	return windows.Handle(h), nil
}

// openDesktop opens a desktop of this session by name.
func openDesktop(name string) (windows.Handle, error) {
	ptr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return 0, err
	}
	h, _, callErr := procOpenDesktopW.Call(uintptr(unsafe.Pointer(ptr)), 0, 0, windows.MAXIMUM_ALLOWED)
	if h == 0 {
		return 0, fmt.Errorf("opening the %s desktop: %w", name, callErr)
	}
	return windows.Handle(h), nil
}

// setThreadDesktop attaches the calling thread to the desktop, which is what
// points its SendInput calls at that desktop's input queue.
func setThreadDesktop(h windows.Handle) bool {
	r, _, _ := procSetThreadDesktop.Call(uintptr(h))
	return r != 0
}

// setProcessStation makes the station the calling process's own, which is
// what points the desktop lookups at the session's interactive station.
func setProcessStation(h windows.Handle) bool {
	r, _, _ := procSetProcessWindowStation.Call(uintptr(h))
	return r != 0
}

// sessionToken duplicates this process's primary token with the session id
// replaced, which is the token the in-session helper runs under. Setting the
// session id needs SeTcbPrivilege, so a caller that is not SYSTEM fails here
// before anything is started.
func sessionToken(id uint32) (windows.Token, error) {
	var tok windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY, &tok); err != nil {
		return 0, err
	}
	defer tok.Close()

	var dup windows.Token
	err := windows.DuplicateTokenEx(tok,
		windows.TOKEN_ASSIGN_PRIMARY|windows.TOKEN_DUPLICATE|windows.TOKEN_QUERY|
			windows.TOKEN_ADJUST_DEFAULT|windows.TOKEN_ADJUST_SESSIONID,
		nil, windows.SecurityAnonymous, windows.TokenPrimary, &dup)
	if err != nil {
		return 0, err
	}
	sid := uint32(id)
	if err := windows.SetTokenInformation(dup, windows.TokenSessionId,
		(*byte)(unsafe.Pointer(&sid)), uint32(unsafe.Sizeof(sid))); err != nil {
		dup.Close()
		return 0, err
	}
	return dup, nil
}

// RunningAsSystem reports whether this process is LocalSystem, the identity
// that may set a token's session and reach the secure desktop, the one a
// system service runs as, and the one the credential provider install requires.
func RunningAsSystem() bool {
	system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		return false
	}
	// Windows 7 does not support the current-process pseudo-token handle.
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_QUERY, &token); err != nil {
		return false
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return false
	}
	return windows.EqualSid(user.User.Sid, system)
}

// verifyPassword proves an account's password with LogonUser before anything
// types it, so a wrong one fails the call the way a wrong password should and
// the lock screen never counts a failed attempt. The NETWORK logon type is
// the password check's own shape: it proves the credential without opening a
// session or a profile of its own.
func verifyPassword(user, secret string) error {
	name, domain := splitAccount(user)
	namePtr, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return fmt.Errorf("account name %q is not a valid Windows name", name)
	}
	var domainPtr *uint16
	if domain != "" {
		if domainPtr, err = windows.UTF16PtrFromString(domain); err != nil {
			return fmt.Errorf("domain %q is not a valid Windows name", domain)
		}
	}
	secretPtr, err := windows.UTF16PtrFromString(secret)
	if err != nil {
		return fmt.Errorf("the password holds characters Windows does not accept")
	}
	var tok windows.Token
	r, _, callErr := procLogonUserW.Call(
		uintptr(unsafe.Pointer(namePtr)),
		uintptr(unsafe.Pointer(domainPtr)),
		uintptr(unsafe.Pointer(secretPtr)),
		logon32Network, logon32ProviderDefault,
		uintptr(unsafe.Pointer(&tok)))
	if r == 0 {
		if errors.Is(callErr, windows.ERROR_LOGON_FAILURE) {
			return fmt.Errorf("the password for %s was refused: wrong account or wrong password", user)
		}
		return fmt.Errorf("verifying the password for %s: %w", user, callErr)
	}
	tok.Close()
	return nil
}

// splitAccount splits DOMAIN\user, user@realm, and bare names into the parts
// LogonUser takes. A bare name is looked up on this machine, a UPN carries
// its domain in itself.
func splitAccount(user string) (name, domain string) {
	if i := strings.LastIndex(user, `\`); i >= 0 {
		return user[i+1:], user[:i]
	}
	if strings.IndexByte(user, '@') >= 0 {
		return user, ""
	}
	return user, "."
}

// namePasswordLogon flips the logon screen to its name-and-password fields —
// the Windows counterpart of the macOS SHOWFULLNAME flip, and the shape the
// typed sign-in needs: the account-tile list would put the password under
// whichever account was signed in last. It returns the function that puts the
// previous value back. Only a SYSTEM process may write the policy, and only
// it can restore what it found.
func namePasswordLogon() (restore func(), flipped bool) {
	if !RunningAsSystem() {
		return nil, false
	}
	key, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return nil, false
	}
	defer key.Close()

	const value = "dontdisplaylastusername"
	previous, _, err := key.GetIntegerValue(value)
	hadPrevious := err == nil
	if err := key.SetDWordValue(value, 1); err != nil {
		return nil, false
	}
	return func() {
		k, err := registry.OpenKey(registry.LOCAL_MACHINE,
			`SOFTWARE\Microsoft\Windows\CurrentVersion\Policies\System`,
			registry.SET_VALUE)
		if err != nil {
			return
		}
		defer k.Close()
		if hadPrevious {
			_ = k.SetDWordValue(value, uint32(previous))
			return
		}
		_ = k.DeleteValue(value)
	}, true
}

// restartLogonUI ends every LogonUI process, which Windows replaces at once
// from the policy now on disk. A logon screen that was already up is showing
// the tiles the previous policy chose; without the restart the flip would
// wait for a screen that is never drawn again.
func restartLogonUI() {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return
	}
	defer windows.CloseHandle(snapshot)
	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	for err := windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "LogonUI.exe") {
			continue
		}
		if h, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, entry.ProcessID); err == nil {
			_ = windows.TerminateProcess(h, 1)
			_ = windows.CloseHandle(h)
		}
	}
}
