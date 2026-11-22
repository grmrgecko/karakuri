package automation

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"net"
	"os"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// EnvHelperTokenDir names the directory the helper persists the RemoteDesktop
// portal's restore token in. The parent chooses the path — it has to be one the
// delegated user owns, which the privileged parent's own state directory is not
// — and passes it here rather than on the command line, so a caller wiring the
// helper into its CLI does not have to carry a flag for it.
const EnvHelperTokenDir = "KARAKURI_AUTOMATION_TOKEN_DIR"

// EnvHelperSocket names the unix socket the helper serves on when the wire
// cannot be inherited pipes. The macOS helper is started by launchd, which
// gives a job no standard input to read, so the parent passes the socket path
// it expects the helper to bind here and connects once the job is up.
const EnvHelperSocket = "KARAKURI_SESSION_HELPER_SOCKET"

// ServeHelperSocket is ServeHelper over a unix socket instead of inherited
// pipes: the helper binds the path, accepts the one client — the privileged
// parent — and serves it until the connection closes. The socket is left at
// 0600 in the work directory the parent created for it, which only that user
// and root can reach.
func ServeHelperSocket(ctx context.Context, path string, opts Options) error {
	_ = os.Remove(path)
	ln, err := net.Listen("unix", path)
	if err != nil {
		return fmt.Errorf("binding the session helper socket: %w", err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		ln.Close()
		return fmt.Errorf("restricting the session helper socket: %w", err)
	}
	defer ln.Close()

	// One connection for the helper's life. A dead parent closes it and the
	// helper exits; the parent reconnects by starting a fresh job.
	conn, err := ln.Accept()
	if err != nil {
		return fmt.Errorf("accepting the session helper connection: %w", err)
	}
	return ServeHelper(ctx, conn, conn, opts)
}

// ServeHelper runs the session-helper end of the protocol: it composes a
// Session for the desktop this process is in and answers capability calls on
// the pipe until the parent closes it. The process it runs in is the delegated
// user, which is the whole point — everything a desktop refuses a privileged
// process happens here instead.
//
// Every call is handled in its own goroutine, so a dialog held open by nobody
// does not stop the notifications behind it.
func ServeHelper(ctx context.Context, r io.Reader, w io.Writer, opts Options) error {
	if opts.TokenStore == nil {
		if dir := os.Getenv(EnvHelperTokenDir); dir != "" {
			opts.TokenStore = autoapi.FileTokenStore(dir)
		}
	}
	sess, err := buildSession(ctx, opts)
	if err != nil {
		return fmt.Errorf("composing the session in the helper: %w", err)
	}
	wireInput(sess)
	sess.Inspection = newInspection()
	sess.Desktop = localDesktop{sess}
	sess.env.Backends["inspection"] = inspectionBackend
	sess.Window = enrichWindowBackend(sess.Window)
	defer sess.Close()
	return serveSession(ctx, sess, r, w)
}

// ServeHelperCommand is what a caller's hidden helper subcommand runs: it
// serves the composed session over inherited pipes, or over the unix socket
// named in the environment when the helper was started by launchd (the macOS
// case, where no pipe crosses). Every embedding binary wires its helper
// command to this rather than re-deciding the transport.
func ServeHelperCommand(ctx context.Context, opts Options) error {
	if path := os.Getenv(EnvHelperSocket); path != "" {
		err := ServeHelperSocket(ctx, path, opts)
		if err == context.Canceled {
			return nil
		}
		return err
	}
	err := ServeHelper(ctx, os.Stdin, os.Stdout, opts)
	if err == context.Canceled {
		return nil
	}
	return err
}

// serveSession answers capability calls against a session that is already
// composed. It is separate from ServeHelper so the protocol can be exercised
// against a session the caller built, rather than only against whatever desktop
// the test host happens to have.
func serveSession(ctx context.Context, sess *Session, r io.Reader, w io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	var workers sync.WaitGroup
	defer workers.Wait()
	defer cancel()
	conn := hrpc.NewConn(r, w)
	server := NewSessionServer(sess)
	for {
		req, err := conn.ReadRequest()
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		workers.Add(1)
		go func() {
			defer workers.Done()
			result, callErr := server.call(ctx, req)
			if ctx.Err() != nil {
				return
			}
			raw, marshalErr := hrpc.Marshal(result)
			if marshalErr != nil && callErr == nil {
				callErr = marshalErr
			}
			resp := hrpc.Response{ID: req.ID, Err: toWire(callErr)}
			if callErr == nil {
				resp.Result = raw
			}
			// A write failure means the parent is gone; the read loop is about
			// to see the same thing and end the process.
			_ = conn.WriteResponse(resp)
		}()
	}
}

// dispatch performs one capability call against the local session. It returns
// the value to encode, which is nil for the methods that only act.
func dispatch(ctx context.Context, sess *Session, req hrpc.Request) (any, error) {
	switch req.Cap {
	case "desktop":
		return serveDesktop(ctx, sess, req)
	case "inspection":
		return serveInspection(ctx, sess, req)
	case capKeyboard:
		return nil, serveKeyboard(ctx, sess, req)
	case capMouse:
		return serveMouse(ctx, sess, req)
	case capScreen:
		return serveScreen(ctx, sess, req)
	case capWindow:
		return serveWindow(ctx, sess, req)
	case capClipboard:
		return serveClipboard(ctx, sess, req)
	case capNotifier:
		var note autoapi.Notification
		if err := hrpc.Unmarshal(req.Args, &note); err != nil {
			return nil, err
		}
		return nil, sess.Notifier.Notify(ctx, note)
	case capDialog:
		var ask autoapi.DialogRequest
		if err := hrpc.Unmarshal(req.Args, &ask); err != nil {
			return nil, err
		}
		return sess.Dialog.Ask(ctx, ask)
	case capSpeaker:
		var speak autoapi.SpeakRequest
		if err := hrpc.Unmarshal(req.Args, &speak); err != nil {
			return nil, err
		}
		return nil, sess.Speaker.Speak(ctx, speak)
	case capMedia:
		var media autoapi.MediaRequest
		if err := hrpc.Unmarshal(req.Args, &media); err != nil {
			return nil, err
		}
		return nil, sess.Media.Control(ctx, media)
	case capPower:
		var action autoapi.PowerAction
		if err := hrpc.Unmarshal(req.Args, &action); err != nil {
			return nil, err
		}
		return nil, sess.Power.Control(ctx, action)
	case capIdle:
		var args sinceArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		now := args.Now
		if now.IsZero() {
			now = time.Now()
		}
		seconds, source, err := sess.Idle.Since(ctx, now)
		return sinceResult{Seconds: seconds, Source: source}, err
	case capOpener:
		return nil, serveOpener(ctx, sess, req)
	case capSessions:
		var args sessionActArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		if req.Method != mAct {
			return nil, unknownMethod(req)
		}
		return nil, sess.Sessions.Act(ctx, args.Target, args.Action)
	}
	return nil, fmt.Errorf("the session helper has no capability %q", req.Cap)
}

// serveKeyboard performs one keyboard call.
func serveKeyboard(ctx context.Context, sess *Session, req hrpc.Request) error {
	switch req.Method {
	case mTypeText:
		var args typeTextArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return err
		}
		return sess.Keyboard.TypeText(ctx, autoapi.RealClock{}, args.Text, args.Delay)
	case mPressKeys:
		var stroke autoapi.KeyStroke
		if err := hrpc.Unmarshal(req.Args, &stroke); err != nil {
			return err
		}
		return sess.Keyboard.PressKeys(ctx, stroke)
	}
	return unknownMethod(req)
}

// serveMouse performs one pointer call.
func serveMouse(ctx context.Context, sess *Session, req hrpc.Request) (any, error) {
	switch req.Method {
	case mMove:
		var args moveArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return nil, sess.Mouse.Move(ctx, args.X, args.Y, args.Relative)
	case mClick:
		var args clickArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return nil, sess.Mouse.Click(ctx, args.Button, args.Count)
	case mHold:
		var args holdArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return nil, sess.Mouse.Hold(ctx, args.Button, args.Down)
	case mScroll:
		var args scrollArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return nil, sess.Mouse.Scroll(ctx, args.Amount, args.Horizontal)
	case mPosition:
		x, y, err := sess.Mouse.Position(ctx)
		return positionResult{X: x, Y: y}, err
	}
	return nil, unknownMethod(req)
}

// serveScreen photographs the session and encodes the result for the wire.
func serveScreen(ctx context.Context, sess *Session, req hrpc.Request) (any, error) {
	if req.Method != mCapture {
		return nil, unknownMethod(req)
	}
	var capture autoapi.CaptureRequest
	if err := hrpc.Unmarshal(req.Args, &capture); err != nil {
		return nil, err
	}
	img, err := sess.Screen.Capture(ctx, capture)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encoding the screenshot for the parent: %w", err)
	}
	return captureResult{PNG: buf.Bytes(), Mapping: autoapi.ImageCaptureMapping(img)}, nil
}

// serveWindow performs one window call.
func serveWindow(ctx context.Context, sess *Session, req hrpc.Request) (any, error) {
	switch req.Method {
	case mList:
		return sess.Window.List(ctx)
	case mAct:
		var args windowActArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return nil, sess.Window.Act(ctx, args.Window, args.Action)
	}
	return nil, unknownMethod(req)
}

// serveClipboard performs one clipboard call. A write keeps the selection in
// this process for as long as it lives, which is exactly why the helper is one
// long-lived child rather than a process per call.
func serveClipboard(ctx context.Context, sess *Session, req hrpc.Request) (any, error) {
	switch req.Method {
	case mRead:
		var args clipboardReadArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		text, err := sess.Clipboard.Read(ctx, args.Selection)
		return textResult{Text: text}, err
	case mWrite:
		var args clipboardWriteArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
		return nil, sess.Clipboard.Write(ctx, args.Selection, args.Text)
	}
	return nil, unknownMethod(req)
}

// serveOpener performs one launch call.
func serveOpener(ctx context.Context, sess *Session, req hrpc.Request) error {
	switch req.Method {
	case mOpenURL:
		var args openURLArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return err
		}
		return sess.Opener.OpenURL(ctx, args.URL)
	case mOpenApp:
		var args openAppArgs
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return err
		}
		return sess.Opener.OpenApp(ctx, args.App, args.Args)
	}
	return unknownMethod(req)
}

// unknownMethod names what the peer asked for that this build does not have,
// which is what a version skew between parent and child looks like.
func unknownMethod(req hrpc.Request) error {
	return fmt.Errorf("the session helper has no %s.%s", req.Cap, req.Method)
}
