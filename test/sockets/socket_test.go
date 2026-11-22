//go:build karakuri_integration

package sockets_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/socket"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/ipv4"
)

func driver(t *testing.T, id uuid.UUID, kind string, cfg map[string]any) *socket.Driver {
	t.Helper()
	raw, e := json.Marshal(cfg)
	require.NoError(t, e)
	value, e := socket.New(integrations.ConnModel{ID: id, Kind: kind, Config: raw}, integrations.Deps{})
	require.NoError(t, e)
	d := value.(*socket.Driver)
	require.NoError(t, d.Start(t.Context()))
	t.Cleanup(d.Stop)
	return d
}
func subscribe(t *testing.T, d *socket.Driver) *socket.Subscription {
	t.Helper()
	s, e := d.Subscribe()
	require.NoError(t, e)
	t.Cleanup(s.Close)
	return s
}
func next(t *testing.T, s *socket.Subscription) socket.Frame {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	f, e := s.Next(ctx)
	require.NoError(t, e)
	return f
}
func dialTCP(t *testing.T, address string) net.Conn {
	t.Helper()
	c, e := net.DialTimeout("tcp", address, time.Second)
	require.NoError(t, e)
	t.Cleanup(func() { _ = c.Close() })
	require.NoError(t, c.SetDeadline(time.Now().Add(3*time.Second)))
	return c
}
func readBytes(t *testing.T, c net.Conn, n int) string {
	t.Helper()
	b := make([]byte, n)
	_, e := io.ReadFull(c, b)
	require.NoError(t, e)
	return string(b)
}
func runNode(t *testing.T, drivers map[string]*socket.Driver, typ string, cfg map[string]any, payload any, meta map[string]any) *engine.NodeTestResult {
	t.Helper()
	reg := engine.NewRegistry()
	core.RegisterAll(reg)
	eng := engine.New(engine.Options{Registry: reg, ConnResolver: func(id string) (any, error) {
		d := drivers[id]
		if d == nil {
			return nil, fmt.Errorf("missing connection")
		}
		return d, nil
	}})
	t.Cleanup(eng.Stop)
	raw, e := json.Marshal(cfg)
	require.NoError(t, e)
	result, e := eng.TestNode(t.Context(), engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "socket", Type: typ, Config: raw, Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 1}}}}, StartNodeID: "socket", SeedInputs: map[string]*engine.Msg{"in": {Payload: payload, Topic: "kept", Meta: meta, Group: []engine.GroupFrame{{ID: "group", Count: 1}}}}})
	require.NoError(t, e)
	return result
}

// TestTCPFramesAndTargets pins TCP byte-stream boundaries and prevents replies
// from crossing peers or connection generations after a listener is replaced.
func TestTCPFramesAndTargets(t *testing.T) {
	for _, tc := range []struct {
		name, mode, wire string
		length           int
		want             []string
	}{
		{"delimiter survives fragmented and coalesced writes", "delimiter", "one\r\ntwo\r\n", 0, []string{"one", "two"}},
		{"fixed length counts bytes", "fixed", "abcdef", 3, []string{"abc", "def"}},
		{"close terminates a whole stream", "close", "whole", 0, []string{"whole"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := driver(t, uuid.New(), "tcp", map[string]any{"mode": "listener", "address": "127.0.0.1:0", "framing": tc.mode, "delimiter": `\r\n`, "fixed_length": tc.length, "max_bytes": 32})
			sub := subscribe(t, d)
			peer := dialTCP(t, d.LocalAddress())
			_, e := peer.Write([]byte(tc.wire[:2]))
			require.NoError(t, e)
			_, e = peer.Write([]byte(tc.wire[2:]))
			require.NoError(t, e)
			if tc.mode == "close" {
				require.NoError(t, peer.(*net.TCPConn).CloseWrite())
			}
			for _, want := range tc.want {
				assert.Equal(t, want, string(next(t, sub).Data))
			}
		})
	}
	id := uuid.New()
	d := driver(t, id, "tcp", map[string]any{"mode": "listener", "address": "127.0.0.1:0", "max_bytes": 8})
	sub := subscribe(t, d)
	a := dialTCP(t, d.LocalAddress())
	b := dialTCP(t, d.LocalAddress())
	_, e := a.Write([]byte("a\n"))
	require.NoError(t, e)
	fa := next(t, sub)
	_, e = b.Write([]byte("b\n"))
	require.NoError(t, e)
	fb := next(t, sub)
	assert.NotEqual(t, fa.SessionID, fb.SessionID)
	drivers := map[string]*socket.Driver{id.String(): d}
	result := runNode(t, drivers, "tcp-out", map[string]any{"connection": id.String(), "target": "reply"}, "reply", map[string]any{"socket_connection_id": id.String(), "socket_session_id": fa.SessionID})
	assert.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
	assert.Equal(t, "reply", readBytes(t, a, 5))
	require.NoError(t, d.Send(t.Context(), socket.Target{Mode: "broadcast"}, []byte("all"), false, false))
	assert.Equal(t, "all", readBytes(t, a, 3))
	assert.Equal(t, "all", readBytes(t, b, 3), "reply to the first peer must not have reached the second")
	address := d.LocalAddress()
	d.Stop()
	_, e = sub.Next(t.Context())
	assert.ErrorIs(t, e, io.EOF)
	replacement := driver(t, id, "tcp", map[string]any{"mode": "listener", "address": address})
	c := dialTCP(t, replacement.LocalAddress())
	replacementSub := subscribe(t, replacement)
	_, e = c.Write([]byte("new\n"))
	require.NoError(t, e)
	fc := next(t, replacementSub)
	assert.NotEqual(t, fa.SessionID, fc.SessionID)
	result = runNode(t, map[string]*socket.Driver{id.String(): replacement}, "tcp-out", map[string]any{"connection": id.String(), "target": "reply"}, "old", map[string]any{"socket_connection_id": id.String(), "socket_session_id": fa.SessionID})
	assert.Equal(t, engine.RunStatusFailed, result.Status)
	require.NoError(t, replacement.Send(t.Context(), socket.Target{Mode: "reply", ConnectionID: id.String(), SessionID: fc.SessionID}, []byte("fresh"), false, false))
	assert.Equal(t, "fresh", readBytes(t, c, 5))
}

// TestTCPRequests pins response completion, partial EOF rejection, TLS verification
// and absolute deadlines against real peers; authored retries never repeat a send.
func TestTCPRequests(t *testing.T) {
	for _, tc := range []struct {
		name, mode, wire string
		length, limit    int
		wait             time.Duration
		want             string
		ok, secure       bool
	}{
		{name: "delimiter", mode: "delimiter", wire: "ok\r\nignored", want: "ok", ok: true},
		{name: "fixed", mode: "fixed", wire: "abcdextra", length: 4, want: "abcd", ok: true},
		{name: "close", mode: "close", wire: "closed", want: "closed", ok: true},
		{name: "idle", mode: "idle", wire: "idle", wait: 200 * time.Millisecond, want: "idle", ok: true},
		{name: "partial delimiter", mode: "delimiter", wire: "partial"},
		{name: "partial fixed", mode: "fixed", wire: "abc", length: 4},
		{name: "oversized", mode: "close", wire: "12345", limit: 4},
		{name: "overall deadline", mode: "idle", wait: 300 * time.Millisecond},
		{name: "verified TLS", mode: "close", wire: "secure", want: "secure", ok: true, secure: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, e := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, e)
			t.Cleanup(func() { _ = ln.Close() })
			cfg := map[string]any{"mode": "request", "address": ln.Addr().String()}
			if tc.secure {
				certServer := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
				pair := certServer.TLS.Certificates[0]
				certServer.Close()
				cfg["tls"] = true
				cfg["ca"] = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}))
				ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12})
			}
			var calls atomic.Int32
			var peers sync.WaitGroup
			done := make(chan struct{})
			go func() {
				defer close(done)
				for {
					c, e := ln.Accept()
					if e != nil {
						return
					}
					calls.Add(1)
					peers.Add(1)
					go func() {
						defer peers.Done()
						defer c.Close()
						_ = c.SetDeadline(time.Now().Add(time.Second))
						_, _ = bufio.NewReader(c).ReadString('\n')
						_, _ = c.Write([]byte(tc.wire))
						if tc.wait > 0 {
							time.Sleep(tc.wait)
						}
					}()
				}
			}()
			id := uuid.New()
			d := driver(t, id, "tcp", cfg)
			timeout := "1s"
			if tc.name == "overall deadline" {
				timeout = "80ms"
			}
			limit := tc.limit
			if limit == 0 {
				limit = 65536
			}
			result := runNode(t, map[string]*socket.Driver{id.String(): d}, "tcp-request", map[string]any{"connection": id.String(), "append_delimiter": `\n`, "response_mode": tc.mode, "delimiter": `\r\n`, "length": tc.length, "idle": "40ms", "timeout": timeout, "max_bytes": limit}, "request", map[string]any{"request_id": "kept"})
			if tc.ok {
				require.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
				require.Len(t, result.Outputs["out"], 1)
				var msg engine.Msg
				require.NoError(t, json.Unmarshal(result.Outputs["out"][0], &msg))
				assert.Equal(t, tc.want, msg.Payload)
				assert.Equal(t, "kept", msg.Topic)
				assert.Equal(t, "kept", msg.Meta["request_id"])
				assert.Equal(t, "group", msg.Group[0].ID)
			} else {
				assert.Equal(t, engine.RunStatusFailed, result.Status)
			}
			assert.Equal(t, int32(1), calls.Load(), "a failed socket exchange must not replay despite the authored retry policy")
			_ = ln.Close()
			<-done
			peers.Wait()
		})
	}
}

// TestConcurrentTCPRequests pins private request sockets and cancellation, so a
// slow or canceled exchange cannot consume another execution's response.
func TestConcurrentTCPRequests(t *testing.T) {
	ln, e := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, e)
	t.Cleanup(func() { _ = ln.Close() })
	closed := make(chan struct{}, 2)
	var peers sync.WaitGroup
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			peers.Add(1)
			go func() {
				defer peers.Done()
				defer c.Close()
				_ = c.SetDeadline(time.Now().Add(time.Second))
				line, e := bufio.NewReader(c).ReadString('\n')
				if e != nil {
					return
				}
				if line == "cancel\n" {
					_, _ = io.Copy(io.Discard, c)
					closed <- struct{}{}
					return
				}
				_, _ = c.Write([]byte(line))
			}()
		}
	}()
	d := driver(t, uuid.New(), "tcp", map[string]any{"mode": "request", "address": ln.Addr().String()})
	frame := socket.Framing{Mode: "delimiter", Delimiter: `\n`, MaxBytes: 32}
	var wg sync.WaitGroup
	for _, s := range []string{"first", "second"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, e := d.Request(t.Context(), []byte(s+"\n"), frame, time.Second, false)
			assert.NoError(t, e)
			assert.Equal(t, s, string(f.Data))
		}()
	}
	wg.Wait()
	ctx, cancel := context.WithTimeout(t.Context(), 40*time.Millisecond)
	defer cancel()
	_, e = d.Request(ctx, []byte("cancel\n"), frame, time.Second, false)
	assert.Error(t, e)
	select {
	case <-closed:
	case <-time.After(time.Second):
		require.FailNow(t, "cancellation did not close the private request socket")
	}
	_ = ln.Close()
	<-done
	peers.Wait()
}

// TestUDPDatagrams pins empty and binary datagrams, source-port replies and IPv6
// against kernel sockets; the output action must keep one payload per datagram.
func TestUDPDatagrams(t *testing.T) {
	for _, tc := range []struct{ family, address string }{{"udp4", "127.0.0.1:0"}, {"udp6", "[::1]:0"}} {
		t.Run(tc.family, func(t *testing.T) {
			address, e := net.ResolveUDPAddr(tc.family, tc.address)
			require.NoError(t, e)
			peer, e := net.ListenUDP(tc.family, address)
			if e != nil && tc.family == "udp6" {
				t.Skip("IPv6 loopback is unavailable")
			}
			require.NoError(t, e)
			t.Cleanup(func() { _ = peer.Close() })
			require.NoError(t, peer.SetDeadline(time.Now().Add(3*time.Second)))
			id := uuid.New()
			d := driver(t, id, "udp", map[string]any{"family": tc.family, "address": tc.address, "max_bytes": 32})
			sub := subscribe(t, d)
			target, e := net.ResolveUDPAddr(tc.family, d.LocalAddress())
			require.NoError(t, e)
			for _, data := range [][]byte{{}, {0, 1, 255}} {
				_, e = peer.WriteToUDP(data, target)
				require.NoError(t, e)
				f := next(t, sub)
				assert.Equal(t, data, append([]byte{}, f.Data...))
				result := runNode(t, map[string]*socket.Driver{id.String(): d}, "udp-out", map[string]any{"connection": id.String(), "target": "reply", "format": "binary"}, data, map[string]any{"socket_connection_id": id.String(), "remote_address": f.Remote})
				assert.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
				b := make([]byte, 64)
				n, from, e := peer.ReadFromUDP(b)
				require.NoError(t, e)
				assert.Equal(t, data, append([]byte{}, b[:n]...))
				assert.Equal(t, target.Port, from.Port)
			}
			_, e = peer.WriteToUDP(make([]byte, 33), target)
			require.NoError(t, e)
			_, e = peer.WriteToUDP([]byte("next"), target)
			require.NoError(t, e)
			assert.Equal(t, "next", string(next(t, sub).Data), "an oversized datagram must be dropped without merging it with the next one")
		})
	}
}

// TestWebSocketPolicyAndSessions pins native binary frames, bearer/origin/path
// enforcement, client headers, targeted replies and cleanup of upgraded sockets.
func TestWebSocketPolicyAndSessions(t *testing.T) {
	id := uuid.New()
	listener := driver(t, id, "websocket", map[string]any{"mode": "listener", "address": "127.0.0.1:0", "path": "/device", "token": "fixture", "origins": []string{"allowed.invalid"}, "subprotocols": []string{"device"}, "max_bytes": 32})
	sub := subscribe(t, listener)
	url := "ws://" + listener.LocalAddress() + "/device"
	for _, tc := range []struct {
		name, path, token, origin string
		ok                        bool
	}{
		{"missing token", "/device", "", "http://allowed.invalid", false},
		{"wrong path", "/other", "fixture", "http://allowed.invalid", false},
		{"foreign origin", "/device", "fixture", "http://denied.invalid", false},
		{"allowed browser", "/device", "fixture", "http://allowed.invalid", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{"Authorization": []string{"Bearer " + tc.token}, "Origin": []string{tc.origin}}
			c, _, e := websocket.Dial(t.Context(), "ws://"+listener.LocalAddress()+tc.path, &websocket.DialOptions{HTTPHeader: h, Subprotocols: []string{"device"}})
			if !tc.ok {
				assert.Error(t, e)
				return
			}
			require.NoError(t, e)
			defer c.CloseNow()
			assert.Equal(t, "device", c.Subprotocol())
			require.NoError(t, c.Write(t.Context(), websocket.MessageBinary, []byte{0, 255}))
			f := next(t, sub)
			assert.Equal(t, "binary", f.Type)
			assert.Equal(t, []byte{0, 255}, f.Data)
			result := runNode(t, map[string]*socket.Driver{id.String(): listener}, "websocket-out", map[string]any{"connection": id.String(), "target": "reply", "format": "binary"}, []byte{7, 8}, map[string]any{"socket_connection_id": id.String(), "socket_session_id": f.SessionID})
			assert.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			typ, b, e := c.Read(ctx)
			require.NoError(t, e)
			assert.Equal(t, websocket.MessageBinary, typ)
			assert.Equal(t, []byte{7, 8}, b)
		})
	}
	client := driver(t, uuid.New(), "websocket", map[string]any{"mode": "client", "url": url, "headers_json": `{"Authorization":"Bearer fixture"}`, "subprotocols": []string{"device"}, "max_bytes": 32})
	clientSub := subscribe(t, client)
	require.Eventually(t, func() bool { return client.Status().State == integrations.StateConnected }, time.Second, time.Millisecond)
	require.NoError(t, client.Send(t.Context(), socket.Target{Mode: "peer"}, []byte("client"), false, false))
	f := next(t, sub)
	assert.Equal(t, "client", string(f.Data))
	require.NoError(t, listener.Send(t.Context(), socket.Target{Mode: "reply", ConnectionID: id.String(), SessionID: f.SessionID}, []byte("server"), false, false))
	assert.Equal(t, "server", string(next(t, clientSub).Data))
	listener.Stop()
	_, e := sub.Next(t.Context())
	assert.ErrorIs(t, e, io.EOF)
	require.Eventually(t, func() bool { return client.Status().State != integrations.StateConnected }, time.Second, time.Millisecond)
}

// TestWebSocketTLS pins custom CA verification over a real HTTPS upgrade.
func TestWebSocketTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, e := websocket.Accept(w, r, nil)
		if e != nil {
			return
		}
		defer c.CloseNow()
		_, b, e := c.Read(r.Context())
		if e == nil {
			_ = c.Write(r.Context(), websocket.MessageText, b)
		}
	}))
	t.Cleanup(server.Close)
	cert := server.TLS.Certificates[0].Certificate[0]
	_, e := x509.ParseCertificate(cert)
	require.NoError(t, e)
	d := driver(t, uuid.New(), "websocket", map[string]any{"url": "wss" + strings.TrimPrefix(server.URL, "https"), "ca": string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert}))})
	sub := subscribe(t, d)
	require.Eventually(t, func() bool { return d.Status().State == integrations.StateConnected }, time.Second, time.Millisecond)
	require.NoError(t, d.Send(t.Context(), socket.Target{Mode: "peer"}, []byte("tls"), false, false))
	assert.Equal(t, "tls", string(next(t, sub).Data))
}

// TestUDPMulticastAndBroadcast pins loopback multicast delivery and explicit
// IPv4 broadcast permission against the kernel rather than configuration flags.
func TestUDPMulticastAndBroadcast(t *testing.T) {
	interfaces, e := net.Interfaces()
	require.NoError(t, e)
	var iface *net.Interface
	for i := range interfaces {
		if interfaces[i].Flags&net.FlagLoopback != 0 {
			iface = &interfaces[i]
			break
		}
	}
	if iface == nil {
		t.Skip("no loopback interface")
	}
	group := "239.255.42.99"
	receiver := driver(t, uuid.New(), "udp", map[string]any{"address": "0.0.0.0:0", "multicast_group": group, "interface": iface.Name})
	sub := subscribe(t, receiver)
	_, port, e := net.SplitHostPort(receiver.LocalAddress())
	require.NoError(t, e)
	sender := driver(t, uuid.New(), "udp", map[string]any{"address": "127.0.0.1:0", "interface": iface.Name, "ttl": 3, "loopback": true})
	require.NoError(t, sender.Send(t.Context(), socket.Target{Destination: net.JoinHostPort(group, port)}, []byte("group"), false, false))
	assert.Equal(t, "group", string(next(t, sub).Data))

	wirePeer, e := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero})
	require.NoError(t, e)
	t.Cleanup(func() { _ = wirePeer.Close() })
	packet := ipv4.NewPacketConn(wirePeer)
	require.NoError(t, packet.JoinGroup(iface, &net.UDPAddr{IP: net.ParseIP(group)}))
	require.NoError(t, packet.SetControlMessage(ipv4.FlagTTL, true))
	require.NoError(t, wirePeer.SetReadDeadline(time.Now().Add(time.Second)))
	_, wirePort, e := net.SplitHostPort(wirePeer.LocalAddr().String())
	require.NoError(t, e)
	require.NoError(t, sender.Send(t.Context(), socket.Target{Destination: net.JoinHostPort(group, wirePort)}, []byte("ttl"), false, false))
	bytes := make([]byte, 32)
	n, control, _, e := packet.ReadFrom(bytes)
	require.NoError(t, e)
	require.NotNil(t, control)
	assert.Equal(t, "ttl", string(bytes[:n]))
	assert.Equal(t, 3, control.TTL, "the multicast TTL must reach the kernel packet")
	denied := sender.Send(t.Context(), socket.Target{Destination: net.JoinHostPort("127.255.255.255", port)}, []byte("denied"), false, false)
	assert.Error(t, denied, "broadcast must require the socket permission")
	broadcast := driver(t, uuid.New(), "udp", map[string]any{"address": "127.0.0.1:0", "broadcast": true})
	require.NoError(t, broadcast.Send(t.Context(), socket.Target{Destination: net.JoinHostPort("127.255.255.255", port)}, []byte("broadcast"), false, false))
	assert.Equal(t, "broadcast", string(next(t, sub).Data))
}
