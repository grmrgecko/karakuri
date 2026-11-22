package homeassistant

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Registry names, used for status and update events (<name>_registry_updated).
const (
	RegistryFloor  = "floor"
	RegistryArea   = "area"
	RegistryDevice = "device"
	RegistryEntity = "entity"
	RegistryLabel  = "label"
)

// registryNames is the order the registries are read in.
var registryNames = []string{RegistryFloor, RegistryArea, RegistryDevice, RegistryEntity, RegistryLabel}

// registryCommands are the read-only list commands for each registry. The
// entity registry is read in the compact form the Home Assistant frontend
// loads, which leaves out disabled entities.
var registryCommands = map[string]string{
	RegistryFloor:  "config/floor_registry/list",
	RegistryArea:   "config/area_registry/list",
	RegistryDevice: "config/device_registry/list",
	RegistryEntity: "config/entity_registry/list_for_display",
	RegistryLabel:  "config/label_registry/list",
}

// registryDebounce bounds how often a burst of update events rereads a
// registry: adding a device with twenty entities fires twenty events.
const registryDebounce = 2 * time.Second

// Floor is one floor of the house.
type Floor struct {
	ID    string `json:"floor_id"`
	Name  string `json:"name"`
	Level *int   `json:"level,omitempty"`
}

// Area is one room or zone.
type Area struct {
	ID      string   `json:"area_id"`
	Name    string   `json:"name"`
	FloorID string   `json:"floor_id,omitempty"`
	Aliases []string `json:"aliases,omitempty"`
	Labels  []string `json:"labels,omitempty"`
}

// Device is one physical or virtual device holding entities.
type Device struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	AreaID       string   `json:"area_id,omitempty"`
	Manufacturer string   `json:"manufacturer,omitempty"`
	Model        string   `json:"model,omitempty"`
	Labels       []string `json:"labels,omitempty"`
}

// EntityEntry is an entity's registry record: where it sits, not its state.
type EntityEntry struct {
	EntityID string   `json:"entity_id"`
	DeviceID string   `json:"device_id,omitempty"`
	AreaID   string   `json:"area_id,omitempty"`
	Name     string   `json:"name,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	Hidden   bool     `json:"hidden,omitempty"`
	// Category is config or diagnostic for entities that are not part of
	// what a device does, empty otherwise.
	Category string `json:"category,omitempty"`
}

// Label is one user-defined tag.
type Label struct {
	ID    string `json:"label_id"`
	Name  string `json:"name"`
	Color string `json:"color,omitempty"`
}

// Registry is one connection's copy of Home Assistant's registries at a
// moment. It is replaced, never changed, so a snapshot can be read without a
// lock.
type Registry struct {
	Floors  []Floor
	Areas   []Area
	Devices []Device
	Labels  []Label
	// Status says, per registry, "loaded" or why it is empty.
	Status map[string]string

	entities  map[string]EntityEntry
	areaByID  map[string]Area
	floorByID map[string]Floor
	devByID   map[string]Device
	labelByID map[string]Label
}

// EmptyRegistry is the registry of a connection that has read nothing yet.
func EmptyRegistry() *Registry {
	r := &Registry{Status: map[string]string{}}
	r.index(nil)
	return r
}

// index builds the lookups after a registry changes.
func (r *Registry) index(entities []EntityEntry) {
	r.entities = make(map[string]EntityEntry, len(entities))
	for _, e := range entities {
		r.entities[e.EntityID] = e
	}
	r.areaByID = make(map[string]Area, len(r.Areas))
	for _, a := range r.Areas {
		r.areaByID[a.ID] = a
	}
	r.floorByID = make(map[string]Floor, len(r.Floors))
	for _, f := range r.Floors {
		r.floorByID[f.ID] = f
	}
	r.devByID = make(map[string]Device, len(r.Devices))
	for _, d := range r.Devices {
		r.devByID[d.ID] = d
	}
	r.labelByID = make(map[string]Label, len(r.Labels))
	for _, l := range r.Labels {
		r.labelByID[l.ID] = l
	}
}

// Loaded reports whether a registry was read.
func (r *Registry) Loaded(name string) bool { return r.Status[name] == "loaded" }

// Entries returns every entity registry record.
func (r *Registry) Entries() []EntityEntry {
	out := make([]EntityEntry, 0, len(r.entities))
	for _, e := range r.entities {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EntityID < out[j].EntityID })
	return out
}

// Entry returns one entity's registry record.
func (r *Registry) Entry(entityID string) (EntityEntry, bool) {
	e, ok := r.entities[entityID]
	return e, ok
}

// Area returns one area by ID.
func (r *Registry) Area(id string) (Area, bool) { a, ok := r.areaByID[id]; return a, ok }

// Floor returns one floor by ID.
func (r *Registry) Floor(id string) (Floor, bool) { f, ok := r.floorByID[id]; return f, ok }

// Device returns one device by ID.
func (r *Registry) Device(id string) (Device, bool) { d, ok := r.devByID[id]; return d, ok }

// Label returns one label by ID.
func (r *Registry) Label(id string) (Label, bool) { l, ok := r.labelByID[id]; return l, ok }

// EntityInfo is where an entity sits, with names resolved.
type EntityInfo struct {
	AreaID   string   `json:"area_id,omitempty"`
	Area     string   `json:"area,omitempty"`
	FloorID  string   `json:"floor_id,omitempty"`
	Floor    string   `json:"floor,omitempty"`
	DeviceID string   `json:"device_id,omitempty"`
	Device   string   `json:"device,omitempty"`
	Labels   []string `json:"labels,omitempty"`
	// LabelNames are the labels' names, for searching by what people see.
	LabelNames []string `json:"label_names,omitempty"`
	Hidden     bool     `json:"hidden,omitempty"`
	Category   string   `json:"category,omitempty"`
}

// Describe resolves an entity's area, floor, device, and labels. An entity
// with no area of its own sits in its device's area, as Home Assistant
// places it; its labels include its device's, as Home Assistant's targeting
// counts them.
func (r *Registry) Describe(entityID string) EntityInfo {
	e, ok := r.entities[entityID]
	if !ok {
		return EntityInfo{}
	}
	info := EntityInfo{DeviceID: e.DeviceID, AreaID: e.AreaID, Hidden: e.Hidden, Category: e.Category}
	info.Labels = append(info.Labels, e.Labels...)
	if d, ok := r.devByID[e.DeviceID]; ok {
		info.Device = d.Name
		if info.AreaID == "" {
			info.AreaID = d.AreaID
		}
		for _, l := range d.Labels {
			if !contains(info.Labels, l) {
				info.Labels = append(info.Labels, l)
			}
		}
	}
	for _, id := range info.Labels {
		if l, ok := r.labelByID[id]; ok {
			info.LabelNames = append(info.LabelNames, l.Name)
		}
	}
	if a, ok := r.areaByID[info.AreaID]; ok {
		info.Area = a.Name
		info.FloorID = a.FloorID
		if f, ok := r.floorByID[a.FloorID]; ok {
			info.Floor = f.Name
		}
	}
	return info
}

// FloorOrder ranks a floor for sorting: by level, then name, with no floor
// last.
func (r *Registry) FloorOrder(id string) (int, string) {
	f, ok := r.floorByID[id]
	if !ok {
		return 1 << 30, ""
	}
	if f.Level != nil {
		return *f.Level, f.Name
	}
	return 1 << 29, f.Name
}

// Target names entities by where they sit rather than by ID. An entity
// matches when it matches any listed area, floor, device, or label, and then
// every set narrowing.
type Target struct {
	AreaIDs     []string
	FloorIDs    []string
	DeviceIDs   []string
	LabelIDs    []string
	Domain      string
	DeviceClass string
}

// Empty reports whether the target names nothing to match by.
func (t Target) Empty() bool {
	return len(t.AreaIDs) == 0 && len(t.FloorIDs) == 0 && len(t.DeviceIDs) == 0 && len(t.LabelIDs) == 0
}

// Matches reports whether an entity is in the target. attributes is the
// entity's current state attributes, read for its device class.
func (r *Registry) Matches(t Target, entityID string, attributes map[string]any) bool {
	if t.Domain != "" && domainOf(entityID) != t.Domain {
		return false
	}
	if t.DeviceClass != "" {
		class, _ := attributes["device_class"].(string)
		if class != t.DeviceClass {
			return false
		}
	}
	info := r.Describe(entityID)
	if contains(t.AreaIDs, info.AreaID) && info.AreaID != "" {
		return true
	}
	if contains(t.FloorIDs, info.FloorID) && info.FloorID != "" {
		return true
	}
	if contains(t.DeviceIDs, info.DeviceID) && info.DeviceID != "" {
		return true
	}
	for _, l := range info.Labels {
		if contains(t.LabelIDs, l) {
			return true
		}
	}
	// A label on an area reaches the area's entities, as Home Assistant
	// expands a label target.
	if len(t.LabelIDs) > 0 && info.AreaID != "" {
		if area, ok := r.Area(info.AreaID); ok {
			for _, l := range area.Labels {
				if contains(t.LabelIDs, l) {
					return true
				}
			}
		}
	}
	return false
}

// Resolve lists the entities of states that are in the target.
func (r *Registry) Resolve(t Target, states []Entity) []Entity {
	var out []Entity
	for _, s := range states {
		if r.Matches(t, s.EntityID, s.Attributes) {
			out = append(out, s)
		}
	}
	return out
}

// DescribeTarget names a target the way an error or log line reads it: "in
// Kitchen", "on Ground floor", "on Hallway sensor", or "labelled Night",
// falling back to IDs the registry does not know.
func (r *Registry) DescribeTarget(t Target) string {
	var parts []string
	for _, id := range t.AreaIDs {
		if a, ok := r.areaByID[id]; ok {
			parts = append(parts, "in "+a.Name)
		} else {
			parts = append(parts, "in area "+id)
		}
	}
	for _, id := range t.FloorIDs {
		if f, ok := r.floorByID[id]; ok {
			parts = append(parts, "on "+f.Name)
		} else {
			parts = append(parts, "on floor "+id)
		}
	}
	for _, id := range t.DeviceIDs {
		if d, ok := r.devByID[id]; ok {
			parts = append(parts, "on "+d.Name)
		} else {
			parts = append(parts, "on device "+id)
		}
	}
	for _, id := range t.LabelIDs {
		if l, ok := r.labelByID[id]; ok {
			parts = append(parts, "labelled "+l.Name)
		} else {
			parts = append(parts, "labelled "+id)
		}
	}
	return strings.Join(parts, " or ")
}

// SplitIDs reads a comma-separated ID list as the target fields store it.
func SplitIDs(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}

// domainOf returns the domain half of an entity ID.
func domainOf(entityID string) string {
	if i := strings.IndexByte(entityID, '.'); i > 0 {
		return entityID[:i]
	}
	return ""
}

// registryState is the driver's registry copy and its watchers.
type registryState struct {
	// loading serializes loads: each starts from the copy the last one
	// published, so two registries reread together both land.
	loading  sync.Mutex
	mu       sync.Mutex
	current  *Registry
	entities []EntityEntry
	watchers map[int]func(RegistryChange)
	nextW    int
	timers   map[string]*time.Timer
	// renames collects entity renames seen in update events since the last
	// notification, old ID to new.
	renames map[string]string
}

// Registry returns the connection's current registry copy.
func (d *Driver) Registry() *Registry {
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	if d.reg.current == nil {
		return EmptyRegistry()
	}
	return d.reg.current
}

// RegistryChange is a new registry copy and the entity renames that led to it.
type RegistryChange struct {
	Registry *Registry
	// Renamed maps an entity's old ID to its new one.
	Renamed map[string]string
}

// WatchRegistry calls fn with each new registry copy and returns the function
// that stops it.
func (d *Driver) WatchRegistry(fn func(RegistryChange)) func() {
	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	if d.reg.watchers == nil {
		d.reg.watchers = map[int]func(RegistryChange){}
	}
	id := d.reg.nextW
	d.reg.nextW++
	d.reg.watchers[id] = fn
	return func() {
		d.reg.mu.Lock()
		delete(d.reg.watchers, id)
		d.reg.mu.Unlock()
	}
}

// subscribeRegistryEvents registers the driver's own handlers for the five
// registry update events; resubscribe keeps them across reconnects.
func (d *Driver) subscribeRegistryEvents() {
	for _, name := range registryNames {
		name := name
		_, err := d.SubscribeEvents(name+"_registry_updated", func(event json.RawMessage) {
			d.registryUpdated(name, event)
		})
		if err != nil {
			d.log.Warn("homeassistant registry subscribe failed", zap.String("registry", name), zap.Error(err))
		}
	}
}

// registryUpdated schedules a reread of one registry, collecting entity
// renames so a device bound to the old ID can follow.
func (d *Driver) registryUpdated(name string, event json.RawMessage) {
	if name == RegistryEntity {
		var e struct {
			Data struct {
				Action      string `json:"action"`
				EntityID    string `json:"entity_id"`
				OldEntityID string `json:"old_entity_id"`
			} `json:"data"`
		}
		if json.Unmarshal(event, &e) == nil && e.Data.Action == "update" &&
			e.Data.OldEntityID != "" && e.Data.OldEntityID != e.Data.EntityID {
			d.reg.mu.Lock()
			if d.reg.renames == nil {
				d.reg.renames = map[string]string{}
			}
			d.reg.renames[e.Data.OldEntityID] = e.Data.EntityID
			d.reg.mu.Unlock()
		}
	}

	d.reg.mu.Lock()
	defer d.reg.mu.Unlock()
	if d.reg.timers == nil {
		d.reg.timers = map[string]*time.Timer{}
	}
	if t := d.reg.timers[name]; t != nil {
		t.Stop()
	}
	d.reg.timers[name] = time.AfterFunc(d.regDebounce, func() {
		d.mu.Lock()
		ctx := d.ctx
		d.mu.Unlock()
		if ctx == nil || ctx.Err() != nil {
			return
		}
		d.loadRegistries(ctx, name)
	})
}

// loadRegistries reads the named registries, or all of them when none are
// named, and publishes the new copy. A registry that cannot be read is left
// empty with its reason; the connection carries on without it.
func (d *Driver) loadRegistries(ctx context.Context, names ...string) {
	if len(names) == 0 {
		names = registryNames
	}
	d.reg.loading.Lock()

	d.reg.mu.Lock()
	prev := d.reg.current
	entities := d.reg.entities
	d.reg.mu.Unlock()
	next := &Registry{Status: map[string]string{}}
	if prev != nil {
		*next = Registry{Floors: prev.Floors, Areas: prev.Areas, Devices: prev.Devices, Labels: prev.Labels,
			Status: map[string]string{}}
		for k, v := range prev.Status {
			next.Status[k] = v
		}
	}

	for _, name := range names {
		raw, err := d.readRegistry(ctx, name)
		if err != nil {
			next.Status[name] = err.Error()
			d.log.Info("homeassistant registry unavailable", zap.String("registry", name), zap.Error(err))
			raw = nil
		} else {
			next.Status[name] = "loaded"
		}
		switch name {
		case RegistryFloor:
			next.Floors = decodeFloors(raw)
		case RegistryArea:
			next.Areas = decodeAreas(raw)
		case RegistryDevice:
			next.Devices = decodeDevices(raw)
		case RegistryEntity:
			entities = decodeEntities(raw)
		case RegistryLabel:
			next.Labels = decodeLabels(raw)
		}
	}
	next.index(entities)

	d.reg.mu.Lock()
	d.reg.current = next
	d.reg.entities = entities
	renamed := d.reg.renames
	d.reg.renames = nil
	watchers := make([]func(RegistryChange), 0, len(d.reg.watchers))
	for _, fn := range d.reg.watchers {
		watchers = append(watchers, fn)
	}
	d.reg.mu.Unlock()
	d.reg.loading.Unlock()

	for _, fn := range watchers {
		fn(RegistryChange{Registry: next, Renamed: renamed})
	}
}

// readRegistry runs one registry list command.
func (d *Driver) readRegistry(ctx context.Context, name string) (json.RawMessage, error) {
	msg, _, err := d.command(ctx, map[string]any{"type": registryCommands[name]}, nil)
	if err != nil {
		return nil, fmt.Errorf("reading the %s registry: %w", name, err)
	}
	return msg.Result, nil
}

func decodeFloors(raw json.RawMessage) []Floor {
	var out []Floor
	_ = json.Unmarshal(raw, &out)
	sort.SliceStable(out, func(i, j int) bool {
		li, lj := levelOf(out[i]), levelOf(out[j])
		if li != lj {
			return li < lj
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func levelOf(f Floor) int {
	if f.Level == nil {
		return 1 << 29
	}
	return *f.Level
}

func decodeAreas(raw json.RawMessage) []Area {
	var in []struct {
		AreaID  string   `json:"area_id"`
		Name    string   `json:"name"`
		FloorID *string  `json:"floor_id"`
		Aliases []string `json:"aliases"`
		Labels  []string `json:"labels"`
	}
	_ = json.Unmarshal(raw, &in)
	out := make([]Area, 0, len(in))
	for _, a := range in {
		area := Area{ID: a.AreaID, Name: a.Name, Aliases: a.Aliases, Labels: a.Labels}
		if a.FloorID != nil {
			area.FloorID = *a.FloorID
		}
		out = append(out, area)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func decodeDevices(raw json.RawMessage) []Device {
	var in []struct {
		ID           string   `json:"id"`
		Name         *string  `json:"name"`
		NameByUser   *string  `json:"name_by_user"`
		AreaID       *string  `json:"area_id"`
		Manufacturer *string  `json:"manufacturer"`
		Model        *string  `json:"model"`
		Labels       []string `json:"labels"`
		DisabledBy   *string  `json:"disabled_by"`
	}
	_ = json.Unmarshal(raw, &in)
	out := make([]Device, 0, len(in))
	for _, d := range in {
		if d.DisabledBy != nil {
			continue
		}
		dev := Device{ID: d.ID, Name: str(d.NameByUser), AreaID: str(d.AreaID),
			Manufacturer: str(d.Manufacturer), Model: str(d.Model), Labels: d.Labels}
		if dev.Name == "" {
			dev.Name = str(d.Name)
		}
		out = append(out, dev)
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

// decodeEntities reads list_for_display, whose entries use short keys and
// give the entity category as an index into entity_categories.
func decodeEntities(raw json.RawMessage) []EntityEntry {
	var in struct {
		Categories map[string]string `json:"entity_categories"`
		Entities   []struct {
			EI string          `json:"ei"`
			DI *string         `json:"di"`
			AI *string         `json:"ai"`
			LB []string        `json:"lb"`
			EN *string         `json:"en"`
			HB bool            `json:"hb"`
			EC json.RawMessage `json:"ec"`
		} `json:"entities"`
	}
	_ = json.Unmarshal(raw, &in)
	out := make([]EntityEntry, 0, len(in.Entities))
	for _, e := range in.Entities {
		if e.EI == "" {
			continue
		}
		entry := EntityEntry{EntityID: e.EI, DeviceID: str(e.DI), AreaID: str(e.AI), Name: str(e.EN),
			Labels: e.LB, Hidden: e.HB}
		if len(e.EC) > 0 && string(e.EC) != "null" {
			var idx json.Number
			var name string
			if json.Unmarshal(e.EC, &idx) == nil {
				entry.Category = in.Categories[idx.String()]
			} else if json.Unmarshal(e.EC, &name) == nil {
				entry.Category = name
			}
		}
		out = append(out, entry)
	}
	return out
}

func decodeLabels(raw json.RawMessage) []Label {
	var in []struct {
		LabelID string  `json:"label_id"`
		Name    string  `json:"name"`
		Color   *string `json:"color"`
	}
	_ = json.Unmarshal(raw, &in)
	out := make([]Label, 0, len(in))
	for _, l := range in {
		out = append(out, Label{ID: l.LabelID, Name: l.Name, Color: str(l.Color)})
	}
	sort.SliceStable(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func str(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
