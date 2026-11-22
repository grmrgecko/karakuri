package socket

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"go.uber.org/zap"
)

// Frame is an immutable delivery with transient peer identity.
type Frame struct {
	Data      []byte
	SessionID string
	Remote    string
	Local     string
	Type      string
}

// Subscription holds a bounded queue owned by one input node.
type Subscription struct {
	driver *Driver
	frames chan Frame
	bytes  atomic.Int64
	closed atomic.Bool
}

// Next waits for a frame and discards queued frames from a retired connection.
func (s *Subscription) Next(ctx context.Context) (Frame, error) {
	select {
	case <-ctx.Done():
		return Frame{}, ctx.Err()
	case f, ok := <-s.frames:
		if !ok || s.closed.Load() {
			return Frame{}, io.EOF
		}
		s.bytes.Add(-int64(len(f.Data)))
		return f, nil
	}
}

// Close removes this subscriber without changing shared sockets.
func (s *Subscription) Close() {
	s.driver.mu.Lock()
	defer s.driver.mu.Unlock()
	if !s.closed.Swap(true) {
		delete(s.driver.subs, s)
		close(s.frames)
	}
}

type session struct {
	id            string
	tcp           net.Conn
	ws            *websocket.Conn
	remote, local string
	gate          chan struct{}
	ready         bool
}

func (s *session) close() {
	if s.tcp != nil {
		_ = s.tcp.Close()
	}
	if s.ws != nil {
		_ = s.ws.CloseNow()
	}
}

// Driver owns a connection generation and its bounded subscriptions.
type Driver struct {
	id        uuid.UUID
	kind      string
	cfg       Config
	deps      integrations.Deps
	lifecycle sync.Mutex
	started   bool
	mu        sync.Mutex
	status    integrations.Status
	sessions  map[string]*session
	subs      map[*Subscription]struct{}
	listener  net.Listener
	udp       *net.UDPConn
	server    *http.Server
	ctx       context.Context
	cancel    context.CancelFunc
	stopped   bool
	once      sync.Once
	wg        sync.WaitGroup
	udpGate   chan struct{}
}

// New builds a socket driver from decrypted settings.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	cfg, err := ParseConfig(model.Kind, model.Config)
	if err != nil {
		return nil, err
	}
	if deps.Logger == nil {
		deps.Logger = zap.NewNop()
	}
	return &Driver{id: model.ID, kind: model.Kind, cfg: cfg, deps: deps, sessions: make(map[string]*session), subs: make(map[*Subscription]struct{}), udpGate: make(chan struct{}, 1), status: integrations.Status{State: integrations.StateDisabled, Since: time.Now()}}, nil
}

// ID returns the configured connection identity.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the network protocol.
func (d *Driver) Kind() string { return d.kind }

// Status returns a synchronized lifecycle snapshot.
func (d *Driver) Status() integrations.Status { d.mu.Lock(); defer d.mu.Unlock(); return d.status }

func (d *Driver) setStatus(state string, err error) {
	st := integrations.Status{State: state, Since: time.Now()}
	if err != nil {
		st.Error = err.Error()
	}
	d.mu.Lock()
	if d.stopped && state != integrations.StateDisabled {
		d.mu.Unlock()
		return
	}
	d.status = st
	d.mu.Unlock()
	if d.deps.PublishStatus != nil {
		d.deps.PublishStatus(st)
	}
}

// Start binds listeners synchronously and supervises client reconnects.
func (d *Driver) Start(parent context.Context) error {
	d.lifecycle.Lock()
	defer d.lifecycle.Unlock()
	d.mu.Lock()
	if d.stopped || d.started {
		d.mu.Unlock()
		return fmt.Errorf("socket driver cannot be started twice or after stopping")
	}
	d.ctx, d.cancel = context.WithCancel(parent)
	d.mu.Unlock()
	var err error
	switch d.kind {
	case "tcp":
		if d.cfg.Mode == "listener" {
			err = d.startTCPListener()
		} else if d.cfg.Mode == "request" {
			d.setStatus(integrations.StateConnected, nil)
		} else {
			d.wg.Add(1)
			go d.clientLoop()
		}
	case "udp":
		err = d.startUDP()
	case "websocket":
		if d.cfg.Mode == "listener" {
			err = d.startWSListener()
		} else {
			d.wg.Add(1)
			go d.clientLoop()
		}
	}
	if err != nil {
		d.shutdown()
		d.wg.Wait()
		return err
	}
	d.mu.Lock()
	d.started = true
	d.mu.Unlock()
	d.wg.Add(1)
	go func() { defer d.wg.Done(); <-d.ctx.Done(); d.shutdown() }()
	return nil
}

// Stop closes sockets and subscriptions and waits for owned readers.
func (d *Driver) Stop() {
	d.lifecycle.Lock()
	if d.cancel != nil {
		d.cancel()
	}
	d.shutdown()
	d.lifecycle.Unlock()
	d.wg.Wait()
}

func (d *Driver) shutdown() {
	d.once.Do(func() {
		d.mu.Lock()
		d.stopped = true
		if d.cancel != nil {
			d.cancel()
		}
		for sub := range d.subs {
			sub.closed.Store(true)
			close(sub.frames)
			delete(d.subs, sub)
		}
		peers := make([]*session, 0, len(d.sessions))
		for _, s := range d.sessions {
			peers = append(peers, s)
		}
		d.sessions = make(map[string]*session)
		d.mu.Unlock()
		if d.listener != nil {
			_ = d.listener.Close()
		}
		if d.udp != nil {
			_ = d.udp.Close()
		}
		if d.server != nil {
			_ = d.server.Close()
		}
		for _, s := range peers {
			s.close()
		}
		d.setStatus(integrations.StateDisabled, nil)
	})
}

// Subscribe registers an input node without opening a new transport socket.
func (d *Driver) Subscribe() (*Subscription, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || !d.started {
		return nil, fmt.Errorf("connection is stopped")
	}
	if d.kind == "tcp" && d.cfg.Mode == "request" {
		return nil, fmt.Errorf("request-only tcp connections have no input stream")
	}
	if len(d.subs) >= 64 {
		return nil, fmt.Errorf("connection subscriber limit reached")
	}
	s := &Subscription{driver: d, frames: make(chan Frame, 32)}
	d.subs[s] = struct{}{}
	return s, nil
}

func (d *Driver) publish(f Frame) {
	d.mu.Lock()
	dropped := false
	for s := range d.subs {
		if len(s.frames) >= cap(s.frames) || s.bytes.Load()+int64(len(f.Data)) > 4<<20 {
			dropped = true
			continue
		}
		s.bytes.Add(int64(len(f.Data)))
		s.frames <- f
	}
	d.mu.Unlock()
	if dropped {
		d.deps.Logger.Error("socket input queue full; frame dropped")
	}
}

func (d *Driver) addSession(s *session) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped || len(d.sessions) >= d.cfg.MaxSessions {
		return false
	}
	s.id = uuid.NewString()
	s.gate = make(chan struct{}, 1)
	d.sessions[s.id] = s
	return true
}
func (d *Driver) removeSession(s *session) {
	d.mu.Lock()
	delete(d.sessions, s.id)
	d.mu.Unlock()
	s.close()
}

func (d *Driver) clientLoop() {
	defer d.wg.Done()
	for d.ctx.Err() == nil {
		d.setStatus(integrations.StateConnecting, nil)
		s, err := d.dialSession(d.ctx)
		if err == nil {
			if !d.addSession(s) {
				s.close()
				return
			}
			d.setStatus(integrations.StateConnected, nil)
			err = d.readSession(s)
			d.removeSession(s)
		}
		if d.ctx.Err() != nil {
			return
		}
		d.setStatus(integrations.StateError, err)
		d.deps.Logger.Error("socket client disconnected", zap.Error(err))
		timer := time.NewTimer(d.cfg.reconnectDelay)
		select {
		case <-d.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

// Target selects one peer, a validated reply session, or an explicit broadcast.
type Target struct {
	Mode         string
	ConnectionID string
	SessionID    string
	Destination  string
}

// Send writes once and never queues or replays after a transport failure.
func (d *Driver) Send(parent context.Context, target Target, data []byte, binary, closeAfter bool) error {
	d.mu.Lock()
	generation := d.ctx
	stopped := d.stopped || !d.started
	d.mu.Unlock()
	if stopped || generation == nil {
		return fmt.Errorf("connection is stopped")
	}
	ctx, cancel := context.WithTimeout(parent, d.cfg.writeTimeout)
	defer cancel()
	stopGeneration := context.AfterFunc(generation, cancel)
	defer stopGeneration()
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(data) > d.cfg.MaxBytes {
		return fmt.Errorf("outgoing frame exceeds %d bytes", d.cfg.MaxBytes)
	}
	if d.kind == "udp" {
		return d.sendUDP(ctx, target, data)
	}
	if target.Mode == "" {
		target.Mode = "peer"
	}
	d.mu.Lock()
	peers := []*session{}
	if d.stopped {
		d.mu.Unlock()
		return fmt.Errorf("connection is stopped")
	}
	switch target.Mode {
	case "reply":
		if target.ConnectionID != d.id.String() || target.SessionID == "" {
			d.mu.Unlock()
			return fmt.Errorf("reply requires a session from this connection")
		}
		if s := d.sessions[target.SessionID]; s != nil && s.ready {
			peers = append(peers, s)
		}
	case "peer":
		if d.cfg.Mode != "client" {
			d.mu.Unlock()
			return fmt.Errorf("peer target requires a client connection")
		}
		for _, s := range d.sessions {
			if s.ready {
				peers = append(peers, s)
			}
		}
	case "broadcast":
		if d.cfg.Mode != "listener" {
			d.mu.Unlock()
			return fmt.Errorf("broadcast target requires a listener connection")
		}
		for _, s := range d.sessions {
			if s.ready {
				peers = append(peers, s)
			}
		}
	default:
		d.mu.Unlock()
		return fmt.Errorf("invalid socket target")
	}
	d.mu.Unlock()
	if len(peers) == 0 {
		return fmt.Errorf("socket target is disconnected or stale")
	}
	for _, s := range peers {
		if err := d.writeSession(ctx, s, data, binary, closeAfter); err != nil {
			return err
		}
	}
	return nil
}

func (d *Driver) writeSession(parent context.Context, s *session, data []byte, binary, closeAfter bool) error {
	ctx, cancel := context.WithTimeout(parent, d.cfg.writeTimeout)
	defer cancel()
	select {
	case s.gate <- struct{}{}:
		defer func() { <-s.gate }()
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	live := !d.stopped && d.sessions[s.id] == s && s.ready
	d.mu.Unlock()
	if !live {
		return fmt.Errorf("socket session is stale")
	}
	var err error
	if s.ws != nil {
		typ := websocket.MessageText
		if binary {
			typ = websocket.MessageBinary
		}
		err = s.ws.Write(ctx, typ, data)
	} else {
		deadline, _ := ctx.Deadline()
		err = s.tcp.SetWriteDeadline(deadline)
		stop := context.AfterFunc(ctx, func() { _ = s.tcp.SetWriteDeadline(time.Now()) })
		if err == nil {
			err = writeAll(s.tcp, data)
		}
		// Waiting for a running callback avoids applying its deadline to a later write.
		if !stop() {
			s.close()
		}
	}
	if err != nil {
		s.close()
		return fmt.Errorf("socket write failed: %w", err)
	}
	if closeAfter {
		s.close()
	}
	return nil
}

func writeAll(w io.Writer, b []byte) error {
	for len(b) > 0 {
		n, e := w.Write(b)
		if e != nil {
			return e
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		b = b[n:]
	}
	return nil
}

// Decode converts wire bytes to a declared input format.
func Decode(data []byte, format string) (any, error) { return decode(data, format) }

var errFrameLimit = errors.New("socket frame exceeds configured byte limit")

// LocalAddress returns the bound listener or UDP address, including an assigned port.
func (d *Driver) LocalAddress() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.listener != nil {
		return d.listener.Addr().String()
	}
	if d.udp != nil {
		return d.udp.LocalAddr().String()
	}
	return ""
}
