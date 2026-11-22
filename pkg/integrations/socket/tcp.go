package socket

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations"
	"go.uber.org/zap"
)

func (d *Driver) dialTCP(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: d.cfg.dialTimeout}
	if d.cfg.TLS {
		return (&tls.Dialer{NetDialer: dialer, Config: d.cfg.tlsConfig}).DialContext(ctx, "tcp", d.cfg.Address)
	}
	return dialer.DialContext(ctx, "tcp", d.cfg.Address)
}

func (d *Driver) startTCPListener() error {
	ln, e := net.Listen("tcp", d.cfg.Address)
	if e != nil {
		return fmt.Errorf("tcp listen: %w", e)
	}
	d.mu.Lock()
	d.listener = ln
	d.mu.Unlock()
	d.setStatus(integrations.StateConnected, nil)
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		var backoff time.Duration
		for {
			c, e := ln.Accept()
			if e != nil {
				if d.ctx.Err() != nil || errors.Is(e, net.ErrClosed) {
					return
				}
				// Transient failures such as descriptor exhaustion must not stop the
				// listener for good; retry with the same capped backoff as net/http.
				if backoff == 0 {
					backoff = 5 * time.Millisecond
				} else {
					backoff = min(2*backoff, time.Second)
				}
				d.deps.Logger.Error("tcp accept failed", zap.Error(e), zap.Duration("retry_in", backoff))
				select {
				case <-d.ctx.Done():
					return
				case <-time.After(backoff):
				}
				continue
			}
			backoff = 0
			s := &session{tcp: c, remote: c.RemoteAddr().String(), local: c.LocalAddr().String(), ready: !d.cfg.TLS}
			if d.cfg.TLS {
				s.tcp = tls.Server(c, d.cfg.tlsConfig)
			}
			if !d.addSession(s) {
				s.close()
				continue
			}
			d.wg.Add(1)
			go func() {
				defer d.wg.Done()
				defer d.removeSession(s)
				if d.cfg.TLS {
					secure := s.tcp.(*tls.Conn)
					ctx, cancel := context.WithTimeout(d.ctx, d.cfg.dialTimeout)
					e := secure.HandshakeContext(ctx)
					cancel()
					if e != nil {
						d.deps.Logger.Error("tcp tls handshake failed", zap.Error(e))
						return
					}
					// Handshakes count toward the session limit but cannot receive sends.
					d.mu.Lock()
					s.ready = true
					d.mu.Unlock()
				}
				if e := d.readSession(s); e != nil && d.ctx.Err() == nil {
					if !errors.Is(e, io.EOF) && !errors.Is(e, net.ErrClosed) {
						d.deps.Logger.Error("tcp peer read failed", zap.Error(e))
					}
				}
			}()
		}
	}()
	return nil
}

func (d *Driver) readTCP(s *session) error {
	f := Framing{Mode: d.cfg.Framing, Delimiter: d.cfg.Delimiter, IncludeDelimiter: d.cfg.IncludeDelimiter, Length: d.cfg.FixedLength, MaxBytes: d.cfg.MaxBytes}
	reader := bufio.NewReader(s.tcp)
	for {
		b, e := readFrame(reader, f)
		if e != nil {
			return e
		}
		d.publish(Frame{Data: b, SessionID: s.id, Remote: s.remote, Local: s.local, Type: "binary"})
		if f.Mode == "close" {
			return io.EOF
		}
	}
}

// Request opens a private socket and includes every operation in one deadline.
func (d *Driver) Request(parent context.Context, data []byte, f Framing, timeout time.Duration, closeWrite bool) (Frame, error) {
	if d.kind != "tcp" || d.cfg.Mode == "listener" {
		return Frame{}, fmt.Errorf("tcp Request requires a client or request-only tcp connection")
	}
	if timeout <= 0 || timeout > 120*time.Second {
		return Frame{}, fmt.Errorf("request timeout must be positive and at most 120s")
	}
	if f.MaxBytes <= 0 || f.MaxBytes > d.cfg.MaxBytes {
		f.MaxBytes = d.cfg.MaxBytes
	}
	if len(data) > d.cfg.MaxBytes {
		return Frame{}, errFrameLimit
	}
	if err := f.Validate(true); err != nil {
		return Frame{}, err
	}
	d.mu.Lock()
	stopped := d.stopped
	generation := d.ctx
	d.mu.Unlock()
	if stopped || generation == nil {
		return Frame{}, fmt.Errorf("connection is stopped")
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	stopGeneration := context.AfterFunc(generation, cancel)
	defer stopGeneration()
	c, e := d.dialTCP(ctx)
	if e != nil {
		return Frame{}, e
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { _ = c.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if e = c.SetDeadline(deadline); e != nil {
		return Frame{}, e
	}
	if e = writeAll(c, data); e != nil {
		return Frame{}, fmt.Errorf("tcp request write: %w", e)
	}
	if closeWrite {
		switch conn := c.(type) {
		case *net.TCPConn:
			e = conn.CloseWrite()
		case *tls.Conn:
			e = conn.CloseWrite()
		default:
			e = fmt.Errorf("transport cannot close its write side")
		}
		if e != nil {
			return Frame{}, e
		}
	}
	var response []byte
	if f.Mode == "idle" {
		response, e = readIdle(c, f, deadline)
	} else {
		response, e = readFrame(bufio.NewReader(c), f)
	}
	if e != nil {
		return Frame{}, fmt.Errorf("tcp response: %w", e)
	}
	if ctx.Err() != nil {
		return Frame{}, ctx.Err()
	}
	return Frame{Data: response, Remote: c.RemoteAddr().String(), Local: c.LocalAddr().String(), Type: "binary"}, nil
}

func readIdle(c net.Conn, f Framing, absolute time.Time) ([]byte, error) {
	data := make([]byte, 0, 256)
	buf := make([]byte, 4096)
	for {
		deadline := absolute
		if len(data) > 0 {
			if next := time.Now().Add(f.Idle); next.Before(deadline) {
				deadline = next
			}
		}
		if e := c.SetReadDeadline(deadline); e != nil {
			return nil, e
		}
		n, e := c.Read(buf)
		if len(data)+n > f.MaxBytes {
			return nil, errFrameLimit
		}
		data = append(data, buf[:n]...)
		if e != nil {
			if e == io.EOF {
				return data, nil
			}
			if timeout, ok := e.(net.Error); ok && timeout.Timeout() && len(data) > 0 && deadline.Before(absolute) && time.Now().Before(absolute) {
				return data, nil
			}
			return nil, e
		}
	}
}
