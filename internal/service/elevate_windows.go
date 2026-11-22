//go:build windows

package service

import (
	"errors"
	"fmt"
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ElevateIfNeeded relaunches this process through the UAC prompt when the
// action needs the system service manager and the token is not elevated,
// which saves a round trip through "access is denied" and an administrator
// prompt. It re-runs os.Args verbatim, so only a bare `service` command may
// call it: anything with side effects before the service action would run
// them twice. Status stays local, since querying a service needs no
// elevation. Handled means the answer is final: the command ran elevated (or
// the prompt was declined) and the caller has nothing left to do.
func ElevateIfNeeded(action string, userService bool) (handled bool, err error) {
	if userService || action == "status" || Privileged() {
		return false, nil
	}
	return true, relaunchElevated()
}

// shellExecuteInfo is SHELLEXECUTEINFOW, laid out for ShellExecuteExW.
type shellExecuteInfo struct {
	cbSize         uint32
	fMask          uint32
	hwnd           windows.Handle
	verb           *uint16
	file           *uint16
	parameters     *uint16
	directory      *uint16
	nShow          int32
	hInstApp       windows.Handle
	idList         uintptr
	class          *uint16
	hkeyClass      windows.Handle
	dwHotKey       uint32
	hIconOrMonitor windows.Handle
	hProcess       windows.Handle
}

// relaunchElevated runs this command again with the "runas" verb, waits for
// it, and mirrors its exit status. The child prints into the caller's own
// console (adoptCallerConsole, from the top of Control), so the output reads
// as if the command had run here; a non-zero exit therefore only names the
// status, since the child already said what failed.
func relaunchElevated() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}

	// SEE_MASK_NOCLOSEPROCESS keeps the process handle to wait on, and
	// SEE_MASK_NO_CONSOLE asks for the caller's console rather than a new
	// one; elevation does not always honor it, which is what the child's
	// console adoption covers.
	const (
		seeMaskNoCloseProcess = 0x00000040
		seeMaskNoConsole      = 0x00008000
	)
	info := shellExecuteInfo{
		fMask:      seeMaskNoCloseProcess | seeMaskNoConsole,
		verb:       windows.StringToUTF16Ptr("runas"),
		file:       windows.StringToUTF16Ptr(exe),
		parameters: windows.StringToUTF16Ptr(joinArgs(os.Args[1:])),
		nShow:      windows.SW_HIDE,
	}
	info.cbSize = uint32(unsafe.Sizeof(info))

	shellExecuteEx := windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")
	ok, _, callErr := shellExecuteEx.Call(uintptr(unsafe.Pointer(&info)))
	if ok == 0 {
		if errors.Is(callErr, windows.ERROR_CANCELLED) {
			return fmt.Errorf("elevation declined; re-run from an administrator prompt, or pass --scope=user for a logon scheduled task")
		}
		return fmt.Errorf("requesting elevation: %w", callErr)
	}
	defer func() { _ = windows.CloseHandle(info.hProcess) }()

	if _, err := windows.WaitForSingleObject(info.hProcess, windows.INFINITE); err != nil {
		return fmt.Errorf("waiting for the elevated command: %w", err)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.hProcess, &code); err != nil {
		return fmt.Errorf("reading the elevated command's exit status: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("the elevated command exited with status %d", code)
	}
	return nil
}

// adoptCallerConsole moves an elevated relaunch's output onto the console the
// operator is watching. Elevation may allocate the child a console of its
// own (hidden, per relaunchElevated), so an elevated process that owns its
// console alone attaches to its parent's instead: the parent of an elevated
// launch is the caller, whose console is the operator's shell. The standard
// streams are reopened on the adopted console, since the ones inherited at
// startup still point at the freed one. Failing any of it leaves the hidden
// console in place, where the exit status still reaches the parent.
func adoptCallerConsole() {
	if !Privileged() {
		return
	}
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")

	var pids [2]uint32
	n, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(
		uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return
	}

	const attachParentProcess = ^uintptr(0)
	_, _, _ = kernel32.NewProc("FreeConsole").Call()
	ok, _, _ := kernel32.NewProc("AttachConsole").Call(attachParentProcess)
	if ok == 0 {
		return
	}
	if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout = out
		os.Stderr = out
	}
}
