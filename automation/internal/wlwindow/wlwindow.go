//go:build !windows && !darwin

// Package wlwindow lists and acts on a Wayland session's windows through the
// wlroots foreign-toplevel protocol (zwlr_foreign_toplevel_manager_v1), the
// interface sway, Hyprland, river, Wayfire, and niri serve. It speaks the wire
// itself over the compositor's socket, the same way wldata and wlinput do, so a
// wlroots session reports its windows and can activate, minimize, maximize, or
// close one without XWayland — which only ever saw the X11 windows and reported
// nothing for a Wayland-native one.
//
// The protocol carries a window's title, app id, and state but no process id, so
// the PID is left zero on this path; a window is matched across the two calls
// (list, then act) by app id and title, since the toplevel handle is scoped to
// the connection that saw it.
package wlwindow

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

const (
	displayID = 1

	displayRequestSync        = 0
	displayRequestGetRegistry = 1
	displayEventError         = 0

	registryRequestBind = 0
	registryEventGlobal = 0
	callbackEventDone   = 0

	// zwlr_foreign_toplevel_manager_v1.
	managerEventToplevel = 0

	// zwlr_foreign_toplevel_handle_v1 events.
	handleEventTitle  = 0
	handleEventAppID  = 1
	handleEventState  = 4
	handleEventClosed = 6

	// zwlr_foreign_toplevel_handle_v1 requests.
	handleRequestUnsetMaximized  = 1
	handleRequestUnsetMinimized  = 3
	handleRequestSetMaximized    = 0
	handleRequestSetMinimized    = 2
	handleRequestActivate        = 4
	handleRequestClose           = 5
	handleRequestUnsetFullscreen = 9

	// State enum values in the handle's state array.
	stateActivated = 2
)

// errNoToplevel reports a compositor that does not serve the foreign-toplevel
// interface; the caller falls back to the X path.
var errNoToplevel = errors.New("the compositor does not serve the foreign-toplevel protocol")

// toplevel is the window information gathered for one handle during an
// enumeration.
type toplevel struct {
	handle uint32
	title  string
	appID  string
	active bool
}

// conn is one connection to the compositor with the foreign-toplevel manager
// and a seat bound.
type conn struct {
	fd     int
	buf    []byte
	nextID uint32

	registry       uint32
	seat           uint32
	manager        uint32
	managerVersion uint32
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
	deadline := time.Now().Add(10 * time.Second)
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	tv := unix.NsecToTimeval(time.Until(deadline).Nanoseconds())
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	_ = unix.SetsockoptTimeval(fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv)
	return c, nil
}

func (c *conn) close() {
	if c.fd >= 0 {
		unix.Close(c.fd)
		c.fd = -1
	}
}

func (c *conn) newID() uint32 {
	id := c.nextID
	c.nextID++
	return id
}

// request marshals and sends one request.
func (c *conn) request(object uint32, opcode int, args ...any) error {
	body := make([]byte, 0, 16)
	le := binary.LittleEndian
	for _, arg := range args {
		switch v := arg.(type) {
		case uint32:
			body = le.AppendUint32(body, v)
		case string:
			body = le.AppendUint32(body, uint32(len(v)+1))
			body = append(body, v...)
			body = append(body, 0)
			for len(body)%4 != 0 {
				body = append(body, 0)
			}
		default:
			return fmt.Errorf("unsupported argument %T", arg)
		}
	}
	msg := make([]byte, 8, 8+len(body))
	le.PutUint32(msg, object)
	le.PutUint32(msg[4:], uint32(len(body)+8)<<16|uint32(opcode))
	msg = append(msg, body...)
	_, err := unix.Write(c.fd, msg)
	return err
}

type event struct {
	object uint32
	opcode int
	body   []byte
}

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

// roundtrip sends a sync and processes events until its callback answers.
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

// setup binds the registry, a seat, and the foreign-toplevel manager. It fails
// with errNoToplevel when the manager global is absent.
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
	var seat, mgr global
	var haveSeat, haveMgr bool
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
		case "zwlr_foreign_toplevel_manager_v1":
			mgr, haveMgr = global{name, version}, true
		}
		return nil
	})
	if err != nil {
		c.close()
		return nil, err
	}
	if !haveMgr {
		c.close()
		return nil, errNoToplevel
	}

	bind := func(g global, iface string, version uint32) (uint32, error) {
		if g.version < version {
			version = g.version
		}
		id := c.newID()
		return id, c.request(c.registry, registryRequestBind, g.name, iface, version, id)
	}
	if haveSeat {
		if c.seat, err = bind(seat, "wl_seat", 1); err != nil {
			c.close()
			return nil, err
		}
	}
	mgrVersion := min(mgr.version, 3)
	c.managerVersion = mgrVersion
	if c.manager, err = bind(mgr, "zwlr_foreign_toplevel_manager_v1", mgrVersion); err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// enumerate binds the manager and gathers every toplevel the compositor reports
// in one roundtrip. The manager emits a toplevel event per window and each
// handle then emits its title, app id, and state before a done; one sync flushes
// that whole initial burst.
func (c *conn) enumerate() (map[uint32]*toplevel, error) {
	tops := map[uint32]*toplevel{}
	err := c.roundtrip(func(ev event) error {
		switch {
		case ev.object == c.manager && ev.opcode == managerEventToplevel:
			if len(ev.body) >= 4 {
				handle := binary.LittleEndian.Uint32(ev.body)
				tops[handle] = &toplevel{handle: handle}
			}
		case ev.opcode == handleEventClosed:
			delete(tops, ev.object)
		default:
			t, ok := tops[ev.object]
			if !ok {
				return nil
			}
			switch ev.opcode {
			case handleEventTitle:
				if s, _, ok := parseString(ev.body); ok {
					t.title = s
				}
			case handleEventAppID:
				if s, _, ok := parseString(ev.body); ok {
					t.appID = s
				}
			case handleEventState:
				// The state is a wl_array of uint32 values; a leading length in
				// bytes precedes the packed values.
				if len(ev.body) >= 4 {
					n := int(binary.LittleEndian.Uint32(ev.body))
					data := ev.body[4:]
					for i := 0; i+4 <= n && i+4 <= len(data); i += 4 {
						if binary.LittleEndian.Uint32(data[i:]) == stateActivated {
							t.active = true
						}
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tops, nil
}
