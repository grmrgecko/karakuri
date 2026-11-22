package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"gitlab.com/gomidi/midi/v2"
	"gitlab.com/gomidi/midi/v2/drivers"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
)

// Kind is the connection kind registered with the integrations registry.
const Kind = "midi"

// reopenDelay is how long the driver waits before looking for a named port
// again while it is missing, which is what a device not plugged in yet looks
// like.
const reopenDelay = 5 * time.Second

// midiBackend returns the process-wide MIDI backend. A seam so tests inject
// the in-memory testdrv instead of the rtmidi hardware driver main.go
// registers.
var midiBackend = func() (drivers.Driver, error) {
	d := drivers.Get()
	if d == nil {
		return nil, fmt.Errorf("no MIDI backend registered")
	}
	return d, nil
}

// Config is the driver configuration stored (encrypted) on a connection. A
// connection names a local input port, an output port, or both; each is
// matched as a case-insensitive substring of the port name the backend
// reports.
type Config struct {
	InputPort  string `json:"input_port,omitempty"`
	OutputPort string `json:"output_port,omitempty"`
}

// validate checks the config beyond required fields.
func (c Config) validate() error {
	if c.InputPort == "" && c.OutputPort == "" {
		return fmt.Errorf("midi config needs an input port, an output port, or both")
	}
	return nil
}

// KindDef returns the MIDI connection kind: the driver factory plus the
// config schema the settings UI renders and the validation and test hooks.
func KindDef() integrations.ConnectionKind {
	return integrations.ConnectionKind{
		Kind:  Kind,
		Label: "MIDI",
		ConfigSpec: []flow.FieldSpec{
			{
				ID: "input_port", Label: "Input port", Type: flow.FieldString,
				Placeholder: "Midi Through",
				Help: "Port received messages arrive on, matched as part of the port name in any case. " +
					"Leave empty for a send-only connection. Test lists the ports this machine has.",
			},
			{
				ID: "output_port", Label: "Output port", Type: flow.FieldString,
				Placeholder: "Midi Through",
				Help:        "Port messages are sent to, matched the same way. Leave empty for a receive-only connection.",
			},
		},
		Factory: New,
		Validate: func(cfg []byte) error {
			var c Config
			if err := json.Unmarshal(cfg, &c); err != nil {
				return fmt.Errorf("invalid midi config: %w", err)
			}
			return c.validate()
		},
		Test: TestConnection,
	}
}

// TestConnection checks that the named ports exist right now, and lists what
// the machine has when one does not, since a port name is easiest to get
// right by copying it from the error.
func TestConnection(ctx context.Context, id uuid.UUID, cfg []byte) error {
	var c Config
	if err := json.Unmarshal(cfg, &c); err != nil {
		return fmt.Errorf("invalid midi config: %w", err)
	}
	if err := c.validate(); err != nil {
		return err
	}
	backend, err := midiBackend()
	if err != nil {
		return err
	}
	if c.InputPort != "" {
		if _, err := findIn(backend, c.InputPort); err != nil {
			return err
		}
	}
	if c.OutputPort != "" {
		if _, err := findOut(backend, c.OutputPort); err != nil {
			return err
		}
	}
	return nil
}

// findIn resolves an input port by case-insensitive substring, naming the
// ports that exist when the match fails.
func findIn(backend drivers.Driver, name string) (drivers.In, error) {
	ports, err := backend.Ins()
	if err != nil {
		return nil, fmt.Errorf("listing MIDI input ports: %w", err)
	}
	names := make([]string, 0, len(ports))
	for _, p := range ports {
		if strings.Contains(strings.ToLower(p.String()), strings.ToLower(name)) {
			return p, nil
		}
		names = append(names, p.String())
	}
	return nil, fmt.Errorf("no MIDI input port matches %q; available: %s", name, portList(names))
}

// findOut resolves an output port the same way.
func findOut(backend drivers.Driver, name string) (drivers.Out, error) {
	ports, err := backend.Outs()
	if err != nil {
		return nil, fmt.Errorf("listing MIDI output ports: %w", err)
	}
	names := make([]string, 0, len(ports))
	for _, p := range ports {
		if strings.Contains(strings.ToLower(p.String()), strings.ToLower(name)) {
			return p, nil
		}
		names = append(names, p.String())
	}
	return nil, fmt.Errorf("no MIDI output port matches %q; available: %s", name, portList(names))
}

// portList renders port names for an error message.
func portList(names []string) string {
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// Driver is a running MIDI connection shared by all nodes referencing it: a
// listener on the named input port fanning messages out to subscriptions, and
// a send function bound to the named output port. Ports that are not present
// yet are retried, which is how a connection survives a device plugged in
// after startup.
type Driver struct {
	id  uuid.UUID
	cfg Config
	log *zap.Logger

	publishStatus func(integrations.Status)

	mu        sync.Mutex
	status    integrations.Status
	subs      map[int]func(midi.Message)
	nextSubID int

	// in and out are the opened ports; send and stopListen are their live
	// handles. All nil until the open loop finds the port.
	in         drivers.In
	out        drivers.Out
	send       func(midi.Message) error
	stopListen func()

	ctx    context.Context
	cancel context.CancelFunc
}

// New builds a MIDI driver from a connection model. Registered as the factory
// for the "midi" kind.
func New(model integrations.ConnModel, deps integrations.Deps) (integrations.Driver, error) {
	var cfg Config
	if err := json.Unmarshal(model.Config, &cfg); err != nil {
		return nil, fmt.Errorf("invalid midi config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
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
		subs:          make(map[int]func(midi.Message)),
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

// Start begins looking for the named ports. The connection reports connected
// once every named port is open; until then the status carries which port is
// missing and what the machine has instead.
func (d *Driver) Start(ctx context.Context) error {
	backend, err := midiBackend()
	if err != nil {
		return err
	}
	d.ctx, d.cancel = context.WithCancel(context.Background())
	go d.openLoop(backend)
	return nil
}

// Stop closes the ports. The backend itself stays registered: it is
// process-wide and other connections share it.
func (d *Driver) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
	d.mu.Lock()
	stop := d.stopListen
	d.stopListen = nil
	in, out := d.in, d.out
	d.in, d.out = nil, nil
	d.send = nil
	d.mu.Unlock()

	if stop != nil {
		stop()
	}
	if in != nil {
		in.Close()
	}
	if out != nil {
		out.Close()
	}
	d.setStatus(integrations.StateDisabled, "")
}

// openLoop opens the named ports, retrying while one is missing, and settles
// once both are up. A port that vanishes later is not re-detected; sends fail
// and the operator restarts the connection, which is the honest limit of a
// backend with no unplug notification.
func (d *Driver) openLoop(backend drivers.Driver) {
	for d.ctx.Err() == nil {
		var missing []string

		if d.cfg.InputPort != "" && !d.listening() {
			if in, err := findIn(backend, d.cfg.InputPort); err != nil {
				missing = append(missing, err.Error())
			} else if stop, err := midi.ListenTo(in, func(msg midi.Message, _ int32) {
				d.dispatch(msg)
				// UseSysEx: listeners drop SysEx unless asked, and the trigger
				// offers a SysEx filter.
			}, midi.UseSysEx()); err != nil {
				missing = append(missing, fmt.Sprintf("opening input %s: %v", in, err))
			} else {
				// Stop cancels before it takes the lock, so a port opened while
				// it ran is one it did not see and this loop has to close.
				d.mu.Lock()
				if d.ctx.Err() != nil {
					d.mu.Unlock()
					stop()
					in.Close()
					return
				}
				d.in = in
				d.stopListen = stop
				d.mu.Unlock()
				d.log.Info("midi input open", zap.String("port", in.String()))
			}
		}

		if d.cfg.OutputPort != "" && !d.sendable() {
			if out, err := findOut(backend, d.cfg.OutputPort); err != nil {
				missing = append(missing, err.Error())
			} else if send, err := midi.SendTo(out); err != nil {
				missing = append(missing, fmt.Sprintf("opening output %s: %v", out, err))
			} else {
				d.mu.Lock()
				if d.ctx.Err() != nil {
					d.mu.Unlock()
					out.Close()
					return
				}
				d.out = out
				d.send = send
				d.mu.Unlock()
				d.log.Info("midi output open", zap.String("port", out.String()))
			}
		}

		if len(missing) == 0 {
			d.setStatus(integrations.StateConnected, "")
			return
		}
		d.setStatus(integrations.StateError, strings.Join(missing, "; "))
		select {
		case <-time.After(reopenDelay):
		case <-d.ctx.Done():
			return
		}
	}
}

// listening reports whether the input port is open.
func (d *Driver) listening() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.stopListen != nil
}

// sendable reports whether the output port is open.
func (d *Driver) sendable() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.send != nil
}

// dispatch hands one received message to every subscription. Handlers are
// resolved under the lock and run outside it.
func (d *Driver) dispatch(msg midi.Message) {
	d.mu.Lock()
	handlers := make([]func(midi.Message), 0, len(d.subs))
	for _, h := range d.subs {
		handlers = append(handlers, h)
	}
	d.mu.Unlock()
	for _, h := range handlers {
		h(msg)
	}
}

// Subscribe registers a handler for received messages. Filtering by type or
// channel is the subscriber's business: MIDI is slow enough to hand everyone
// everything. The returned function unsubscribes.
func (d *Driver) Subscribe(handler func(midi.Message)) (func(), error) {
	if d.cfg.InputPort == "" {
		return nil, fmt.Errorf("midi connection has no input port, so it cannot receive")
	}
	d.mu.Lock()
	d.nextSubID++
	id := d.nextSubID
	d.subs[id] = handler
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.subs, id)
		d.mu.Unlock()
	}, nil
}

// Send transmits one message on the output port.
func (d *Driver) Send(msg midi.Message) error {
	d.mu.Lock()
	send := d.send
	d.mu.Unlock()
	if send == nil {
		if d.cfg.OutputPort == "" {
			return fmt.Errorf("midi connection has no output port")
		}
		return fmt.Errorf("midi output port %q is not open yet", d.cfg.OutputPort)
	}
	return send(msg)
}
