//go:build !windows && !darwin

// Package wldata is a Wayland client for the data-control protocol, the
// clipboard-manager interface KDE and the wlroots compositors serve
// (ext-data-control-v1, and its zwlr predecessor). It speaks the wire format
// itself over the compositor's socket — a header of object id and
// size-plus-opcode words, little-endian arguments, and file descriptors over
// SCM_RIGHTS — which is what reads and writes the selection of a
// Wayland-native session with nothing installed and no X server involved.
// GNOME does not serve the interface, and answers by falling back to the
// XWayland selection owner instead.
package wldata

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

// Core protocol object ids and opcodes: wl_display is always object 1, and
// the request and event numbers are fixed by the XML the compositor was built
// from.
const (
	displayID = 1

	displayRequestSync        = 0
	displayRequestGetRegistry = 1
	displayEventError         = 0

	registryRequestBind = 0
	registryEventGlobal = 0
	callbackEventDone   = 0

	managerRequestCreateSource  = 0
	managerRequestGetDataDevice = 1

	deviceRequestSetSelection        = 0
	deviceRequestSetPrimarySelection = 2
	deviceEventDataOffer             = 0
	deviceEventSelection             = 1
	deviceEventFinished              = 2
	deviceEventPrimarySelection      = 3

	sourceRequestOffer   = 0
	sourceEventSend      = 0
	sourceEventCancelled = 1

	offerRequestReceive = 0
	offerEventOffer     = 0
)

// textMimes are the types offered and accepted for a text selection, best
// first — the same set wl-clipboard negotiates.
var textMimes = []string{"text/plain;charset=utf-8", "UTF8_STRING", "text/plain", "TEXT", "STRING"}

// errNoDataControl reports a compositor that does not serve the interface;
// the caller falls back to the X path.
var errNoDataControl = errors.New("the compositor does not serve data-control")

// conn is one connection to the compositor with the data-control objects
// bound: ids are allocated sequentially from 2, so the layout is fixed at
// setup and remembered in the fields.
type conn struct {
	fd     int
	buf    []byte
	fds    []int
	nextID uint32

	registry uint32
	seat     uint32
	manager  uint32
	device   uint32

	// primary reports whether the bound interface and version carry the
	// primary selection; ext has it from v1, zwlr from v2.
	primary bool
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

// setDeadline bounds every read on the socket.
func (c *conn) setDeadline(t time.Time) {
	tv := unix.NsecToTimeval(time.Until(t).Nanoseconds())
	_ = unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, unix.SO_RCVTIMEO, &tv)
	_ = unix.SetsockoptTimeval(c.fd, unix.SOL_SOCKET, unix.SO_SNDTIMEO, &tv)
}

// close releases the socket and any file descriptors received but not used.
func (c *conn) close() {
	for _, fd := range c.fds {
		unix.Close(fd)
	}
	c.fds = nil
	unix.Close(c.fd)
}

// newID allocates the next client-side object id.
func (c *conn) newID() uint32 {
	id := c.nextID
	c.nextID++
	return id
}

// request marshals and sends one request. Arguments are uint32 words and
// strings; a string is a length including its NUL, the bytes, and padding to
// a word.
func (c *conn) request(object uint32, opcode int, args ...any) error {
	body := make([]byte, 0, 32)
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

// requestFD sends one request whose final argument is a file descriptor,
// which travels as SCM_RIGHTS beside the message rather than in it.
func (c *conn) requestFD(object uint32, opcode int, passFD int, args ...any) error {
	body := make([]byte, 0, 32)
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
	return unix.Sendmsg(c.fd, msg, unix.UnixRights(passFD), nil, 0)
}

// event is one parsed message from the compositor. A descriptor riding
// beside a message is queued on the connection rather than carried here,
// because the kernel delivers it with whatever read happened to collect the
// bytes, not necessarily with the message that logically owns it.
type event struct {
	object uint32
	opcode int
	body   []byte
}

// readEvent returns the next complete message, receiving more bytes — and any
// file descriptors riding beside them — as needed.
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
		oob := make([]byte, 256)
		n, oobn, _, _, err := unix.Recvmsg(c.fd, chunk, oob, 0)
		// A signal landing on this thread fails the receive with EINTR, because
		// the socket's receive timeout stops the kernel restarting it.
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
		if oobn > 0 {
			msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
			if err == nil {
				for _, m := range msgs {
					if fds, err := unix.ParseUnixRights(&m); err == nil {
						c.fds = append(c.fds, fds...)
					}
				}
			}
		}
	}
}

// takeFD pops the oldest received descriptor, for the event that carries one.
func (c *conn) takeFD() int {
	if len(c.fds) == 0 {
		return -1
	}
	fd := c.fds[0]
	c.fds = c.fds[1:]
	return fd
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
func (c *conn) checkError(ev event) error {
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
		if err := c.checkError(ev); err != nil {
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

// setup binds the registry, a seat, and the data-control manager, and creates
// the data device. It fails with errNoDataControl when no manager global
// exists.
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

	// One pass over the globals: remember the seat and the best manager. ext
	// is the standardized interface, so it wins over the zwlr predecessor.
	type global struct {
		name    uint32
		version uint32
	}
	var seat, extManager, wlrManager global
	var haveSeat, haveExt, haveWlr bool
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
		case "ext_data_control_manager_v1":
			extManager, haveExt = global{name, version}, true
		case "zwlr_data_control_manager_v1":
			wlrManager, haveWlr = global{name, version}, true
		}
		return nil
	})
	if err != nil {
		c.close()
		return nil, err
	}
	if !haveSeat || (!haveExt && !haveWlr) {
		c.close()
		return nil, errNoDataControl
	}

	// Bind the seat and the manager, then make the device for that seat.
	bind := func(g global, iface string, version uint32) (uint32, error) {
		if g.version < version {
			version = g.version
		}
		id := c.newID()
		return id, c.request(c.registry, registryRequestBind, g.name, iface, version, id)
	}
	c.seat, err = bind(seat, "wl_seat", 1)
	if err == nil {
		if haveExt {
			c.manager, err = bind(extManager, "ext_data_control_manager_v1", 1)
			c.primary = true
		} else {
			c.manager, err = bind(wlrManager, "zwlr_data_control_manager_v1", 2)
			c.primary = wlrManager.version >= 2
		}
	}
	if err == nil {
		c.device = c.newID()
		err = c.request(c.manager, managerRequestGetDataDevice, c.device, c.seat)
	}
	if err != nil {
		c.close()
		return nil, err
	}
	return c, nil
}

// Available reports whether the compositor serves data-control at all, which
// is what decides the backend at session build.
func Available(ctx context.Context) bool {
	c, err := setup(ctx)
	if err != nil {
		return false
	}
	c.close()
	return true
}

// selectionOpcodes maps the caller's selection onto the device's request and
// event pair, failing where the bound interface has no primary selection.
func (c *conn) selectionOpcodes(primary bool) (setRequest, offerEvent int, err error) {
	if !primary {
		return deviceRequestSetSelection, deviceEventSelection, nil
	}
	if !c.primary {
		return 0, 0, errors.New("this compositor's data-control has no primary selection")
	}
	return deviceRequestSetPrimarySelection, deviceEventPrimarySelection, nil
}

// Read returns the selection's text. An empty clipboard reads as empty.
func Read(ctx context.Context, primary bool) (string, error) {
	c, err := setup(ctx)
	if err != nil {
		return "", err
	}
	defer c.close()
	_, wantEvent, err := c.selectionOpcodes(primary)
	if err != nil {
		return "", err
	}

	// The device announces the current selection right after creation: an
	// offer object, its mime types, then which offer is the selection.
	offerMimes := map[uint32][]string{}
	var selection uint32
	var haveSelection bool
	err = c.roundtrip(func(ev event) error {
		switch {
		case ev.object == c.device && ev.opcode == deviceEventDataOffer:
			if len(ev.body) >= 4 {
				offerMimes[binary.LittleEndian.Uint32(ev.body)] = nil
			}
		case ev.object == c.device && ev.opcode == wantEvent:
			if len(ev.body) >= 4 {
				selection = binary.LittleEndian.Uint32(ev.body)
				haveSelection = selection != 0
			}
		case ev.opcode == offerEventOffer:
			if _, tracked := offerMimes[ev.object]; tracked {
				if mime, _, ok := parseString(ev.body); ok {
					offerMimes[ev.object] = append(offerMimes[ev.object], mime)
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if !haveSelection {
		return "", nil
	}

	mime := chooseMime(offerMimes[selection])
	if mime == "" {
		return "", errors.New("the selection offers no text")
	}

	// Hand the owner a pipe to write into and drain it to EOF.
	pipe, writeFD, err := selectionPipe()
	if err != nil {
		return "", err
	}
	defer pipe.Close()
	if err := c.requestFD(selection, offerRequestReceive, writeFD, mime); err != nil {
		unix.Close(writeFD)
		return "", err
	}
	// The owner writes into its own copy; this one has to go or the read
	// never sees EOF.
	unix.Close(writeFD)
	// Flush the request so the owner starts writing before the pipe read.
	if err := c.roundtrip(nil); err != nil {
		return "", err
	}

	return readSelection(pipe, 5*time.Second)
}

// readSelection drains the owner's end of a transfer to EOF. An owner that
// stops before closing its end has sent part of the selection, which is
// reported rather than returned as the clipboard.
func readSelection(pipe *os.File, timeout time.Duration) (string, error) {
	if err := pipe.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return "", fmt.Errorf("bounding the selection read: %w", err)
	}
	var text strings.Builder
	chunk := make([]byte, 4096)
	for {
		n, err := pipe.Read(chunk)
		text.Write(chunk[:n])
		if errors.Is(err, io.EOF) {
			return text.String(), nil
		}
		if err != nil {
			return "", fmt.Errorf("the selection owner did not finish sending: %w", err)
		}
	}
}

// selectionPipe makes the pipe one selection transfer travels through: the
// read end as a file this process drains, the write end as the descriptor the
// owner is handed.
func selectionPipe() (*os.File, int, error) {
	var fds [2]int
	if err := unix.Pipe2(fds[:], unix.O_CLOEXEC); err != nil {
		return nil, -1, err
	}
	// A file deadline is honored only on a non-blocking descriptor. The flag
	// belongs to each end separately, so the owner still gets a blocking one.
	if err := unix.SetNonblock(fds[0], true); err != nil {
		unix.Close(fds[0])
		unix.Close(fds[1])
		return nil, -1, err
	}
	// The read end becomes an os.File right away, so one owner closes it.
	return os.NewFile(uintptr(fds[0]), "selection"), fds[1], nil
}

// sendSelection writes the text into a paster's descriptor and closes it. The
// descriptor arrives blocking, and the write deadline is honored only once it
// is not: without it a paster that stops reading holds this goroutine, and
// with it every later paste, for as long as the selection is owned.
func sendSelection(fd int, text string, timeout time.Duration) {
	_ = unix.SetNonblock(fd, true)
	pipe := os.NewFile(uintptr(fd), "paste")
	_ = pipe.SetWriteDeadline(time.Now().Add(timeout))
	_, _ = pipe.Write([]byte(text))
	_ = pipe.Close()
}

// chooseMime picks the best text type the owner offers.
func chooseMime(offered []string) string {
	for _, want := range textMimes {
		for _, have := range offered {
			if strings.EqualFold(have, want) {
				return have
			}
		}
	}
	return ""
}

// Write puts text on the selection and holds ownership until the compositor
// reports it replaced: the connection lives on in a goroutine answering each
// paste, which is what wl-copy forks a child for.
func Write(ctx context.Context, primary bool, text string) error {
	c, err := setup(ctx)
	if err != nil {
		return err
	}
	setRequest, _, err := c.selectionOpcodes(primary)
	if err != nil {
		c.close()
		return err
	}

	source := c.newID()
	if err := c.request(c.manager, managerRequestCreateSource, source); err != nil {
		c.close()
		return err
	}
	for _, mime := range textMimes {
		if err := c.request(source, sourceRequestOffer, mime); err != nil {
			c.close()
			return err
		}
	}
	if err := c.request(c.device, setRequest, source); err != nil {
		c.close()
		return err
	}
	// The roundtrip confirms the compositor accepted the selection before the
	// call returns; pastes are served after.
	if err := c.roundtrip(nil); err != nil {
		c.close()
		return err
	}

	go serveSelection(c, source, text)
	return nil
}

// serveSelection answers paste requests until the selection is replaced. The
// deadline is lifted: ownership lasts until the next copy, however far away.
func serveSelection(c *conn, source uint32, text string) {
	defer c.close()
	c.setDeadline(time.Now().Add(24 * 365 * time.Hour))
	for {
		ev, err := c.readEvent()
		if err != nil {
			return
		}
		switch {
		case ev.object == source && ev.opcode == sourceEventSend:
			fd := c.takeFD()
			if fd < 0 {
				continue
			}
			sendSelection(fd, text, 5*time.Second)
		case ev.object == source && ev.opcode == sourceEventCancelled:
			return
		case ev.object == c.device && ev.opcode == deviceEventFinished:
			return
		}
	}
}
