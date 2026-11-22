package openhab

import (
	"encoding/json"
	"sort"
	"strings"
)

// StateDescription is what openHAB says an item's state may be. openHAB does
// not enforce it, so karakuri checks commands against it before sending.
type StateDescription struct {
	Minimum  *float64 `json:"minimum,omitempty"`
	Maximum  *float64 `json:"maximum,omitempty"`
	Step     *float64 `json:"step,omitempty"`
	Pattern  string   `json:"pattern,omitempty"`
	ReadOnly bool     `json:"readOnly,omitempty"`
	Options  []Option `json:"options,omitempty"`
}

// Option is one state an item offers.
type Option struct {
	Value string `json:"value"`
	Label string `json:"label,omitempty"`
}

// Semantics is an item's place in openHAB's semantic model.
type Semantics struct {
	// Value is the semantic class, such as Location_Indoor_Room_Kitchen,
	// Equipment_LightSource_Lightbulb, or Point_Control.
	Value string `json:"value"`
	// Config holds the relations: hasLocation, isPointOf, isPartOf.
	Config map[string]any `json:"config,omitempty"`
}

// Item is one openHAB item as karakuri holds it.
type Item struct {
	Name  string `json:"name"`
	Label string `json:"label,omitempty"`
	// Type is the item type, with a dimension for numbers such as
	// Number:Temperature.
	Type string `json:"type"`
	// GroupType is a group's base type, empty for other items and for
	// groups without one.
	GroupType string `json:"groupType,omitempty"`
	State     string `json:"state"`
	// UnitSymbol is a dimensioned number's unit, such as °C, which openHAB 4
	// and later report even while the state is NULL.
	UnitSymbol       string            `json:"unitSymbol,omitempty"`
	StateDescription *StateDescription `json:"stateDescription,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	GroupNames       []string          `json:"groupNames,omitempty"`
	Semantics        *Semantics        `json:"-"`
}

// rawItem is an item as the REST API and event payloads write it.
type rawItem struct {
	Item
	Metadata struct {
		Semantics *Semantics `json:"semantics"`
	} `json:"metadata"`
}

// decodeItems reads the REST item list.
func decodeItems(raw []byte) ([]Item, error) {
	var in []rawItem
	if err := json.Unmarshal(raw, &in); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(in))
	for _, r := range in {
		it := r.Item
		it.Semantics = r.Metadata.Semantics
		out = append(out, it)
	}
	return out, nil
}

// HasValue reports whether a state is a value rather than openHAB's NULL
// (never set) or UNDEF (unknown).
func HasValue(state string) bool {
	return state != "" && state != "NULL" && state != "UNDEF"
}

// BaseType is the item's type without a dimension, and a group's base type
// when it has one.
func (it Item) BaseType() string {
	t := it.Type
	if t == "Group" && it.GroupType != "" {
		t = it.GroupType
	}
	if i := strings.IndexByte(t, ':'); i >= 0 {
		t = t[:i]
	}
	return t
}

// Dimension is a number's unit dimension, such as Temperature, empty for
// plain numbers and other types.
func (it Item) Dimension() string {
	t := it.Type
	if t == "Group" {
		t = it.GroupType
	}
	if i := strings.IndexByte(t, ':'); i >= 0 {
		return t[i+1:]
	}
	return ""
}

// DisplayLabel is the label, or the name for an item without one.
func (it Item) DisplayLabel() string {
	if strings.TrimSpace(it.Label) != "" {
		return it.Label
	}
	return it.Name
}

// relation reads one semantic relation.
func (it Item) relation(key string) string {
	if it.Semantics == nil {
		return ""
	}
	s, _ := it.Semantics.Config[key].(string)
	return s
}

// isLocation reports whether an item is a location group.
func (it Item) isLocation() bool {
	return it.Semantics != nil && strings.HasPrefix(it.Semantics.Value, "Location")
}

// isFloor reports whether an item is a floor location.
func (it Item) isFloor() bool {
	return it.Semantics != nil && strings.Contains(it.Semantics.Value, "_Floor")
}

// Place is where an item sits in the semantic model.
type Place struct {
	Location      string `json:"location,omitempty"`
	LocationLabel string `json:"location_label,omitempty"`
	Floor         string `json:"floor,omitempty"`
	FloorLabel    string `json:"floor_label,omitempty"`
}

// Catalog is a set of items by name, as one connection holds them.
type Catalog map[string]Item

// Locate places an item: its own location, else its equipment's, else the
// nearest location among the groups it belongs to; the floor is the
// location's enclosing floor, or the location itself when it is a floor.
func (c Catalog) Locate(name string) Place {
	it, ok := c[name]
	if !ok {
		return Place{}
	}
	loc := it.relation("hasLocation")
	if loc == "" {
		if eq, ok := c[it.relation("isPointOf")]; ok {
			loc = eq.relation("hasLocation")
		}
	}
	if loc == "" {
		loc = c.nearestLocation(it, map[string]bool{})
	}
	l, ok := c[loc]
	if !ok {
		return Place{}
	}
	p := Place{Location: l.Name, LocationLabel: l.DisplayLabel()}
	switch {
	case l.isFloor():
		p.Floor, p.FloorLabel = l.Name, l.DisplayLabel()
	default:
		if f, ok := c[l.relation("isPartOf")]; ok && f.isFloor() {
			p.Floor, p.FloorLabel = f.Name, f.DisplayLabel()
		}
	}
	return p
}

// nearestLocation walks an item's groups breadth first for a location. seen
// stops a group cycle, which openHAB allows.
func (c Catalog) nearestLocation(it Item, seen map[string]bool) string {
	queue := append([]string(nil), it.GroupNames...)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true
		g, ok := c[name]
		if !ok {
			continue
		}
		if g.isLocation() {
			return g.Name
		}
		queue = append(queue, g.GroupNames...)
	}
	return ""
}

// MemberOf reports whether an item belongs to a group at any depth.
func (c Catalog) MemberOf(name, group string) bool {
	it, ok := c[name]
	if !ok {
		return false
	}
	seen := map[string]bool{}
	queue := append([]string(nil), it.GroupNames...)
	for len(queue) > 0 {
		g := queue[0]
		queue = queue[1:]
		if g == group {
			return true
		}
		if seen[g] {
			continue
		}
		seen[g] = true
		if parent, ok := c[g]; ok {
			queue = append(queue, parent.GroupNames...)
		}
	}
	return false
}

// InLocation reports whether an item sits in a location or anywhere inside
// it, such as a room on a floor.
func (c Catalog) InLocation(name, location string) bool {
	p := c.Locate(name)
	if p.Location == "" {
		return false
	}
	if p.Location == location || p.Floor == location {
		return true
	}
	return c.MemberOf(p.Location, location)
}

// Sorted lists the catalog's items by floor, location, and label, with items
// in no location last.
func (c Catalog) Sorted() []Item {
	out := make([]Item, 0, len(c))
	places := make(map[string]Place, len(c))
	for name, it := range c {
		out = append(out, it)
		places[name] = c.Locate(name)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := places[out[i].Name], places[out[j].Name]
		if (a.Location == "") != (b.Location == "") {
			return a.Location != ""
		}
		if a.FloorLabel != b.FloorLabel {
			return strings.ToLower(a.FloorLabel) < strings.ToLower(b.FloorLabel)
		}
		if a.LocationLabel != b.LocationLabel {
			return strings.ToLower(a.LocationLabel) < strings.ToLower(b.LocationLabel)
		}
		return strings.ToLower(out[i].DisplayLabel()) < strings.ToLower(out[j].DisplayLabel())
	})
	return out
}

// ItemView is an item as pickers, REST, and MCP list it.
type ItemView struct {
	Name     string `json:"name" description:"Item name."`
	Label    string `json:"label" description:"Item label, or the name when it has none."`
	Type     string `json:"type" description:"Item type, with a dimension for numbers such as Number:Temperature."`
	State    string `json:"state" description:"Current state; NULL or UNDEF when it has none."`
	ReadOnly bool   `json:"read_only,omitempty" description:"Whether openHAB marks the item read only."`
	Place
}

// Views lists the catalog's items in Sorted order.
func (c Catalog) Views() []ItemView {
	sorted := c.Sorted()
	out := make([]ItemView, 0, len(sorted))
	for _, it := range sorted {
		v := ItemView{Name: it.Name, Label: it.DisplayLabel(), Type: it.Type, State: it.State, Place: c.Locate(it.Name)}
		if it.Type == "Group" && it.GroupType != "" {
			v.Type = "Group:" + it.GroupType
		}
		v.ReadOnly = it.StateDescription != nil && it.StateDescription.ReadOnly
		out = append(out, v)
	}
	return out
}
