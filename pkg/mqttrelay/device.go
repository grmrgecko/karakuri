package mqttrelay

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"go.uber.org/zap"
)

// DeviceTopicPrefix is the broker namespace a device's decoded state is
// published in. A widget watches mqttdevice:{id} and receives the whole state
// every time any part of it changes.
const DeviceTopicPrefix = "mqttdevice"

// decodeTimeout bounds one decode unless the device sets its own limit. It
// runs per message, so the budget is small on purpose: a decoder is reading a
// payload, not doing work.
const decodeTimeout = models.DefaultStateTimeout

// decodeScopePreamble names the extras a decoder gets beyond the message
// itself. The message rides in the sandbox's own shape — `topic`, `payload`,
// and `meta` are what a script node sees, so a decoder reads like one — and
// this unpacks the metadata: whether the payload is base64 rather than text,
// what the device knows so far, for a decoder that needs the old value to
// work out the new one, and whether the broker delivered the message from its
// retained store rather than live. It shares the script's first line so a reported error
// line is the line the author wrote.
const decodeScopePreamble = `const {binary, state, retained} = meta; `

// device is one running MQTT device: its definition, the subscriptions it
// holds, and the state its decoder has accumulated.
type device struct {
	model models.MQTTDevice

	mu     sync.Mutex
	state  map[string]any
	drop   []func()
	failed string
	// lastMessage is when anything last arrived on the device's topics,
	// including messages the decoder ignored or failed on. It measures whether
	// the topics are wired right, which the decoded state alone cannot: a
	// device that hears nothing and a decoder that returns nothing look the
	// same from the values.
	lastMessage time.Time
	// stale marks state restored from storage that the device has not
	// confirmed since the server started; reportedAt is when the state script
	// last merged a report, restored or live. dirty marks state not yet
	// written to storage.
	stale      bool
	reportedAt time.Time
	dirty      bool

	// availability is online, offline, or unknown; availabilitySource says
	// what decided it and availabilityAt when it last changed.
	// availabilityStale marks one restored from storage and not yet
	// confirmed. availabilityNote is the last availability payload that
	// matched neither setting. expectFrom is when the expected interval
	// started counting, the device's start or its connection's return.
	availability       string
	availabilitySource string
	availabilityAt     time.Time
	availabilityStale  bool
	availabilityNote   string
	expectFrom         time.Time
	// entityAvail marks a device whose transport says when its entity
	// cannot answer, which then decides availability while the connection
	// is up.
	entityAvail bool
}

// Devices runs the decoders. It is deliberately *not* demand-driven, unlike the
// raw topic relay: a device that reports only what changed is unreadable if
// nothing was listening when it changed, so its subscriptions live as long as
// the device does and the state is there the moment a dashboard opens.
type Devices struct {
	resolve Resolver
	log     *zap.Logger

	// reloadMu serializes Reload and Rebind. Each reads the running set,
	// releases mu, and then starts or reattaches devices, so two running at
	// once could both start one device and leave the first instance
	// subscribed with no way to drop it.
	reloadMu sync.Mutex

	mu      sync.Mutex
	running map[uuid.UUID]*device

	watchMu       sync.Mutex
	watchers      map[int]func(DeviceChange)
	availWatchers map[int]func(AvailabilityChange)
	nextID        int

	// statusWatcher reports connection status; connUp holds the last report
	// per connection and connCancel the watches to end on Stop. clock is
	// replaced in tests.
	statusWatcher StatusWatcher
	connUp        map[uuid.UUID]bool
	connCancel    []func()
	clock         func() time.Time

	// persist is set by PersistState; without it devices start empty and
	// nothing is written, which is what tests without a database get.
	persist  bool
	stopSave chan struct{}
	saveDone chan struct{}
}

// stateSaveInterval bounds how often one device's state is written. Devices
// such as power meters report every second; a crash loses at most this much.
const stateSaveInterval = 5 * time.Second

// PersistState restores each device's last stored state when it starts,
// marked stale until the device reports again, and writes changed state at
// most every few seconds. Call it before the first Reload.
func (d *Devices) PersistState() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.persist {
		return
	}
	d.persist = true
	d.stopSave = make(chan struct{})
	d.saveDone = make(chan struct{})
	go d.saveLoop(d.stopSave, d.saveDone)
}

// saveLoop writes dirty device state until stop closes.
func (d *Devices) saveLoop(stop <-chan struct{}, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(stateSaveInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			d.saveDirty()
		case <-stop:
			d.saveDirty()
			return
		}
	}
}

// saveDirty writes every device whose state changed since its last write. A
// failed write stays dirty and is retried on the next pass.
func (d *Devices) saveDirty() {
	d.mu.Lock()
	devices := make([]*device, 0, len(d.running))
	for _, dev := range d.running {
		devices = append(devices, dev)
	}
	d.mu.Unlock()
	for _, dev := range devices {
		d.saveDevice(dev)
	}
}

// saveDevice writes one device's state when it is dirty.
func (d *Devices) saveDevice(dev *device) {
	dev.mu.Lock()
	if !dev.dirty {
		dev.mu.Unlock()
		return
	}
	row := &models.MQTTDeviceState{
		DeviceID: dev.model.ID, Control: dev.model.Control, ReportedAt: dev.reportedAt,
		Availability: dev.availability, AvailabilitySource: dev.availabilitySource, AvailabilityAt: dev.availabilityAt,
	}
	raw, err := json.Marshal(dev.state)
	dev.dirty = false
	name := dev.model.Name
	dev.mu.Unlock()
	if err != nil {
		d.log.Error("MQTT device state cannot be stored", zap.String("device", name), zap.Error(err))
		return
	}
	row.State = string(raw)
	if err := models.UpsertMQTTDeviceState(row); err != nil {
		d.log.Error("failed to store MQTT device state", zap.String("device", name), zap.Error(err))
		dev.mu.Lock()
		dev.dirty = true
		dev.mu.Unlock()
	}
}

// restoreState seeds a starting device from its stored state, marked stale.
// State stored under another control kind is dropped instead.
func (d *Devices) restoreState(dev *device) {
	row, err := models.FindMQTTDeviceState(dev.model.ID)
	if err != nil {
		return
	}
	if row.Control != dev.model.Control {
		if err := models.DeleteMQTTDeviceState(dev.model.ID); err != nil {
			d.log.Error("failed to drop MQTT device state of another kind",
				zap.String("device", dev.model.Name), zap.Error(err))
		}
		return
	}
	var state map[string]any
	if err := json.Unmarshal([]byte(row.State), &state); err != nil || state == nil {
		d.log.Warn("stored MQTT device state is unreadable; starting empty",
			zap.String("device", dev.model.Name), zap.Error(err))
		return
	}
	dev.state = state
	dev.stale = true
	dev.reportedAt = row.ReportedAt
	if row.Availability != "" {
		dev.availability = row.Availability
		dev.availabilitySource = row.AvailabilitySource
		dev.availabilityAt = row.AvailabilityAt
		dev.availabilityStale = true
	}
	d.log.Debug("MQTT device state restored", zap.String("device", dev.model.Name))
}

// D is the running device set, wired by serve.go at boot and read by the
// device nodes, the counterpart of controls.S. Nil when no server is running,
// which the nodes report as the device not running.
var D *Devices

// DeviceChange is one decoded message's effect on a device, handed to Watch
// handlers so a flow can trigger on a device the way it triggers on a control.
type DeviceChange struct {
	DeviceID uuid.UUID
	Name     string
	Kind     string
	// Topic is the MQTT topic the message arrived on, which a wildcard device
	// needs to say which of its things spoke.
	Topic string
	// Values is what this message carried: the keys the decoder returned.
	Values map[string]any
	// State is the device's whole accumulated state after the merge.
	State map[string]any
}

// NewDevices builds the device runner over the same connection resolver the
// relay uses.
func NewDevices(resolve Resolver, log *zap.Logger) *Devices {
	return &Devices{
		resolve:       resolve,
		log:           log,
		running:       make(map[uuid.UUID]*device),
		watchers:      make(map[int]func(DeviceChange)),
		availWatchers: make(map[int]func(AvailabilityChange)),
		connUp:        make(map[uuid.UUID]bool),
	}
}

// Watch registers a change handler and returns the function that removes it.
// Handlers run on the decoding goroutine, the same bargain the control store
// makes: a watcher that blocks blocks the decode, so a trigger hands off to
// the engine and returns.
func (d *Devices) Watch(fn func(DeviceChange)) func() {
	d.watchMu.Lock()
	id := d.nextID
	d.nextID++
	d.watchers[id] = fn
	d.watchMu.Unlock()

	return func() {
		d.watchMu.Lock()
		delete(d.watchers, id)
		d.watchMu.Unlock()
	}
}

// fanout hands a change to every watcher.
func (d *Devices) fanout(c DeviceChange) {
	d.watchMu.Lock()
	handlers := make([]func(DeviceChange), 0, len(d.watchers))
	for _, fn := range d.watchers {
		handlers = append(handlers, fn)
	}
	d.watchMu.Unlock()

	for _, fn := range handlers {
		fn(c)
	}
}

// Reload brings the running set in line with what is stored: devices that are
// gone stop, devices that changed are rebuilt, and the rest are left alone so a
// save elsewhere does not drop state a device spent hours accumulating.
func (d *Devices) Reload() {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	stored, err := models.ListMQTTDevices()
	if err != nil {
		d.log.Error("failed to load MQTT devices", zap.Error(err))
		return
	}

	seen := make(map[uuid.UUID]bool, len(stored))
	for _, m := range stored {
		seen[m.ID] = true
		d.mu.Lock()
		current, running := d.running[m.ID]
		unchanged := running && current.model.Topics == m.Topics &&
			current.model.StateScript == m.StateScript && current.model.ConnectionID == m.ConnectionID &&
			current.model.AvailabilityTopic == m.AvailabilityTopic && current.model.StateQoS == m.StateQoS &&
			current.model.Transport == m.Transport && current.model.EntityKey == m.EntityKey
		kindChanged := running && current.model.Control != m.Control
		intervalGone := false
		if unchanged {
			// The fields that decide the subscriptions match, but the rest —
			// name, kind, options, payload script — may not, and a stale copy
			// would misname logs and mislabel changes until a restart.
			current.mu.Lock()
			current.model = *m
			// The interval is the one availability rule an edit removes
			// without rebuilding the device.
			intervalGone = current.availabilitySource == SourceInterval && !current.sourceApplies()
			if kindChanged {
				// Values decoded for one kind may not fit another.
				current.state = make(map[string]any)
				current.stale, current.dirty = false, false
			}
			current.mu.Unlock()
		}
		persist := d.persist
		d.mu.Unlock()
		if kindChanged && persist {
			if err := models.DeleteMQTTDeviceState(m.ID); err != nil {
				d.log.Error("failed to drop MQTT device state after a kind change",
					zap.String("device", m.Name), zap.Error(err))
			}
		}
		if intervalGone {
			d.intervalRemoved(current)
		}
		if unchanged {
			continue
		}
		d.stop(m.ID, !kindChanged)
		d.start(*m)
	}

	d.mu.Lock()
	var gone []uuid.UUID
	for id := range d.running {
		if !seen[id] {
			gone = append(gone, id)
		}
	}
	persist := d.persist
	d.mu.Unlock()
	for _, id := range gone {
		// A deleted device's stored state went with it; writing it back here
		// would leave an orphan row, and the periodic writer may have written
		// it between the delete and this reload.
		d.stop(id, false)
		if persist {
			if err := models.DeleteMQTTDeviceState(id); err != nil {
				d.log.Error("failed to drop a removed MQTT device's state", zap.String("device_id", id.String()), zap.Error(err))
			}
		}
	}
}

// Stop tears every device down.
func (d *Devices) Stop() {
	d.mu.Lock()
	ids := make([]uuid.UUID, 0, len(d.running))
	for id := range d.running {
		ids = append(ids, id)
	}
	d.mu.Unlock()
	for _, id := range ids {
		d.stop(id, true)
	}
	d.mu.Lock()
	persist, stopSave, saveDone := d.persist, d.stopSave, d.saveDone
	d.persist = false
	cancels := d.connCancel
	d.connCancel = nil
	d.connUp = make(map[uuid.UUID]bool)
	d.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
	if persist {
		close(stopSave)
		<-saveDone
	}
}

// State returns a device's accumulated state, which is what a dashboard reads
// when it opens rather than waiting for the next message.
func (d *Devices) State(id uuid.UUID) (map[string]any, bool) {
	status, running := d.Status(id)
	return status.State, running
}

// DeviceStatus is a running device's condition: the values it holds, its
// decoder's standing failure, and when it last heard anything at all.
type DeviceStatus struct {
	State map[string]any
	// Err is why the last message failed to decode, empty when it decoded
	// cleanly. It is part of the device's condition, not a log line: a device
	// whose decoder broke an hour ago shows a stale value, and the panel needs
	// to say why.
	Err string
	// LastMessage is when anything last arrived on the device's topics, zero
	// when nothing has since it started.
	LastMessage time.Time
	// Stale is set while the state is what was stored before the server
	// started and the device has not reported since.
	Stale bool
	// ReportedAt is when the state script last merged a report, zero when it
	// never has.
	ReportedAt time.Time
	// Availability is online, offline, or unknown, with its source, when it
	// last changed, whether it is a restored value not yet confirmed, and the
	// last unrecognised availability payload.
	Availability       string
	AvailabilitySource string
	AvailabilityAt     time.Time
	AvailabilityStale  bool
	AvailabilityNote   string
}

// Status returns a device's condition, false when it is not running at all.
func (d *Devices) Status(id uuid.UUID) (DeviceStatus, bool) {
	d.mu.Lock()
	dev, ok := d.running[id]
	d.mu.Unlock()
	if !ok {
		return DeviceStatus{}, false
	}
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return DeviceStatus{
		State:       copyState(dev.state),
		Err:         dev.failed,
		LastMessage: dev.lastMessage,
		Stale:       dev.stale,
		ReportedAt:  dev.reportedAt,

		Availability:       dev.availability,
		AvailabilitySource: dev.availabilitySource,
		AvailabilityAt:     dev.availabilityAt,
		AvailabilityStale:  dev.availabilityStale,
		AvailabilityNote:   dev.availabilityNote,
	}, true
}

// start subscribes a device's topic filters and begins decoding.
func (d *Devices) start(m models.MQTTDevice) {
	driver, ok := d.resolve(m.ConnectionID)
	if !ok {
		// Not an error: the connection may simply not have started yet, and
		// OnDriverStart reloads the devices when it does. But silence here
		// left a device that never came up looking like it had nothing to say.
		d.log.Debug("MQTT device waiting for its connection",
			zap.String("device", m.Name), zap.String("connection_id", m.ConnectionID.String()))
		return
	}
	if !transportFits(m, driver) {
		d.log.Warn("device points at a connection of the wrong kind",
			zap.String("device", m.Name), zap.String("transport", m.Transport),
			zap.String("connection_id", m.ConnectionID.String()))
		return
	}

	dev := &device{model: m, state: make(map[string]any)}
	if _, ok := driver.(EntityAvailability); ok && IsEntityTransport(m.Transport) {
		dev.entityAvail = true
	}
	d.mu.Lock()
	persist := d.persist
	d.mu.Unlock()
	if persist {
		d.restoreState(dev)
	}
	d.initialAvailability(dev)
	d.attach(dev, driver)

	d.mu.Lock()
	d.running[m.ID] = dev
	d.mu.Unlock()
	d.log.Debug("MQTT device listening",
		zap.String("device", m.Name), zap.Strings("topics", m.TopicList()))
}

// transportFits reports whether a driver can carry a device's transport.
func transportFits(m models.MQTTDevice, driver any) bool {
	if IsEntityTransport(m.Transport) {
		_, ok := driver.(EntityTransport)
		return ok
	}
	_, ok := driver.(*mqtt.Driver)
	return ok
}

// attach listens for a device's reports on its connection: topic filters and
// an availability topic for MQTT, the entity's states and availability for ESPHome and Frigate.
func (d *Devices) attach(dev *device, driver any) {
	if conn, ok := driver.(*mqtt.Driver); ok && !IsEntityTransport(dev.model.Transport) {
		d.subscribe(dev, conn)
		d.subscribeAvailability(dev, conn)
		return
	}
	transport, ok := driver.(EntityTransport)
	if !ok {
		return
	}
	dev.mu.Lock()
	objectID, name := dev.model.EntityKey, dev.model.Name
	dev.mu.Unlock()
	drop, err := transport.WatchEntity(objectID, func(values map[string]any) {
		now := time.Now()
		dev.mu.Lock()
		dev.lastMessage = now
		before := copyState(dev.state)
		dev.mu.Unlock()
		d.heard(dev, now)
		d.mergeValues(dev, "", values, before, true)
	})
	if err != nil {
		d.log.Warn("failed to watch an entity", zap.String("device", name), zap.Error(err))
		return
	}
	dev.mu.Lock()
	dev.drop = append(dev.drop, drop)
	dev.mu.Unlock()

	watcher, ok := driver.(EntityAvailability)
	if !ok {
		return
	}
	dropAvail, err := watcher.WatchEntityAvailability(objectID, func(available bool) {
		value := AvailabilityOffline
		if available {
			value = AvailabilityOnline
		}
		d.setAvailability(dev, value, SourceEntity, d.now())
	})
	if err != nil {
		d.log.Warn("failed to watch an entity's availability", zap.String("device", name), zap.Error(err))
		return
	}
	dev.mu.Lock()
	dev.drop = append(dev.drop, dropAvail)
	dev.mu.Unlock()
}

// subscribe opens a device's topic filters on a driver, appending the drop
// handles. A per-filter failure is logged and skipped so one bad filter does
// not silence the rest.
func (d *Devices) subscribe(dev *device, conn *mqtt.Driver) {
	dev.mu.Lock()
	m := dev.model
	dev.mu.Unlock()

	var drops []func()
	for _, filter := range m.TopicList() {
		drop, err := conn.SubscribeMessages(filter, byte(m.StateQoS), func(msg mqtt.Message) {
			d.receive(dev, msg.Topic, msg.Payload, msg.Retained)
		})
		if err != nil {
			d.log.Warn("failed to subscribe an MQTT device",
				zap.String("device", m.Name), zap.String("filter", filter), zap.Error(err))
			continue
		}
		drops = append(drops, drop)
	}

	dev.mu.Lock()
	dev.drop = append(dev.drop, drops...)
	dev.mu.Unlock()
}

// Rebind re-establishes the subscriptions of every device on a connection
// whose driver was replaced. The old handles died with the old driver, and
// Reload cannot see that: the definitions did not change. State is kept — the
// outage already cost whatever was missed, and dropping hours of accumulated
// values would cost more.
func (d *Devices) Rebind(connectionID uuid.UUID) {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	d.mu.Lock()
	var affected []*device
	for _, dev := range d.running {
		dev.mu.Lock()
		if dev.model.ConnectionID == connectionID {
			affected = append(affected, dev)
		}
		dev.mu.Unlock()
	}
	d.mu.Unlock()
	if len(affected) == 0 {
		return
	}

	driver, ok := d.resolve(connectionID)
	if !ok {
		return
	}
	for _, dev := range affected {
		dev.mu.Lock()
		drops := dev.drop
		dev.drop = nil
		name := dev.model.Name
		dev.mu.Unlock()
		for _, drop := range drops {
			drop()
		}
		d.attach(dev, driver)
		d.log.Debug("device rebound to a replaced driver", zap.String("device", name))
	}
}

// stop withdraws a device's subscriptions and forgets its state, first
// writing state not yet stored when save is set, so a rebuilt device restores
// what it had.
func (d *Devices) stop(id uuid.UUID, save bool) {
	d.mu.Lock()
	dev, ok := d.running[id]
	delete(d.running, id)
	d.mu.Unlock()
	if !ok {
		return
	}
	d.mu.Lock()
	persist := d.persist
	d.mu.Unlock()
	if save && persist {
		d.saveDevice(dev)
	}
	dev.mu.Lock()
	drops := dev.drop
	dev.drop = nil
	dev.mu.Unlock()
	for _, drop := range drops {
		drop()
	}
}

// receive decodes one message and merges what it carried. Only the keys the
// decoder returned are touched: a message that says nothing about brightness
// leaves the brightness where it was, which is the whole reason a device exists
// rather than a binding reading a path out of a topic.
func (d *Devices) receive(dev *device, topic string, payload []byte, retained bool) {
	dev.mu.Lock()
	dev.lastMessage = time.Now()
	heardAt := dev.lastMessage
	m := dev.model
	before := copyState(dev.state)
	dev.mu.Unlock()
	// Any message shows the device is alive, including ones the decoder
	// ignores or fails on.
	d.heard(dev, heardAt)

	values, err := d.decode(m, topic, payload, before, retained)
	if err != nil {
		dev.mu.Lock()
		first := dev.failed == ""
		dev.failed = err.Error()
		dev.mu.Unlock()
		// A decoder that is wrong is wrong for every message, so the log says so
		// once rather than once per message.
		if first {
			d.log.Warn("MQTT device decode failed",
				zap.String("device", m.Name), zap.String("topic", topic), zap.Error(err))
		}
		mq.B.Publish(DeviceTopic(m.ID), "mqtt_device_error",
			map[string]any{"device_id": m.ID.String(), "error": err.Error()})
		return
	}
	if values == nil {
		// The decoder said the message was not for it; there is nothing to
		// merge, publish, or hand to a watcher.
		return
	}
	d.mergeValues(dev, topic, values, before, false)
}

// mergeValues folds one report's values into a device's state, then
// publishes, persists, and hands the change to watchers. MQTT devices reach it
// after their state script decodes a message, and merge, since a script may
// report one value per message. Entity devices reach it with every state their
// entity reports, which is the whole value, so replace is set: a value the
// entity no longer reports, such as a stopped player's title, is cleared.
func (d *Devices) mergeValues(dev *device, topic string, values, before map[string]any, replace bool) {
	dev.mu.Lock()
	m := dev.model
	dev.failed = ""
	if replace {
		dev.state = make(map[string]any, len(values))
	}
	for k, v := range values {
		dev.state[k] = v
	}
	if m.Control == controls.KindLocation {
		// since is when the state last moved, which only the relay can tell;
		// a script cannot see the time the previous message arrived.
		dev.state = controls.StampSince(before, dev.state, time.Now()).(map[string]any)
	}
	merged := copyState(dev.state)
	dev.stale = false
	dev.reportedAt = time.Now()
	dev.dirty = true
	reportedAt := dev.reportedAt
	dev.mu.Unlock()

	dev.mu.Lock()
	availability, availabilityAt := dev.availability, dev.availabilityAt
	dev.mu.Unlock()

	mq.B.Publish(DeviceTopic(m.ID), "mqtt_device_state", map[string]any{
		"device_id":       m.ID.String(),
		"topic":           topic,
		"state":           merged,
		"stale":           false,
		"reported_at":     reportedAt.UTC().Format(time.RFC3339Nano),
		"availability":    availability,
		"availability_at": availabilityAt.UTC().Format(time.RFC3339Nano),
	})
	d.fanout(DeviceChange{
		DeviceID: m.ID,
		Name:     m.Name,
		Kind:     m.Control,
		Topic:    topic,
		Values:   values,
		State:    merged,
	})
}

// decode runs one message through the device's script. The payload arrives as
// text when it is text and base64 when it is not, with a flag saying which, the
// same bargain the raw relay makes: nothing here guesses at what bytes mean.
func (d *Devices) decode(
	m models.MQTTDevice,
	topic string,
	payload []byte,
	state map[string]any,
	retained bool,
) (map[string]any, error) {
	text, binary := string(payload), false
	if !utf8.Valid(payload) {
		// Bytes that are not text cannot ride in as text, so they arrive base64
		// with the flag saying so rather than as mojibake.
		text, binary = base64.StdEncoding.EncodeToString(payload), true
	}

	limit := m.StateTimeout()
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	res, err := sandbox.JSRuntime{}.Run(ctx, sandbox.Request{
		Language: sandbox.LangJavaScript,
		Source:   decodeScopePreamble + m.StateScript,
		Topic:    topic,
		Payload:  text,
		Meta:     map[string]any{"binary": binary, "state": state, "retained": retained},
		Network:  sandbox.NetworkNone,
		Limits:   sandbox.Limits{Timeout: limit}.Normalize(),
	})
	if err != nil {
		return nil, err
	}

	switch v := res.Value.(type) {
	case nil:
		// Returning nothing is how a decoder says "this message was not for
		// me", which a device watching a wildcard needs to be able to say.
		return nil, nil
	case map[string]any:
		return v, nil
	default:
		return nil, fmt.Errorf("decoder returned %T, want an object of values", res.Value)
	}
}

// GrantedQoS is the lowest QoS the broker granted any of a device's topic
// filters on the current session; ok is false while none is subscribed.
func (d *Devices) GrantedQoS(m *models.MQTTDevice) (int, bool) {
	driver, ok := d.resolve(m.ConnectionID)
	if !ok {
		return 0, false
	}
	conn, ok := driver.(*mqtt.Driver)
	if !ok {
		return 0, false
	}
	lowest, found := 2, false
	for _, filter := range m.TopicList() {
		if q, ok := conn.GrantedQoS(filter); ok {
			found = true
			if int(q) < lowest {
				lowest = int(q)
			}
		}
	}
	return lowest, found
}

// DeviceTopic builds the broker topic a device's state is published on.
func DeviceTopic(id uuid.UUID) string {
	return fmt.Sprintf("%s:%s", DeviceTopicPrefix, id)
}

// copyState snapshots a device's state so a reader never shares the map the
// decoder writes into.
func copyState(state map[string]any) map[string]any {
	out := make(map[string]any, len(state))
	for k, v := range state {
		out[k] = v
	}
	return out
}

// TestStateScript runs a state script against one sample message without
// touching a running device, and hands back what it printed as well as what it
// returned — the editor's Test button is a debugging tool, so the console lines
// matter as much as the result.
func TestStateScript(
	ctx context.Context,
	script, topic, payload string,
	state map[string]any,
) (map[string]any, []string, error) {
	return TestStateScriptWith(ctx, script, topic, payload, state, false, decodeTimeout)
}

// TestStateScriptWith is TestStateScript with the retained flag the sample
// message carries and the time limit the device runs its script under.
func TestStateScriptWith(
	ctx context.Context,
	script, topic, payload string,
	state map[string]any,
	retained bool,
	limit time.Duration,
) (map[string]any, []string, error) {
	if limit <= 0 {
		limit = decodeTimeout
	}
	// The JavaScript runtime stops a script when its context ends.
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	if state == nil {
		state = map[string]any{}
	}
	text, binary := payload, false
	if !utf8.ValidString(payload) {
		text, binary = base64.StdEncoding.EncodeToString([]byte(payload)), true
	}

	res, err := sandbox.JSRuntime{}.Run(ctx, sandbox.Request{
		Language: sandbox.LangJavaScript,
		Source:   decodeScopePreamble + script,
		Topic:    topic,
		Payload:  text,
		Meta:     map[string]any{"binary": binary, "state": state, "retained": retained},
		Network:  sandbox.NetworkNone,
		Limits:   sandbox.Limits{Timeout: limit}.Normalize(),
	})
	if err != nil {
		return nil, nil, err
	}
	switch v := res.Value.(type) {
	case nil:
		return nil, res.Output, nil
	case map[string]any:
		return v, res.Output, nil
	default:
		return nil, res.Output, fmt.Errorf("state script returned %T, want an object of values", res.Value)
	}
}
