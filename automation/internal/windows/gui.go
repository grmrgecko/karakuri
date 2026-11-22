//go:build windows

package windows

import (
	"context"
	"fmt"
	"image"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf16"
	"unsafe"

	"github.com/kbinani/screenshot"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Windows needs no helper installed: input goes through SendInput and the
// window list through user32, both of which the agent calls directly. The
// structures SendInput takes are a union whose layout differs by architecture,
// so they come from lxn/win, which the screenshot library already brings in;
// the three functions it does not wrap are declared here against the same
// user32 the rest of it uses.
var (
	user32                   = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows          = user32.NewProc("EnumWindows")
	procGetWindowTextW       = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW = user32.NewProc("GetWindowTextLengthW")
	procVkKeyScanW           = user32.NewProc("VkKeyScanW")
)

// windowsKeyCodes maps the key vocabulary onto virtual key codes.
var windowsKeyCodes = map[string]uint16{
	"return": win.VK_RETURN, "escape": 0x1B, "tab": 0x09, "space": 0x20,
	"backspace": 0x08, "delete": 0x2E, "insert": 0x2D, "home": 0x24, "end": 0x23,
	"pageup": 0x21, "pagedown": 0x22,
	"up": 0x26, "down": 0x28, "left": 0x25, "right": 0x27,
	"printscreen": 0x2C, "menu": win.VK_APPS, "pause": win.VK_PAUSE,
	"capslock": win.VK_CAPITAL, "numlock": 0x90, "scrolllock": 0x91,
	"plus": win.VK_OEM_PLUS,
}

// windowsModifierKeys maps a canonical modifier onto the key held for it.
var windowsModifierKeys = map[string]uint16{
	autoapi.ModControl: win.VK_CONTROL,
	autoapi.ModAlt:     win.VK_MENU,
	autoapi.ModShift:   win.VK_SHIFT,
	autoapi.ModSuper:   win.VK_LWIN,
}

// keyboardInputSize is what SendInput is told each record is. It is the size of
// the whole union, which the keyboard record carries padding to reach.
var keyboardInputSize = int32(unsafe.Sizeof(win.KEYBD_INPUT{}))

// mouseInputSize is the same size measured through the mouse record.
var mouseInputSize = int32(unsafe.Sizeof(win.MOUSE_INPUT{}))

// captureScreen photographs the screen through the same library the other
// platforms use, which talks to Windows directly and needs no cgo.
func captureScreen(ctx context.Context, req autoapi.CaptureRequest) (image.Image, error) {
	displays := screenshot.NumActiveDisplays()
	if displays == 0 {
		return nil, fmt.Errorf("this session reports no active screens")
	}
	if req.Display < 0 || req.Display >= displays {
		return nil, fmt.Errorf("screen %d does not exist; this session has %d", req.Display, displays)
	}

	bounds := screenshot.GetDisplayBounds(req.Display)
	if req.Region && req.DesktopCoordinates {
		for i := 0; i < displays; i++ {
			bounds = bounds.Union(screenshot.GetDisplayBounds(i))
		}
	}
	if req.Region {
		region := image.Rect(0, 0, req.Width, req.Height).Add(image.Pt(bounds.Min.X+req.X, bounds.Min.Y+req.Y))
		if req.DesktopCoordinates {
			region = image.Rect(0, 0, req.Width, req.Height).Add(image.Pt(req.X, req.Y))
		}
		clipped := region.Intersect(bounds)
		if clipped.Empty() {
			return nil, fmt.Errorf("the region %dx%d at %d,%d is outside screen %d",
				req.Width, req.Height, req.X, req.Y, req.Display)
		}
		img, err := screenshot.CaptureRect(clipped)
		if err != nil {
			return nil, fmt.Errorf("cannot capture the region: %w", err)
		}
		return autoapi.WithCaptureMapping(img, &autoapi.CaptureMapping{OriginX: float64(clipped.Min.X), OriginY: float64(clipped.Min.Y), UnitsPerPixelX: 1, UnitsPerPixelY: 1}), nil
	}

	img, err := screenshot.CaptureDisplay(req.Display)
	if err != nil {
		return nil, fmt.Errorf("cannot capture screen %d: %w", req.Display, err)
	}
	return autoapi.WithCaptureMapping(img, &autoapi.CaptureMapping{OriginX: float64(bounds.Min.X), OriginY: float64(bounds.Min.Y), UnitsPerPixelX: 1, UnitsPerPixelY: 1}), nil
}

// listWindows reports the visible top-level windows. A window with no title is
// skipped: those are the invisible message-only and tool windows every process
// keeps, and none of them is what a caller means by a window.
func listWindows(ctx context.Context) ([]autoapi.WindowInfo, error) {
	foreground := win.GetForegroundWindow()

	var windowsFound []autoapi.WindowInfo
	callback := syscall.NewCallback(func(hwnd syscall.Handle, _ uintptr) uintptr {
		handle := win.HWND(hwnd)
		if !win.IsWindowVisible(handle) {
			return 1
		}
		title := windowTitle(handle)
		if title == "" {
			return 1
		}
		var pid uint32
		win.GetWindowThreadProcessId(handle, &pid)

		class := make([]uint16, 256)
		className := ""
		if n, err := win.GetClassName(handle, &class[0], len(class)); err == nil && n > 0 {
			className = syscall.UTF16ToString(class[:n])
		}

		enabled := win.IsWindowEnabled(handle)
		imagePath := ""
		if process, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid); err == nil {
			buf := make([]uint16, 32768)
			size := uint32(len(buf))
			if windows.QueryFullProcessImageName(process, 0, &buf[0], &size) == nil {
				imagePath = windows.UTF16ToString(buf[:size])
			}
			windows.CloseHandle(process)
		}
		windowsFound = append(windowsFound, autoapi.WindowInfo{
			Enabled: &enabled, Image: imagePath,
			ID:     fmt.Sprintf("0x%x", uintptr(hwnd)),
			Title:  title,
			Class:  className,
			PID:    int(pid),
			Active: handle == foreground,
		})
		var rect win.RECT
		if win.GetWindowRect(handle, &rect) {
			last := &windowsFound[len(windowsFound)-1]
			last.X, last.Y = int(rect.Left), int(rect.Top)
			last.Width, last.Height = int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
		}
		return 1
	})

	if ret, _, err := procEnumWindows.Call(callback, 0); ret == 0 {
		return nil, fmt.Errorf("cannot list windows: %w", err)
	}
	return windowsFound, nil
}

// windowTitle reads one window's title.
func windowTitle(hwnd win.HWND) string {
	length, _, _ := procGetWindowTextLengthW.Call(uintptr(hwnd))
	if length == 0 {
		return ""
	}
	buf := make([]uint16, length+1)
	read, _, _ := procGetWindowTextW.Call(uintptr(hwnd), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	if read == 0 {
		return ""
	}
	return syscall.UTF16ToString(buf[:read])
}

// windowAction acts on one window.
func windowAction(ctx context.Context, target autoapi.WindowInfo, action string) error {
	hwnd, err := windowsHandle(target.ID)
	if err != nil {
		return err
	}
	switch action {
	case windowActivate:
		// Restoring first is what makes activating a minimized window bring it
		// back rather than leave it on the taskbar with the focus.
		win.ShowWindow(hwnd, win.SW_RESTORE)
		if !win.SetForegroundWindow(hwnd) {
			return fmt.Errorf("windows refused to bring %q forward, which it does when another "+
				"application is holding the foreground", target.Title)
		}
		return nil
	case windowMinimize:
		win.ShowWindow(hwnd, win.SW_MINIMIZE)
		return nil
	case windowMaximize:
		win.ShowWindow(hwnd, win.SW_MAXIMIZE)
		return nil
	case windowClose:
		win.PostMessage(hwnd, win.WM_CLOSE, 0, 0)
		return nil
	default:
		return fmt.Errorf("unknown window action %q", action)
	}
}

// windowsHandle reads a window handle back out of an id.
func windowsHandle(id string) (win.HWND, error) {
	trimmed := strings.TrimSpace(strings.ToLower(id))
	value, err := strconv.ParseUint(strings.TrimPrefix(trimmed, "0x"), 16, 64)
	if err != nil {
		return 0, fmt.Errorf("%q is not a window id", id)
	}
	return win.HWND(uintptr(value)), nil
}

// typeText types text as characters rather than as keys, so what arrives is
// what was written whatever layout the session has.
func typeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	units := utf16.Encode([]rune(text))
	for i, unit := range units {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := sendUnicode(unit); err != nil {
			return err
		}
		if delay > 0 && i < len(units)-1 {
			if err := waitFor(ctx, clock, delay); err != nil {
				return err
			}
		}
	}
	return nil
}

// sendUnicode presses and releases one UTF-16 unit as a character.
func sendUnicode(unit uint16) error {
	down := win.KEYBD_INPUT{
		Type: win.INPUT_KEYBOARD,
		Ki:   win.KEYBDINPUT{WScan: unit, DwFlags: win.KEYEVENTF_UNICODE},
	}
	up := down
	up.Ki.DwFlags |= win.KEYEVENTF_KEYUP
	return sendKeyboardInputs(down, up)
}

// pressKeys presses one combination: modifiers down, the key, modifiers up.
func pressKeys(ctx context.Context, stroke autoapi.KeyStroke) error {
	code, layoutMods, err := windowsKeyCode(stroke)
	if err != nil {
		return err
	}

	held := make([]uint16, 0, len(stroke.Mods)+1)
	for _, mod := range stroke.Mods {
		key, ok := windowsModifierKeys[mod]
		if !ok {
			return fmt.Errorf("windows has no %s key", mod)
		}
		held = append(held, key)
	}
	for _, mod := range layoutMods {
		if !stroke.HasMod(mod) {
			held = append(held, windowsModifierKeys[mod])
		}
	}

	inputs := make([]win.KEYBD_INPUT, 0, len(held)*2+2)
	for _, key := range held {
		inputs = append(inputs, keyInput(key, false))
	}
	inputs = append(inputs, keyInput(code, false), keyInput(code, true))
	for i := len(held) - 1; i >= 0; i-- {
		inputs = append(inputs, keyInput(held[i], true))
	}
	return sendKeyboardInputs(inputs...)
}

// keyInput builds one virtual-key event.
func keyInput(code uint16, up bool) win.KEYBD_INPUT {
	input := win.KEYBD_INPUT{Type: win.INPUT_KEYBOARD, Ki: win.KEYBDINPUT{WVk: code}}
	if up {
		input.Ki.DwFlags = win.KEYEVENTF_KEYUP
	}
	return input
}

// windowsKeyCode resolves the key half of a combination, asking the layout what
// it takes to produce a character so a hotkey written on one keyboard still
// works on another. It also returns the modifiers the layout needs held for
// that character.
func windowsKeyCode(stroke autoapi.KeyStroke) (uint16, []string, error) {
	if stroke.Named() {
		if code, ok := windowsKeyCodes[stroke.Key]; ok {
			return code, nil, nil
		}
		if number, ok := autoapi.FunctionKeyNumber(stroke.Key); ok {
			return uint16(win.VK_F1 + number - 1), nil, nil
		}
		return 0, nil, fmt.Errorf("windows has no key called %q", stroke.Key)
	}

	char := utf16.Encode([]rune(stroke.Key))
	if len(char) != 1 {
		return 0, nil, fmt.Errorf("%q is not a single key", stroke.Key)
	}
	scan, _, _ := procVkKeyScanW.Call(uintptr(char[0]))
	code, mods, ok := decodeVkKeyScan(uint16(scan))
	if !ok {
		return 0, nil, fmt.Errorf("this keyboard layout cannot produce %q as a single key", stroke.Key)
	}
	return code, mods, nil
}

// sendKeyboardInputs hands a batch of key events to Windows at once, so a
// combination arrives as one sequence rather than racing another program's.
func sendKeyboardInputs(inputs ...win.KEYBD_INPUT) error {
	sent := win.SendInput(uint32(len(inputs)), unsafe.Pointer(&inputs[0]), keyboardInputSize)
	if int(sent) != len(inputs) {
		return fmt.Errorf("windows accepted %d of %d key events, which is what it does when a "+
			"more privileged window has the focus", sent, len(inputs))
	}
	return nil
}

// sendMouseInputs hands a batch of pointer events to Windows.
func sendMouseInputs(inputs ...win.MOUSE_INPUT) error {
	sent := win.SendInput(uint32(len(inputs)), unsafe.Pointer(&inputs[0]), mouseInputSize)
	if int(sent) != len(inputs) {
		return fmt.Errorf("windows accepted %d of %d pointer events, which is what it does when a "+
			"more privileged window has the focus", sent, len(inputs))
	}
	return nil
}

// moveMouse moves the pointer. An absolute move is a direct set rather than an
// event, which is the one that lands on the pixel asked for: SendInput scales
// absolute coordinates into a 0 to 65535 range across the virtual desktop and
// loses the odd pixel doing it.
func moveMouse(ctx context.Context, x, y int, relative bool) error {
	if relative {
		return sendMouseInputs(win.MOUSE_INPUT{
			Type: win.INPUT_MOUSE,
			Mi:   win.MOUSEINPUT{Dx: int32(x), Dy: int32(y), DwFlags: win.MOUSEEVENTF_MOVE},
		})
	}
	if !win.SetCursorPos(int32(x), int32(y)) {
		return fmt.Errorf("windows refused to move the pointer to %d,%d", x, y)
	}
	return nil
}

// clickMouse clicks wherever the pointer is.
func clickMouse(ctx context.Context, button string, count int) error {
	down, up, err := windowsButtonFlags(button)
	if err != nil {
		return err
	}
	inputs := make([]win.MOUSE_INPUT, 0, count*2)
	for i := 0; i < count; i++ {
		inputs = append(inputs, mouseInput(down), mouseInput(up))
	}
	return sendMouseInputs(inputs...)
}

// holdMouse holds a button down or lets it up.
func holdMouse(ctx context.Context, button string, down bool) error {
	downFlag, upFlag, err := windowsButtonFlags(button)
	if err != nil {
		return err
	}
	flag := upFlag
	if down {
		flag = downFlag
	}
	return sendMouseInputs(mouseInput(flag))
}

// scrollMouse turns the wheel. Windows counts a wheel click as 120 units,
// which is the number every application divides by.
func scrollMouse(ctx context.Context, amount int, horizontal bool) error {
	const wheelDelta = 120
	flag := uint32(win.MOUSEEVENTF_WHEEL)
	if horizontal {
		flag = win.MOUSEEVENTF_HWHEEL
	}
	return sendMouseInputs(win.MOUSE_INPUT{
		Type: win.INPUT_MOUSE,
		Mi:   win.MOUSEINPUT{MouseData: uint32(int32(amount * wheelDelta)), DwFlags: flag},
	})
}

// mousePoint reports where the pointer is.
func mousePoint(ctx context.Context) (int, int, error) {
	var point win.POINT
	if !win.GetCursorPos(&point) {
		return 0, 0, fmt.Errorf("windows did not report the pointer position")
	}
	return int(point.X), int(point.Y), nil
}

// mouseInput builds one pointer event carrying no movement.
func mouseInput(flags uint32) win.MOUSE_INPUT {
	return win.MOUSE_INPUT{Type: win.INPUT_MOUSE, Mi: win.MOUSEINPUT{DwFlags: flags}}
}

// windowsButtonFlags maps a named button onto its down and up events.
func windowsButtonFlags(button string) (uint32, uint32, error) {
	switch button {
	case "", buttonLeft:
		return win.MOUSEEVENTF_LEFTDOWN, win.MOUSEEVENTF_LEFTUP, nil
	case buttonMiddle:
		return win.MOUSEEVENTF_MIDDLEDOWN, win.MOUSEEVENTF_MIDDLEUP, nil
	case buttonRight:
		return win.MOUSEEVENTF_RIGHTDOWN, win.MOUSEEVENTF_RIGHTUP, nil
	default:
		return 0, 0, fmt.Errorf("unknown mouse button %q", button)
	}
}
