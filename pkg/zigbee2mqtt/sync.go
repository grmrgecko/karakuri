package zigbee2mqtt

import (
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"go.uber.org/zap"
)

// Sync keeps devices added from Zigbee2MQTT in step with its device list. It
// holds one subscription to <base>/bridge/devices per connection and base
// topic that added devices use; Zigbee2MQTT republishes the retained list
// after any rename, removal, or interview, so the list alone is enough.
type Sync struct {
	resolve func(uuid.UUID) (Subscriber, bool)
	log     *zap.Logger

	mu   sync.Mutex
	subs map[syncKey]func()
	// names are the friendly names of the last device list seen per
	// connection and base topic, by IEEE address. A rename is the difference
	// between two lists, which a device with no availability topic cannot
	// say on its own.
	names map[syncKey]map[string]string
}

// syncKey is one connection and base topic.
type syncKey struct {
	conn uuid.UUID
	base string
}

// NewSync builds a sync over a connection resolver.
func NewSync(resolve func(uuid.UUID) (Subscriber, bool), log *zap.Logger) *Sync {
	return &Sync{resolve: resolve, log: log, subs: map[syncKey]func(){}, names: map[syncKey]map[string]string{}}
}

// Refresh subscribes the device lists that added devices need and drops the
// rest. Call it after devices change and when a connection starts.
func (s *Sync) Refresh() {
	devices, err := models.ListMQTTDevices()
	if err != nil {
		s.log.Error("failed to load MQTT devices for Zigbee2MQTT sync", zap.Error(err))
		return
	}
	need := map[syncKey]bool{}
	for _, m := range devices {
		if m.Z2MIEEE != "" {
			need[syncKey{conn: m.ConnectionID, base: m.Z2MBase}] = true
		}
	}

	s.mu.Lock()
	var drops []func()
	for key, drop := range s.subs {
		if !need[key] {
			drops = append(drops, drop)
			delete(s.subs, key)
		}
	}
	var missing []syncKey
	for key := range need {
		if _, ok := s.subs[key]; !ok {
			missing = append(missing, key)
		}
	}
	s.mu.Unlock()
	for _, drop := range drops {
		drop()
	}

	for _, key := range missing {
		sub, ok := s.resolve(key.conn)
		if !ok {
			// The connection has not started; Rebind subscribes when it does.
			continue
		}
		key := key
		drop, err := sub.Subscribe(key.base+"/bridge/devices", 0, func(_ string, payload []byte) {
			// Applying saves devices, which refreshes this sync; doing that on
			// the driver's delivery goroutine would re-enter its subscribe.
			go s.apply(key, payload)
		})
		if err != nil {
			s.log.Warn("failed to follow a Zigbee2MQTT device list",
				zap.String("base", key.base), zap.Error(err))
			continue
		}
		s.mu.Lock()
		if _, raced := s.subs[key]; raced {
			s.mu.Unlock()
			drop()
			continue
		}
		s.subs[key] = drop
		s.mu.Unlock()
	}
}

// Rebind drops a connection's subscriptions, whose handles died with its old
// driver, and subscribes again.
func (s *Sync) Rebind(conn uuid.UUID) {
	s.mu.Lock()
	for key := range s.subs {
		if key.conn == conn {
			delete(s.subs, key)
		}
	}
	s.mu.Unlock()
	s.Refresh()
}

// Stop drops every subscription.
func (s *Sync) Stop() {
	s.mu.Lock()
	subs := s.subs
	s.subs = map[syncKey]func(){}
	s.mu.Unlock()
	for _, drop := range subs {
		drop()
	}
}

// apply follows one device list.
func (s *Sync) apply(key syncKey, payload []byte) {
	list, err := ParseDevices(payload)
	if err != nil {
		s.log.Warn("unreadable Zigbee2MQTT device list", zap.String("base", key.base), zap.Error(err))
		return
	}
	byIEEE := map[string]Device{}
	names := make(map[string]string, len(list))
	for _, d := range list {
		byIEEE[d.IEEEAddress] = d
		names[d.IEEEAddress] = d.FriendlyName
	}
	s.mu.Lock()
	before := s.names[key]
	s.names[key] = names
	s.mu.Unlock()
	devices, err := models.ListMQTTDevices()
	if err != nil {
		s.log.Error("failed to load MQTT devices for Zigbee2MQTT sync", zap.Error(err))
		return
	}
	for _, m := range devices {
		if m.Z2MIEEE == "" || m.ConnectionID != key.conn || m.Z2MBase != key.base {
			continue
		}
		d, found := byIEEE[m.Z2MIEEE]
		if found != !m.Missing {
			if err := mqttrelay.SetMissing(m.ID, !found); err != nil {
				s.log.Error("failed to mark a Zigbee2MQTT device missing", zap.String("device", m.Name), zap.Error(err))
			} else {
				s.log.Info("Zigbee2MQTT device presence changed", zap.String("device", m.Name), zap.Bool("missing", !found))
			}
		}
		if found {
			// The name in the list before this one is the old name. Without
			// an earlier list, as after a restart, or when its name moved
			// nothing, the generated availability topic still holds it.
			if !s.follow(m, key.base, before[m.Z2MIEEE], d.FriendlyName) {
				s.follow(m, key.base, friendlyFromAvailability(m.AvailabilityTopic, key.base), d.FriendlyName)
			}
		}
	}
}

// follow rewrites the topics of a device whose friendly name changed from old.
// Only topics that name the old friendly name exactly are changed, so a topic
// the author added by hand stays as written and an availability topic the
// author cleared stays cleared. It reports whether a topic moved.
func (s *Sync) follow(m *models.MQTTDevice, base, old, friendly string) bool {
	if old == "" || old == friendly {
		return false
	}
	def := mqttrelay.DefinitionOf(m)
	moved := false
	for i, t := range def.Topics {
		if t == base+"/"+old {
			def.Topics[i] = base + "/" + friendly
			moved = true
		}
	}
	if def.AvailabilityTopic == base+"/"+old+"/availability" {
		def.AvailabilityTopic = base + "/" + friendly + "/availability"
		moved = true
	}
	if !moved {
		return false
	}
	if _, err := mqttrelay.SaveDefinition(m.ID, def, mqttrelay.Origin{}); err != nil {
		s.log.Error("failed to follow a Zigbee2MQTT rename", zap.String("device", m.Name), zap.Error(err))
		return false
	}
	s.log.Info("followed a Zigbee2MQTT rename",
		zap.String("device", m.Name), zap.String("from", old), zap.String("to", friendly))
	return true
}

// friendlyFromAvailability reads the friendly name a device's generated
// availability topic was built from, or "" when the topic is not one.
func friendlyFromAvailability(topic, base string) string {
	rest, ok := strings.CutPrefix(topic, base+"/")
	if !ok {
		return ""
	}
	name, ok := strings.CutSuffix(rest, "/availability")
	if !ok {
		return ""
	}
	return name
}
