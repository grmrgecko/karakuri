//go:build windows

package windows

import (
	"context"
	"fmt"
	"io"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// interactiveDesktop is the window station and desktop a signed-in user's
// visible session runs on. A process started there drives that user's real
// keyboard, mouse, screen, and windows; it is the counterpart of the Winlogon
// desktop the credentialed unlock types on.
const interactiveDesktop = `WinSta0\Default`

// ActiveConsoleUser reports the session on the physical console and the account
// signed into it, when one is. It answers only for a caller allowed to mint
// that user's token — a system service — because querying the token is what
// proves a user is there rather than a bare sign-in screen. The ok result is
// false when the console is at the logon screen with nobody signed in.
func ActiveConsoleUser() (id uint32, user string, ok bool) {
	id = activeConsoleSession()
	if id == 0 || id == 0xffffffff {
		return 0, "", false
	}
	var tok windows.Token
	if err := windows.WTSQueryUserToken(id, &tok); err != nil {
		return 0, "", false
	}
	tok.Close()
	return id, querySessionString(id, wtsUserName), true
}

// SpawnInteractive starts argv as the user signed into the physical console, on
// their visible desktop, and returns the framed pipes to it. It is how a system
// service reaches a desktop it is not part of: Windows delivers synthesized
// input only to a process inside the target session, so reaching the desktop
// means starting a process there, and only SYSTEM may mint the session's user
// token to do so. The child runs as the console user, not as SYSTEM, so it
// holds no more than the desktop it drives.
//
// The desktop is named in the child's startup information rather than joined
// after the fact, so every thread the Go runtime later starts inherits it. That
// matters because the helper answers each capability call on its own goroutine,
// and a thread not on this desktop would send its input into the void.
func SpawnInteractive(ctx context.Context, exe string, argv []string, logf func(string, ...any)) (io.ReadCloser, io.WriteCloser, func(), error) {
	log := logf
	if log == nil {
		log = func(string, ...any) {}
	}
	if !RunningAsSystem() {
		return nil, nil, nil, fmt.Errorf("reaching the console desktop from outside its session needs an agent running as a system service")
	}
	id, _, ok := ActiveConsoleUser()
	if !ok {
		return nil, nil, nil, fmt.Errorf("no user is signed in at the console to run the desktop helper as")
	}
	var token windows.Token
	if err := windows.WTSQueryUserToken(id, &token); err != nil {
		return nil, nil, nil, fmt.Errorf("minting the console user's token for session %d: %w", id, err)
	}
	defer token.Close()

	// The user's own environment, so the helper sees their profile, AppData,
	// and temp directories rather than the service account's. A failure here is
	// not fatal: the input and capture capabilities do not need it, so the
	// helper falls back to the service environment with a note.
	var envBlock *uint16
	if err := windows.CreateEnvironmentBlock(&envBlock, token, false); err != nil {
		log("loading the console user's environment: %v (using the service environment)", err)
		envBlock = nil
	}
	defer func() {
		if envBlock != nil {
			_ = windows.DestroyEnvironmentBlock(envBlock)
		}
	}()

	inheritSA := &windows.SecurityAttributes{InheritHandle: 1}
	inheritSA.Length = uint32(unsafe.Sizeof(*inheritSA))

	// Three pipes: the child reads its standard input, writes its output, and
	// writes diagnostics. Only the child's ends are inheritable; the parent's
	// ends are cleared so the child never holds a handle to them.
	var childStdinR, parentStdinW windows.Handle
	if err := windows.CreatePipe(&childStdinR, &parentStdinW, inheritSA, 0); err != nil {
		return nil, nil, nil, fmt.Errorf("creating the helper's input pipe: %w", err)
	}
	var parentStdoutR, childStdoutW windows.Handle
	if err := windows.CreatePipe(&parentStdoutR, &childStdoutW, inheritSA, 0); err != nil {
		closeHandles(childStdinR, parentStdinW)
		return nil, nil, nil, fmt.Errorf("creating the helper's output pipe: %w", err)
	}
	var parentStderrR, childStderrW windows.Handle
	if err := windows.CreatePipe(&parentStderrR, &childStderrW, inheritSA, 0); err != nil {
		closeHandles(childStdinR, parentStdinW, parentStdoutR, childStdoutW)
		return nil, nil, nil, fmt.Errorf("creating the helper's diagnostics pipe: %w", err)
	}
	_ = windows.SetHandleInformation(parentStdinW, windows.HANDLE_FLAG_INHERIT, 0)
	_ = windows.SetHandleInformation(parentStdoutR, windows.HANDLE_FLAG_INHERIT, 0)
	_ = windows.SetHandleInformation(parentStderrR, windows.HANDLE_FLAG_INHERIT, 0)

	desktop, err := windows.UTF16PtrFromString(interactiveDesktop)
	if err != nil {
		closeHandles(childStdinR, parentStdinW, parentStdoutR, childStdoutW, parentStderrR, childStderrW)
		return nil, nil, nil, err
	}
	appName, err := windows.UTF16PtrFromString(exe)
	if err != nil {
		closeHandles(childStdinR, parentStdinW, parentStdoutR, childStdoutW, parentStderrR, childStderrW)
		return nil, nil, nil, err
	}
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(append([]string{exe}, argv...)))
	if err != nil {
		closeHandles(childStdinR, parentStdinW, parentStdoutR, childStdoutW, parentStderrR, childStderrW)
		return nil, nil, nil, err
	}

	si := windows.StartupInfo{
		Desktop:   desktop,
		Flags:     windows.STARTF_USESTDHANDLES,
		StdInput:  childStdinR,
		StdOutput: childStdoutW,
		StdErr:    childStderrW,
	}
	si.Cb = uint32(unsafe.Sizeof(si))
	var pi windows.ProcessInformation

	flags := uint32(windows.CREATE_NO_WINDOW)
	if envBlock != nil {
		flags |= windows.CREATE_UNICODE_ENVIRONMENT
	}
	err = windows.CreateProcessAsUser(token, appName, cmdLine, nil, nil, true, flags, envBlock, nil, &si, &pi)
	// The child holds its own copies of the inheritable ends; drop the parent's
	// so a closed pipe reports end-of-file rather than hanging on this side.
	closeHandles(childStdinR, childStdoutW, childStderrW)
	if err != nil {
		closeHandles(parentStdinW, parentStdoutR, parentStderrR)
		return nil, nil, nil, fmt.Errorf("starting the desktop helper in session %d: %w", id, err)
	}
	_ = windows.CloseHandle(pi.Thread)

	stdinFile := os.NewFile(uintptr(parentStdinW), "karakuri-helper-stdin")
	stdoutFile := os.NewFile(uintptr(parentStdoutR), "karakuri-helper-stdout")
	stderrFile := os.NewFile(uintptr(parentStderrR), "karakuri-helper-stderr")

	// Drain diagnostics so the child never blocks on a full pipe, and so a
	// backend failing inside it reaches the parent's log rather than vanishing.
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErr := stderrFile.Read(buf)
			if n > 0 {
				log("desktop helper: %s", string(buf[:n]))
			}
			if readErr != nil {
				return
			}
		}
	}()

	stop := func() {
		_ = stdinFile.Close() // end-of-file asks the helper to exit on its own
		_ = windows.TerminateProcess(pi.Process, 1)
		_ = windows.CloseHandle(pi.Process)
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	}
	return stdoutFile, stdinFile, stop, nil
}

// closeHandles closes each valid handle, for the error paths that must not leak
// a half-built set of pipes.
func closeHandles(handles ...windows.Handle) {
	for _, h := range handles {
		if h != 0 && h != windows.InvalidHandle {
			_ = windows.CloseHandle(h)
		}
	}
}
