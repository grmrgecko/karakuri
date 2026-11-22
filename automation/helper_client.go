package automation

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// helper is the calling side of the session helper: it starts the child on the
// first call that needs it, correlates replies by id so a dialog sitting open
// does not hold up the notification behind it, and starts a new child when the
// old one dies. A helper that has never been asked for anything has never
// spawned a process, which is what keeps a root agent whose flows only read
// files from prompting anybody for portal consent.
type helper struct {
	noRetry bool
	start   func(context.Context) (*hrpc.Conn, func(), error)
	logf    func(string, ...any)

	mu      sync.Mutex
	conn    *hrpc.Conn
	stop    func()
	pending map[uint64]chan hrpc.Response
	nextID  uint64
	// gen counts restarts, so a caller that lost its child knows whether
	// somebody else already replaced it before it tries to.
	gen uint64
}

// newHelper builds the client around a spawn function.
func newHelper(start func(context.Context) (*hrpc.Conn, func(), error), logf func(string, ...any)) *helper {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &helper{start: start, logf: logf, pending: map[uint64]chan hrpc.Response{}}
}

// call performs one capability call, starting or restarting the child as
// needed. A child that died is retried once: the common cause is the desktop
// session ending under it, and the retry is what picks up the new one.
func (h *helper) call(ctx context.Context, capability, method string, args, out any) error {
	err := h.callOnce(ctx, capability, method, args, out)
	if err == nil || ctx.Err() != nil {
		return err
	}
	if h.noRetry || !isPeerGone(err) {
		return err
	}
	h.logf("session helper went away during %s.%s, starting another", capability, method)
	return h.callOnce(ctx, capability, method, args, out)
}

// callOnce sends one request and waits for its reply.
func (h *helper) callOnce(ctx context.Context, capability, method string, args, out any) error {
	raw, err := hrpc.Marshal(args)
	if err != nil {
		return err
	}

	conn, gen, id, reply, err := h.enqueue(ctx)
	if err != nil {
		return err
	}
	defer h.forget(id)

	if err := conn.WriteRequest(hrpc.Request{ID: id, Cap: capability, Method: method, Args: raw}); err != nil {
		h.dropChild(gen)
		return fmt.Errorf("%w: %v", hrpc.ErrPeerGone, err)
	}

	select {
	case <-ctx.Done():
		if h.noRetry {
			h.dropChild(gen)
		}
		return ctx.Err()
	case resp, ok := <-reply:
		if !ok {
			return hrpc.ErrPeerGone
		}
		if resp.Err != nil {
			return fromWire(resp.Err)
		}
		return hrpc.Unmarshal(resp.Result, out)
	}
}

// enqueue makes sure a child is running and registers a slot for the reply.
func (h *helper) enqueue(ctx context.Context) (*hrpc.Conn, uint64, uint64, chan hrpc.Response, error) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.conn == nil {
		conn, stop, err := h.start(ctx)
		if err != nil {
			return nil, 0, 0, nil, err
		}
		h.conn, h.stop, h.gen = conn, stop, h.gen+1
		go h.readLoop(conn, h.gen)
	}

	h.nextID++
	id := h.nextID
	reply := make(chan hrpc.Response, 1)
	h.pending[id] = reply
	return h.conn, h.gen, id, reply, nil
}

// forget drops a reply slot once its caller is done with it.
func (h *helper) forget(id uint64) {
	h.mu.Lock()
	delete(h.pending, id)
	h.mu.Unlock()
}

// readLoop delivers replies until the child stops answering, then fails every
// call still waiting so none of them hangs on a process that is gone.
func (h *helper) readLoop(conn *hrpc.Conn, gen uint64) {
	for {
		resp, err := conn.ReadResponse()
		if err != nil {
			h.dropChild(gen)
			return
		}
		h.mu.Lock()
		reply, ok := h.pending[resp.ID]
		if ok {
			reply <- resp
		}
		h.mu.Unlock()
	}
}

// dropChild tears down the child of that generation, if it is still the current
// one. The generation check is what keeps a late reader from killing the
// replacement somebody else already started.
func (h *helper) dropChild(gen uint64) {
	h.mu.Lock()
	if h.gen != gen || h.conn == nil {
		h.mu.Unlock()
		return
	}
	stop := h.stop
	waiting := h.pending
	h.conn, h.stop, h.pending = nil, nil, map[uint64]chan hrpc.Response{}
	h.mu.Unlock()

	for _, reply := range waiting {
		close(reply)
	}
	if stop != nil {
		stop()
	}
}

// Close stops the child if one is running.
func (h *helper) Close() error {
	h.mu.Lock()
	gen := h.gen
	h.mu.Unlock()
	h.dropChild(gen)
	return nil
}

// isPeerGone reports whether a failure was the child dying rather than the call
// itself failing.
func isPeerGone(err error) bool {
	return err == hrpc.ErrPeerGone || (err != nil && errorsIs(err, hrpc.ErrPeerGone))
}

// errorsIs is errors.Is, kept local so this file does not import errors twice
// over for one call.
func errorsIs(err, target error) bool {
	for err != nil {
		if err == target {
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

// The capability stubs. Each is the local face of one remote capability; they
// hold nothing but the shared helper, so every call goes down the same pipe and
// the child stays one process.

type helperKeyboard struct{ h *helper }

// TypeText types the text in the delegated session. The injected clock does not
// cross: pacing is the helper's own wait, and a caller driving a fake clock is
// testing the module rather than a privileged agent.
func (k helperKeyboard) TypeText(ctx context.Context, _ autoapi.Clock, text string, delay time.Duration) error {
	return k.h.call(ctx, capKeyboard, mTypeText, typeTextArgs{Text: text, Delay: delay}, nil)
}

func (k helperKeyboard) PressKeys(ctx context.Context, stroke autoapi.KeyStroke) error {
	return k.h.call(ctx, capKeyboard, mPressKeys, stroke, nil)
}

type helperMouse struct{ h *helper }

func (m helperMouse) Move(ctx context.Context, x, y int, relative bool) error {
	return m.h.call(ctx, capMouse, mMove, moveArgs{X: x, Y: y, Relative: relative}, nil)
}

func (m helperMouse) Click(ctx context.Context, button autoapi.Button, count int) error {
	return m.h.call(ctx, capMouse, mClick, clickArgs{Button: button, Count: count}, nil)
}

func (m helperMouse) Hold(ctx context.Context, button autoapi.Button, down bool) error {
	return m.h.call(ctx, capMouse, mHold, holdArgs{Button: button, Down: down}, nil)
}

func (m helperMouse) Scroll(ctx context.Context, amount int, horizontal bool) error {
	return m.h.call(ctx, capMouse, mScroll, scrollArgs{Amount: amount, Horizontal: horizontal}, nil)
}

func (m helperMouse) Position(ctx context.Context) (int, int, error) {
	var out positionResult
	if err := m.h.call(ctx, capMouse, mPosition, nil, &out); err != nil {
		return 0, 0, err
	}
	return out.X, out.Y, nil
}

type helperScreen struct{ h *helper }

// Capture photographs the delegated session. The image crosses as PNG because
// that is the one encoding every backend's result already survives losslessly,
// and it is what the caller encodes to anyway.
func (s helperScreen) Capture(ctx context.Context, req autoapi.CaptureRequest) (image.Image, error) {
	var out captureResult
	if err := s.h.call(ctx, capScreen, mCapture, req, &out); err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(out.PNG))
	if err != nil {
		return nil, fmt.Errorf("decoding the screenshot the session helper returned: %w", err)
	}
	return autoapi.WithCaptureMapping(img, out.Mapping), nil
}

type helperWindow struct{ h *helper }

func (w helperWindow) List(ctx context.Context) ([]autoapi.WindowInfo, error) {
	var out []autoapi.WindowInfo
	err := w.h.call(ctx, capWindow, mList, nil, &out)
	return out, err
}

func (w helperWindow) Act(ctx context.Context, win autoapi.WindowInfo, action autoapi.WindowAction) error {
	return w.h.call(ctx, capWindow, mAct, windowActArgs{Window: win, Action: action}, nil)
}

type helperClipboard struct{ h *helper }

func (c helperClipboard) Read(ctx context.Context, sel autoapi.Selection) (string, error) {
	var out textResult
	err := c.h.call(ctx, capClipboard, mRead, clipboardReadArgs{Selection: sel}, &out)
	return out.Text, err
}

func (c helperClipboard) Write(ctx context.Context, sel autoapi.Selection, text string) error {
	return c.h.call(ctx, capClipboard, mWrite, clipboardWriteArgs{Selection: sel, Text: text}, nil)
}

type helperNotifier struct{ h *helper }

func (n helperNotifier) Notify(ctx context.Context, note autoapi.Notification) error {
	return n.h.call(ctx, capNotifier, mNotify, note, nil)
}

type helperDialog struct{ h *helper }

func (d helperDialog) Ask(ctx context.Context, req autoapi.DialogRequest) (autoapi.DialogResult, error) {
	var out autoapi.DialogResult
	err := d.h.call(ctx, capDialog, mAsk, req, &out)
	return out, err
}

type helperSpeaker struct{ h *helper }

func (s helperSpeaker) Speak(ctx context.Context, req autoapi.SpeakRequest) error {
	return s.h.call(ctx, capSpeaker, mSpeak, req, nil)
}

type helperMedia struct{ h *helper }

func (m helperMedia) Control(ctx context.Context, req autoapi.MediaRequest) error {
	return m.h.call(ctx, capMedia, mControl, req, nil)
}

type helperPower struct{ h *helper }

func (p helperPower) Control(ctx context.Context, action autoapi.PowerAction) error {
	return p.h.call(ctx, capPower, mControl, action, nil)
}

type helperIdle struct{ h *helper }

func (i helperIdle) Since(ctx context.Context, now time.Time) (float64, string, error) {
	var out sinceResult
	if err := i.h.call(ctx, capIdle, mSince, sinceArgs{Now: now}, &out); err != nil {
		return 0, "", err
	}
	return out.Seconds, out.Source, nil
}

type helperOpener struct{ h *helper }

func (o helperOpener) OpenURL(ctx context.Context, url string) error {
	return o.h.call(ctx, capOpener, mOpenURL, openURLArgs{URL: url}, nil)
}

func (o helperOpener) OpenApp(ctx context.Context, app string, args []string) error {
	return o.h.call(ctx, capOpener, mOpenApp, openAppArgs{App: app, Args: args}, nil)
}

// helperSessions is the privileged parent's actor for the verbs that must run
// inside the session itself. It is not the composed Session's Sessions — that
// stays the parent's own backend, which reads the table and verifies
// passwords — it is the actor the backend hands its lock, switch, and
// login-window verbs to when it cannot perform them itself. On macOS a
// privileged process's own session verbs are refused, and the helper, which
// belongs to the session, performs them.
type helperSessions struct{ h *helper }

// Act runs one session verb in the delegated session.
func (s helperSessions) Act(ctx context.Context, target autoapi.SessionInfo, action autoapi.SessionAction) error {
	return s.h.call(ctx, capSessions, mAct, sessionActArgs{Target: target, Action: action}, nil)
}

// List is never reached: the parent reads the session table itself, which
// every platform allows a privileged process to do.
func (s helperSessions) List(ctx context.Context) ([]autoapi.SessionInfo, error) {
	return nil, fmt.Errorf("the session helper does not list sessions; the parent reads the table itself")
}

// Login is never reached: proving a password and typing it stay with the
// parent, which drives the typing through the helper's keyboard instead.
func (s helperSessions) Login(ctx context.Context, req autoapi.LoginRequest) error {
	return fmt.Errorf("the session helper does not log in; the parent proves the password and types through the keyboard")
}
