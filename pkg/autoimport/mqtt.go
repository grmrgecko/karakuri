package autoimport

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/hadiscovery"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/grmrgecko/karakuri/pkg/zigbee2mqtt"
	"go.uber.org/zap"
)

// queueSize bounds the messages waiting for the importer. A broker replays
// every retained discovery config at once on subscribe, and a large house
// has a few hundred.
const queueSize = 4096

// discoveryKey and z2mKey are the source keys of MQTT-sourced devices.
func discoveryKey(key string) string { return "ha:" + key }
func z2mKey(ieee string) string      { return "z2m:" + ieee }

// mqttFollower holds what an MQTT connection has announced.
type mqttFollower struct {
	f   *follower
	drv *mqtt.Driver

	queue chan mqtt.Message

	mu sync.Mutex
	// configs are the current discovery configs by discovery key, with the
	// mapping each became and the activity subscriptions it holds.
	configs map[string]*candidate
	// byActivity maps an activity topic to the discovery keys it reports.
	byActivity map[string]map[string]bool
	// z2mDevices are Zigbee2MQTT's devices by IEEE address, and z2mNames
	// their IEEE addresses by friendly name.
	z2mDevices map[string]zigbee2mqtt.Device
	z2mNames   map[string]string
}

// candidate is one discovery config.
type candidate struct {
	cfg    hadiscovery.Config
	mapped hadiscovery.Mapped
	drops  []func()
}

// followMQTT subscribes discovery and Zigbee2MQTT on an MQTT connection.
func (f *follower) followMQTT(drv *mqtt.Driver) {
	mf := &mqttFollower{f: f, drv: drv, queue: make(chan mqtt.Message, queueSize),
		configs: map[string]*candidate{}, byActivity: map[string]map[string]bool{},
		z2mDevices: map[string]zigbee2mqtt.Device{}, z2mNames: map[string]string{}}
	f.mqttFollow = mf
	done := make(chan struct{})
	go mf.work(done)
	f.addStop(func() {
		close(done)
		mf.mu.Lock()
		for _, c := range mf.configs {
			for _, drop := range c.drops {
				drop()
			}
		}
		mf.configs = map[string]*candidate{}
		mf.mu.Unlock()
	})

	enqueue := func(msg mqtt.Message) {
		select {
		case mf.queue <- msg:
		default:
			f.m.log.Warn("auto import queue full; dropping a message", zap.String("connection", f.name), zap.String("topic", msg.Topic))
		}
	}
	filters := []string{f.settings.DiscoveryPrefix + "/#"}
	if base := f.settings.Z2MBase; base != "" {
		filters = append(filters, base+"/bridge/devices", base+"/+", base+"/+/availability")
	}
	for _, filter := range filters {
		drop, err := drv.SubscribeMessages(filter, 0, enqueue)
		if err != nil {
			f.m.log.Warn("auto import could not subscribe", zap.String("connection", f.name), zap.String("filter", filter), zap.Error(err))
			continue
		}
		f.addStop(drop)
	}
}

// work handles queued messages one at a time, off the driver's delivery
// goroutine, since adding a device reloads the runner and subscribes.
func (mf *mqttFollower) work(done <-chan struct{}) {
	for {
		select {
		case msg := <-mf.queue:
			mf.handle(msg)
		case <-done:
			return
		}
	}
}

// handle routes one message.
func (mf *mqttFollower) handle(msg mqtt.Message) {
	s := mf.f.settings
	switch {
	case strings.HasPrefix(msg.Topic, s.DiscoveryPrefix+"/"):
		mf.discovery(msg)
	case s.Z2MBase != "" && msg.Topic == s.Z2MBase+"/bridge/devices":
		mf.z2mList(msg)
	case s.Z2MBase != "" && strings.HasPrefix(msg.Topic, s.Z2MBase+"/"):
		mf.z2mActivity(msg)
	default:
		mf.activity(msg)
	}
}

// discovery applies one discovery config message.
func (mf *mqttFollower) discovery(msg mqtt.Message) {
	f := mf.f
	cfgs, err := hadiscovery.Parse(f.settings.DiscoveryPrefix, msg.Topic, msg.Payload)
	if err != nil {
		f.skip(msg.Topic, msg.Topic, err.Error())
		return
	}
	for _, cfg := range cfgs {
		if cfg.Removed {
			mf.withdraw(cfg.Topic)
			continue
		}
		if f.settings.Z2MBase != "" && cfg.FromZigbee2MQTT() {
			// The bridge list describes the same device with more detail.
			continue
		}
		mapped, err := hadiscovery.Map(cfg, f.id)
		if err != nil {
			f.skip(discoveryKey(cfg.Key), cfg.Name(), err.Error())
			continue
		}
		mf.remember(cfg, mapped)
		if !msg.Retained {
			f.report(discoveryKey(cfg.Key))
		}
		mf.ensureDiscovery(cfg.Key, !msg.Retained)
	}
}

// remember stores a config and subscribes its activity topics, replacing an
// earlier version of it.
func (mf *mqttFollower) remember(cfg hadiscovery.Config, mapped hadiscovery.Mapped) {
	mf.mu.Lock()
	old := mf.configs[cfg.Key]
	if old != nil && sameTopics(old.mapped.Activity, mapped.Activity) {
		old.cfg, old.mapped = cfg, mapped
		mf.mu.Unlock()
		return
	}
	if old != nil {
		mf.unindex(cfg.Key, old)
	}
	c := &candidate{cfg: cfg, mapped: mapped}
	mf.configs[cfg.Key] = c
	for _, t := range mapped.Activity {
		if mf.byActivity[t] == nil {
			mf.byActivity[t] = map[string]bool{}
		}
		mf.byActivity[t][cfg.Key] = true
	}
	mf.mu.Unlock()
	if old != nil {
		for _, drop := range old.drops {
			drop()
		}
	}
	for _, t := range mapped.Activity {
		drop, err := mf.drv.SubscribeMessages(t, 0, func(msg mqtt.Message) {
			select {
			case mf.queue <- msg:
			default:
			}
		})
		if err != nil {
			mf.f.m.log.Warn("auto import could not follow a device topic", zap.String("topic", t), zap.Error(err))
			continue
		}
		mf.mu.Lock()
		c.drops = append(c.drops, drop)
		mf.mu.Unlock()
	}
}

// unindex removes a candidate's activity topics from the index. Callers hold
// mu.
func (mf *mqttFollower) unindex(key string, c *candidate) {
	for _, t := range c.mapped.Activity {
		delete(mf.byActivity[t], key)
		if len(mf.byActivity[t]) == 0 {
			delete(mf.byActivity, t)
		}
	}
}

// withdraw handles an emptied config topic: its devices are marked missing.
func (mf *mqttFollower) withdraw(topic string) {
	mf.mu.Lock()
	var keys []string
	for key, c := range mf.configs {
		if c.cfg.Topic == topic || key == topic {
			keys = append(keys, key)
			mf.unindex(key, c)
			for _, drop := range c.drops {
				drop()
			}
			delete(mf.configs, key)
		}
	}
	mf.mu.Unlock()
	for _, key := range keys {
		if d := mf.deviceFor(key); d != nil && !d.Missing {
			if err := mqttrelay.SetMissing(d.ID, true); err != nil {
				mf.f.m.log.Error("failed to mark a withdrawn device missing", zap.String("device", d.Name), zap.Error(err))
			}
		}
	}
}

// activity reads a message on a discovery device's state or availability
// topic. Only live messages count, and an availability message counts only
// when it says online.
func (mf *mqttFollower) activity(msg mqtt.Message) {
	if msg.Retained {
		return
	}
	mf.mu.Lock()
	var keys []string
	for key := range mf.byActivity[msg.Topic] {
		c := mf.configs[key]
		if c == nil {
			continue
		}
		if c.mapped.AvailabilityTopic == msg.Topic && !isOnline(msg.Payload, c.mapped.Online) {
			continue
		}
		keys = append(keys, key)
	}
	mf.mu.Unlock()
	for _, key := range keys {
		mf.f.report(discoveryKey(key))
		mf.ensureDiscovery(key, true)
	}
}

// isOnline matches an availability payload against the online payload, as
// the plain text or a JSON object's state.
func isOnline(payload []byte, online string) bool {
	text := strings.TrimSpace(string(payload))
	if text == online {
		return true
	}
	var obj struct {
		State string `json:"state"`
	}
	return strings.HasPrefix(text, "{") && json.Unmarshal([]byte(text), &obj) == nil && obj.State == online
}

// deviceFor finds the device made from a discovery key.
func (mf *mqttFollower) deviceFor(key string) *models.MQTTDevice {
	devices, err := models.ListMQTTDevicesOnConnection(mf.f.id)
	if err != nil {
		return nil
	}
	for _, d := range devices {
		if d.DiscoveryKey == key {
			return d
		}
	}
	return nil
}

// ensureDiscovery adds a discovery config's device when it has none, and
// keeps an existing device's topics and availability in step with the
// config. A tombstoned device returns only on live activity.
func (mf *mqttFollower) ensureDiscovery(key string, live bool) {
	f := mf.f
	mf.mu.Lock()
	c := mf.configs[key]
	mf.mu.Unlock()
	if c == nil {
		return
	}
	if d := mf.deviceFor(key); d != nil {
		mf.refresh(d, c.mapped)
		return
	}
	if !mf.allowed(discoveryKey(key), live) {
		return
	}
	def := c.mapped.Definition
	def.Name = uniqueName(def.Name)
	saved, err := mqttrelay.SaveDefinition(uuid.Nil, def, mqttrelay.Origin{DiscoveryKey: key, Auto: true})
	if err != nil {
		f.skip(discoveryKey(key), def.Name, err.Error())
		return
	}
	f.unskip(discoveryKey(key))
	f.imported([]string{saved.Name})
}

// refresh updates an existing discovery device from a newer config: its
// topics and availability topic follow, and a missing mark clears. The kind,
// options, and scripts are left alone, since the author may have edited them.
func (mf *mqttFollower) refresh(d *models.MQTTDevice, mapped hadiscovery.Mapped) {
	def := mqttrelay.DefinitionOf(d)
	changed := !sameTopics(def.Topics, mapped.Definition.Topics) || def.AvailabilityTopic != mapped.Definition.AvailabilityTopic
	if changed {
		def.Topics = mapped.Definition.Topics
		def.AvailabilityTopic = mapped.Definition.AvailabilityTopic
		def.AvailabilityOnline = mapped.Definition.AvailabilityOnline
		def.AvailabilityOffline = mapped.Definition.AvailabilityOffline
		if _, err := mqttrelay.SaveDefinition(d.ID, def, mqttrelay.Origin{}); err != nil {
			mf.f.m.log.Error("failed to follow a changed discovery config", zap.String("device", d.Name), zap.Error(err))
		}
	}
	if d.Missing {
		if err := mqttrelay.SetMissing(d.ID, false); err != nil {
			mf.f.m.log.Error("failed to clear a missing mark", zap.String("device", d.Name), zap.Error(err))
		}
	}
}

// allowed reports whether a device with no record may be imported: it may
// unless auto import removed it for not reporting, in which case only live
// activity brings it back, clearing the tombstone.
func (mf *mqttFollower) allowed(key string, live bool) bool {
	stones, err := models.ListAutoImportTombstones(mf.f.id)
	if err != nil {
		mf.f.m.log.Error("failed to read auto import tombstones", zap.Error(err))
		return false
	}
	if !stones[key] {
		return true
	}
	if !live {
		return false
	}
	if err := models.DeleteAutoImportTombstone(mf.f.id, key); err != nil {
		mf.f.m.log.Error("failed to clear an auto import tombstone", zap.Error(err))
		return false
	}
	return true
}

// z2mList applies Zigbee2MQTT's device list.
func (mf *mqttFollower) z2mList(msg mqtt.Message) {
	list, err := zigbee2mqtt.ParseDevices(msg.Payload)
	if err != nil {
		mf.f.m.log.Warn("unreadable Zigbee2MQTT device list", zap.String("connection", mf.f.name), zap.Error(err))
		return
	}
	mf.mu.Lock()
	mf.z2mDevices = map[string]zigbee2mqtt.Device{}
	mf.z2mNames = map[string]string{}
	for _, d := range list {
		mf.z2mDevices[d.IEEEAddress] = d
		mf.z2mNames[d.FriendlyName] = d.IEEEAddress
	}
	mf.mu.Unlock()
	for _, d := range list {
		if d.IsCoordinator() {
			continue
		}
		mf.ensureZ2M(d.IEEEAddress, false)
	}
}

// z2mActivity reads a live message on a Zigbee2MQTT device's topic or its
// availability topic.
func (mf *mqttFollower) z2mActivity(msg mqtt.Message) {
	if msg.Retained {
		return
	}
	rest := strings.TrimPrefix(msg.Topic, mf.f.settings.Z2MBase+"/")
	name, avail := strings.CutSuffix(rest, "/availability")
	if name == "bridge" || strings.Contains(name, "/") {
		return
	}
	if avail && !isOnline(msg.Payload, "online") {
		return
	}
	mf.mu.Lock()
	ieee := mf.z2mNames[name]
	mf.mu.Unlock()
	if ieee == "" {
		return
	}
	mf.f.report(z2mKey(ieee))
	mf.ensureZ2M(ieee, true)
}

// ensureZ2M adds a Zigbee2MQTT device when it has no karakuri device.
func (mf *mqttFollower) ensureZ2M(ieee string, live bool) {
	f := mf.f
	mf.mu.Lock()
	d, ok := mf.z2mDevices[ieee]
	mf.mu.Unlock()
	if !ok {
		return
	}
	devices, err := models.ListMQTTDevicesOnConnection(f.id)
	if err != nil {
		return
	}
	for _, m := range devices {
		if m.Z2MIEEE == ieee && m.Z2MBase == zigbee2mqtt.NormalizeBase(f.settings.Z2MBase) {
			return
		}
	}
	if can, why := d.CanAdd(); !can {
		f.skip(z2mKey(ieee), d.FriendlyName, why)
		return
	}
	if !mf.allowed(z2mKey(ieee), live) {
		return
	}
	result, err := zigbee2mqtt.AddFrom([]zigbee2mqtt.Device{d}, f.id, f.settings.Z2MBase, []string{ieee}, true)
	if err != nil {
		f.skip(z2mKey(ieee), d.FriendlyName, err.Error())
		return
	}
	for _, fail := range result.Failed {
		f.skip(z2mKey(ieee), d.FriendlyName, fail.Error)
	}
	var names []string
	for _, v := range result.Added {
		names = append(names, v.Name)
		f.unskip(z2mKey(ieee))
	}
	f.imported(names)
}

// reimport adds back devices the author deleted while their source still
// announces them: every known config and Zigbee2MQTT device with no device.
// Tombstoned ones wait for live activity.
func (mf *mqttFollower) reimport() {
	mf.mu.Lock()
	keys := make([]string, 0, len(mf.configs))
	for key := range mf.configs {
		keys = append(keys, key)
	}
	ieees := make([]string, 0, len(mf.z2mDevices))
	for ieee, d := range mf.z2mDevices {
		if !d.IsCoordinator() {
			ieees = append(ieees, ieee)
		}
	}
	mf.mu.Unlock()
	for _, key := range keys {
		mf.ensureDiscovery(key, false)
	}
	for _, ieee := range ieees {
		mf.ensureZ2M(ieee, false)
	}
}

// sameTopics compares two topic lists as sets.
func sameTopics(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	set := map[string]bool{}
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		if !set[t] {
			return false
		}
	}
	return true
}

// uniqueName returns name, or name with the lowest free numeric suffix.
func uniqueName(name string) string {
	devices, err := models.ListMQTTDevices()
	if err != nil {
		return name
	}
	taken := map[string]bool{}
	for _, d := range devices {
		taken[strings.ToLower(d.Name)] = true
	}
	if !taken[strings.ToLower(name)] {
		return name
	}
	for i := 2; ; i++ {
		candidate := name + " " + strconv.Itoa(i)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}
