//go:build windows

package windows

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/lxn/win"
	syswin "golang.org/x/sys/windows"
)

// The balloon fallback is Shell_NotifyIcon, the call the old PowerShell
// script reached through WinForms: a hidden window anchors a
// tray icon whose NIF_INFO text the shell renders as the balloon. The icon
// has to outlive the balloon, so the call waits for the shell's display
// interval before removing it.

// balloonLinger keeps the icon alive through the usual ten-second display
// interval. Windows accessibility settings can request a longer interval.
const balloonLinger = 11 * time.Second

// showBalloon shows one tray balloon. It fails when the shell refuses the
// icon, which is a session with no tray to put it in.
func showBalloon(ctx context.Context, title, message string) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	class, err := syswin.UTF16PtrFromString("STATIC")
	if err != nil {
		return err
	}
	hwnd := win.CreateWindowEx(0, class, nil, 0, 0, 0, 0, 0, 0, 0, 0, nil)
	if hwnd == 0 {
		return fmt.Errorf("no window could anchor the tray icon")
	}

	defer win.DestroyWindow(hwnd)

	var nid win.NOTIFYICONDATA
	nid.CbSize = uint32(unsafe.Sizeof(nid))
	nid.HWnd = hwnd
	nid.UID = 1
	nid.UFlags = win.NIF_ICON
	nid.HIcon = win.LoadIcon(0, win.MAKEINTRESOURCE(win.IDI_INFORMATION))
	nid.DwInfoFlags = win.NIIF_INFO
	copyUTF16(nid.SzInfo[:], message)
	copyUTF16(nid.SzInfoTitle[:], title)

	if !win.Shell_NotifyIcon(win.NIM_ADD, &nid) {
		return fmt.Errorf("the shell declined the tray balloon")
	}
	defer win.Shell_NotifyIcon(win.NIM_DELETE, &nid)
	nid.UFlags = win.NIF_INFO
	if !win.Shell_NotifyIcon(win.NIM_MODIFY, &nid) {
		return fmt.Errorf("the shell declined the tray notification")
	}
	// A short-lived CLI must keep the window alive for the shell to show
	// the balloon. DestroyWindow must run on the thread that created it.
	timer := time.NewTimer(balloonLinger)
	defer timer.Stop()
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		var msg win.MSG
		for win.PeekMessage(&msg, 0, 0, 0, win.PM_REMOVE) {
			win.TranslateMessage(&msg)
			win.DispatchMessage(&msg)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case <-tick.C:
		}
	}
}

// copyUTF16 fills a fixed UTF-16 field, truncating to leave the terminator.
func copyUTF16(dst []uint16, s string) {
	encoded := syswin.StringToUTF16(s)
	if len(encoded) > len(dst) {
		encoded = encoded[:len(dst)-1]
	}
	copy(dst, encoded)
}
