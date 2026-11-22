//go:build !windows && !darwin

// Package wlinput injects keyboard and pointer input into a Wayland session
// through the compositor's own virtual-device protocols —
// zwp_virtual_keyboard_manager_v1 and zwlr_virtual_pointer_manager_v1 — the
// interfaces the wlroots family (sway, Hyprland, river, Wayfire, labwc) serves.
// It speaks the wire itself over the compositor's socket, the same way wldata
// reads the clipboard: a header of object id and size-plus-opcode words,
// little-endian arguments, and a keymap handed over as a file descriptor on
// SCM_RIGHTS.
//
// This is the input route for a Wayland session with no RemoteDesktop portal:
// the wlroots portals implement Screenshot and ScreenCast but not
// org.freedesktop.impl.portal.RemoteDesktop, so the portal path used on KDE,
// GNOME, and COSMIC has no backend there and hangs. The virtual-device
// protocols need no portal and no per-use consent, and — unlike the portal —
// the virtual pointer can be placed at an absolute coordinate.
package wlinput

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"golang.org/x/sys/unix"
)

// Core protocol object ids and opcodes. wl_display is always object 1; the
// request and event numbers are fixed by the protocol XML.
const (
	displayID = 1

	displayRequestSync        = 0
	displayRequestGetRegistry = 1
	displayEventError         = 0

	registryRequestBind = 0
	registryEventGlobal = 0
	callbackEventDone   = 0

	// zwp_virtual_keyboard_manager_v1.
	vkbManagerRequestCreate = 0
	// zwp_virtual_keyboard_v1.
	vkbRequestKeymap    = 0
	vkbRequestKey       = 1
	vkbRequestModifiers = 2

	// zwlr_virtual_pointer_manager_v1.
	vpManagerRequestCreate = 0
	// zwlr_virtual_pointer_v1.
	vpRequestMotion         = 0
	vpRequestMotionAbsolute = 1
	vpRequestButton         = 2
	vpRequestFrame          = 4
	vpRequestAxisDiscrete   = 7

	// wl_output.
	outputEventMode = 1

	// wl_keyboard keymap format and key/button states.
	keymapFormatXKB = 1
	statePressed    = 1
	stateReleased   = 0

	// evdev pointer button codes, the same values the portal path uses.
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
)

// errNoVirtualInput reports a compositor that serves neither virtual-device
// manager; the caller falls back to the portal or the X path.
var errNoVirtualInput = errors.New("the compositor serves no virtual-keyboard or virtual-pointer protocol")

// conn is one connection to the compositor. Object ids are allocated
// sequentially from 2, so the layout is fixed at setup and remembered here.
type conn struct {
	fd     int
	buf    []byte
	nextID uint32

	registry uint32
	seat     uint32

	vkbManager uint32 // zwp_virtual_keyboard_manager_v1
	vkbVersion uint32
	vpManager  uint32 // zwlr_virtual_pointer_manager_v1
	vpVersion  uint32

	// outputs holds the bound wl_output ids, and outW/outH the largest mode seen
	// across them, used to scale an absolute pointer move.
	outputs    []uint32
	outW, outH int
}

// dial connects to the session's compositor socket.
func dial(ctx context.Context) (*conn, error) {
	display := os.Getenv("WAYLAND_DISPLAY")
	if display == "" {
		display = "wayland-0"
	}
	path := display
	if !filepath.IsAbs(path) {
		runtime := os.Getenv("XDG_RUNTIME_DIR")
		if runtime == "" {
			return nil, errors.New("no XDG_RUNTIME_DIR to find the Wayland socket in")
		}
		path = filepath.Join(runtime, display)
	}

	fd, err := unix.Socket(unix.AF_UNIX, unix.SOCK_STREAM|unix.SOCK_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	if err := unix.Connect(fd, &unix.SockaddrUnix{Name: path}); err != nil {
		unix.Close(fd)
		return nil, fmt.Errorf("connecting to the compositor at %s: %w", path, err)
	}
	c := &conn{fd: fd, nextID: 2}
	if deadline, ok := ctx.Deadline(); ok {
		c.setDeadline(deadline)
	} else {
		c.setDeadline(time.Now().Add(10 * time.Second))
	}
	return c, nil
}

// setDeadline bounds every read and write on the socket.
func (c *conn) setDeadline(t time.Time) {
	tv := unix.NsecToTimeval(time.Until(t).Nanoseconds())
	_ = unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	_ = unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv)
}

// close releases the socket.
func (c *conn) close() {
	if c.fd >= 0 {
		unix.Close(c.fd)
		c.fd = -1
	}
}

// newID allocates the next client-side object id.
func (c *conn) newID() uint32 {
	id := c.nextID
	c.nextID++
	return id
}

// request marshals and sends one request. Arguments are uint32 words and
// strings; a string is a length including its NUL, the bytes, and padding to a
// word.
func (c *conn) request(object uint32, opcode int, args ...any) error {
	body := marshalArgs(args)
	msg := make([]byte, 8, 8+len(body))
	le := binary.LittleEndian
	le.PutUint32(msg, object)
	le.PutUint32(msg[4:], uint32(len(body)+8)<<16|uint32(opcode))
	msg = append(msg, body...)
	_, err := unix.Write(c.fd, msg)
	return err
}

// requestFD sends one request whose file-descriptor argument travels beside the
// message as SCM_RIGHTS rather than in the body.
func (c *conn) requestFD(object uint32, opcode int, passFD int, args ...any) error {
	body := marshalArgs(args)
	msg := make([]byte, 8, 8+len(body))
	le := binary.LittleEndian
	le.PutUint32(msg, object)
	le.PutUint32(msg[4:], uint32(len(body)+8)<<16|uint32(opcode))
	msg = append(msg, body...)
	return unix.Sendmsg(c.fd, msg, unix.UnixRights(passFD), nil, 0)
}

// marshalArgs encodes request arguments into a little-endian body.
func marshalArgs(args []any) []byte {
	body := make([]byte, 0, 32)
	le := binary.LittleEndian
	for _, arg := range args {
		switch v := arg.(type) {
		case uint32:
			body = le.AppendUint32(body, v)
		case int32:
			body = le.AppendUint32(body, uint32(v))
		case string:
			body = le.AppendUint32(body, uint32(len(v)+1))
			body = append(body, v...)
			body = append(body, 0)
			for len(body)%4 != 0 {
				body = append(body, 0)
			}
		default:
			panic(fmt.Sprintf("wlinput: unsupported argument %T", arg))
		}
	}
	return body
}

// event is one parsed message from the compositor.
type event struct {
	object uint32
	opcode int
	body   []byte
}

// readEvent returns the next complete message.
func (c *conn) readEvent() (event, error) {
	le := binary.LittleEndian
	for {
		if len(c.buf) >= 8 {
			size := int(le.Uint32(c.buf[4:]) >> 16)
			if size >= 8 && len(c.buf) >= size {
				ev := event{
					object: le.Uint32(c.buf),
					opcode: int(le.Uint32(c.buf[4:]) & 0xffff),
					body:   append([]byte(nil), c.buf[8:size]...),
				}
				c.buf = c.buf[size:]
				return ev, nil
			}
		}
		chunk := make([]byte, 4096)
		n, _, _, _, err := unix.Recvmsg(c.fd, chunk, nil, 0)
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil {
			return event{}, fmt.Errorf("reading from the compositor: %w", err)
		}
		if n == 0 {
			return event{}, errors.New("the compositor closed the connection")
		}
		c.buf = append(c.buf, chunk[:n]...)
	}
}

// parseString reads one wire string and returns it with the rest of the body.
func parseString(body []byte) (string, []byte, bool) {
	if len(body) < 4 {
		return "", nil, false
	}
	length := int(binary.LittleEndian.Uint32(body))
	rest := body[4:]
	if length == 0 || len(rest) < length {
		return "", nil, false
	}
	s := string(rest[:length-1])
	consumed := (length + 3) &^ 3
	if len(rest) < consumed {
		return "", nil, false
	}
	return s, rest[consumed:], true
}

// checkError turns a wl_display error event into a Go error.
func checkError(ev event) error {
	if ev.object == displayID && ev.opcode == displayEventError {
		if len(ev.body) >= 8 {
			if msg, _, ok := parseString(ev.body[8:]); ok {
				return fmt.Errorf("the compositor reported: %s", msg)
			}
		}
		return errors.New("the compositor reported a protocol error")
	}
	return nil
}

// roundtrip sends a sync and processes events until its callback answers,
// handing each intervening event to handle (which may be nil).
func (c *conn) roundtrip(handle func(event) error) error {
	callback := c.newID()
	if err := c.request(displayID, displayRequestSync, callback); err != nil {
		return err
	}
	for {
		ev, err := c.readEvent()
		if err != nil {
			return err
		}
		if err := checkError(ev); err != nil {
			return err
		}
		if ev.object == callback && ev.opcode == callbackEventDone {
			return nil
		}
		if handle != nil {
			if err := handle(ev); err != nil {
				return err
			}
		}
	}
}

// setup binds the registry, a seat, whichever virtual-device managers exist,
// and one output. It fails with errNoVirtualInput when neither manager global
// is present.
func setup(ctx context.Context) (*conn, error) {
	c, err := dial(ctx)
	if err != nil {
		return nil, err
	}

	c.registry = c.newID()
	if err := c.request(displayID, displayRequestGetRegistry, c.registry); err != nil {
		c.close()
		return nil, err
	}

	type global struct {
		name    uint32
		version uint32
	}
	var seat, vkbMgr, vpMgr global
	var outputGlobals []global
	var haveSeat, haveVKB, haveVP bool
	err = c.roundtrip(func(ev event) error {
		if ev.object != c.registry || ev.opcode != registryEventGlobal {
			return nil
		}
		if len(ev.body) < 4 {
			return nil
		}
		name := binary.LittleEndian.Uint32(ev.body)
		iface, rest, ok := parseString(ev.body[4:])
		if !ok || len(rest) < 4 {
			return nil
		}
		version := binary.LittleEndian.Uint32(rest)
		switch iface {
		case "wl_seat":
			if !haveSeat {
				seat, haveSeat = global{name, version}, true
			}
		case "zwp_virtual_keyboard_manager_v1":
			vkbMgr, haveVKB = global{name, version}, true
		case "zwlr_virtual_pointer_manager_v1":
			vpMgr, haveVP = global{name, version}, true
		case "wl_output":
			outputGlobals = append(outputGlobals, global{name, version})
		}
		return nil
	})
	if err != nil {
		c.close()
		return nil, err
	}
	if !haveSeat || (!haveVKB && !haveVP) {
		c.close()
		return nil, errNoVirtualInput
	}

	bind := func(g global, iface string, version uint32) (uint32, error) {
		if g.version < version {
			version = g.version
		}
		id := c.newID()
		return id, c.request(c.registry, registryRequestBind, g.name, iface, version, id)
	}

	if c.seat, err = bind(seat, "wl_seat", 1); err != nil {
		c.close()
		return nil, err
	}
	if haveVKB {
		c.vkbVersion = vkbMgr.version
		if c.vkbManager, err = bind(vkbMgr, "zwp_virtual_keyboard_manager_v1", 1); err != nil {
			c.close()
			return nil, err
		}
	}
	if haveVP {
		c.vpVersion = vpMgr.version
		v := min(vpMgr.version, 2)
		if c.vpManager, err = bind(vpMgr, "zwlr_virtual_pointer_manager_v1", v); err != nil {
			c.close()
			return nil, err
		}
	}
	for _, g := range outputGlobals {
		id, berr := bind(g, "wl_output", 2)
		if berr != nil {
			c.close()
			return nil, berr
		}
		c.outputs = append(c.outputs, id)
	}
	if len(c.outputs) > 0 {
		// Collect each output's current mode, keeping the largest — the extents
		// an absolute pointer move scales against. A mode event carries a flags
		// word and only the current mode has the current bit set, so a
		// stale mode never overwrites the live size. Two syncs give a compositor
		// that batches its output events room to send them.
		const modeFlagCurrent = 0x1
		isOutput := func(id uint32) bool {
			for _, o := range c.outputs {
				if o == id {
					return true
				}
			}
			return false
		}
		collect := func(ev event) error {
			if ev.opcode == outputEventMode && isOutput(ev.object) && len(ev.body) >= 12 {
				flags := binary.LittleEndian.Uint32(ev.body)
				w := int(int32(binary.LittleEndian.Uint32(ev.body[4:])))
				h := int(int32(binary.LittleEndian.Uint32(ev.body[8:])))
				if flags&modeFlagCurrent != 0 && w > 0 && h > 0 && w*h > c.outW*c.outH {
					c.outW, c.outH = w, h
				}
			}
			return nil
		}
		_ = c.roundtrip(collect)
		if c.outW == 0 {
			_ = c.roundtrip(collect)
		}
	}
	return c, nil
}
