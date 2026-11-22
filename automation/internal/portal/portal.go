// Package portal drives a Wayland session through the freedesktop desktop
// portals: RemoteDesktop for keyboard and mouse input — the one route that
// reaches Wayland-native windows on KDE and GNOME without a uinput daemon —
// and Screenshot for the screen. The first use of each opens a consent dialog
// the user approves once; a restore token persisted through the caller's
// TokenStore lets later RemoteDesktop sessions start without prompting.
//
// Absolute motion uses monitor streams selected through ScreenCast. Reading
// the current pointer position requires a separate compositor API.
package portal

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/grmrgecko/karakuri/automation/internal/dbusconn"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

const (
	portalService = "org.freedesktop.portal.Desktop"
	portalPath    = "/org/freedesktop/portal/desktop"
	rdIface       = "org.freedesktop.portal.RemoteDesktop"
	scIface       = "org.freedesktop.portal.ScreenCast"

	deviceKeyboard = 1
	devicePointer  = 2

	persistUntilRevoked = 2

	stateReleased = 0
	statePressed  = 1

	// evdev pointer button codes.
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112

	startTimeout = 90 * time.Second // a person has to approve the first one
	callTimeout  = 20 * time.Second
)

// Backend implements autoapi.Keyboard and autoapi.Mouse over the portal.
type Backend struct {
	appID string
	store autoapi.TokenStore

	mu      sync.Mutex
	conn    *dbus.Conn
	sigc    chan *dbus.Signal
	session dbus.ObjectPath
	started bool
	streams []monitorStream
	devices uint32
	tokenN  int
}

// New returns a portal backend that persists its restore token through store
// (which may be nil for no persistence).
func New(appID string, store autoapi.TokenStore) *Backend {
	return &Backend{appID: appID, store: store}
}

// tokenKey separates monitor-sharing grants from older input-only sessions.
const tokenKey = "remote-desktop-monitor-restore"

// ensureStarted brings the portal session up the first time it is needed. It is
// lazy so building a Session never prompts; the consent dialog appears on the
// first keystroke or click instead.
func (b *Backend) ensureStarted(ctx context.Context) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		if b.sessionClosed() {
			b.reset()
		} else {
			return nil
		}
	}
	defer func() {
		if !b.started {
			b.reset()
		}
	}()

	if b.conn == nil {
		// A private connection keeps the portal's Response signals off the shared
		// bus the other backends use.
		conn, err := dbusconn.SessionPrivate()
		if err != nil {
			return fmt.Errorf("no session bus for the RemoteDesktop portal: %w", err)
		}
		if err := conn.Auth(nil); err != nil {
			_ = conn.Close()
			return err
		}
		if err := conn.Hello(); err != nil {
			_ = conn.Close()
			return err
		}
		if err := conn.AddMatchSignal(
			dbus.WithMatchInterface("org.freedesktop.portal.Request"),
			dbus.WithMatchMember("Response"),
		); err != nil {
			_ = conn.Close()
			return err
		}
		if err := conn.AddMatchSignal(dbus.WithMatchInterface("org.freedesktop.portal.Session"), dbus.WithMatchMember("Closed")); err != nil {
			_ = conn.Close()
			return err
		}
		b.sigc = make(chan *dbus.Signal, 16)
		conn.Signal(b.sigc)
		b.conn = conn
	}

	// Create the session.
	created, err := b.call(ctx, rdIface+".CreateSession", map[string]dbus.Variant{
		"session_handle_token": dbus.MakeVariant(b.nextToken("session")),
	})
	if err != nil {
		return fmt.Errorf("the RemoteDesktop portal would not create a session: %w", err)
	}
	handleVar, ok := created["session_handle"]
	if !ok {
		return fmt.Errorf("the portal returned no session handle")
	}
	handle, ok := handleVar.Value().(string)
	if !ok || !dbus.ObjectPath(handle).IsValid() {
		return fmt.Errorf("invalid portal session handle")
	}
	b.session = dbus.ObjectPath(handle)

	// Select keyboard and pointer, asking to persist so a later start is silent.
	selectOpts := map[string]dbus.Variant{
		"types":        dbus.MakeVariant(uint32(deviceKeyboard | devicePointer)),
		"persist_mode": dbus.MakeVariant(uint32(persistUntilRevoked)),
	}
	if b.store != nil {
		if token, ok := b.store.Load(tokenKey); ok {
			selectOpts["restore_token"] = dbus.MakeVariant(token)
		}
	}
	if _, err := b.call(ctx, rdIface+".SelectDevices", selectOpts, b.session); err != nil {
		return fmt.Errorf("the portal would not select the input devices: %w", err)
	}

	// ScreenCast supplies the monitor coordinates required by absolute input.
	// Older input-only portals keep relative input available.
	obj := b.conn.Object(portalService, portalPath)
	var sourceTypes dbus.Variant
	if err := obj.CallWithContext(ctx, "org.freedesktop.DBus.Properties.Get", 0, scIface, "AvailableSourceTypes").Store(&sourceTypes); err == nil {
		if types, ok := sourceTypes.Value().(uint32); ok && types&1 != 0 {
			if _, err := b.call(ctx, scIface+".SelectSources", map[string]dbus.Variant{
				"types": dbus.MakeVariant(uint32(1)), "multiple": dbus.MakeVariant(true),
			}, b.session); err != nil {
				return fmt.Errorf("selecting portal monitors: %w", err)
			}
		}
	}

	// Start the session. This is the call that prompts on first use.
	startCtx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	results, err := b.callStart(startCtx)
	if err != nil {
		return fmt.Errorf("the RemoteDesktop session was not started (approval declined or timed out): %w", err)
	}
	if b.store != nil {
		if tok, ok := results["restore_token"]; ok {
			if s, ok := tok.Value().(string); ok && s != "" {
				_ = b.store.Save(tokenKey, s)
			}
		}
	}

	if devices, ok := results["devices"]; ok {
		b.devices, _ = devices.Value().(uint32)
	}
	if b.devices == 0 {
		return fmt.Errorf("remote interaction was not granted; enable it in the portal sharing dialog")
	}
	if streams, ok := results["streams"]; ok {
		b.streams, err = decodeStreams(streams)
		if err != nil {
			return err
		}
	}

	// Some compositors create the session's virtual input devices lazily on the
	// first event and consume that event doing it — COSMIC eats the first
	// keysym and the first pointer event of every fresh session, however long
	// the caller waits. Spend a harmless event on each device (a bare Shift
	// tap, a zero-pixel motion) so the caller's first real one is delivered,
	// and give the devices a moment to finish appearing.
	shift := int32(0xffe1) // XK_Shift_L
	empty := map[string]dbus.Variant{}
	if b.devices&deviceKeyboard != 0 {
		_ = obj.CallWithContext(ctx, rdIface+".NotifyKeyboardKeysym", 0, b.session, empty, shift, uint32(statePressed)).Err
		releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = obj.CallWithContext(releaseCtx, rdIface+".NotifyKeyboardKeysym", 0, b.session, empty, shift, uint32(stateReleased)).Err
		releaseCancel()
	}
	if b.devices&devicePointer != 0 {
		_ = obj.CallWithContext(ctx, rdIface+".NotifyPointerMotion", 0, b.session, empty, float64(0), float64(0)).Err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(250 * time.Millisecond):
	}

	b.started = true
	return nil
}

// nextToken returns a handle token unique within this backend's lifetime.
func (b *Backend) nextToken(prefix string) string {
	b.tokenN++
	return "karakuri_" + prefix + "_" + strconv.Itoa(b.tokenN)
}

// call invokes a portal method whose first extra argument beyond the options is
// nothing (CreateSession) or the session handle (SelectDevices), and waits for
// the matching Response. The session handle, when present, comes before options
// in the wire order, so callers pass it after options here and it is reordered.
func (b *Backend) call(ctx context.Context, method string, options map[string]dbus.Variant, session ...dbus.ObjectPath) (map[string]dbus.Variant, error) {
	token := b.nextToken("req")
	options["handle_token"] = dbus.MakeVariant(token)

	var args []any
	for _, s := range session {
		args = append(args, s)
	}
	args = append(args, options)

	obj := b.conn.Object(portalService, portalPath)
	var request dbus.ObjectPath
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	if err := obj.CallWithContext(callCtx, method, 0, args...).Store(&request); err != nil {
		return nil, err
	}
	return b.waitResponse(callCtx, request)
}

// callStart runs Start, which takes a parent-window handle before its options.
func (b *Backend) callStart(ctx context.Context) (map[string]dbus.Variant, error) {
	token := b.nextToken("start")
	obj := b.conn.Object(portalService, portalPath)
	var request dbus.ObjectPath
	if err := obj.CallWithContext(ctx, rdIface+".Start", 0,
		b.session, "", map[string]dbus.Variant{"handle_token": dbus.MakeVariant(token)},
	).Store(&request); err != nil {
		return nil, err
	}
	return b.waitResponse(ctx, request)
}

// waitResponse blocks for the Response signal on one Request path.
func (b *Backend) waitResponse(ctx context.Context, request dbus.ObjectPath) (map[string]dbus.Variant, error) {
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case sig, ok := <-b.sigc:
			if !ok || sig == nil {
				return nil, fmt.Errorf("portal connection closed")
			}
			if sig.Path == b.session && sig.Name == "org.freedesktop.portal.Session.Closed" {
				return nil, fmt.Errorf("portal session closed")
			}
			if sig.Path != request || sig.Name != "org.freedesktop.portal.Request.Response" {
				continue
			}
			if len(sig.Body) < 2 {
				return nil, fmt.Errorf("malformed portal response")
			}
			code, _ := sig.Body[0].(uint32)
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			switch code {
			case 0:
				return results, nil
			case 1:
				return nil, fmt.Errorf("the request was cancelled")
			default:
				return nil, fmt.Errorf("the portal ended the request (code %d)", code)
			}
		}
	}
}

// notify calls a fire-and-forget Notify* method on the started session.
func (b *Backend) notify(ctx context.Context, method string, args ...any) error {
	if err := b.ensureStarted(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	device := uint32(devicePointer)
	if strings.HasPrefix(method, "NotifyKeyboard") {
		device = deviceKeyboard
	}
	if b.devices&device == 0 {
		return fmt.Errorf("portal permission for this input device was not granted")
	}
	conn, session := b.conn, b.session
	if conn == nil {
		return fmt.Errorf("portal session closed")
	}

	full := append([]any{session, map[string]dbus.Variant{}}, args...)
	return conn.Object(portalService, portalPath).CallWithContext(ctx, rdIface+"."+method, 0, full...).Err
}

// release attempts cleanup independently of a cancelled command. It never
// opens a new portal session just to release a key from an old one.
func (b *Backend) release(method string, code int32) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil || !b.started {
		return nil
	}
	return b.conn.Object(portalService, portalPath).CallWithContext(ctx, rdIface+"."+method, 0,
		b.session, map[string]dbus.Variant{}, code, uint32(stateReleased)).Err
}

// --- autoapi.Keyboard ---

// TypeText types each character as its keysym, pausing between them so a toolkit
// that drops fast synthetic input still receives every one.
func (b *Backend) TypeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	if err := b.ensureStarted(ctx); err != nil {
		return err
	}
	runes := []rune(text)
	for i, r := range runes {
		keysym := runeKeysym(r)
		if err := b.notify(ctx, "NotifyKeyboardKeysym", keysym, uint32(statePressed)); err != nil {
			_ = b.release("NotifyKeyboardKeysym", keysym)
			return err
		}
		if err := b.release("NotifyKeyboardKeysym", keysym); err != nil {
			return err
		}
		if delay > 0 && i < len(runes)-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-clock.After(delay):
			}
		}
	}
	return nil
}

// PressKeys holds the modifiers, presses the key, and releases in reverse.
// Modifiers cross as evdev keycodes and the key itself as a keysym: the
// keycode path is what makes the compositor track the modifier as held (see
// modifierKeycodes), while the keysym keeps the key layout-independent. The
// events are paced a few milliseconds apart so the asynchronous pipeline
// applies the modifier before the key it holds.
func (b *Backend) PressKeys(ctx context.Context, k autoapi.KeyStroke) (resultErr error) {
	if err := b.ensureStarted(ctx); err != nil {
		return err
	}
	key, err := strokeKeysym(k)
	if err != nil {
		return err
	}

	held := make([]int32, 0, len(k.Mods))
	for _, mod := range k.Mods {
		code, ok := modifierKeycodes[mod]
		if !ok {
			return fmt.Errorf("no keycode for modifier %q", mod)
		}
		held = append(held, code)
	}
	pace := func() {
		select {
		case <-ctx.Done():
		case <-time.After(15 * time.Millisecond):
		}
	}
	pressed := make([]int32, 0, len(held))
	defer func() {
		for i := len(pressed) - 1; i >= 0; i-- {
			if err := b.release("NotifyKeyboardKeycode", pressed[i]); resultErr == nil {
				resultErr = err
			}
		}
	}()
	for _, code := range held {
		// Include a possibly delivered event even when the reply is lost.
		pressed = append(pressed, code)
		if err := b.notify(ctx, "NotifyKeyboardKeycode", code, uint32(statePressed)); err != nil {
			return err
		}
		pace()
	}
	defer func() {
		if err := b.release("NotifyKeyboardKeysym", key); resultErr == nil {
			resultErr = err
		}
	}()
	if err := b.notify(ctx, "NotifyKeyboardKeysym", key, uint32(statePressed)); err != nil {
		return err
	}
	pace()
	return ctx.Err()
}

// --- autoapi.Mouse ---

// Move applies a relative offset or maps desktop logical coordinates to a
// selected monitor stream. Missing monitor geometry is never guessed.
func (b *Backend) Move(ctx context.Context, x, y int, relative bool) error {
	if relative {
		return b.notify(ctx, "NotifyPointerMotion", float64(x), float64(y))
	}
	if err := b.ensureStarted(ctx); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn == nil || b.devices&devicePointer == 0 {
		return fmt.Errorf("portal pointer permission is unavailable")
	}
	id, dx, dy, err := streamPoint(b.streams, x, y)
	if err != nil {
		return err
	}
	return b.conn.Object(portalService, portalPath).CallWithContext(ctx,
		rdIface+".NotifyPointerMotionAbsolute", 0, b.session, map[string]dbus.Variant{}, id, dx, dy).Err
}

// Click presses and releases a button, however many times.
func (b *Backend) Click(ctx context.Context, button autoapi.Button, count int) error {
	code, err := buttonCode(button)
	if err != nil {
		return err
	}
	for i := 0; i < count; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := b.notify(ctx, "NotifyPointerButton", code, uint32(statePressed)); err != nil {
			_ = b.release("NotifyPointerButton", code)
			return err
		}
		if err := b.release("NotifyPointerButton", code); err != nil {
			return err
		}
	}
	return nil
}

// Hold holds a button down or lets it up.
func (b *Backend) Hold(ctx context.Context, button autoapi.Button, down bool) error {
	code, err := buttonCode(button)
	if err != nil {
		return err
	}
	state := uint32(stateReleased)
	if down {
		state = statePressed
	}
	return b.notify(ctx, "NotifyPointerButton", code, state)
}

// Scroll turns the wheel by discrete steps. One step is a notch, and a positive
// amount scrolls up the way the X path and the config both mean it.
func (b *Backend) Scroll(ctx context.Context, amount int, horizontal bool) error {
	axis := uint32(0) // vertical
	steps := int32(-amount)
	if horizontal {
		axis = 1
		steps = int32(amount)
	}
	return b.notify(ctx, "NotifyPointerAxisDiscrete", axis, steps)
}

// Position is unavailable: a Wayland client is never told where the pointer is.
func (b *Backend) Position(ctx context.Context) (int, int, error) {
	return 0, 0, fmt.Errorf("%w: a Wayland compositor does not report the pointer position to a client", autoapi.ErrUnsupported)
}

// buttonCode maps a named button onto its evdev code.
func buttonCode(button autoapi.Button) (int32, error) {
	switch button {
	case "", autoapi.ButtonLeft:
		return btnLeft, nil
	case autoapi.ButtonRight:
		return btnRight, nil
	case autoapi.ButtonMiddle:
		return btnMiddle, nil
	default:
		return 0, fmt.Errorf("unknown mouse button %q", button)
	}
}
