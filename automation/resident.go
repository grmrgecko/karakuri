package automation

import (
	"context"
	"net"
	"sync"

	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

// SessionServer shares one desktop session between local command clients.
// The owner authenticates connections and closes the session after all Serve
// calls return. Input calls are serialized across clients.
type SessionServer struct {
	session *Session
	input   chan struct{}
}

// NewSessionServer returns a server for an already composed desktop session.
func NewSessionServer(session *Session) *SessionServer {
	return &SessionServer{session: session, input: make(chan struct{}, 1)}
}

// Serve handles a local connection until it closes or ctx is cancelled.
// Disconnecting cancels pending work without closing the shared desktop session.
func (s *SessionServer) Serve(ctx context.Context, connection net.Conn) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer connection.Close()
	stop := context.AfterFunc(ctx, func() { _ = connection.Close() })
	defer stop()
	wire := hrpc.NewConn(connection, connection)
	requests := make(chan hrpc.Request)
	var reader sync.WaitGroup
	reader.Add(1)
	defer reader.Wait()
	defer cancel()
	go func() {
		defer reader.Done()
		defer cancel()
		defer close(requests)
		for {
			req, err := wire.ReadRequest()
			if err != nil {
				return
			}
			select {
			case requests <- req:
			case <-ctx.Done():
				return
			}
		}
	}()
	for req := range requests {
		result, err := s.call(ctx, req)
		raw, marshalErr := hrpc.Marshal(result)
		if err == nil {
			err = marshalErr
		}
		response := hrpc.Response{ID: req.ID, Err: toWire(err)}
		if err == nil {
			response.Result = raw
		}
		if err := wire.WriteResponse(response); err != nil {
			return err
		}
	}
	return nil
}

func (s *SessionServer) call(ctx context.Context, req hrpc.Request) (any, error) {
	if req.Cap == "describe" {
		return s.session.Describe(), nil
	}
	if req.Cap == capKeyboard || req.Cap == capMouse || (req.Cap == capWindow && req.Method == mAct) || (req.Cap == "desktop" && (req.Method == "click_control" || req.Method == "invoke_menu" || req.Method == "drag" || req.Method == "place_window")) {
		select {
		case s.input <- struct{}{}:
			defer func() { <-s.input }()
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return dispatch(ctx, s.session, req)
}

// NewRemoteSession connects capability calls to an authenticated local resident.
// Closing the returned session closes only this connection. Calls are never
// retried after a disconnect because an input event may already have executed.
func NewRemoteSession(ctx context.Context, connection net.Conn) (*Session, error) {
	h := newHelper(func(context.Context) (*hrpc.Conn, func(), error) {
		return hrpc.NewConn(connection, connection), func() { _ = connection.Close() }, nil
	}, nil)
	h.noRetry = true
	s := &Session{
		Desktop: helperDesktop{h}, Inspection: helperInspection{h}, Keyboard: helperKeyboard{h},
		Mouse: helperMouse{h}, Screen: helperScreen{h}, Window: helperWindow{h},
		Clipboard: helperClipboard{h}, Notifier: helperNotifier{h}, Dialog: helperDialog{h},
		Speaker: helperSpeaker{h}, Media: helperMedia{h}, Power: helperPower{h},
		Idle: helperIdle{h}, Opener: helperOpener{h}, Sessions: helperSessions{h},
		closers: []func() error{h.Close},
	}
	if err := h.call(ctx, "describe", "", nil, &s.env); err != nil {
		_ = s.Close()
		_ = connection.Close()
		return nil, err
	}
	return s, nil
}
