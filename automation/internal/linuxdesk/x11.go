//go:build !windows && !darwin

package linuxdesk

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jezek/xgb/screensaver"
	"github.com/jezek/xgb/xproto"
	"github.com/jezek/xgb/xtest"
	"github.com/jezek/xgbutil"
	"github.com/jezek/xgbutil/ewmh"
	"github.com/jezek/xgbutil/icccm"
	"github.com/jezek/xgbutil/keybind"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// X11 is the one session type with an interface for this rather than a tool:
// the server itself accepts synthetic input through XTEST and reports its
// windows through the window manager's EWMH properties, so nothing has to be
// installed alongside the agent. A connection is opened per action and closed
// after it, which costs a socket and keeps the nodes working across a session
// restart that a cached connection would not survive.

// x11ModKeysyms names the key held down for each modifier, since XTEST presses
// keys rather than setting a mask.
var x11ModKeysyms = map[string]string{
	autoapi.ModShift:   "Shift_L",
	autoapi.ModControl: "Control_L",
	autoapi.ModAlt:     "Alt_L",
	autoapi.ModSuper:   "Super_L",
}

// x11KeyNames maps the key vocabulary onto X keysym names. The ones that
// disagree are the reason this table exists: X still calls the paging keys
// Prior and Next.
var x11KeyNames = map[string]string{
	"return": "Return", "escape": "Escape", "tab": "Tab", "space": "space",
	"backspace": "BackSpace", "delete": "Delete", "insert": "Insert",
	"home": "Home", "end": "End", "pageup": "Prior", "pagedown": "Next",
	"up": "Up", "down": "Down", "left": "Left", "right": "Right",
	"printscreen": "Print", "menu": "Menu", "pause": "Pause",
	"capslock": "Caps_Lock", "numlock": "Num_Lock", "scrolllock": "Scroll_Lock",
	"plus": "plus",
}

// X11 pointer button numbers. Four through seven are the scroll wheel, which
// X11 reports as button presses rather than as an axis.
const (
	x11ButtonLeft       = 1
	x11ButtonMiddle     = 2
	x11ButtonRight      = 3
	x11ScrollUp         = 4
	x11ScrollDown       = 5
	x11ScrollLeft       = 6
	x11ScrollRight      = 7
	x11RemapSettleDelay = 15 * time.Millisecond
)

// x11Session is one connection to the X server.
type x11Session struct {
	xu *xgbutil.XUtil
}

// x11Present reports whether there is an X display to talk to at all. It is
// what the dispatch checks before preferring X11 over a Wayland helper.
func x11Present() bool {
	return strings.TrimSpace(os.Getenv("DISPLAY")) != ""
}

// openX11 connects to the display the agent inherited from its session.
func openX11() (*x11Session, error) {
	if !x11Present() {
		return nil, errors.New("no DISPLAY in the agent's environment; it was not started from a graphical session")
	}
	xu, err := xgbutil.NewConn()
	if err != nil {
		return nil, fmt.Errorf("cannot reach the X display %s: %w", os.Getenv("DISPLAY"), err)
	}
	return &x11Session{xu: xu}, nil
}

// close releases the connection.
func (s *x11Session) close() {
	s.xu.Conn().Close()
}

// initInput enables XTEST, which is separate from connecting: a server can list
// windows for a client it will not accept synthetic input from.
func (s *x11Session) initInput() error {
	if err := xtest.Init(s.xu.Conn()); err != nil {
		return fmt.Errorf("the X server has no XTEST extension, so it accepts no synthetic input: %w", err)
	}
	keybind.Initialize(s.xu)
	return nil
}

// x11ListWindows reports the managed windows the window manager advertises.
// Only what is in _NET_CLIENT_LIST is listed: everything else on an X display
// is menus, tooltips, and the desktop itself, which nothing wants to activate.
func x11ListWindows(ctx context.Context) ([]autoapi.WindowInfo, error) {
	session, err := openX11()
	if err != nil {
		return nil, err
	}
	defer session.close()
	xu := session.xu

	clients, err := ewmh.ClientListGet(xu)
	if err != nil {
		return nil, fmt.Errorf("the window manager does not advertise its window list: %w", err)
	}
	active, _ := ewmh.ActiveWindowGet(xu)

	windows := make([]autoapi.WindowInfo, 0, len(clients))
	for _, win := range clients {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info := autoapi.WindowInfo{ID: x11WindowID(win), Active: win == active}
		if name, err := ewmh.WmNameGet(xu, win); err == nil && name != "" {
			info.Title = name
		} else if name, err := icccm.WmNameGet(xu, win); err == nil {
			info.Title = name
		}
		if class, err := icccm.WmClassGet(xu, win); err == nil && class != nil {
			info.Class = class.Class
		}
		if pid, err := ewmh.WmPidGet(xu, win); err == nil {
			info.PID = int(pid)
		}
		if geometry, err := xproto.GetGeometry(xu.Conn(), xproto.Drawable(win)).Reply(); err == nil {
			info.Width, info.Height = int(geometry.Width), int(geometry.Height)
			if translated, err := xproto.TranslateCoordinates(xu.Conn(), win, xu.RootWin(), 0, 0).Reply(); err == nil {
				info.X, info.Y = int(translated.DstX), int(translated.DstY)
			}
		}
		windows = append(windows, info)
	}
	return windows, nil
}

// x11WindowAction asks the window manager to act on one window. Every action is
// a request rather than a change: the window manager owns the stacking order
// and the frame, and a client that moved them itself would be fighting it.
func x11WindowAction(ctx context.Context, target autoapi.WindowInfo, action string) error {
	win, err := x11ParseWindowID(target.ID)
	if err != nil {
		return err
	}
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	xu := session.xu

	switch action {
	case windowActivate:
		// Source 2 says a pager is asking, which is the indication window
		// managers honor without a focus-stealing timestamp.
		return ewmh.ActiveWindowReqExtra(xu, win, 2, 0, 0)
	case windowMinimize:
		// Iconifying is a WM_CHANGE_STATE message to the root, the way ICCCM
		// has clients ask; _NET_WM_STATE_HIDDEN is the window manager's to set.
		return ewmh.ClientEvent(xu, win, "WM_CHANGE_STATE", icccm.StateIconic)
	case windowMaximize:
		return ewmh.WmStateReqExtra(xu, win, ewmh.StateAdd,
			"_NET_WM_STATE_MAXIMIZED_VERT", "_NET_WM_STATE_MAXIMIZED_HORZ", 2)
	case windowClose:
		return ewmh.CloseWindow(xu, win)
	default:
		return fmt.Errorf("unknown window action %q", action)
	}
}

// x11WindowID renders a window id the way X tools print it, so an id read off
// xwininfo or a previous listing is the same string.
func x11WindowID(win xproto.Window) string {
	return fmt.Sprintf("0x%x", uint32(win))
}

// x11ParseWindowID reads an id back, accepting the hex form and a plain number.
func x11ParseWindowID(id string) (xproto.Window, error) {
	trimmed := strings.TrimSpace(id)
	value, err := strconv.ParseUint(strings.TrimPrefix(strings.ToLower(trimmed), "0x"), 16, 32)
	if err != nil {
		return 0, fmt.Errorf("%q is not a window id", id)
	}
	return xproto.Window(value), nil
}

// x11IdleMillis reports milliseconds since the last keyboard or pointer input,
// read from the MIT-SCREEN-SAVER extension — the same XScreenSaverQueryInfo
// call xprintidle wraps, so an X11 server answers with nothing installed. On a
// Wayland session reached through XWayland this counts only input delivered to
// X clients, so the caller keeps it off the Wayland fast path.
func x11IdleMillis() (uint32, error) {
	session, err := openX11()
	if err != nil {
		return 0, err
	}
	defer session.close()
	conn := session.xu.Conn()

	if err := screensaver.Init(conn); err != nil {
		return 0, fmt.Errorf("the X server has no MIT-SCREEN-SAVER extension: %w", err)
	}
	root := xproto.Setup(conn).DefaultScreen(conn).Root
	reply, err := screensaver.QueryInfo(conn, xproto.Drawable(root)).Reply()
	if err != nil {
		return 0, fmt.Errorf("querying the idle time: %w", err)
	}
	return reply.MsSinceUserInput, nil
}

// x11TypeText types text one character at a time, in the session's own layout.
func x11TypeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.initInput(); err != nil {
		return err
	}

	return typePaced(ctx, clock, text, delay, func(char rune) error {
		return session.typeRune(ctx, clock, char)
	})
}

// typePaced hands each character to press and waits delay between characters.
// The last one is found by character count: ranging over the string yields
// byte offsets, which never reach the byte length minus one when the text ends
// in a multi-byte character.
func typePaced(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration, press func(rune) error) error {
	chars := []rune(text)
	for i, char := range chars {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := press(char); err != nil {
			return err
		}
		if delay > 0 && i < len(chars)-1 {
			if err := waitFor(ctx, clock, delay); err != nil {
				return err
			}
		}
	}
	return nil
}

// typeRune presses whatever the layout needs for one character. A character the
// layout cannot produce is bound to a spare keycode for the length of the
// keystroke and unbound afterwards, which is how a US keyboard types an accent
// without the caller changing their layout.
func (s *x11Session) typeRune(ctx context.Context, clock autoapi.Clock, char rune) error {
	keysym := x11Keysym(char)
	keycode, shifted, found := s.keycodeFor(keysym)
	if found {
		return s.pressKeycode(keycode, shifted)
	}

	scratch, per, err := s.scratchKeycode()
	if err != nil {
		return err
	}
	if err := s.remapKeycode(scratch, per, keysym); err != nil {
		return err
	}
	defer func() { _ = s.remapKeycode(scratch, per, 0) }()

	// The server needs a moment to apply the new mapping before an event
	// against it means anything.
	if err := waitFor(ctx, clock, x11RemapSettleDelay); err != nil {
		return err
	}
	return s.pressKeycode(scratch, false)
}

// x11Keysym maps a character onto the keysym that produces it. Latin-1 is its
// own value and everything else is the Unicode keysym range. A line break or
// tab is the Return or Tab key, because a code point below 0x20 is not a
// keysym and no layout has a key for it.
func x11Keysym(char rune) xproto.Keysym {
	switch char {
	case '\n', '\r':
		return 0xFF0D
	case '\t':
		return 0xFF09
	}
	if char < 0x100 {
		return xproto.Keysym(char)
	}
	return xproto.Keysym(0x01000000 + char)
}

// keycodeFor finds a key that produces the keysym, and whether shift is needed
// to reach it. Only the first two levels are searched: the third and fourth
// need a layout's own AltGr arrangement, and the scratch keycode reaches those
// characters more predictably than guessing at a level-three modifier.
func (s *x11Session) keycodeFor(keysym xproto.Keysym) (xproto.Keycode, bool, bool) {
	setup := s.xu.Setup()
	for code := int(setup.MinKeycode); code <= int(setup.MaxKeycode); code++ {
		for column := byte(0); column < 2; column++ {
			if keybind.KeysymGet(s.xu, xproto.Keycode(code), column) == keysym {
				return xproto.Keycode(code), column == 1, true
			}
		}
	}
	return 0, false, false
}

// scratchKeycode finds a keycode the layout does not use, for temporarily
// binding a character the layout cannot otherwise produce.
func (s *x11Session) scratchKeycode() (xproto.Keycode, byte, error) {
	keyMap, _ := keybind.MapsGet(s.xu)
	if keyMap == nil || keyMap.KeysymsPerKeycode == 0 {
		return 0, 0, errors.New("the X server returned no keyboard mapping")
	}
	setup := s.xu.Setup()

	// Searching from the top finds the unused high keycodes first, which is
	// where a layout leaves room and where xdotool takes its scratch key from.
	for code := int(setup.MaxKeycode); code >= int(setup.MinKeycode); code-- {
		used := false
		for column := byte(0); column < keyMap.KeysymsPerKeycode; column++ {
			if keybind.KeysymGet(s.xu, xproto.Keycode(code), column) != 0 {
				used = true
				break
			}
		}
		if !used {
			return xproto.Keycode(code), keyMap.KeysymsPerKeycode, nil
		}
	}
	return 0, 0, errors.New("the layout uses every keycode, so there is none spare to type an unmapped character with")
}

// remapKeycode binds every level of one keycode to a keysym, or clears it when
// the keysym is zero.
func (s *x11Session) remapKeycode(code xproto.Keycode, per byte, keysym xproto.Keysym) error {
	syms := make([]xproto.Keysym, per)
	for i := range syms {
		syms[i] = keysym
	}
	if err := xproto.ChangeKeyboardMappingChecked(s.xu.Conn(), 1, code, per, syms).Check(); err != nil {
		return fmt.Errorf("cannot bind a spare key to type with: %w", err)
	}
	keybind.Initialize(s.xu)
	return nil
}

// pressKeycode presses and releases one key, holding shift when the character
// sits on the shifted level.
func (s *x11Session) pressKeycode(code xproto.Keycode, shifted bool) error {
	if shifted {
		shift := s.modifierKeycode(autoapi.ModShift)
		if shift == 0 {
			return errors.New("the layout has no shift key, so the character cannot be typed")
		}
		if err := s.fakeKey(shift, true); err != nil {
			return err
		}
		defer func() { _ = s.fakeKey(shift, false) }()
	}
	if err := s.fakeKey(code, true); err != nil {
		return err
	}
	return s.fakeKey(code, false)
}

// x11PressKeys presses one combination: modifiers down, the key, and the
// modifiers back up in reverse.
func x11PressKeys(ctx context.Context, stroke autoapi.KeyStroke) error {
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.initInput(); err != nil {
		return err
	}

	code, shifted, err := session.strokeKeycode(stroke)
	if err != nil {
		return err
	}

	held := make([]xproto.Keycode, 0, len(stroke.Mods)+1)
	for _, mod := range stroke.Mods {
		keycode := session.modifierKeycode(mod)
		if keycode == 0 {
			return fmt.Errorf("this layout has no %s key", mod)
		}
		held = append(held, keycode)
	}
	if shifted && !stroke.HasMod(autoapi.ModShift) {
		if shift := session.modifierKeycode(autoapi.ModShift); shift != 0 {
			held = append(held, shift)
		}
	}

	for _, keycode := range held {
		if err := session.fakeKey(keycode, true); err != nil {
			return err
		}
	}
	pressErr := session.fakeKey(code, true)
	if pressErr == nil {
		pressErr = session.fakeKey(code, false)
	}
	// The modifiers come back up whatever happened to the key, or the session
	// is left with control held down.
	for i := len(held) - 1; i >= 0; i-- {
		if err := session.fakeKey(held[i], false); err != nil && pressErr == nil {
			pressErr = err
		}
	}
	return pressErr
}

// strokeKeycode resolves the key half of a combination, by keysym name for the
// named keys and through the layout for a character.
func (s *x11Session) strokeKeycode(stroke autoapi.KeyStroke) (xproto.Keycode, bool, error) {
	if stroke.Named() {
		name, ok := x11KeyNames[stroke.Key]
		if !ok {
			if number, isFunction := autoapi.FunctionKeyNumber(stroke.Key); isFunction {
				name = fmt.Sprintf("F%d", number)
			} else {
				return 0, false, fmt.Errorf("X11 has no key called %q", stroke.Key)
			}
		}
		codes := keybind.StrToKeycodes(s.xu, name)
		if len(codes) == 0 {
			return 0, false, fmt.Errorf("this layout has no %s key", stroke.Key)
		}
		return codes[0], false, nil
	}

	char := []rune(stroke.Key)[0]
	code, shifted, found := s.keycodeFor(x11Keysym(char))
	if !found {
		return 0, false, fmt.Errorf("this layout cannot produce %q as a single key", stroke.Key)
	}
	return code, shifted, nil
}

// modifierKeycode returns the key held for a canonical modifier.
func (s *x11Session) modifierKeycode(mod string) xproto.Keycode {
	name, ok := x11ModKeysyms[mod]
	if !ok {
		return 0
	}
	codes := keybind.StrToKeycodes(s.xu, name)
	if len(codes) == 0 {
		return 0
	}
	return codes[0]
}

// fakeKey sends one key event through XTEST.
func (s *x11Session) fakeKey(code xproto.Keycode, down bool) error {
	eventType := byte(xproto.KeyRelease)
	if down {
		eventType = xproto.KeyPress
	}
	return xtest.FakeInputChecked(s.xu.Conn(), eventType, byte(code), 0, s.xu.RootWin(), 0, 0, 0).Check()
}

// fakeButton sends one pointer button event through XTEST.
func (s *x11Session) fakeButton(button byte, down bool) error {
	eventType := byte(xproto.ButtonRelease)
	if down {
		eventType = xproto.ButtonPress
	}
	return xtest.FakeInputChecked(s.xu.Conn(), eventType, button, 0, s.xu.RootWin(), 0, 0, 0).Check()
}

// x11MoveMouse moves the pointer, absolutely or by an offset.
func x11MoveMouse(ctx context.Context, x, y int, relative bool) error {
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.initInput(); err != nil {
		return err
	}

	// XTEST calls a relative motion detail 1 and an absolute one 0, which is
	// the only difference between the two moves.
	detail := byte(0)
	if relative {
		detail = 1
	}
	return xtest.FakeInputChecked(session.xu.Conn(), xproto.MotionNotify, detail, 0,
		session.xu.RootWin(), int16(x), int16(y), 0).Check()
}

// x11ClickMouse presses and releases a button, however many times.
func x11ClickMouse(ctx context.Context, button string, count int) error {
	code, err := x11ButtonCode(button)
	if err != nil {
		return err
	}
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.initInput(); err != nil {
		return err
	}

	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := session.fakeButton(code, true); err != nil {
			return err
		}
		if err := session.fakeButton(code, false); err != nil {
			return err
		}
	}
	return nil
}

// x11HoldMouse holds a button down or lets it up, for a drag a move sits
// between.
func x11HoldMouse(ctx context.Context, button string, down bool) error {
	code, err := x11ButtonCode(button)
	if err != nil {
		return err
	}
	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.initInput(); err != nil {
		return err
	}
	return session.fakeButton(code, down)
}

// x11ScrollMouse turns the wheel, which X11 reports as clicks of buttons four
// through seven rather than as an axis.
func x11ScrollMouse(ctx context.Context, amount int, horizontal bool) error {
	button := byte(x11ScrollUp)
	switch {
	case horizontal && amount < 0:
		button = x11ScrollRight
	case horizontal:
		button = x11ScrollLeft
	case amount < 0:
		button = x11ScrollDown
	}
	steps := amount
	if steps < 0 {
		steps = -steps
	}

	session, err := openX11()
	if err != nil {
		return err
	}
	defer session.close()
	if err := session.initInput(); err != nil {
		return err
	}
	for i := 0; i < steps; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := session.fakeButton(button, true); err != nil {
			return err
		}
		if err := session.fakeButton(button, false); err != nil {
			return err
		}
	}
	return nil
}

// x11MousePoint reports where the pointer is, in root coordinates.
func x11MousePoint(ctx context.Context) (int, int, error) {
	session, err := openX11()
	if err != nil {
		return 0, 0, err
	}
	defer session.close()

	reply, err := xproto.QueryPointer(session.xu.Conn(), session.xu.RootWin()).Reply()
	if err != nil {
		return 0, 0, fmt.Errorf("cannot read the pointer position: %w", err)
	}
	return int(reply.RootX), int(reply.RootY), nil
}

// x11ButtonCode maps a named button onto X11's number for it.
func x11ButtonCode(button string) (byte, error) {
	switch button {
	case "", buttonLeft:
		return x11ButtonLeft, nil
	case buttonMiddle:
		return x11ButtonMiddle, nil
	case buttonRight:
		return x11ButtonRight, nil
	default:
		return 0, fmt.Errorf("unknown mouse button %q", button)
	}
}
