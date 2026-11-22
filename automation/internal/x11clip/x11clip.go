//go:build !windows && !darwin

// Package x11clip is a native X11 clipboard backend: it owns the CLIPBOARD and
// PRIMARY selections through the X server directly rather than shelling out to
// xclip or xsel, so it needs nothing installed. It works on a real X11 session
// and, because a Wayland compositor running XWayland answers X selection
// requests for its X clients, on GNOME and any other Wayland session with
// XWayland — the case wlr-data-control does not reach.
//
// X11 clipboard ownership is not fire-and-forget: the owner keeps serving the
// data to every application that pastes, for as long as it owns the selection.
// So a single long-lived connection and one goroutine hold the selection and
// answer SelectionRequest events for the life of the session, which is what a
// running xclip does too.
package x11clip

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// readTimeout bounds a paste: a ConvertSelection that no owner answers would
// otherwise wait forever, which is exactly what a clipboard with no owner does.
const readTimeout = 5 * time.Second

// Clip owns the X selections for the session's lifetime.
type Clip struct {
	conn *xgb.Conn
	win  xproto.Window

	// Atoms resolved once at connect.
	aClipboard xproto.Atom
	aPrimary   xproto.Atom
	aTargets   xproto.Atom
	aUTF8      xproto.Atom
	aString    xproto.Atom
	aText      xproto.Atom
	aProp      xproto.Atom
	aIncr      xproto.Atom

	mu    sync.Mutex
	owned map[xproto.Atom]string // selection atom -> the text we serve for it

	// readMu serializes reads so one ConvertSelection reply is never mistaken
	// for another, and notifies carries the owner loop's SelectionNotify to the
	// waiting Read.
	readMu   sync.Mutex
	notifies chan xproto.SelectionNotifyEvent
}

// New connects to the X server and starts serving selections. It returns an
// error when there is no reachable X display, which is the signal to fall back
// to another clipboard backend.
func New() (*Clip, error) {
	if strings.TrimSpace(os.Getenv("DISPLAY")) == "" {
		return nil, errors.New("no DISPLAY in the environment")
	}
	conn, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("cannot reach the X display %s: %w", os.Getenv("DISPLAY"), err)
	}

	c := &Clip{
		conn:     conn,
		owned:    make(map[xproto.Atom]string),
		notifies: make(chan xproto.SelectionNotifyEvent, 1),
	}
	if err := c.intern(); err != nil {
		conn.Close()
		return nil, err
	}
	if err := c.createWindow(); err != nil {
		conn.Close()
		return nil, err
	}

	go c.serve()
	return c, nil
}

// intern resolves the atoms the selection protocol names.
func (c *Clip) intern() error {
	names := []struct {
		name string
		dst  *xproto.Atom
	}{
		{"CLIPBOARD", &c.aClipboard},
		{"TARGETS", &c.aTargets},
		{"UTF8_STRING", &c.aUTF8},
		{"TEXT", &c.aText},
		{"INCR", &c.aIncr},
		{"KARAKURI_CLIPBOARD", &c.aProp},
	}
	for _, n := range names {
		reply, err := xproto.InternAtom(c.conn, false, uint16(len(n.name)), n.name).Reply()
		if err != nil {
			return fmt.Errorf("interning atom %s: %w", n.name, err)
		}
		*n.dst = reply.Atom
	}
	// PRIMARY and STRING are predefined atoms, not interned.
	c.aPrimary = xproto.AtomPrimary
	c.aString = xproto.AtomString
	return nil
}

// createWindow makes the unmapped, input-only window that owns the selections
// and receives their requests.
func (c *Clip) createWindow() error {
	id, err := c.conn.NewId()
	if err != nil {
		return fmt.Errorf("allocating a window id: %w", err)
	}
	c.win = xproto.Window(id)
	screen := xproto.Setup(c.conn).DefaultScreen(c.conn)
	// InputOnly (class 2), never mapped: it exists only to hold selections.
	err = xproto.CreateWindowChecked(c.conn, 0, c.win, screen.Root,
		0, 0, 1, 1, 0, 2, screen.RootVisual, 0, nil).Check()
	if err != nil {
		return fmt.Errorf("creating the selection window: %w", err)
	}
	return nil
}

// selectionAtom maps a capability selection onto its X atom.
func (c *Clip) selectionAtom(sel autoapi.Selection) xproto.Atom {
	if sel == autoapi.SelectionPrimary {
		return c.aPrimary
	}
	return c.aClipboard
}

// Write takes ownership of the selection and serves text until another client
// claims it or the session ends.
func (c *Clip) Write(ctx context.Context, sel autoapi.Selection, text string) error {
	selAtom := c.selectionAtom(sel)

	c.mu.Lock()
	c.owned[selAtom] = text
	c.mu.Unlock()

	if err := xproto.SetSelectionOwnerChecked(c.conn, c.win, selAtom, xproto.TimeCurrentTime).Check(); err != nil {
		return fmt.Errorf("taking the %s selection: %w", sel, err)
	}
	// Confirm the server recorded us as the owner; a refused ownership would
	// otherwise look like a working copy that pastes nothing.
	owner, err := xproto.GetSelectionOwner(c.conn, selAtom).Reply()
	if err != nil {
		return fmt.Errorf("confirming the %s selection: %w", sel, err)
	}
	if owner.Owner != c.win {
		return fmt.Errorf("the X server did not grant ownership of the %s selection", sel)
	}
	return nil
}

// Read asks the current owner for the selection as text.
func (c *Clip) Read(ctx context.Context, sel autoapi.Selection) (string, error) {
	selAtom := c.selectionAtom(sel)

	c.readMu.Lock()
	defer c.readMu.Unlock()

	// Drain any stale notify before issuing our own request.
	select {
	case <-c.notifies:
	default:
	}

	if err := xproto.ConvertSelectionChecked(c.conn, c.win, selAtom, c.aUTF8, c.aProp, xproto.TimeCurrentTime).Check(); err != nil {
		return "", fmt.Errorf("requesting the %s selection: %w", sel, err)
	}

	timer := time.NewTimer(readTimeout)
	defer timer.Stop()
	select {
	case ev := <-c.notifies:
		if ev.Property == 0 {
			// No owner, or the owner cannot provide text: an empty clipboard.
			return "", nil
		}
		return c.readProp()
	case <-ctx.Done():
		return "", ctx.Err()
	case <-timer.C:
		return "", fmt.Errorf("the %s selection owner did not answer", sel)
	}
}

// readProp reads and deletes the property the owner wrote our answer into.
func (c *Clip) readProp() (string, error) {
	reply, err := xproto.GetProperty(c.conn, true, c.win, c.aProp, xproto.GetPropertyTypeAny, 0, (1<<32)-1).Reply()
	if err != nil {
		return "", fmt.Errorf("reading the selection property: %w", err)
	}
	if reply.Type == c.aIncr {
		// The owner wants to hand over a large value in chunks. Text on a
		// clipboard is rarely this big, and the agent caps what it returns, so
		// this is reported rather than half-read.
		return "", errors.New("the clipboard holds an incremental transfer too large to read")
	}
	return string(reply.Value), nil
}

// serve answers selection requests and routes notifies to Read for the life of
// the connection.
func (c *Clip) serve() {
	for {
		ev, err := c.conn.WaitForEvent()
		if ev == nil && err == nil {
			return // connection closed
		}
		switch e := ev.(type) {
		case xproto.SelectionRequestEvent:
			c.answer(e)
		case xproto.SelectionClearEvent:
			// Another client took the selection; stop serving it.
			c.mu.Lock()
			delete(c.owned, e.Selection)
			c.mu.Unlock()
		case xproto.SelectionNotifyEvent:
			select {
			case c.notifies <- e:
			default:
			}
		}
	}
}

// answer responds to one application asking to paste what we hold.
func (c *Clip) answer(req xproto.SelectionRequestEvent) {
	c.mu.Lock()
	text, owned := c.owned[req.Selection]
	c.mu.Unlock()

	property := req.Property
	// A Property of None is an old client's way of asking; reply into Target.
	if property == 0 {
		property = req.Target
	}

	switch {
	case !owned:
		property = 0 // we do not own this selection
	case req.Target == c.aTargets:
		// Advertise the text targets we can satisfy.
		targets := []xproto.Atom{c.aTargets, c.aUTF8, c.aString, c.aText}
		buf := make([]byte, len(targets)*4)
		for i, a := range targets {
			xgb.Put32(buf[i*4:], uint32(a))
		}
		_ = xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, req.Requestor,
			property, xproto.AtomAtom, 32, uint32(len(targets)), buf).Check()
	case req.Target == c.aUTF8 || req.Target == c.aString || req.Target == c.aText:
		data := []byte(text)
		_ = xproto.ChangePropertyChecked(c.conn, xproto.PropModeReplace, req.Requestor,
			property, req.Target, 8, uint32(len(data)), data).Check()
	default:
		property = 0 // a target we do not speak
	}

	notify := xproto.SelectionNotifyEvent{
		Time:      req.Time,
		Requestor: req.Requestor,
		Selection: req.Selection,
		Target:    req.Target,
		Property:  property,
	}
	_ = xproto.SendEventChecked(c.conn, false, req.Requestor, 0, string(notify.Bytes())).Check()
}

// Close releases the selections and the connection.
func (c *Clip) Close() {
	c.conn.Close()
}
