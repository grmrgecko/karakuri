// Package esphometest runs a simulated ESPHome node that speaks the native
// API, plaintext or Noise-encrypted, for tests of the ESPHome driver.
package esphometest

import (
	"bufio"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/flynn/noise"
	"github.com/mycontroller-org/esphome_api/pkg/api"
	"github.com/mycontroller-org/esphome_api/pkg/connection"
	"google.golang.org/protobuf/proto"
)

// Node is a simulated ESPHome node. Fill the exported fields, then Start.
type Node struct {
	Name     string
	MAC      string
	Version  string
	Board    string
	Key      string // base64 Noise PSK; empty for plaintext
	Password string // legacy password; empty for none

	// Entities are ListEntities*Response messages, in listing order.
	Entities []proto.Message
	// Services are the node's user-defined actions.
	Services []*api.ListEntitiesServicesResponse
	// OnCommand turns a command into the state messages the node reports
	// back, as a real node does after acting. Nil reports nothing.
	OnCommand func(cmd proto.Message) []proto.Message

	// Commands receives every command and action request the node gets.
	Commands chan proto.Message

	ln     net.Listener
	mu     sync.Mutex
	states map[uint32]proto.Message
	conns  map[*session]bool
	silent bool
}

// session is one client connection.
type session struct {
	conn       net.Conn
	write      func(proto.Message) error
	read       func() (proto.Message, error)
	subscribed bool
	mu         sync.Mutex
}

// Start listens on a free local port and returns the address.
func (n *Node) Start() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	n.ln = ln
	n.Commands = make(chan proto.Message, 64)
	n.mu.Lock()
	if n.states == nil {
		n.states = map[uint32]proto.Message{}
	}
	n.conns = map[*session]bool{}
	n.mu.Unlock()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go n.serve(conn)
		}
	}()
	return ln.Addr().String(), nil
}

// Close stops listening and drops every client.
func (n *Node) Close() {
	if n.ln != nil {
		_ = n.ln.Close()
	}
	n.Drop()
}

// Drop closes every client connection, as a node that reboots does.
func (n *Node) Drop() {
	n.mu.Lock()
	conns := n.conns
	n.conns = map[*session]bool{}
	n.mu.Unlock()
	for s := range conns {
		_ = s.conn.Close()
	}
}

// SetSilent makes the node stop answering anything, as a node that hangs
// with its socket still open does.
func (n *Node) SetSilent(silent bool) {
	n.mu.Lock()
	n.silent = silent
	n.mu.Unlock()
}

// SetState stores a state and pushes it to subscribed clients.
func (n *Node) SetState(state proto.Message) {
	key := stateKey(state)
	n.mu.Lock()
	if n.states == nil {
		n.states = map[uint32]proto.Message{}
	}
	n.states[key] = state
	conns := make([]*session, 0, len(n.conns))
	for s := range n.conns {
		conns = append(conns, s)
	}
	n.mu.Unlock()
	for _, s := range conns {
		s.mu.Lock()
		subscribed := s.subscribed
		s.mu.Unlock()
		if subscribed {
			_ = s.write(state)
		}
	}
}

// Send pushes a message, such as a Home Assistant action call, to every
// client.
func (n *Node) Send(msg proto.Message) {
	n.mu.Lock()
	conns := make([]*session, 0, len(n.conns))
	for s := range n.conns {
		conns = append(conns, s)
	}
	n.mu.Unlock()
	for _, s := range conns {
		_ = s.write(msg)
	}
}

// serve runs one client connection.
func (n *Node) serve(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	s := &session{conn: conn}
	if n.Key != "" {
		if err := n.handshake(s, reader); err != nil {
			return
		}
	} else {
		plain, _ := connection.NewPlaintextConnection(conn, 5*time.Second)
		s.write = plain.Write
		s.read = func() (proto.Message, error) { return plain.Read(reader) }
	}
	n.mu.Lock()
	n.conns[s] = true
	n.mu.Unlock()
	defer func() {
		n.mu.Lock()
		delete(n.conns, s)
		n.mu.Unlock()
	}()

	for {
		msg, err := s.read()
		if err != nil {
			return
		}
		n.mu.Lock()
		silent := n.silent
		n.mu.Unlock()
		if silent {
			continue
		}
		if !n.handle(s, msg) {
			return
		}
	}
}

// handle answers one request; false ends the session.
func (n *Node) handle(s *session, msg proto.Message) bool {
	switch m := msg.(type) {
	case *api.HelloRequest:
		_ = s.write(&api.HelloResponse{ApiVersionMajor: 1, ApiVersionMinor: 14, ServerInfo: "esphometest", Name: n.Name})
	case *api.AuthenticationRequest: //nolint:staticcheck // SA1019: legacy password auth
		_ = s.write(&api.AuthenticationResponse{InvalidPassword: n.Password != "" && m.Password != n.Password}) //nolint:staticcheck // SA1019
	case *api.DeviceInfoRequest:
		_ = s.write(&api.DeviceInfoResponse{Name: n.Name, MacAddress: n.MAC, EsphomeVersion: n.Version, Model: n.Board})
	case *api.ListEntitiesRequest:
		for _, e := range n.Entities {
			_ = s.write(e)
		}
		for _, svc := range n.Services {
			_ = s.write(svc)
		}
		_ = s.write(&api.ListEntitiesDoneResponse{})
	case *api.SubscribeStatesRequest:
		s.mu.Lock()
		s.subscribed = true
		s.mu.Unlock()
		n.mu.Lock()
		states := make([]proto.Message, 0, len(n.states))
		for _, st := range n.states {
			states = append(states, st)
		}
		n.mu.Unlock()
		for _, st := range states {
			_ = s.write(st)
		}
	case *api.SubscribeHomeassistantServicesRequest:
	case *api.PingRequest:
		_ = s.write(&api.PingResponse{})
	case *api.DisconnectRequest:
		_ = s.write(&api.DisconnectResponse{})
		return false
	default:
		select {
		case n.Commands <- msg:
		default:
		}
		if n.OnCommand != nil {
			for _, st := range n.OnCommand(msg) {
				n.SetState(st)
			}
		}
	}
	return true
}

// handshake runs the Noise NNpsk0 responder side, the way an ESPHome node
// does: a server hello answering the client's empty frame, then the
// handshake message.
func (n *Node) handshake(s *session, reader *bufio.Reader) error {
	psk, err := base64.StdEncoding.DecodeString(n.Key)
	if err != nil {
		return err
	}
	readFrame := func() ([]byte, error) {
		header := make([]byte, 3)
		if _, err := io.ReadFull(reader, header); err != nil {
			return nil, err
		}
		if header[0] != 0x01 {
			return nil, errors.New("not an encrypted frame")
		}
		data := make([]byte, binary.BigEndian.Uint16(header[1:]))
		_, err := io.ReadFull(reader, data)
		return data, err
	}
	var writeMu sync.Mutex
	writeFrame := func(data []byte) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		frame := append([]byte{0x01, byte(len(data) >> 8), byte(len(data))}, data...)
		_, err := s.conn.Write(frame)
		return err
	}

	if _, err := readFrame(); err != nil {
		return err
	}
	hello := append([]byte{0x01}, []byte(n.Name)...)
	hello = append(hello, 0x00)
	hello = append(hello, []byte(n.MAC)...)
	hello = append(hello, 0x00)
	if err := writeFrame(hello); err != nil {
		return err
	}

	hs, err := noise.NewHandshakeState(noise.Config{
		Pattern:               noise.HandshakeNN,
		CipherSuite:           noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashSHA256),
		Initiator:             false,
		Prologue:              []byte("NoiseAPIInit\x00\x00"),
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
	})
	if err != nil {
		return err
	}
	frame, err := readFrame()
	if err != nil {
		return err
	}
	if len(frame) == 0 || frame[0] != 0x00 {
		return errors.New("bad handshake frame")
	}
	if _, _, _, err := hs.ReadMessage(nil, frame[1:]); err != nil {
		_ = writeFrame(append([]byte{0x01}, []byte("Handshake MAC failure")...))
		return err
	}
	reply, recv, send, err := hs.WriteMessage(nil, nil)
	if err != nil {
		return err
	}
	if err := writeFrame(append([]byte{0x00}, reply...)); err != nil {
		return err
	}

	var readMu, sendMu sync.Mutex
	s.write = func(msg proto.Message) error {
		body, err := proto.Marshal(msg)
		if err != nil {
			return err
		}
		id := api.TypeID(msg)
		plain := append([]byte{byte(id >> 8), byte(id), byte(len(body) >> 8), byte(len(body))}, body...)
		// The nonce advances per message, so frames must leave in the order
		// they were sealed.
		sendMu.Lock()
		defer sendMu.Unlock()
		sealed, err := send.Encrypt(nil, nil, plain)
		if err != nil {
			return err
		}
		return writeFrame(sealed)
	}
	s.read = func() (proto.Message, error) {
		data, err := readFrame()
		if err != nil {
			return nil, err
		}
		readMu.Lock()
		plain, err := recv.Decrypt(nil, nil, data)
		readMu.Unlock()
		if err != nil {
			return nil, err
		}
		if len(plain) < 4 {
			return nil, errors.New("short message")
		}
		msg := api.NewMessageByTypeID(uint64(binary.BigEndian.Uint16(plain[0:2])))
		if msg == nil {
			return nil, fmt.Errorf("unknown message type %d", binary.BigEndian.Uint16(plain[0:2]))
		}
		return msg, proto.Unmarshal(plain[4:], msg)
	}
	return nil
}

// stateKey reads the entity key of a state message.
func stateKey(msg proto.Message) uint32 {
	if k, ok := msg.(interface{ GetKey() uint32 }); ok {
		return k.GetKey()
	}
	return 0
}
