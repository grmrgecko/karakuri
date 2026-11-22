package mqttrelay

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"go.uber.org/zap"
)

// Availability values. Unknown means nothing currently says either way: the
// broker is down, or the device has an availability topic that has not
// spoken since the connection came up.
const (
	AvailabilityOnline  = "online"
	AvailabilityOffline = "offline"
	AvailabilityUnknown = "unknown"
)

// What decided a device's availability.
const (
	SourceConnection = "connection"
	SourceTopic      = "topic"
	SourceInterval   = "interval"
	SourceDefault    = "default"
	// SourceEntity is the connection saying one entity cannot answer, such
	// as a camera receiving no frames.
	SourceEntity = "entity"
)

// availabilitySweep is how often devices with an expected reporting interval
// are checked. The shortest interval allowed is 60 seconds, so a device is
// marked offline at most 15 seconds late.
const availabilitySweep = 15 * time.Second

// StatusWatcher reports a connection's status and every later change, the
// shape connmgr.Manager.WatchStatus has.
type StatusWatcher interface {
	WatchStatus(id uuid.UUID) (integrations.Status, bool, <-chan integrations.Status, func())
}

// AvailabilityChange is one device's availability moving, handed to
// availability watchers.
type AvailabilityChange struct {
	DeviceID uuid.UUID
	Name     string
	Previous string
	Current  string
	Source   string
	At       time.Time
}

// SetStatusWatcher wires connection status into availability: a connection
// that leaves connected makes its devices unknown at once. Call it before the
// first Reload; without it every connection counts as up.
func (d *Devices) SetStatusWatcher(w StatusWatcher) {
	d.mu.Lock()
	d.statusWatcher = w
	d.mu.Unlock()
}

// WatchAvailability registers an availability change handler and returns the
// function that removes it. Handlers run on the goroutine that observed the
// change and must not block.
func (d *Devices) WatchAvailability(fn func(AvailabilityChange)) func() {
	d.watchMu.Lock()
	id := d.nextID
	d.nextID++
	d.availWatchers[id] = fn
	d.watchMu.Unlock()
	return func() {
		d.watchMu.Lock()
		delete(d.availWatchers, id)
		d.watchMu.Unlock()
	}
}

// StartAvailabilitySweep marks devices offline that have been quiet longer
// than their expected interval, until stop closes.
func (d *Devices) StartAvailabilitySweep(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(availabilitySweep)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				d.sweepAvailability(d.now())
			case <-stop:
				return
			}
		}
	}()
}

// now reads the runner's clock, which tests replace.
func (d *Devices) now() time.Time {
	if d.clock != nil {
		return d.clock()
	}
	return time.Now()
}

// sweepAvailability applies the expected reporting interval at one moment.
// The interval only takes a device offline that is otherwise online: a topic
// that already says offline, or a broker that is down, says more.
func (d *Devices) sweepAvailability(now time.Time) {
	d.mu.Lock()
	devices := make([]*device, 0, len(d.running))
	for _, dev := range d.running {
		devices = append(devices, dev)
	}
	d.mu.Unlock()
	for _, dev := range devices {
		dev.mu.Lock()
		every := time.Duration(dev.model.ExpectEveryS) * time.Second
		last := dev.lastMessage
		if dev.expectFrom.After(last) {
			last = dev.expectFrom
		}
		due := every > 0 && dev.availability == AvailabilityOnline && now.Sub(last) > every
		dev.mu.Unlock()
		if due {
			d.setAvailability(dev, AvailabilityOffline, SourceInterval, now)
		}
	}
}

// connectionUp reports whether a connection is known to be connected,
// starting a status watch for it on first use.
func (d *Devices) connectionUp(id uuid.UUID) bool {
	d.mu.Lock()
	w := d.statusWatcher
	if w == nil {
		d.mu.Unlock()
		return true
	}
	if up, watching := d.connUp[id]; watching {
		d.mu.Unlock()
		return up
	}
	d.mu.Unlock()

	status, _, updates, cancel := w.WatchStatus(id)
	up := status.State == integrations.StateConnected
	d.mu.Lock()
	if _, raced := d.connUp[id]; raced {
		d.mu.Unlock()
		cancel()
		return d.connectionUp(id)
	}
	d.connUp[id] = up
	d.connCancel = append(d.connCancel, cancel)
	d.mu.Unlock()

	go func() {
		for status := range updates {
			d.connectionChanged(id, status.State == integrations.StateConnected)
		}
	}()
	return up
}

// connectionChanged applies a connection going up or down to its devices.
func (d *Devices) connectionChanged(id uuid.UUID, up bool) {
	d.mu.Lock()
	was := d.connUp[id]
	d.connUp[id] = up
	var devices []*device
	for _, dev := range d.running {
		if dev.model.ConnectionID == id {
			devices = append(devices, dev)
		}
	}
	d.mu.Unlock()
	if was == up {
		return
	}
	now := d.now()
	for _, dev := range devices {
		if !up {
			d.setAvailability(dev, AvailabilityUnknown, SourceConnection, now)
			continue
		}
		dev.mu.Lock()
		hasTopic := dev.model.AvailabilityTopic != ""
		entityAvail := dev.entityAvail
		dev.expectFrom = now
		dev.mu.Unlock()
		// The connection reports the entity's own availability once it is
		// up, so coming up says nothing about it yet.
		if entityAvail {
			continue
		}
		// A device with a topic waits for it: the broker delivers a retained
		// availability message on resubscribe, and anything else would be a
		// guess.
		if !hasTopic {
			d.setAvailability(dev, AvailabilityOnline, SourceDefault, now)
		}
	}
}

// sourceApplies reports whether the rule that decided the device's
// availability is one it still has. An availability topic or expected interval
// removed from the definition can no longer confirm or replace what it
// decided. The caller holds dev.mu.
func (dev *device) sourceApplies() bool {
	switch dev.availabilitySource {
	case SourceTopic:
		return dev.model.AvailabilityTopic != ""
	case SourceInterval:
		return dev.model.ExpectEveryS > 0
	case SourceEntity:
		return dev.entityAvail
	}
	return true
}

// intervalRemoved puts a running device back online after its expected
// interval was removed. The interval only takes offline a device that is
// otherwise online, so online is what is left without it.
func (d *Devices) intervalRemoved(dev *device) {
	dev.mu.Lock()
	source := SourceDefault
	if dev.model.AvailabilityTopic != "" {
		source = SourceTopic
	}
	dev.mu.Unlock()
	d.setAvailability(dev, AvailabilityOnline, source, d.now())
}

// initialAvailability sets a starting device's availability, keeping one
// restored from storage until something confirms or replaces it. One decided
// by a rule the device no longer has is worked out again instead.
func (d *Devices) initialAvailability(dev *device) {
	up := d.connectionUp(dev.model.ConnectionID)
	now := d.now()
	dev.mu.Lock()
	defer dev.mu.Unlock()
	dev.expectFrom = now
	// A restored outage is the exception: the connection is up now, no
	// transition will come to clear it, and a message does not overrule a
	// source of connection.
	outageOver := up && dev.availabilitySource == SourceConnection
	if dev.availability != "" && dev.availabilityStale && !outageOver && dev.sourceApplies() {
		return
	}
	dev.availabilityStale = false
	switch {
	case !up:
		dev.availability, dev.availabilitySource = AvailabilityUnknown, SourceConnection
	case dev.entityAvail:
		dev.availability, dev.availabilitySource = AvailabilityUnknown, SourceEntity
	case dev.model.AvailabilityTopic != "":
		dev.availability, dev.availabilitySource = AvailabilityUnknown, SourceTopic
	default:
		dev.availability, dev.availabilitySource = AvailabilityOnline, SourceDefault
	}
	dev.availabilityAt = now
}

// subscribeAvailability listens on a device's availability topic.
func (d *Devices) subscribeAvailability(dev *device, conn *mqtt.Driver) {
	dev.mu.Lock()
	topic := dev.model.AvailabilityTopic
	name := dev.model.Name
	qos := byte(dev.model.StateQoS)
	dev.mu.Unlock()
	if topic == "" {
		return
	}
	drop, err := conn.Subscribe(topic, qos, func(_ string, payload []byte) {
		d.receiveAvailability(dev, payload)
	})
	if err != nil {
		d.log.Warn("failed to subscribe an MQTT device's availability topic",
			zap.String("device", name), zap.String("topic", topic), zap.Error(err))
		return
	}
	dev.mu.Lock()
	dev.drop = append(dev.drop, drop)
	dev.mu.Unlock()
}

// receiveAvailability reads one availability message.
func (d *Devices) receiveAvailability(dev *device, payload []byte) {
	dev.mu.Lock()
	online, offline := dev.model.AvailabilityOnline, dev.model.AvailabilityOffline
	dev.mu.Unlock()
	switch matchAvailability(payload, orDefault(online, "online"), orDefault(offline, "offline")) {
	case AvailabilityOnline:
		d.setAvailability(dev, AvailabilityOnline, SourceTopic, d.now())
	case AvailabilityOffline:
		d.setAvailability(dev, AvailabilityOffline, SourceTopic, d.now())
	default:
		dev.mu.Lock()
		dev.availabilityNote = truncate(string(payload), 200)
		dev.mu.Unlock()
	}
}

// matchAvailability reads an availability payload: the configured strings,
// or a JSON object whose state is one of them, which is Zigbee2MQTT's form
// since 1.30. Anything else is neither.
func matchAvailability(payload []byte, online, offline string) string {
	text := strings.TrimSpace(string(payload))
	if text == online {
		return AvailabilityOnline
	}
	if text == offline {
		return AvailabilityOffline
	}
	var obj struct {
		State string `json:"state"`
	}
	if strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &obj) == nil {
		switch obj.State {
		case online:
			return AvailabilityOnline
		case offline:
			return AvailabilityOffline
		}
	}
	return ""
}

// heard applies a state message to availability: a device with no topic is
// online when it speaks, and one taken offline by its interval comes back.
func (d *Devices) heard(dev *device, now time.Time) {
	dev.mu.Lock()
	hasTopic := dev.model.AvailabilityTopic != ""
	every := dev.model.ExpectEveryS
	current, source := dev.availability, dev.availabilitySource
	stale := dev.availabilityStale
	entityAvail := dev.entityAvail
	dev.mu.Unlock()
	if entityAvail {
		return
	}
	switch {
	case current == AvailabilityOffline && source == SourceInterval:
		d.setAvailability(dev, AvailabilityOnline, SourceInterval, now)
	case !hasTopic && (current != AvailabilityOnline || stale) && source != SourceConnection:
		src := SourceDefault
		if every > 0 {
			src = SourceInterval
		}
		d.setAvailability(dev, AvailabilityOnline, src, now)
	}
}

// setAvailability records a device's availability and announces a change.
// Setting the same value again only confirms a restored one.
func (d *Devices) setAvailability(dev *device, value, source string, now time.Time) {
	dev.mu.Lock()
	previous := dev.availability
	dev.availabilityStale = false
	if previous == value {
		dev.availabilitySource = source
		dev.mu.Unlock()
		return
	}
	dev.availability, dev.availabilitySource, dev.availabilityAt = value, source, now
	dev.dirty = true
	id, name := dev.model.ID, dev.model.Name
	dev.mu.Unlock()

	d.log.Debug("MQTT device availability changed",
		zap.String("device", name), zap.String("availability", value), zap.String("source", source))
	mq.B.Publish(DeviceTopic(id), "mqtt_device_availability", map[string]any{
		"device_id":    id.String(),
		"availability": value,
		"previous":     previous,
		"source":       source,
		"at":           now.UTC().Format(time.RFC3339Nano),
	})

	change := AvailabilityChange{DeviceID: id, Name: name, Previous: previous, Current: value, Source: source, At: now}
	d.watchMu.Lock()
	handlers := make([]func(AvailabilityChange), 0, len(d.availWatchers))
	for _, fn := range d.availWatchers {
		handlers = append(handlers, fn)
	}
	d.watchMu.Unlock()
	for _, fn := range handlers {
		fn(change)
	}
}

// Availability returns a running device's availability and when it last
// changed; false when the device is not running.
func (d *Devices) Availability(id uuid.UUID) (string, time.Time, bool) {
	d.mu.Lock()
	dev, ok := d.running[id]
	d.mu.Unlock()
	if !ok {
		return "", time.Time{}, false
	}
	dev.mu.Lock()
	defer dev.mu.Unlock()
	return dev.availability, dev.availabilityAt, true
}

// truncate shortens s to at most n bytes for display.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
