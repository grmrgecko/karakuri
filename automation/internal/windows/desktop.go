//go:build windows

package windows

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/idleparse"
	"github.com/grmrgecko/karakuri/automation/internal/toastwin"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// The desktop functions call the Win32 API directly — idle time, the media
// keys, the clipboard, locking, sleeping, and opening things are each one or
// two calls — speech is SAPI over COM (speech.go), toasts are the WinRT
// notification API over COM (toastwin), and the balloon fallback is
// Shell_NotifyIcon (tray.go). Nothing in this backend spawns a process.

// The desktop procs, on the same user32 the input code uses plus the two other
// system DLLs a desktop action needs.
var (
	kernel32             = windows.NewLazySystemDLL("kernel32.dll")
	powrprof             = windows.NewLazySystemDLL("powrprof.dll")
	procGetLastInputInfo = user32.NewProc("GetLastInputInfo")
	procLockWorkStation  = user32.NewProc("LockWorkStation")
	procExitWindowsEx    = user32.NewProc("ExitWindowsEx")
	procGlobalSize       = kernel32.NewProc("GlobalSize")
	procGetTickCount     = kernel32.NewProc("GetTickCount")
	procSetSuspendState  = powrprof.NewProc("SetSuspendState")
)

// Virtual key codes for the media keys, from the Win32 documentation. Windows
// exposes a single play/pause toggle rather than separate keys.
const (
	vkMediaNext      = 0xB0
	vkMediaPrev      = 0xB1
	vkMediaStop      = 0xB2
	vkMediaPlayPause = 0xB3
	vkVolumeMute     = 0xAD
	vkVolumeDown     = 0xAE
	vkVolumeUp       = 0xAF
)

// toastAppID names the agent in the toast and in the notification settings
// panel. Registering it under HKCU is what keeps a toast from being dropped
// as coming from an unknown application, and it is done once per process.
const toastAppID = "Karakuri Agent"

var toastRegister sync.Once

// toastXML builds the toast payload: the toast schema's simplest document,
// with the text escaped here because toastwin hands the XML over verbatim.
func toastXML(title, message string) string {
	var b strings.Builder
	b.WriteString(`<toast><visual><binding template="ToastGeneric">`)
	if title != "" {
		b.WriteString("<text hint-maxLines=\"1\">")
		_ = xml.EscapeText(&b, []byte(title))
		b.WriteString("</text>")
	}
	b.WriteString("<text>")
	_ = xml.EscapeText(&b, []byte(message))
	b.WriteString("</text>")
	b.WriteString(`</binding></visual></toast>`)
	return b.String()
}

// notifyDesktop shows a toast through the WinRT notification API, reached over
// COM with no process spawned (internal/toastwin). The Shell_NotifyIcon
// balloon stays behind it for hosts without the toast API — Windows Server,
// or notifications off at the policy level — and needs the same shell tray
// the old PowerShell fallback did, just without the process.
func notifyDesktop(ctx context.Context, n autoapi.Notification) error {
	toastRegister.Do(func() { _ = toastwin.RegisterAppID(toastAppID) })
	if err := toastwin.Show(toastAppID, toastXML(n.Title, n.Message)); err == nil {
		return nil
	}
	return showBalloon(ctx, n.Title, n.Message)
}

// lastInputInfo mirrors LASTINPUTINFO: the struct size and the tick count of
// the last input event in this session.
type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// idleSeconds measures how long the session has been untouched.
// GetLastInputInfo is per session, so it measures the session the agent runs
// in. The subtraction is unsigned 32-bit, which is what keeps it right across
// the 49.7 day tick wrap.
func idleSeconds(ctx context.Context, now time.Time) (float64, string, error) {
	info := lastInputInfo{cbSize: uint32(unsafe.Sizeof(lastInputInfo{}))}
	ret, _, err := procGetLastInputInfo.Call(uintptr(unsafe.Pointer(&info)))
	if ret == 0 {
		return 0, "", fmt.Errorf("reading the last input time: %w", err)
	}
	ticks, _, _ := procGetTickCount.Call()
	millis := uint32(ticks) - info.dwTime
	return idleparse.FromMillis(int64(millis)), "getlastinputinfo", nil
}

// mediaControl presses the media key for the action, through the same
// SendInput path the keyboard node uses. The shell routes the key to whatever
// holds playback, so the player setting has nothing to address here.
func mediaControl(ctx context.Context, action, player string, volume float64) error {
	var key uint16
	switch action {
	case mediaPlayPause:
		key = vkMediaPlayPause
	case mediaNext:
		key = vkMediaNext
	case mediaPrevious:
		key = vkMediaPrev
	case mediaStop:
		key = vkMediaStop
	case mediaVolumeUp:
		key = vkVolumeUp
	case mediaVolumeDown:
		key = vkVolumeDown
	case mediaMute, mediaMuteToggle:
		// The mute key is a toggle, so an explicit mute cannot be told from it.
		key = vkVolumeMute
	case mediaPlay, mediaPause:
		return fmt.Errorf("windows has one play/pause key, so %q cannot be sent on its own; use play or pause", action)
	case mediaUnmute:
		return fmt.Errorf("windows has one mute key, which toggles; use toggle mute")
	case mediaVolumeSet:
		return fmt.Errorf("windows exposes no volume level to set; use volume up or volume down")
	default:
		return fmt.Errorf("unknown media action %q", action)
	}
	return sendKeyboardInputs(keyInput(key, false), keyInput(key, true))
}

// withClipboard runs one operation with the clipboard open, retrying the open
// briefly because whichever application last touched the clipboard may still
// be holding it.
func withClipboard(op func() error) error {
	var opened bool
	for attempt := 0; attempt < 10; attempt++ {
		if win.OpenClipboard(0) {
			opened = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !opened {
		return fmt.Errorf("another application is holding the clipboard")
	}
	defer win.CloseClipboard()
	return op()
}

// clipboardRead returns the clipboard's text. Windows has one clipboard, so
// the primary selection reads the same board; a clipboard holding no text
// reads as empty rather than failing.
func clipboardRead(ctx context.Context, selection string) (string, error) {
	var text string
	err := withClipboard(func() error {
		handle := win.GetClipboardData(win.CF_UNICODETEXT)
		if handle == 0 {
			return nil
		}
		ptr := win.GlobalLock(win.HGLOBAL(handle))
		if ptr == nil {
			return fmt.Errorf("cannot read the clipboard's memory")
		}
		defer win.GlobalUnlock(win.HGLOBAL(handle))
		size, _, _ := procGlobalSize.Call(uintptr(handle))
		if size < 2 {
			return nil
		}
		units := unsafe.Slice((*uint16)(ptr), size/2)
		text = windows.UTF16ToString(units)
		return nil
	})
	return text, err
}

// clipboardWrite puts text on the clipboard. The system owns the memory once
// SetClipboardData accepts it, so the handle is freed only on refusal.
func clipboardWrite(ctx context.Context, selection, text string) error {
	units := utf16.Encode([]rune(text))
	units = append(units, 0)

	handle := win.GlobalAlloc(win.GMEM_MOVEABLE, uintptr(len(units)*2))
	if handle == 0 {
		return fmt.Errorf("cannot allocate clipboard memory")
	}
	ptr := win.GlobalLock(handle)
	if ptr == nil {
		win.GlobalFree(handle)
		return fmt.Errorf("cannot write the clipboard's memory")
	}
	copy(unsafe.Slice((*uint16)(ptr), len(units)), units)
	win.GlobalUnlock(handle)

	err := withClipboard(func() error {
		if !win.EmptyClipboard() {
			return fmt.Errorf("cannot clear the clipboard")
		}
		if win.SetClipboardData(win.CF_UNICODETEXT, win.HANDLE(handle)) == 0 {
			return fmt.Errorf("windows refused the clipboard text")
		}
		return nil
	})
	if err != nil {
		win.GlobalFree(handle)
	}
	return err
}

// openURL opens a link in the default browser. ShellExecute takes the URL as
// one argument, so a query string needs no quoting of its own.
func openURL(ctx context.Context, url string) error {
	return shellOpen(url, "")
}

// openApp launches a program and leaves it running. A name that is not an
// executable in PATH is handed to the shell, which resolves registered
// applications and file associations the way the Start menu does.
func openApp(ctx context.Context, app string, args []string) error {
	if path, ok := toolsh.ToolPath(app); ok {
		return toolsh.StartDetached(path, args...)
	}
	return shellOpen(app, windows.ComposeCommandLine(args))
}

// shellOpen asks the shell to open a target with its default verb.
func shellOpen(target, params string) error {
	targetPtr, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	var paramsPtr *uint16
	if params != "" {
		if paramsPtr, err = windows.UTF16PtrFromString(params); err != nil {
			return err
		}
	}
	if err := windows.ShellExecute(0, nil, targetPtr, paramsPtr, nil, win.SW_SHOWNORMAL); err != nil {
		return fmt.Errorf("the shell cannot open %s: %w", target, err)
	}
	return nil
}

// ExitWindowsEx flags: what to do, and force only what is hung — the gentler
// force, so an application holding an unsaved document blocks the action
// visibly rather than dying with it.
const (
	ewxLogoff      = 0x0
	ewxReboot      = 0x2
	ewxPoweroff    = 0x8
	ewxForceIfHung = 0x10

	shutdownReasonPlanned = 0x80000000
)

// powerControl performs a session or power action.
func powerControl(ctx context.Context, action string) error {
	switch action {
	case powerLock:
		if ret, _, err := procLockWorkStation.Call(); ret == 0 {
			return fmt.Errorf("windows refused to lock the session: %w", err)
		}
		return nil
	case powerSleep:
		// The first argument asks for sleep rather than hibernate, but Windows
		// hibernates anyway when hibernation is enabled. Turn it off with
		// powercfg /hibernate off to get sleep.
		if ret, _, _ := procSetSuspendState.Call(0, 1, 0); ret == 0 {
			return fmt.Errorf("windows refused to suspend")
		}
		return nil
	case powerLogout:
		return exitWindows(ewxLogoff | ewxForceIfHung)
	case powerRestart:
		return exitWindows(ewxReboot | ewxForceIfHung)
	case powerShutdown:
		return exitWindows(ewxPoweroff | ewxForceIfHung)
	}
	return fmt.Errorf("unknown power action %q", action)
}

// exitWindows ends the session or the machine. Shutting down and rebooting
// need SeShutdownPrivilege enabled on the process token first; enabling it is
// attempted for every action and only ExitWindowsEx's own answer decides.
func exitWindows(flags uint32) error {
	enableShutdownPrivilege()
	if ret, _, err := procExitWindowsEx.Call(uintptr(flags), shutdownReasonPlanned); ret == 0 {
		return fmt.Errorf("windows refused: %w", err)
	}
	return nil
}

// enableShutdownPrivilege turns on SeShutdownPrivilege, which every
// interactive user holds disabled by default.
func enableShutdownPrivilege() {
	var token windows.Token
	err := windows.OpenProcessToken(windows.CurrentProcess(),
		windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token)
	if err != nil {
		return
	}
	defer token.Close()

	name, err := windows.UTF16PtrFromString("SeShutdownPrivilege")
	if err != nil {
		return
	}
	var luid windows.LUID
	if err := windows.LookupPrivilegeValue(nil, name, &luid); err != nil {
		return
	}
	privileges := windows.Tokenprivileges{
		PrivilegeCount: 1,
		Privileges: [1]windows.LUIDAndAttributes{
			{Luid: luid, Attributes: windows.SE_PRIVILEGE_ENABLED},
		},
	}
	_ = windows.AdjustTokenPrivileges(token, false, &privileges, 0, nil, nil)
}

// windowsSpeechRate converts words per minute into the synthesizer's own scale,
// where zero is its default pace of about 200 words a minute and each step is
// roughly a tenth of that either way.
func windowsSpeechRate(wpm int) int {
	const defaultRate = 200
	scaled := (wpm - defaultRate) * 10 / defaultRate
	if scaled > 10 {
		return 10
	}
	if scaled < -10 {
		return -10
	}
	return scaled
}
