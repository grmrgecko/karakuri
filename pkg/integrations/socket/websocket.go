package socket

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"go.uber.org/zap"
)

func (d *Driver) dialSession(parent context.Context) (*session, error) {
	ctx, cancel := context.WithTimeout(parent, d.cfg.dialTimeout)
	defer cancel()
	if d.kind == "tcp" {
		c, e := d.dialTCP(ctx)
		if e != nil {
			return nil, e
		}
		return &session{tcp: c, remote: c.RemoteAddr().String(), local: c.LocalAddr().String(), ready: true}, nil
	}
	var remote, local string
	dialer := &net.Dialer{Timeout: d.cfg.dialTimeout}
	transport := &http.Transport{TLSClientConfig: d.cfg.tlsConfig, Proxy: nil, DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
		c, e := dialer.DialContext(ctx, network, address)
		if e == nil {
			remote = c.RemoteAddr().String()
			local = c.LocalAddr().String()
		}
		return c, e
	}}
	defer transport.CloseIdleConnections()
	c, resp, e := websocket.Dial(ctx, d.cfg.URL, &websocket.DialOptions{HTTPClient: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, HTTPHeader: d.cfg.headers, Subprotocols: d.cfg.Subprotocols})
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if e != nil {
		return nil, fmt.Errorf("websocket handshake failed")
	}
	c.SetReadLimit(int64(d.cfg.MaxBytes))
	// The URL may contain handshake credentials, so it is never message metadata.
	return &session{ws: c, remote: remote, local: local, ready: true}, nil
}

func (d *Driver) readSession(s *session) error {
	if s.tcp != nil {
		return d.readTCP(s)
	}
	for {
		typ, b, e := s.ws.Read(d.ctx)
		if e != nil {
			return e
		}
		frameType := "text"
		if typ == websocket.MessageBinary {
			frameType = "binary"
		}
		d.publish(Frame{Data: b, SessionID: s.id, Remote: s.remote, Local: s.local, Type: frameType})
	}
}

func (d *Driver) startWSListener() error {
	ln, e := net.Listen("tcp", d.cfg.Address)
	if e != nil {
		return fmt.Errorf("websocket listen: %w", e)
	}
	ln = &limitedListener{Listener: ln, slots: make(chan struct{}, d.cfg.MaxSessions)}
	if d.cfg.TLS {
		ln = tls.NewListener(ln, d.cfg.tlsConfig)
	}
	d.mu.Lock()
	d.listener = ln
	d.mu.Unlock()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		d.mu.Lock()
		if d.stopped {
			d.mu.Unlock()
			http.Error(w, "Connection stopped", http.StatusServiceUnavailable)
			return
		}
		// Track the upgrade before hijacking removes it from http.Server.Close.
		d.wg.Add(1)
		d.mu.Unlock()
		defer d.wg.Done()
		if r.URL.Path != d.cfg.Path {
			http.NotFound(w, r)
			return
		}
		if d.cfg.Token != "" {
			want := sha256.Sum256([]byte("Bearer " + d.cfg.Token))
			got := sha256.Sum256([]byte(r.Header.Get("Authorization")))
			if subtle.ConstantTimeCompare(want[:], got[:]) != 1 {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}
		}
		c, e := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: d.cfg.Subprotocols, OriginPatterns: d.cfg.Origins})
		if e != nil {
			return
		}
		c.SetReadLimit(int64(d.cfg.MaxBytes))
		local := ""
		if a, ok := r.Context().Value(http.LocalAddrContextKey).(net.Addr); ok {
			local = a.String()
		}
		s := &session{ws: c, remote: r.RemoteAddr, local: local, ready: true}
		d.mu.Lock()
		if d.stopped || len(d.sessions) >= d.cfg.MaxSessions {
			d.mu.Unlock()
			s.close()
			return
		}
		s.id = uuid.NewString()
		s.gate = make(chan struct{}, 1)
		d.sessions[s.id] = s
		d.mu.Unlock()
		defer d.removeSession(s)
		if e := d.readSession(s); e != nil && d.ctx.Err() == nil {
			code := websocket.CloseStatus(e)
			if code != websocket.StatusNormalClosure && code != websocket.StatusGoingAway {
				d.deps.Logger.Error("websocket peer read failed", zap.Error(e))
			}
		}
	})
	d.server = &http.Server{Handler: handler, ReadHeaderTimeout: d.cfg.dialTimeout, ReadTimeout: d.cfg.dialTimeout, WriteTimeout: d.cfg.dialTimeout, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 32 << 10, BaseContext: func(net.Listener) context.Context { return d.ctx }}
	d.setStatus(integrations.StateConnected, nil)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		if e := d.server.Serve(ln); e != nil && e != http.ErrServerClosed && d.ctx.Err() == nil {
			d.setStatus(integrations.StateError, e)
		}
	}()
	return nil
}

// limitedListener counts pending HTTP/TLS handshakes as well as upgraded sockets.
type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (l *limitedListener) Accept() (net.Conn, error) {
	for {
		c, e := l.Listener.Accept()
		if e != nil {
			return nil, e
		}
		select {
		case l.slots <- struct{}{}:
			return &limitedConn{Conn: c, release: func() { <-l.slots }}, nil
		default:
			_ = c.Close()
		}
	}
}

type limitedConn struct {
	net.Conn
	once    sync.Once
	release func()
}

func (c *limitedConn) Close() error { e := c.Conn.Close(); c.once.Do(c.release); return e }
