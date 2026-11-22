package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/Hundemeier/go-sacn/sacn"
	"github.com/google/uuid"
	"github.com/jsimonetti/go-artnet/packet"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// Kind is the connection kind registered with the integrations registry.
const Kind = "dmx"

// Protocols a connection speaks. Both carry DMX512 frames over UDP; they
// differ in port, addressing, and who keeps the frame alive.
const (
	protoSACN   = "sacn"
	protoArtNet = "artnet"
)

// artnetPort is the fixed Art-Net UDP port.
const artnetPort = 6454

// artnetResend is how often an unchanged frame is re-transmitted. Art-Net
// nodes drop an input that goes silent; the ArtDmx spec text recommends a
// re-transmit of 800ms to 1000ms to converge with sACN behavior.
const artnetResend = time.Second

// Universe bounds per protocol: sACN universes are 1 to 63999 (E1.31), an
// Art-Net port-address is 15 bits.
const (
	sacnUniverseMax   = 63999
	artnetUniverseMax = 0x7fff
)

// Config is the driver configuration stored (encrypted) on a connection.
type Config struct {
	Protocol    string `json:"protocol"`
	Destination string `json:"destination,omitempty"`
	BindAddress string `json:"bind_address,omitempty"`
	SourceName  string `json:"source_name,omitempty"`
}

// validate checks the config beyond required fields.
func (c Config) validate() error {
	switch c.Protocol {
	case protoSACN:
		if c.Destination != "" && hasPort(c.Destination) {
			return fmt.Errorf("sacn destination is a bare host; the port is always 5568")
		}
	case protoArtNet:
		if c.Destination == "" {
			return fmt.Errorf("artnet config requires a destination host or broadcast address")
		}
	default:
		return fmt.Errorf("dmx protocol must be %q or %q", protoSACN, protoArtNet)
	}
	return nil
}

// hasPort reports whether a destination string carries an explicit port.
func hasPort(dest string) bool {
	_, _, err := net.SplitHostPort(dest)
	return err == nil
}

// artnetDest resolves an Art-Net destination, defaulting the port to 6454.
func artnetDest(dest string) (*net.UDPAddr, error) {
	if !hasPort(dest) {
		dest = net.JoinHostPort(dest, strconv.Itoa(artnetPort))
	}
	return net.ResolveUDPAddr("udp", dest)
}

// KindDef returns the DMX connection kind: the driver factory plus the config
// schema the settings UI renders and the validation and test hooks.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "DMX",
		ConfigSpec: []flow.FieldSpec{
			{
				ID: "protocol", Label: "Protocol", Type: flow.FieldSelect,
				Required: true, Default: protoSACN,
				Options: []flow.Option{
					{Value: protoSACN, Label: "sACN (E1.31)"},
					{Value: protoArtNet, Label: "Art-Net"},
				},
				Help: "How DMX frames leave and arrive. sACN multicasts by default; Art-Net sends to one node or a broadcast address.",
			},
			{
				ID: "destination", Label: "Destination", Type: flow.FieldString,
				Placeholder: "10.0.0.40",
				Help: "Where frames are sent. sACN: a unicast host, or empty to multicast the universe's own group. " +
					"Art-Net: required, the node's address or a broadcast address; an optional :port overrides 6454.",
			},
			{
				ID: "bind_address", Label: "Bind address", Type: flow.FieldString,
				Placeholder: "0.0.0.0",
				Help:        "Local address sockets bind to. Left empty, the OS picks.",
			},
			{
				ID: "source_name", Label: "Source name", Type: flow.FieldString,
				Placeholder: "karakuri",
				ShowIf:      &flow.ShowIf{Field: "protocol", Equals: protoSACN},
				Help:        "Name this source reports in sACN packets.",
			},
		},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid dmx config: %w", err)
			}
			return c.validate()
		},
		Test: TestConnection,
	}
}

// TestConnection checks what a connectionless protocol lets us check: the
// config is coherent and the destination resolves. UDP has no handshake, so
// an unreachable node cannot be detected here.
func TestConnection(ctx context.Context, id uuid.UUID, cfg []byte) error {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("invalid dmx config: %w", err)
	}
	if err := c.validate(); err != nil {
		return err
	}
	switch {
	case c.Protocol == protoArtNet:
		if _, err := artnetDest(c.Destination); err != nil {
			return fmt.Errorf("cannot resolve %s: %w", c.Destination, err)
		}
	case c.Destination != "":
		addr := net.JoinHostPort(c.Destination, "5568")
		if _, err := net.ResolveUDPAddr("udp", addr); err != nil {
			return fmt.Errorf("cannot resolve %s: %w", addr, err)
		}
	}
	return nil
}

// ReceiveEvent is one received DMX frame handed to a subscription: the full
// frame plus which channels moved since the last one, 1-based.
type ReceiveEvent struct {
	Universe int
	Channels []byte
	Changed  map[int]byte
}

// subscription is one registered handler for a universe.
type subscription struct {
	universe uint16
	handler  func(ReceiveEvent)
}

// Driver is a running DMX connection shared by all nodes referencing it. It
// owns one accumulated 512-channel output frame per universe: a channel set
// merges into it and the whole frame goes out, since a DMX frame always
// carries every channel. The sACN transmitter keeps its universes alive on
// its own; the Art-Net path re-sends on a ticker.
type Driver struct {
	id  uuid.UUID
	cfg Config
	log *zap.Logger

	publishStatus func(integrations.Status)

	mu     sync.Mutex
	status integrations.Status
	// frames is the accumulated output frame per universe.
	frames map[uint16][]byte
	// lastIn is the last received frame per universe, for change detection.
	lastIn    map[uint16][]byte
	subs      map[int]*subscription
	nextSubID int

	// sACN state: the transmitter owns the sockets and keepalive, txCh is the
	// per-universe channel Activate returned, recv is the receive socket
	// created on the first subscription.
	tx   *sacn.Transmitter
	txCh map[uint16]chan<- []byte
	recv *sacn.ReceiverSocket

	// Art-Net state: an ephemeral send socket, the resolved destination, a
	// per-universe sequence counter, and a listener bound on the first
	// subscription.
	sendConn *net.UDPConn
	dest     *net.UDPAddr
	seq      map[uint16]uint8
	listener net.PacketConn

	ctx    context.Context
	cancel context.CancelFunc
}

// New builds a DMX driver from a connection model. Registered as the factory
// for the "dmx" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid dmx config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	if cfg.SourceName == "" {
		cfg.SourceName = "karakuri"
	}
	log := deps.Logger
	if log == nil {
		log = zap.NewNop()
	}
	return &Driver{
		id:            model.ID,
		cfg:           cfg,
		log:           log,
		publishStatus: deps.PublishStatus,
		frames:        make(map[uint16][]byte),
		lastIn:        make(map[uint16][]byte),
		subs:          make(map[int]*subscription),
		txCh:          make(map[uint16]chan<- []byte),
		seq:           make(map[uint16]uint8),
		status:        integrations.Status{State: integrations.StateConnecting, Since: time.Now()},
	}, nil
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

// Start sets up the send path. UDP is connectionless, so the connection is
// usable once its sockets exist; receive sockets are bound when the first
// subscription arrives.
func (d *Driver) Start(ctx context.Context) error {
	d.ctx, d.cancel = context.WithCancel(context.Background())
	switch d.cfg.Protocol {
	case protoSACN:
		// The transmitter resolves its bind string as host:port (the receiver
		// appends its own port), so a configured bare host gets an ephemeral
		// port here. The connection UUID is 16 bytes, exactly the sACN CID,
		// so a connection is a stable source identity across restarts.
		bind := d.cfg.BindAddress
		if bind != "" {
			bind = net.JoinHostPort(bind, "0")
		}
		tx, err := sacn.NewTransmitter(bind, [16]byte(d.id), d.cfg.SourceName)
		if err != nil {
			return fmt.Errorf("sacn transmitter: %w", err)
		}
		d.tx = &tx
	case protoArtNet:
		dest, err := artnetDest(d.cfg.Destination)
		if err != nil {
			return fmt.Errorf("cannot resolve %s: %w", d.cfg.Destination, err)
		}
		laddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(d.cfg.BindAddress, "0"))
		if err != nil {
			return fmt.Errorf("cannot resolve bind address %q: %w", d.cfg.BindAddress, err)
		}
		conn, err := net.ListenUDP("udp", laddr)
		if err != nil {
			return fmt.Errorf("artnet send socket: %w", err)
		}
		d.sendConn = conn
		d.dest = dest
		go d.resendLoop()
	}
	d.setStatus(integrations.StateConnected, "")
	return nil
}

// Stop tears the sockets down. Closing a sACN universe channel makes the
// transmitter send a stream-terminated packet, the protocol's own goodbye.
func (d *Driver) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Lock()
	chans := d.txCh
	d.txCh = make(map[uint16]chan<- []byte)
	recv := d.recv
	d.recv = nil
	sendConn := d.sendConn
	d.sendConn = nil
	listener := d.listener
	d.listener = nil
	d.mu.Unlock()

	for _, ch := range chans {
		close(ch)
	}
	if recv != nil {
		recv.Close()
	}
	if sendConn != nil {
		sendConn.Close()
	}
	if listener != nil {
		listener.Close()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// universeMax returns the largest universe the configured protocol addresses.
func (d *Driver) universeMax() int {
	if d.cfg.Protocol == protoArtNet {
		return artnetUniverseMax
	}
	return sacnUniverseMax
}

// universeMin returns the smallest valid universe: sACN starts at 1, an
// Art-Net port-address at 0.
func (d *Driver) universeMin() int {
	if d.cfg.Protocol == protoArtNet {
		return 0
	}
	return 1
}

// checkUniverse validates a universe number against the protocol.
func (d *Driver) checkUniverse(universe int) (uint16, error) {
	if universe < d.universeMin() || universe > d.universeMax() {
		return 0, fmt.Errorf("%s universe must be between %d and %d",
			d.cfg.Protocol, d.universeMin(), d.universeMax())
	}
	return uint16(universe), nil
}

// SetChannels merges channel values (1-based) into the universe's output
// frame and transmits it.
func (d *Driver) SetChannels(universe int, values map[int]byte) error {
	u, err := d.checkUniverse(universe)
	if err != nil {
		return err
	}

	d.mu.Lock()
	frame, ok := d.frames[u]
	if !ok {
		frame = make([]byte, 512)
		d.frames[u] = frame
	}
	for ch, v := range values {
		frame[ch-1] = v
	}
	out := make([]byte, 512)
	copy(out, frame)
	d.mu.Unlock()

	if d.cfg.Protocol == protoArtNet {
		return d.sendArtNet(u, out)
	}
	return d.sendSACN(u, out)
}

// sendSACN pushes a frame to the transmitter, activating the universe on
// first use. Activation binds a socket and starts the transmitter's own
// keepalive, which re-sends the last frame every second from then on.
func (d *Driver) sendSACN(u uint16, frame []byte) error {
	d.mu.Lock()
	ch, ok := d.txCh[u]
	if !ok {
		var err error
		ch, err = d.tx.Activate(u)
		if err != nil {
			d.mu.Unlock()
			return fmt.Errorf("activating sacn universe %d: %w", u, err)
		}
		d.txCh[u] = ch
		if d.cfg.Destination == "" {
			d.tx.SetMulticast(u, true)
		}
	}
	// The universe stays activated when its unicast destination fails to
	// resolve, so the lookup is retried on each send until it succeeds.
	if d.cfg.Destination != "" && len(d.tx.Destinations(u)) == 0 {
		if errs := d.tx.SetDestinations(u, []string{d.cfg.Destination}); errs != nil {
			d.mu.Unlock()
			return fmt.Errorf("sacn destination %s: %v", d.cfg.Destination, errs[0])
		}
	}
	// The send happens under the lock: Stop takes it before closing the
	// channels, so it cannot close one while a frame is on its way in, which
	// would panic and take the plugin process down.
	ch <- frame
	d.mu.Unlock()
	return nil
}

// sendArtNet transmits one ArtDmx packet for the universe. The 15-bit
// port-address splits into Net (top 7 bits) and SubUni (low 8).
func (d *Driver) sendArtNet(u uint16, frame []byte) error {
	d.mu.Lock()
	// Sequence runs 0x01 to 0xff; zero would tell the node to ignore ordering.
	d.seq[u]++
	if d.seq[u] == 0 {
		d.seq[u] = 1
	}
	p := &packet.ArtDMXPacket{
		Sequence: d.seq[u],
		Net:      uint8(u >> 8),
		SubUni:   uint8(u & 0xff),
	}
	copy(p.Data[:], frame)
	conn, dest := d.sendConn, d.dest
	d.mu.Unlock()

	if conn == nil {
		return fmt.Errorf("dmx connection is stopped")
	}
	raw, err := p.MarshalBinary()
	if err != nil {
		return fmt.Errorf("encoding artnet frame: %w", err)
	}
	if _, err := conn.WriteToUDP(raw, dest); err != nil {
		return fmt.Errorf("sending artnet frame: %w", err)
	}
	return nil
}

// resendLoop re-transmits every active Art-Net universe once a second, since
// a node treats a silent input as failed. The sACN transmitter does the same
// for its universes internally.
func (d *Driver) resendLoop() {
	ticker := time.NewTicker(artnetResend)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.mu.Lock()
			frames := make(map[uint16][]byte, len(d.frames))
			for u, f := range d.frames {
				out := make([]byte, 512)
				copy(out, f)
				frames[u] = out
			}
			d.mu.Unlock()
			for u, f := range frames {
				if err := d.sendArtNet(u, f); err != nil {
					d.log.Debug("artnet resend failed", zap.Uint16("universe", u), zap.Error(err))
				}
			}
		case <-d.ctx.Done():
			return
		}
	}
}

// Subscribe registers a handler for frames received on a universe. The
// receive socket is bound on the first subscription, since both protocols
// listen on a fixed port that should stay free while nothing needs it. The
// returned function unsubscribes.
func (d *Driver) Subscribe(universe int, handler func(ReceiveEvent)) (func(), error) {
	u, err := d.checkUniverse(universe)
	if err != nil {
		return nil, err
	}
	if err := d.ensureReceiver(u); err != nil {
		return nil, err
	}

	d.mu.Lock()
	d.nextSubID++
	id := d.nextSubID
	d.subs[id] = &subscription{universe: u, handler: handler}
	d.mu.Unlock()

	return func() {
		d.mu.Lock()
		delete(d.subs, id)
		remaining := false
		for _, s := range d.subs {
			if s.universe == u {
				remaining = true
				break
			}
		}
		recv := d.recv
		d.mu.Unlock()
		if !remaining && recv != nil {
			recv.LeaveUniverse(u)
		}
	}, nil
}

// ensureReceiver binds the protocol's receive socket if it is not up yet and
// joins the universe's multicast group on sACN.
func (d *Driver) ensureReceiver(u uint16) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cfg.Protocol == protoArtNet {
		if d.listener != nil {
			return nil
		}
		addr := net.JoinHostPort(d.cfg.BindAddress, strconv.Itoa(artnetPort))
		listener, err := net.ListenPacket("udp", addr)
		if err != nil {
			return fmt.Errorf("artnet listen on %s: %w", addr, err)
		}
		d.listener = listener
		go d.artnetReceiveLoop(listener)
		return nil
	}

	if d.recv == nil {
		recv, err := sacn.NewReceiverSocket(d.cfg.BindAddress, nil)
		if err != nil {
			return fmt.Errorf("sacn listen: %w", err)
		}
		recv.SetOnChangeCallback(func(_ sacn.DataPacket, newest sacn.DataPacket) {
			d.deliver(newest.Universe(), newest.Data())
		})
		recv.Start()
		d.recv = recv
	}
	d.recv.JoinUniverse(u)
	return nil
}

// artnetReceiveLoop reads ArtDmx packets until the socket closes. Other
// Art-Net opcodes (polls, replies) are ignored; a packet that does not parse
// is dropped.
func (d *Driver) artnetReceiveLoop(listener net.PacketConn) {
	buf := make([]byte, 2048)
	for {
		n, _, err := listener.ReadFrom(buf)
		if err != nil {
			return
		}
		p, err := packet.Unmarshal(buf[:n])
		if err != nil {
			continue
		}
		dmx, ok := p.(*packet.ArtDMXPacket)
		if !ok {
			continue
		}
		u := uint16(dmx.Net)<<8 | uint16(dmx.SubUni)
		d.deliver(u, dmx.Data[:])
	}
}

// deliver diffs a received frame against the universe's last one and hands
// the event to matching subscriptions. An unchanged frame is dropped: both
// protocols re-transmit idle frames as keepalive, and a trigger firing on
// every keepalive would run a flow at the wire rate.
func (d *Driver) deliver(u uint16, data []byte) {
	frame := make([]byte, 512)
	copy(frame, data)

	d.mu.Lock()
	// A universe never heard from diffs against all zeros, so the first event
	// reports the channels actually driven rather than all 512.
	last := d.lastIn[u]
	if last == nil {
		last = make([]byte, 512)
	}
	changed := make(map[int]byte)
	for i := range frame {
		if last[i] != frame[i] {
			changed[i+1] = frame[i]
		}
	}
	if len(changed) == 0 {
		d.mu.Unlock()
		return
	}
	d.lastIn[u] = frame
	handlers := make([]func(ReceiveEvent), 0, len(d.subs))
	for _, s := range d.subs {
		if s.universe == u {
			handlers = append(handlers, s.handler)
		}
	}
	d.mu.Unlock()

	if len(handlers) == 0 {
		return
	}
	evt := ReceiveEvent{Universe: int(u), Channels: frame, Changed: changed}
	for _, h := range handlers {
		h(evt)
	}
}
