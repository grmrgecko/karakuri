package entitydevices

import (
	"fmt"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/controls"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
)

// esphomeSource offers an ESPHome node's entities.
type esphomeSource struct{ d *esphome.Driver }

// ESPHome is a node's entities as a source.
func ESPHome(d *esphome.Driver) Source { return esphomeSource{d} }

func (s esphomeSource) Transport() string { return mqttrelay.TransportESPHome }

func (s esphomeSource) Connected() bool { return s.d.Status().State == integrations.StateConnected }

func (s esphomeSource) Label() string {
	info := s.d.Info()
	if info.Friendly != "" {
		return info.Friendly
	}
	return info.Name
}

func (s esphomeSource) Entities() []Entity { return s.convert(s.d.Entities()) }

func (s esphomeSource) WatchEntityList(fn func([]Entity)) func() {
	return s.d.WatchEntityList(func(list []esphome.Entity) { fn(s.convert(list)) })
}

func (s esphomeSource) convert(list []esphome.Entity) []Entity {
	node := s.d.Info().Name
	out := make([]Entity, 0, len(list))
	for _, e := range list {
		p := e.Propose()
		out = append(out, Entity{ObjectID: e.ObjectID, Name: e.Name, Type: e.Type, Kind: p.Kind,
			Options: p.Options, Writable: p.Writable, Reason: p.Reason,
			Description: fmt.Sprintf("ESPHome %s on %s", strings.ReplaceAll(e.Type, "_", " "), node)})
	}
	return out
}

// frigateSource offers a Frigate server's camera switches.
type frigateSource struct{ d *frigate.Driver }

// Frigate is a server's camera switches as a source.
func Frigate(d *frigate.Driver) Source { return frigateSource{d} }

func (s frigateSource) Transport() string { return mqttrelay.TransportFrigate }

func (s frigateSource) Connected() bool { return s.d.Status().State == integrations.StateConnected }

// Label is empty: a switch's name already says its camera and feature.
func (s frigateSource) Label() string { return "" }

func (s frigateSource) Entities() []Entity {
	switches := s.d.Switches()
	out := make([]Entity, 0, len(switches))
	for _, sw := range switches {
		out = append(out, Entity{ObjectID: sw.ObjectID, Name: sw.Camera + " " + sw.Feature, Type: "switch",
			Kind: controls.KindBoolean, Options: map[string]any{}, Writable: true,
			Description: fmt.Sprintf("Frigate %s switch of camera %s on %s", sw.Feature, sw.Camera, s.d.Name())})
	}
	return out
}

func (s frigateSource) WatchEntityList(fn func([]Entity)) func() {
	return s.d.WatchCameraList(func() { fn(s.Entities()) })
}

// SourceFor resolves a running connection whose entities can become devices.
func SourceFor(connectionID string) (Source, error) {
	c, err := connops.Load(connectionID)
	if err != nil {
		return nil, err
	}
	switch c.Kind {
	case esphome.Kind:
		d, err := connops.ESPHomeDriver(connectionID)
		if err != nil {
			return nil, err
		}
		return ESPHome(d), nil
	case frigate.Kind:
		d, err := connops.FrigateDriver(connectionID)
		if err != nil {
			return nil, err
		}
		return Frigate(d), nil
	case openhab.Kind:
		d, err := connops.OpenHABDriver(connectionID)
		if err != nil {
			return nil, err
		}
		return OpenHAB(d), nil
	case homeassistant.Kind:
		d, err := connops.HADriver(connectionID)
		if err != nil {
			return nil, err
		}
		return HomeAssistant(d), nil
	}
	return nil, apperrors.ErrValidation.WithMessage("connection " + c.Name + " has no entities to add as devices; use an ESPHome, Frigate, Home Assistant, or openHAB connection")
}

// homeAssistantSource offers a Home Assistant connection's entities.
type homeAssistantSource struct{ d *homeassistant.Driver }

// HomeAssistant is a Home Assistant connection's entities as a source.
func HomeAssistant(d *homeassistant.Driver) Source { return homeAssistantSource{d} }

func (s homeAssistantSource) Transport() string { return mqttrelay.TransportHomeAssistant }

func (s homeAssistantSource) Connected() bool {
	if s.d.Status().State != integrations.StateConnected {
		return false
	}
	_, seeded := s.d.KnownStates()
	return seeded
}

// Label is empty: Home Assistant's friendly names already name the device.
func (s homeAssistantSource) Label() string { return "" }

// Entities lists every entity with a state, grouped by floor and area with
// entities in no area last, as the pickers order them.
func (s homeAssistantSource) Entities() []Entity {
	states, _ := s.d.KnownStates()
	reg := s.d.Registry()
	type row struct {
		Entity
		level int
		floor string
		area  string
	}
	rows := make([]row, 0, len(states))
	for _, st := range states {
		m := homeassistant.Propose(st)
		info := reg.Describe(st.EntityID)
		name, _ := st.Attributes["friendly_name"].(string)
		if name == "" {
			name = st.EntityID
		}
		domain, _, _ := strings.Cut(st.EntityID, ".")
		group := info.Area
		if info.Floor != "" {
			group = info.Floor + " · " + info.Area
		}
		desc := "Home Assistant " + strings.ReplaceAll(domain, "_", " ")
		if info.Area != "" {
			desc += " in " + info.Area
		}
		level, floor := reg.FloorOrder(info.FloorID)
		rows = append(rows, row{Entity: Entity{ObjectID: st.EntityID, Name: name, Type: domain, Kind: m.Kind,
			Options: m.Options, Writable: m.Writable, Description: desc, Group: group,
			Places: places(info.AreaID, info.Area, info.FloorID, info.Floor)},
			level: level, floor: floor, area: info.Area})
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if (a.area == "") != (b.area == "") {
			return a.area != ""
		}
		if a.level != b.level {
			return a.level < b.level
		}
		if a.floor != b.floor {
			return a.floor < b.floor
		}
		if a.area != b.area {
			return a.area < b.area
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	out := make([]Entity, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Entity)
	}
	return out
}

// WatchEntityList reports the list after the registries change and whenever
// an entity appears or disappears.
func (s homeAssistantSource) WatchEntityList(fn func([]Entity)) func() {
	stopRegistry := s.d.WatchRegistry(func(homeassistant.RegistryChange) {
		if s.Connected() {
			fn(s.Entities())
		}
	})
	stopStates, err := s.d.SubscribeStateChangesDetailed(func(c homeassistant.StateChange) {
		added := len(c.Old) == 0 || string(c.Old) == "null"
		removed := len(c.New) == 0 || string(c.New) == "null"
		if (added || removed) && s.Connected() {
			fn(s.Entities())
		}
	})
	if err != nil {
		stopStates = func() {}
	}
	return func() {
		stopRegistry()
		stopStates()
	}
}

// WatchRenames reports entities renamed in Home Assistant's entity registry.
func (s homeAssistantSource) WatchRenames(fn func(map[string]string)) func() {
	return s.d.WatchRegistry(func(c homeassistant.RegistryChange) {
		if len(c.Renamed) > 0 {
			fn(c.Renamed)
		}
	})
}

// openHABSource offers an openHAB server's items.
type openHABSource struct{ d *openhab.Driver }

// OpenHAB is an openHAB server's items as a source.
func OpenHAB(d *openhab.Driver) Source { return openHABSource{d} }

func (s openHABSource) Transport() string { return mqttrelay.TransportOpenHAB }

func (s openHABSource) Connected() bool { return s.d.Connected() }

// Label is empty: item labels already name the thing.
func (s openHABSource) Label() string { return "" }

// Entities lists every item by floor and location, items in no location last.
func (s openHABSource) Entities() []Entity {
	catalog := s.d.Items()
	views := catalog.Views()
	out := make([]Entity, 0, len(views))
	for _, v := range views {
		it := catalog[v.Name]
		p := openhab.Propose(it)
		group := v.LocationLabel
		if v.FloorLabel != "" && v.FloorLabel != v.LocationLabel {
			group = v.FloorLabel + " · " + v.LocationLabel
		}
		desc := "openHAB " + v.Type + " item " + it.Name
		if v.LocationLabel != "" {
			desc += " in " + v.LocationLabel
		}
		out = append(out, Entity{ObjectID: it.Name, Name: v.Label, Type: v.Type, Kind: p.Kind,
			Options: p.Options, Writable: p.Writable, Reason: p.Reason, Description: desc, Group: group,
			Places: places(v.Location, v.LocationLabel, v.Floor, v.FloorLabel)})
	}
	return out
}

// WatchEntityList reports the list after every connect and item change.
func (s openHABSource) WatchEntityList(fn func([]Entity)) func() {
	return s.d.WatchItems(func(openhab.Catalog) { fn(s.Entities()) })
}

// places lowers the non-empty names of where an entity is.
func places(names ...string) []string {
	var out []string
	for _, n := range names {
		if n = strings.ToLower(strings.TrimSpace(n)); n != "" {
			out = append(out, n)
		}
	}
	return out
}
