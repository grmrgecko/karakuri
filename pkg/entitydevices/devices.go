// Package entitydevices turns a connection's entities into devices and keeps
// those devices in step with the connection's entity list: an ESPHome node's
// entities and a Frigate server's camera switches.
package entitydevices

import (
	"fmt"
	"strings"
	"sync"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"go.uber.org/zap"
)

// Entity is one entity a connection offers as a device.
type Entity struct {
	ObjectID string
	Name     string
	Type     string
	Kind     string
	Options  map[string]any
	Writable bool
	// Reason is why the entity cannot be added, empty when it can.
	Reason string
	// Description is the device's description when added.
	Description string
	// Group is the heading the entity is listed under, such as its area.
	Group string
	// Places name where the entity is, lower case, for auto import filters:
	// a Home Assistant area's ID and name and its floor, or an openHAB
	// location's item name and label and its floor.
	Places []string
}

// Matches reports whether an entity passes auto import filters. An empty
// filter matches everything; a type filter entry matches the type or, for
// openHAB, its base type before the colon, so Number matches Number:Power.
func (e Entity) Matches(places, types []string) bool {
	if len(types) > 0 {
		t := strings.ToLower(e.Type)
		base, _, _ := strings.Cut(t, ":")
		ok := false
		for _, want := range types {
			if want == t || want == base {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if len(places) > 0 {
		for _, want := range places {
			for _, have := range e.Places {
				if want == have {
					return true
				}
			}
		}
		return false
	}
	return true
}

// Source is a connection whose entities can become devices.
type Source interface {
	// Transport is the devices' transport.
	Transport() string
	// Connected reports whether the entity list is current.
	Connected() bool
	// Label names the connection's device in added device names, empty when
	// entity names already say enough.
	Label() string
	// Entities lists the entities in a stable order.
	Entities() []Entity
	// WatchEntityList calls fn with the entities after every connect and
	// change, and returns the function that stops it.
	WatchEntityList(fn func([]Entity)) func()
}

// Discovered is one entity as the add dialog lists it.
type Discovered struct {
	ObjectID    string `json:"object_id" description:"Entity object ID: an ESPHome object ID, or <camera>/<feature> for Frigate."`
	Name        string `json:"name" description:"Entity name."`
	Type        string `json:"type" description:"Entity type."`
	Kind        string `json:"kind,omitempty" description:"Control kind the device would be."`
	Commandable bool   `json:"commandable" description:"Whether the device would take commands."`
	CanAdd      bool   `json:"can_add" description:"Whether the entity can be added."`
	Reason      string `json:"reason,omitempty" description:"Why the entity cannot be added."`
	DeviceID    string `json:"device_id,omitempty" description:"Device already added from it."`
	Group       string `json:"group,omitempty" description:"Heading the entity is listed under: a Home Assistant entity's floor and area."`
}

// Discover lists a connected source's entities against the devices already
// added from it.
func Discover(src Source, existing []*models.MQTTDevice, connID uuid.UUID) []Discovered {
	added := map[string]string{}
	for _, m := range existing {
		if m.ConnectionID == connID && m.Transport == src.Transport() {
			added[m.EntityKey] = m.ID.String()
		}
	}
	out := []Discovered{}
	for _, e := range src.Entities() {
		d := Discovered{ObjectID: e.ObjectID, Name: e.Name, Type: e.Type, Kind: e.Kind,
			Commandable: e.Writable, CanAdd: e.Reason == "", Reason: e.Reason, DeviceID: added[e.ObjectID], Group: e.Group}
		if d.DeviceID != "" {
			d.CanAdd, d.Reason = false, "already added"
		}
		out = append(out, d)
	}
	return out
}

// AddResult is what adding entities did.
type AddResult struct {
	Added  []mqttrelay.View `json:"added" description:"Devices created."`
	Failed []AddFailure     `json:"failed" description:"Entities that could not be added, with why."`
}

// AddFailure is one entity that could not be added.
type AddFailure struct {
	ObjectID string `json:"object_id" description:"Entity object ID."`
	Error    string `json:"error" description:"Why it was not added."`
}

// Add creates a device for each listed entity. Each succeeds or fails on its
// own.
func Add(src Source, connID uuid.UUID, objectIDs []string) (AddResult, error) {
	return add(src, connID, objectIDs, false)
}

// AddAuto is Add for the connection's auto import, which marks the devices
// it creates so they can be removed when their entity stops being reported.
func AddAuto(src Source, connID uuid.UUID, objectIDs []string) (AddResult, error) {
	return add(src, connID, objectIDs, true)
}

// add creates devices, marked auto imported when auto is set.
func add(src Source, connID uuid.UUID, objectIDs []string, auto bool) (AddResult, error) {
	if !src.Connected() {
		return AddResult{}, apperrors.ErrBadGateway.WithMessage("the connection is not connected")
	}
	byID := map[string]Entity{}
	for _, e := range src.Entities() {
		byID[e.ObjectID] = e
	}
	existing, err := models.ListMQTTDevices()
	if err != nil {
		return AddResult{}, apperrors.ErrInternal.WithMessage(err.Error())
	}
	names := map[string]bool{}
	for _, m := range existing {
		names[strings.ToLower(m.Name)] = true
	}

	result := AddResult{Added: []mqttrelay.View{}, Failed: []AddFailure{}}
	for _, id := range objectIDs {
		e, ok := byID[id]
		if !ok {
			result.Failed = append(result.Failed, AddFailure{ObjectID: id, Error: "the connection has no such entity"})
			continue
		}
		if e.Reason != "" {
			result.Failed = append(result.Failed, AddFailure{ObjectID: id, Error: e.Reason})
			continue
		}
		name := uniqueName(strings.TrimSpace(src.Label()+" "+e.Name), names)
		def := mqttrelay.Definition{
			Name:         name,
			ConnectionID: connID,
			Control:      e.Kind,
			Options:      e.Options,
			Description:  e.Description,
			Transport:    src.Transport(),
			EntityKey:    e.ObjectID,
			EntityType:   e.Type,
		}
		saved, err := mqttrelay.SaveDefinition(uuid.Nil, def, mqttrelay.Origin{Auto: auto})
		if err != nil {
			result.Failed = append(result.Failed, AddFailure{ObjectID: id, Error: err.Error()})
			continue
		}
		names[strings.ToLower(saved.Name)] = true
		result.Added = append(result.Added, mqttrelay.BuildView(saved, mqttrelay.D))
	}
	return result, nil
}

// Tracker marks a connection's devices missing when their entity leaves the
// entity list, and clears the mark when it returns.
type Tracker struct {
	log *zap.Logger

	mu     sync.Mutex
	unhook map[uuid.UUID]func()
}

// NewTracker builds a tracker.
func NewTracker(log *zap.Logger) *Tracker {
	return &Tracker{log: log, unhook: map[uuid.UUID]func(){}}
}

// Renamer is a source whose entities can be renamed in place, as Home
// Assistant renames an entity ID; its devices follow the new ID.
type Renamer interface {
	WatchRenames(fn func(renamed map[string]string)) func()
}

// Attach follows one connection's source, replacing any earlier one.
func (t *Tracker) Attach(connID uuid.UUID, src Source) {
	transport := src.Transport()
	unhookList := src.WatchEntityList(func(entities []Entity) {
		// Saving devices reloads the runner, which must not run on the
		// driver's read loop.
		go t.apply(connID, transport, entities)
	})
	unhook := unhookList
	if r, ok := src.(Renamer); ok {
		unhookRenames := r.WatchRenames(func(renamed map[string]string) {
			go t.rename(connID, transport, renamed)
		})
		unhook = func() {
			unhookList()
			unhookRenames()
		}
	}
	t.mu.Lock()
	old := t.unhook[connID]
	t.unhook[connID] = unhook
	t.mu.Unlock()
	if old != nil {
		old()
	}
}

// Stop releases every connection.
func (t *Tracker) Stop() {
	t.mu.Lock()
	hooks := t.unhook
	t.unhook = map[uuid.UUID]func(){}
	t.mu.Unlock()
	for _, unhook := range hooks {
		unhook()
	}
}

// apply sets or clears missing on a connection's devices.
func (t *Tracker) apply(connID uuid.UUID, transport string, entities []Entity) {
	present := map[string]bool{}
	for _, e := range entities {
		present[e.ObjectID] = true
	}
	devices, err := models.ListMQTTDevices()
	if err != nil {
		t.log.Error("failed to load devices for an entity list", zap.Error(err))
		return
	}
	for _, m := range devices {
		if m.ConnectionID != connID || m.Transport != transport {
			continue
		}
		missing := !present[m.EntityKey]
		if missing == m.Missing {
			continue
		}
		if err := mqttrelay.SetMissing(m.ID, missing); err != nil {
			t.log.Error("failed to mark a device missing", zap.String("device", m.Name), zap.Error(err))
			continue
		}
		t.log.Info("entity presence changed", zap.String("device", m.Name), zap.String("transport", transport),
			zap.Bool("missing", missing))
	}
}

// rename moves devices to their entity's new ID.
func (t *Tracker) rename(connID uuid.UUID, transport string, renamed map[string]string) {
	devices, err := models.ListMQTTDevices()
	if err != nil {
		t.log.Error("failed to load devices for an entity rename", zap.Error(err))
		return
	}
	for _, m := range devices {
		if m.ConnectionID != connID || m.Transport != transport {
			continue
		}
		next, ok := renamed[m.EntityKey]
		if !ok {
			continue
		}
		if err := mqttrelay.SetEntityKey(m.ID, next); err != nil {
			t.log.Error("failed to follow an entity rename", zap.String("device", m.Name), zap.Error(err))
			continue
		}
		t.log.Info("entity renamed", zap.String("device", m.Name), zap.String("from", m.EntityKey), zap.String("to", next))
	}
}

// uniqueName returns name, or name with the lowest free numeric suffix.
func uniqueName(name string, taken map[string]bool) string {
	if !taken[strings.ToLower(name)] {
		return name
	}
	for i := 2; ; i++ {
		candidate := fmt.Sprintf("%s %d", name, i)
		if !taken[strings.ToLower(candidate)] {
			return candidate
		}
	}
}
