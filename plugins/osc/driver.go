package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/hypebeast/go-osc/osc"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// Kind is the connection kind registered with the integrations registry.
const Kind = "osc"

// Listen rebind backoff bounds, used when the configured port cannot be bound
// or the socket fails while serving.
const (
	rebindMinBackoff = time.Second
	rebindMaxBackoff = 30 * time.Second
)

// Config is the driver configuration stored (encrypted) on a connection. A
// connection describes one UDP peer: where messages are sent, and which local
// port received ones arrive on. Either half may be left off for a send-only
// or receive-only connection.
type Config struct {
	Host       string `json:"host,omitempty"`
	Port       int    `json:"port,omitempty"`
	ListenHost string `json:"listen_host,omitempty"`
	ListenPort int    `json:"listen_port,omitempty"`
}

// validate checks the config beyond required fields: at least one direction
// must be configured, and ports must be in range.
func (c Config) validate() error {
	if c.Host == "" && c.ListenPort == 0 {
		return fmt.Errorf("osc config needs a send target, a listen port, or both")
	}
	if c.Host != "" && (c.Port < 1 || c.Port > 65535) {
		return fmt.Errorf("osc send port must be between 1 and 65535")
	}
	if c.Host == "" && c.Port != 0 {
		return fmt.Errorf("osc send port is set but the host is empty")
	}
	if c.ListenPort < 0 || c.ListenPort > 65535 {
		return fmt.Errorf("osc listen port must be between 1 and 65535")
	}
	return nil
}

// KindDef returns the OSC connection kind: the driver factory plus the config
// schema the settings UI renders and the validation and test hooks.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "OSC",
		ConfigSpec: []flow.FieldSpec{
			{
				ID: "host", Label: "Host", Type: flow.FieldString,
				Placeholder: "10.0.0.30",
				Help:        "Host OSC messages are sent to. Leave empty for a receive-only connection.",
			},
			{
				ID: "port", Label: "Port", Type: flow.FieldNumber,
				Placeholder: "9000",
				Help:        "UDP port OSC messages are sent to.",
			},
			{
				ID: "listen_host", Label: "Listen address", Type: flow.FieldString,
				Placeholder: "0.0.0.0",
				Help:        "Local address to receive on. Left empty, every interface listens.",
			},
			{
				ID: "listen_port", Label: "Listen port", Type: flow.FieldNumber,
				Placeholder: "9001",
				Help:        "Local UDP port received OSC messages arrive on. Leave empty for a send-only connection; OSC In triggers on it will then fail to arm.",
			},
		},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid osc config: %w", err)
			}
			return c.validate()
		},
		Test: TestConnection,
	}
}

// TestConnection checks what a connectionless protocol lets us check: the
// config is coherent and the send target resolves. UDP has no handshake, so
// an unreachable peer cannot be detected here.
func TestConnection(ctx context.Context, id uuid.UUID, cfg []byte) error {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("invalid osc config: %w", err)
	}
	if err := c.validate(); err != nil {
		return err
	}
	if c.Host != "" {
		addr := net.JoinHostPort(c.Host, strconv.Itoa(c.Port))
		if _, err := net.ResolveUDPAddr("udp", addr); err != nil {
			return fmt.Errorf("cannot resolve %s: %w", addr, err)
		}
	}
	return nil
}

// subscription is one registered handler for an address filter.
type subscription struct {
	filter  string
	handler func(address string, args []any)
}

// Driver is a running OSC connection shared by all nodes referencing it: a
// send client when a target is configured, and a listen socket fanning
// received messages out to subscriptions when a listen port is.
type Driver struct {
	id  uuid.UUID
	cfg Config
	log *zap.Logger

	publishStatus func(integrations.Status)

	// client sends to the configured target, nil on a receive-only
	// connection.
	client *osc.Client

	mu        sync.Mutex
	status    integrations.Status
	subs      map[int]*subscription
	nextSubID int
	pc        net.PacketConn

	ctx    context.Context
	cancel context.CancelFunc
}

// New builds an OSC driver from a connection model. Registered as the factory
// for the "osc" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid osc config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	d := &Driver{
		id:            model.ID,
		cfg:           cfg,
		log:           log,
		publishStatus: deps.PublishStatus,
		subs:          make(map[int]*subscription),
		status:        integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}
	if cfg.Host != "" {
		d.client = osc.NewClient(cfg.Host, cfg.Port)
	}
	return d, nil
}

// ID returns the connection's database identifier.
func (d *Driver) ID() uuid.UUID { return d.id }

// Kind returns the driver kind.
func (d *Driver) Kind() string { return Kind }

// Status returns the connection's current lifecycle state.
func (d *Driver) Status() integrations.Status {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.status
}

// setStatus records a state transition and relays it to the manager. Repeated
// identical transitions are dropped.
func (d *Driver) setStatus(state, errMsg string) {
	d.mu.Lock()
	if d.status.State == state && d.status.Error == errMsg {
		d.mu.Unlock()
		return
	}
	d.status = integrations.Status{State: state, Error: errMsg, Since: time.Now()}
	status := d.status
	d.mu.Unlock()
	if d.publishStatus != nil {
		d.publishStatus(status)
	}
}

// Start binds the listen socket when one is configured. UDP is
// connectionless, so a send-only connection has nothing to establish and is
// usable as soon as it exists.
func (d *Driver) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(context.Background())
	if d.cfg.ListenPort == 0 {
		d.setStatus(integrations.StateConnected, "")
		return nil
	}
	go d.listenLoop()
	return nil
}

// Stop tears the listen socket down and releases the driver.
func (d *Driver) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Lock()
	pc := d.pc
	d.pc = nil
	d.mu.Unlock()
	if pc != nil {
		pc.Close()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// listenLoop binds the configured port and serves it, rebinding with backoff
// whenever the bind fails or the socket dies, until the driver stops.
func (d *Driver) listenLoop() {
	addr := net.JoinHostPort(d.cfg.ListenHost, strconv.Itoa(d.cfg.ListenPort))
	backoff := rebindMinBackoff
	for d.ctx.Err() == nil {
		pc, err := net.ListenPacket("udp", addr)
		if err != nil {
			d.setStatus(integrations.StateError, fmt.Sprintf("listen %s: %v", addr, err))
			select {
			case <-time.After(backoff):
			case <-d.ctx.Done():
				return
			}
			backoff = min(backoff*2, rebindMaxBackoff)
			continue
		}
		backoff = rebindMinBackoff

		// Stop cancels before it takes the lock, so a socket bound while it
		// ran is one it did not see and this loop has to close.
		d.mu.Lock()
		if d.ctx.Err() != nil {
			d.mu.Unlock()
			pc.Close()
			return
		}
		d.pc = pc
		d.mu.Unlock()
		d.setStatus(integrations.StateConnected, "")
		d.log.Info("osc listening", zap.String("addr", addr))

		err = d.serve(pc)

		d.mu.Lock()
		d.pc = nil
		d.mu.Unlock()
		pc.Close()
		if d.ctx.Err() != nil {
			return
		}
		d.setStatus(integrations.StateError, fmt.Sprintf("osc receive on %s: %v", addr, err))
	}
}

// serve reads packets until the socket fails. A packet that does not parse is
// logged and skipped; only a socket error ends the loop.
func (d *Driver) serve(pc net.PacketConn) error {
	srv := &osc.Server{}
	for {
		pkt, err := srv.ReceivePacket(pc)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) || errors.Is(err, net.ErrClosed) {
				return err
			}
			d.log.Debug("osc dropped an unparsable packet", zap.Error(err))
			continue
		}
		d.dispatch(pkt)
	}
}

// dispatch fans a packet's messages out to matching subscriptions. Bundles
// are flattened and delivered immediately; time tags are not honored, since a
// flow trigger has no meaningful way to hold a message for later.
func (d *Driver) dispatch(pkt osc.Packet) {
	switch p := pkt.(type) {
	case *osc.Message:
		d.deliver(p)
	case *osc.Bundle:
		for _, m := range p.Messages {
			d.deliver(m)
		}
		for _, b := range p.Bundles {
			d.dispatch(b)
		}
	}
}

// deliver hands one message to every subscription whose filter matches its
// address. Handlers are resolved under the lock and run outside it, so an
// unsubscribe during delivery cannot deadlock.
func (d *Driver) deliver(msg *osc.Message) {
	if msg == nil || msg.Address == "" {
		return
	}
	d.mu.Lock()
	handlers := make([]func(string, []any), 0, len(d.subs))
	for _, s := range d.subs {
		if s.filter == "" || MatchAddress(s.filter, msg.Address) {
			handlers = append(handlers, s.handler)
		}
	}
	d.mu.Unlock()
	if len(handlers) == 0 {
		return
	}
	args := fromOSCArgs(msg.Arguments)
	for _, h := range handlers {
		h(msg.Address, args)
	}
}

// Subscribe registers a handler for received messages whose address matches
// the filter; an empty filter matches every message. The returned function
// unsubscribes.
func (d *Driver) Subscribe(filter string, handler func(address string, args []any)) (func(), error) {
	if d.cfg.ListenPort == 0 {
		return nil, fmt.Errorf("osc connection has no listen port, so it cannot receive")
	}
	d.mu.Lock()
	d.nextSubID++
	id := d.nextSubID
	d.subs[id] = &subscription{filter: filter, handler: handler}
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.subs, id)
		d.mu.Unlock()
	}, nil
}

// Send builds one OSC message and sends it to the connection's target.
func (d *Driver) Send(ctx context.Context, address string, args []any) error {
	if d.client == nil {
		return fmt.Errorf("osc connection has no send target host")
	}
	return d.client.Send(osc.NewMessage(address, args...))
}
