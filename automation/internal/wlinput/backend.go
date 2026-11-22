//go:build !windows && !darwin

package wlinput

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Backend implements autoapi.Keyboard and autoapi.Mouse over the Wayland
// virtual-device protocols. Each operation opens its own short-lived connection,
// creates the virtual device, does its work, flushes with a roundtrip, and
// closes — the same stateless bargain wldata makes, so there is no long-lived
// connection to supervise and a compositor restart costs nothing.
type Backend struct {
	// keyTime and mu hand out the monotonic event timestamps the protocol wants;
	// they need only be increasing.
	mu      sync.Mutex
	keyTime uint32
}

// Caps reports which of the two capabilities the session actually serves, so
// the caller wires only the ones that work and leaves the rest to another
// backend.
type Caps struct {
	Keyboard bool
	Pointer  bool
}

// New probes the session for the virtual-device managers and returns a backend
// with the capabilities it found, or an error when the compositor serves
// neither protocol.
func New(ctx context.Context) (*Backend, Caps, error) {
	c, err := setup(ctx)
	if err != nil {
		return nil, Caps{}, err
	}
	caps := Caps{Keyboard: c.vkbManager != 0, Pointer: c.vpManager != 0}
	c.close()
	return &Backend{}, caps, nil
}

// nextTime returns the next event timestamp.
func (b *Backend) nextTime() uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.keyTime += 4
	return b.keyTime
}

// --- autoapi.Keyboard ---

// TypeText types each character as its own keysym at the base level, so any
// character types with no shift and any layout. The keymap is built once for
// the whole string (splitting only if it runs past the keycode limit), then
// each key is pressed and released with the caller's delay between them.
func (b *Backend) TypeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	runes := []rune(text)
	if len(runes) == 0 {
		return nil
	}

	c, vkb, err := b.openKeyboard(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	// Build one keymap holding every distinct character in the string; only a
	// string with more than keymapCap distinct characters needs more than one,
	// and that reupload is handled inline below.
	builder := newKeymapBuilder()
	primeCode, _ := builder.add("Shift_L")
	for _, r := range runes {
		if _, ok := builder.add(runeKeysymName(r)); !ok {
			break
		}
	}
	if err := c.uploadKeymap(vkb, builder); err != nil {
		return err
	}
	if err := b.primeKeyboard(c, vkb, uint32(primeCode)); err != nil {
		return err
	}

	for i, r := range runes {
		name := runeKeysymName(r)
		code, ok := builder.add(name)
		if !ok {
			// The keymap filled: start a fresh one for the remaining characters
			// and upload it before pressing.
			builder = newKeymapBuilder()
			primeCode, _ = builder.add("Shift_L")
			code, _ = builder.add(name)
			for _, rr := range runes[i:] {
				if _, ok := builder.add(runeKeysymName(rr)); !ok {
					break
				}
			}
			if err := c.uploadKeymap(vkb, builder); err != nil {
				return err
			}
			if err := b.primeKeyboard(c, vkb, uint32(primeCode)); err != nil {
				return err
			}
		}
		if err := c.key(vkb, b.nextTime(), uint32(code), statePressed); err != nil {
			return err
		}
		if err := c.key(vkb, b.nextTime(), uint32(code), stateReleased); err != nil {
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
	return c.roundtrip(nil)
}

// PressKeys holds the modifiers, presses the key, and releases. The modifiers
// ride the modifiers request as a real-modifier mask the "complete" keymap
// understands, and the key is one keysym assigned its own keycode.
func (b *Backend) PressKeys(ctx context.Context, k autoapi.KeyStroke) error {
	var keyName string
	if k.Named() {
		name, err := namedKeyKeysymName(k.Key)
		if err != nil {
			return err
		}
		keyName = name
	} else {
		runes := []rune(k.Key)
		if len(runes) != 1 {
			return fmt.Errorf("%q is not a single key", k.Key)
		}
		keyName = runeKeysymName(runes[0])
	}

	var mask uint32
	for _, mod := range k.Mods {
		m, ok := modMask[mod]
		if !ok {
			return fmt.Errorf("no mask for modifier %q", mod)
		}
		mask |= m
	}

	c, vkb, err := b.openKeyboard(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	builder := newKeymapBuilder()
	primeCode, _ := builder.add("Shift_L")
	code, _ := builder.add(keyName)
	if err := c.uploadKeymap(vkb, builder); err != nil {
		return err
	}
	if err := b.primeKeyboard(c, vkb, uint32(primeCode)); err != nil {
		return err
	}

	if mask != 0 {
		if err := c.modifiers(vkb, mask, 0, 0, 0); err != nil {
			return err
		}
	}
	pressErr := c.key(vkb, b.nextTime(), uint32(code), statePressed)
	if pressErr == nil {
		pressErr = c.key(vkb, b.nextTime(), uint32(code), stateReleased)
	}
	if mask != 0 {
		if err := c.modifiers(vkb, 0, 0, 0, 0); err != nil && pressErr == nil {
			pressErr = err
		}
	}
	if pressErr != nil {
		return pressErr
	}
	return c.roundtrip(nil)
}

// primeKeyboard wakes compositors that discard the first event from a newly
// attached virtual keyboard. A released Shift key changes no text or modifier
// state, and the roundtrip ensures the device is ready before the real input.
func (b *Backend) primeKeyboard(c *conn, vkb, code uint32) error {
	if err := c.key(vkb, b.nextTime(), code, statePressed); err != nil {
		return err
	}
	if err := c.key(vkb, b.nextTime(), code, stateReleased); err != nil {
		return err
	}
	return c.roundtrip(nil)
}

// --- autoapi.Mouse ---

// Move places the pointer at an absolute coordinate, or offsets it. A wlroots
// compositor accepts absolute motion against an output's extents — the thing
// the RemoteDesktop portal cannot do — so an absolute move here lands on the
// coordinate rather than being refused.
func (b *Backend) Move(ctx context.Context, x, y int, relative bool) error {
	c, vp, err := b.openPointer(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	if relative {
		if err := c.request(vp, vpRequestMotion, b.nextTime(),
			int32(x*256), int32(y*256)); err != nil {
			return err
		}
	} else {
		if c.outW <= 0 || c.outH <= 0 {
			return fmt.Errorf("%w: the compositor reported no output size to place the pointer against", autoapi.ErrUnsupported)
		}
		if err := c.request(vp, vpRequestMotionAbsolute, b.nextTime(),
			uint32(x), uint32(y), uint32(c.outW), uint32(c.outH)); err != nil {
			return err
		}
	}
	if err := c.request(vp, vpRequestFrame); err != nil {
		return err
	}
	return c.roundtrip(nil)
}

// Click presses and releases a button, however many times.
func (b *Backend) Click(ctx context.Context, button autoapi.Button, count int) error {
	code, err := buttonCode(button)
	if err != nil {
		return err
	}
	c, vp, err := b.openPointer(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	for range count {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := c.pointerButton(vp, b.nextTime(), code, statePressed); err != nil {
			return err
		}
		if err := c.pointerButton(vp, b.nextTime(), code, stateReleased); err != nil {
			return err
		}
	}
	return c.roundtrip(nil)
}

// Hold holds a button down or lets it up.
func (b *Backend) Hold(ctx context.Context, button autoapi.Button, down bool) error {
	code, err := buttonCode(button)
	if err != nil {
		return err
	}
	c, vp, err := b.openPointer(ctx)
	if err != nil {
		return err
	}
	defer c.close()
	state := uint32(stateReleased)
	if down {
		state = statePressed
	}
	if err := c.pointerButton(vp, b.nextTime(), code, state); err != nil {
		return err
	}
	return c.roundtrip(nil)
}

// Scroll turns the wheel by discrete steps. A positive amount scrolls up, the
// same direction the X and portal paths mean.
func (b *Backend) Scroll(ctx context.Context, amount int, horizontal bool) error {
	c, vp, err := b.openPointer(ctx)
	if err != nil {
		return err
	}
	defer c.close()

	axis := uint32(0) // vertical
	steps := int32(-amount)
	if horizontal {
		axis = 1
		steps = int32(amount)
	}
	t := b.nextTime()
	// A discrete scroll pairs a continuous axis value with the notch count: the
	// value is the traditional 15 units per notch as a wl_fixed, the discrete
	// argument the number of notches.
	value := steps * 15 * 256
	if err := c.request(vp, vpRequestAxisDiscrete, t, axis, value, steps); err != nil {
		return err
	}
	if err := c.request(vp, vpRequestFrame); err != nil {
		return err
	}
	return c.roundtrip(nil)
}

// Position is unavailable: a Wayland client is never told where the pointer is.
func (b *Backend) Position(ctx context.Context) (int, int, error) {
	return 0, 0, fmt.Errorf("%w: a Wayland compositor does not report the pointer position to a client", autoapi.ErrUnsupported)
}

// --- helpers ---

// openKeyboard dials, confirms the keyboard manager, and creates a virtual
// keyboard for the seat.
func (b *Backend) openKeyboard(ctx context.Context) (*conn, uint32, error) {
	c, err := setup(ctx)
	if err != nil {
		return nil, 0, err
	}
	if c.vkbManager == 0 {
		c.close()
		return nil, 0, fmt.Errorf("%w: this compositor serves no virtual keyboard", autoapi.ErrUnsupported)
	}
	vkb := c.newID()
	if err := c.request(c.vkbManager, vkbManagerRequestCreate, c.seat, vkb); err != nil {
		c.close()
		return nil, 0, err
	}
	return c, vkb, nil
}

// openPointer dials, confirms the pointer manager, and creates a virtual
// pointer for the seat.
func (b *Backend) openPointer(ctx context.Context) (*conn, uint32, error) {
	c, err := setup(ctx)
	if err != nil {
		return nil, 0, err
	}
	if c.vpManager == 0 {
		c.close()
		return nil, 0, fmt.Errorf("%w: this compositor serves no virtual pointer", autoapi.ErrUnsupported)
	}
	vp := c.newID()
	// create_virtual_pointer takes an optional seat; passing the bound seat aims
	// the pointer at the session the seat drives.
	if err := c.request(c.vpManager, vpManagerRequestCreate, c.seat, vp); err != nil {
		c.close()
		return nil, 0, err
	}
	return c, vp, nil
}

// uploadKeymap renders the builder's keymap into an anonymous file and hands
// the descriptor to the virtual keyboard.
func (c *conn) uploadKeymap(vkb uint32, b *keymapBuilder) error {
	text := b.render()
	data := append([]byte(text), 0) // the keymap size includes the terminating NUL

	fd, err := createKeymapFile()
	if err != nil {
		return fmt.Errorf("creating the keymap file: %w", err)
	}
	defer unix.Close(fd)
	if err := unix.Ftruncate(fd, int64(len(data))); err != nil {
		return err
	}
	if _, err := unix.Pwrite(fd, data, 0); err != nil {
		return err
	}
	return c.requestFD(vkb, vkbRequestKeymap, fd, uint32(keymapFormatXKB), uint32(len(data)))
}

// key sends one key press or release.
func (c *conn) key(vkb, time, code, state uint32) error {
	return c.request(vkb, vkbRequestKey, time, code, state)
}

// modifiers sets the depressed, latched, locked, and group modifier state.
func (c *conn) modifiers(vkb, depressed, latched, locked, group uint32) error {
	return c.request(vkb, vkbRequestModifiers, depressed, latched, locked, group)
}

// pointerButton sends one pointer button press or release.
func (c *conn) pointerButton(vp, time, button, state uint32) error {
	if err := c.request(vp, vpRequestButton, time, button, state); err != nil {
		return err
	}
	return c.request(vp, vpRequestFrame)
}

// buttonCode maps a named button onto its evdev code.
func buttonCode(button autoapi.Button) (uint32, error) {
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
