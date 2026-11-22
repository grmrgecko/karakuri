//go:build windows

package service

import (
	"unsafe"

	"golang.org/x/sys/windows"
)

// hideOwnConsole hides the console window when this process is its only
// owner, which is how a scheduled-task launch looks: Windows allocates a
// fresh console for a console binary started in the interactive session, and
// leaving it visible parks a black window on the desktop at every logon. A
// process started from a shell shares that shell's console, so an operator
// running the command by hand keeps their output. The cost is that `start
// karakuri-agent run` from cmd also hides its window; the window is hidden
// rather than freed, so stdout stays a valid handle either way.
func hideOwnConsole() {
	kernel32 := windows.NewLazySystemDLL("kernel32.dll")

	var pids [2]uint32
	n, _, _ := kernel32.NewProc("GetConsoleProcessList").Call(
		uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return
	}
	hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call()
	if hwnd == 0 {
		return
	}
	const swHide = 0
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("ShowWindow").Call(hwnd, swHide)
}
