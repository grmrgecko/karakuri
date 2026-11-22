// Package autoimport adds the devices a connection reports without anyone
// picking them, and removes the ones its source stops reporting. It follows
// each running connection whose config turns auto import on: the entity list
// of an ESPHome, Frigate, Home Assistant, or openHAB connection, and Home
// Assistant MQTT discovery and Zigbee2MQTT's device list on an MQTT
// connection.
package autoimport

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/entitydevices"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"go.uber.org/zap"
)

// sweepInterval is how often the importer refreshes report times and checks
// for devices to remove. "Remove after" is at least an hour, so a minute is
// fine-grained enough.
const sweepInterval = time.Minute

// flushSlack is how far a device's in-memory report time must run ahead of
// the stored one before the sweep writes it, so a device reporting every
// second costs one write a minute, not sixty.
const flushSlack = 30 * time.Second

// maxSkipped bounds the skipped list kept per connection.
const maxSkipped = 200

// Skip is one reported device auto import did not add, and why.
type Skip struct {
	Key    string `json:"key" description:"Source identity of the skipped device."`
	Name   string `json:"name" description:"Name the source gives it."`
	Reason string `json:"reason" description:"Why it was not added."`
}

// Status is what a connection's auto import has done since the server
// started.
type Status struct {
	Enabled       bool       `json:"enabled" description:"Whether auto import is on for the connection."`
	RemoveAfter   string     `json:"remove_after,omitempty" description:"How long a device may go unreported before removal; empty never removes."`
	Imported      int        `json:"imported" description:"Devices auto import created that still exist."`
	Skipped       []Skip     `json:"skipped" description:"Reported devices that were not added, with reasons."`
	LastImportAt  *time.Time `json:"last_import_at,omitempty" description:"When auto import last added a device."`
	LastRemovalAt *time.Time `json:"last_removal_at,omitempty" description:"When auto import last removed a device."`
}

// M is the server's manager, set by serve. REST and MCP read status from it.
var M *Manager

// Manager follows every connection with auto import on.
type Manager struct {
	log *zap.Logger
	// clock is replaced in tests.
	clock func() time.Time
	// OnRemove runs after a device is removed, for cleanup outside the device
	// store such as history ownership.
	OnRemove func(id uuid.UUID)

	mu    sync.Mutex
	conns map[uuid.UUID]*follower
}

// follower is one connection's auto import.
type follower struct {
	m        *Manager
	id       uuid.UUID
	name     string
	kind     string
	settings integrations.AutoImportSettings
	status   func() integrations.Status

	// src is the entity list of an entity connection; nil for MQTT.
	src entitydevices.Source
	// mqttFollow holds the discovery and Zigbee2MQTT state of an MQTT
	// connection; nil otherwise.
	mqttFollow *mqttFollower

	stops []func()

	mu sync.Mutex
	// live is when each source key was last reported, by live MQTT activity
	// or presence in an entity list while connected.
	live map[string]time.Time
	// connectedSince is when the connection last connected, zero while down.
	connectedSince time.Time
	skipped        map[string]Skip
	lastImport     time.Time
	lastRemoval    time.Time
	// importMu serializes imports, so two list callbacks cannot both add one
	// entity.
	importMu sync.Mutex
}

// New builds a manager.
func New(log *zap.Logger) *Manager {
	return &Manager{log: log, clock: time.Now, conns: map[uuid.UUID]*follower{}}
}

// now reads the manager's clock.
func (m *Manager) now() time.Time { return m.clock() }

// Attach follows one connection's driver, replacing any earlier follower for
// it. Called whenever a driver starts, which includes every save of the
// connection, so changed settings apply here.
func (m *Manager) Attach(id uuid.UUID, d integrations.Driver) {
	m.Detach(id)
	conn, err := models.FindConnectionByID(id)
	if err != nil {
		m.log.Error("failed to load a connection for auto import", zap.String("connection_id", id.String()), zap.Error(err))
		return
	}
	raw, err := conn.ConfigJSON()
	if err != nil {
		m.log.Error("failed to read a connection config for auto import", zap.String("connection", conn.Name), zap.Error(err))
		return
	}
	settings, err := integrations.ReadAutoImport(raw)
	if err != nil {
		m.log.Error("connection has unusable auto import settings", zap.String("connection", conn.Name), zap.Error(err))
		return
	}
	f := &follower{m: m, id: id, name: conn.Name, kind: conn.Kind, settings: settings, status: d.Status,
		live: map[string]time.Time{}, skipped: map[string]Skip{}}
	m.mu.Lock()
	m.conns[id] = f
	m.mu.Unlock()
	if !settings.Enabled {
		return
	}

	switch drv := d.(type) {
	case *esphome.Driver:
		f.followEntities(entitydevices.ESPHome(drv))
	case *frigate.Driver:
		f.followEntities(entitydevices.Frigate(drv))
	case *homeassistant.Driver:
		f.followEntities(entitydevices.HomeAssistant(drv))
	case *openhab.Driver:
		f.followEntities(entitydevices.OpenHAB(drv))
	case *mqtt.Driver:
		f.followMQTT(drv)
	default:
		return
	}
	m.log.Info("auto import following a connection", zap.String("connection", conn.Name), zap.String("kind", conn.Kind))
}

// Detach stops following a connection.
func (m *Manager) Detach(id uuid.UUID) {
	m.mu.Lock()
	f := m.conns[id]
	delete(m.conns, id)
	m.mu.Unlock()
	if f != nil {
		f.stop()
	}
}

// Stop releases every connection.
func (m *Manager) Stop() {
	m.mu.Lock()
	conns := m.conns
	m.conns = map[uuid.UUID]*follower{}
	m.mu.Unlock()
	for _, f := range conns {
		f.stop()
	}
}

// stop ends a follower's watches.
func (f *follower) stop() {
	f.mu.Lock()
	stops := f.stops
	f.stops = nil
	f.mu.Unlock()
	for _, s := range stops {
		s()
	}
}

// addStop records a watch to end on stop.
func (f *follower) addStop(s func()) {
	f.mu.Lock()
	f.stops = append(f.stops, s)
	f.mu.Unlock()
}

// Status reports a connection's auto import.
func (m *Manager) Status(id uuid.UUID) Status {
	m.mu.Lock()
	f := m.conns[id]
	m.mu.Unlock()
	out := Status{Skipped: []Skip{}}
	if f == nil {
		return out
	}
	out.Enabled = f.settings.Enabled
	if f.settings.RemoveAfter > 0 {
		out.RemoveAfter = integrations.FormatLongDuration(f.settings.RemoveAfter)
	}
	if devices, err := models.ListMQTTDevicesOnConnection(id); err == nil {
		for _, d := range devices {
			if d.AutoImported {
				out.Imported++
			}
		}
	}
	f.mu.Lock()
	for _, s := range f.skipped {
		out.Skipped = append(out.Skipped, s)
	}
	if !f.lastImport.IsZero() {
		at := f.lastImport
		out.LastImportAt = &at
	}
	if !f.lastRemoval.IsZero() {
		at := f.lastRemoval
		out.LastRemovalAt = &at
	}
	f.mu.Unlock()
	sort.Slice(out.Skipped, func(i, j int) bool { return out.Skipped[i].Name < out.Skipped[j].Name })
	return out
}

// Enabled reports whether a connection imports devices automatically, which
// the device pages use to hide the picker for it.
func (m *Manager) Enabled(id uuid.UUID) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	f := m.conns[id]
	return f != nil && f.settings.Enabled
}

// skip records a reported device that was not added.
func (f *follower) skip(key, name, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if _, known := f.skipped[key]; !known && len(f.skipped) >= maxSkipped {
		return
	}
	f.skipped[key] = Skip{Key: key, Name: name, Reason: reason}
}

// unskip forgets a skip once the device is added.
func (f *follower) unskip(key string) {
	f.mu.Lock()
	delete(f.skipped, key)
	f.mu.Unlock()
}

// report records that the source reported a key now.
func (f *follower) report(key string) {
	now := f.m.now()
	f.mu.Lock()
	f.live[key] = now
	f.mu.Unlock()
}

// imported records added devices in the status and the event log.
func (f *follower) imported(names []string) {
	if len(names) == 0 {
		return
	}
	now := f.m.now()
	f.mu.Lock()
	f.lastImport = now
	f.mu.Unlock()
	f.m.log.Info("auto import added devices", zap.String("connection", f.name), zap.Strings("devices", names))
	shown := names
	if len(shown) > 20 {
		shown = shown[:20]
	}
	events.Record(events.Entry{
		Category: models.EventConnection,
		Level:    models.EventInfo,
		Subject:  events.Subject{Kind: "connection", ID: f.id.String(), Name: f.name},
		Message:  fmt.Sprintf("Auto import added %d device(s): %s", len(names), strings.Join(shown, ", ")),
		Details:  map[string]any{"devices": names},
		Actor:    events.System(),
	})
}

// followEntities imports from an entity connection's list.
func (f *follower) followEntities(src entitydevices.Source) {
	f.src = src
	stop := src.WatchEntityList(func(list []entitydevices.Entity) {
		// Adding devices reloads the runner, which must not run on the
		// driver's read loop.
		go f.importEntities(list)
	})
	f.addStop(stop)
}

// entityKey is an entity device's source key.
func entityKey(objectID string) string { return "entity:" + objectID }

// importEntities adds every listed entity that passes the filters and has no
// device yet.
func (f *follower) importEntities(list []entitydevices.Entity) {
	if f.src == nil || !f.src.Connected() {
		return
	}
	f.importMu.Lock()
	defer f.importMu.Unlock()
	devices, err := models.ListMQTTDevicesOnConnection(f.id)
	if err != nil {
		f.m.log.Error("failed to load devices for auto import", zap.String("connection", f.name), zap.Error(err))
		return
	}
	have := map[string]bool{}
	for _, d := range devices {
		if d.Transport == f.src.Transport() {
			have[d.EntityKey] = true
		}
	}
	var ids []string
	for _, e := range list {
		if !e.Matches(f.settings.Places, f.settings.Types) {
			continue
		}
		f.report(entityKey(e.ObjectID))
		if have[e.ObjectID] {
			continue
		}
		if e.Reason != "" {
			f.skip(entityKey(e.ObjectID), e.Name, e.Reason)
			continue
		}
		ids = append(ids, e.ObjectID)
	}
	if len(ids) == 0 {
		return
	}
	result, err := entitydevices.AddAuto(f.src, f.id, ids)
	if err != nil {
		f.m.log.Warn("auto import could not add entities", zap.String("connection", f.name), zap.Error(err))
		return
	}
	var names []string
	for _, v := range result.Added {
		names = append(names, v.Name)
		f.unskip(entityKey(v.EntityKey))
	}
	for _, fail := range result.Failed {
		f.skip(entityKey(fail.ObjectID), fail.ObjectID, fail.Error)
	}
	f.imported(names)
}

// sourceKey is the key a device is reported under.
func sourceKey(d *models.MQTTDevice) string {
	switch {
	case d.DiscoveryKey != "":
		return discoveryKey(d.DiscoveryKey)
	case d.Z2MIEEE != "":
		return z2mKey(d.Z2MIEEE)
	case mqttrelay.IsEntityTransport(d.Transport):
		return entityKey(d.EntityKey)
	}
	return ""
}

// Start runs the sweep until stop closes.
func (m *Manager) Start(stop <-chan struct{}) {
	go func() {
		ticker := time.NewTicker(sweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				m.Sweep()
			case <-stop:
				return
			}
		}
	}()
}

// Sweep refreshes report times, re-imports devices that were deleted while
// their source still reports them, and removes devices not reported within
// their connection's "Remove after".
func (m *Manager) Sweep() {
	m.mu.Lock()
	conns := make([]*follower, 0, len(m.conns))
	for _, f := range m.conns {
		if f.settings.Enabled {
			conns = append(conns, f)
		}
	}
	m.mu.Unlock()
	for _, f := range conns {
		f.sweep()
	}
}

// sweep runs one connection's sweep.
func (f *follower) sweep() {
	now := f.m.now()
	connected := f.status().State == integrations.StateConnected
	f.mu.Lock()
	switch {
	case connected && f.connectedSince.IsZero():
		f.connectedSince = now
	case !connected:
		f.connectedSince = time.Time{}
	}
	since := f.connectedSince
	f.mu.Unlock()

	// An entity list is the source's word on what exists, so presence while
	// connected is a report, and a matching entity with no device is one the
	// author deleted and auto import adds back.
	if f.src != nil && connected && f.src.Connected() {
		f.importEntities(f.src.Entities())
	}
	if f.mqttFollow != nil && connected {
		f.mqttFollow.reimport()
	}

	devices, err := models.ListMQTTDevicesOnConnection(f.id)
	if err != nil {
		f.m.log.Error("failed to load devices for the auto import sweep", zap.String("connection", f.name), zap.Error(err))
		return
	}
	for _, d := range devices {
		if !d.AutoImported {
			continue
		}
		key := sourceKey(d)
		f.mu.Lock()
		live := f.live[key]
		f.mu.Unlock()

		reported := d.CreatedAt
		if d.LastReportedAt != nil {
			reported = *d.LastReportedAt
		}
		if live.After(reported.Add(flushSlack)) {
			if err := models.SetMQTTDeviceReportedAt(d.ID, live); err != nil {
				f.m.log.Error("failed to store when a device was reported", zap.String("device", d.Name), zap.Error(err))
			}
		}
		if live.After(reported) {
			reported = live
		}

		if f.settings.RemoveAfter <= 0 {
			continue
		}
		ref := reported
		if f.kind != esphome.Kind {
			// An outage of the hub or broker says nothing about the device, so
			// the clock restarts when the connection comes back.
			if !connected {
				continue
			}
			if since.After(ref) {
				ref = since
			}
		}
		if now.Sub(ref) < f.settings.RemoveAfter {
			continue
		}
		f.remove(d, reported)
	}
}

// remove deletes a device its source stopped reporting.
func (f *follower) remove(d *models.MQTTDevice, reported time.Time) {
	if err := mqttrelay.DeleteDevice(d.ID); err != nil {
		f.m.log.Error("failed to remove an unreported device", zap.String("device", d.Name), zap.Error(err))
		return
	}
	if f.m.OnRemove != nil {
		f.m.OnRemove(d.ID)
	}
	now := f.m.now()
	key := sourceKey(d)
	if f.kind == mqtt.Kind && key != "" {
		// A retained replay must not bring it straight back; live activity will.
		if err := models.AddAutoImportTombstone(f.id, key, now); err != nil {
			f.m.log.Error("failed to remember a removed device", zap.String("device", d.Name), zap.Error(err))
		}
	}
	f.mu.Lock()
	f.lastRemoval = now
	delete(f.live, key)
	f.mu.Unlock()
	f.m.log.Info("auto import removed an unreported device", zap.String("device", d.Name),
		zap.String("connection", f.name), zap.Time("last_reported", reported))
	events.Record(events.Entry{
		Category: models.EventDevice,
		Level:    models.EventInfo,
		Subject:  events.Subject{Kind: "device", ID: d.ID.String(), Name: d.Name},
		Message: fmt.Sprintf("Removed by auto import on %s: not reported since %s, longer than %s",
			f.name, reported.UTC().Format(time.RFC3339), integrations.FormatLongDuration(f.settings.RemoveAfter)),
		Details: map[string]any{"connection_id": f.id.String(), "last_reported_at": reported},
		Actor:   events.System(),
	})
}
