package controls

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// S is the package-level store, wired by serve.go at boot and read by the
// control nodes, the API handlers, and the dashboard bridge.
var S = &Store{controls: make(map[uuid.UUID]*entry), watchers: make(map[int]func(Change))}

// Publisher delivers a control change to the WebSocket broker. It is injected
// rather than imported so the store can be exercised without a hub.
type Publisher func(topic, msgType string, payload any)

// Snapshot is a control as everything outside the store sees it: the
// definition plus the decoded current value.
type Snapshot struct {
	ID          uuid.UUID      `json:"id" description:"Unique identifier."`
	Name        string         `json:"name" description:"Display name."`
	Kind        string         `json:"kind" description:"Control kind."`
	Icon        string         `json:"icon" description:"Line-awesome icon class."`
	Options     map[string]any `json:"options" description:"Kind-specific settings."`
	Value       any            `json:"value" description:"Current value."`
	AllowPublic bool           `json:"allow_public" description:"Whether anonymous viewers may read and set it."`
	UpdatedAt   time.Time      `json:"updated_at" description:"When the value or definition last changed."`
}

// Change is one control transition, delivered to watchers and published on the
// broker. Old is the value before the write, which is what lets a flow trigger
// on an edge rather than on every write.
type Change struct {
	Control Snapshot `json:"control"`
	Old     any      `json:"old"`
	// Attribute names which part of a composite control moved, empty when the
	// whole value was written. A flow watching a light's brightness reads it
	// rather than diffing two objects itself.
	Attribute string `json:"attribute,omitempty"`
	// Source names what performed the write — "dashboard", "flow", "api", or
	// "mcp". A flow's write also names the flow and run, so a trigger can
	// ignore its own flow's writes while another flow's still start it.
	Source string `json:"source"`
	FlowID string `json:"flow_id,omitempty"`
	RunID  string `json:"run_id,omitempty"`
	// Depth counts control handoffs between flows in the chain that led to
	// this write; Chain names them, oldest first. Together they stop and
	// explain a loop of flows writing each other's controls.
	Depth int      `json:"depth,omitempty"`
	Chain []string `json:"chain,omitempty"`
	// Actor is who made the write; the system for a write with no actor.
	Actor events.Actor `json:"actor"`
	At    time.Time    `json:"at"`
	// Definition marks the announcement of a control being created or its
	// settings edited rather than written. Old and the value can be equal
	// either way, since a repeated press of a buttons control is a write that
	// moves nothing, so a watcher that acts on writes reads this.
	Definition bool `json:"definition,omitempty"`
}

// Writer is who performs a write: a dashboard, the API, MCP, or a flow run
// with its place in a chain of control handoffs.
type Writer struct {
	Source string
	FlowID string
	RunID  string
	Depth  int
	Chain  []string
	// Actor is who made the write, for the event log: the user and dashboard,
	// the API key, or the flow.
	Actor events.Actor
}

// By is a writer with a source and no flow.
func By(source string) Writer { return Writer{Source: source} }

// Write sources.
const (
	SourceDashboard = "dashboard"
	SourceFlow      = "flow"
	SourceAPI       = "api"
	SourceMCP       = "mcp"
)

// entry is the cached definition and decoded value of one control.
type entry struct {
	snapshot Snapshot
}

// Store holds every control in memory, backed by the database. Reads are in
// memory because a dashboard row and a pulled node both read on the hot path;
// writes go through the database first so a restart cannot lose a value the
// dashboard already showed as set.
type Store struct {
	mu       sync.RWMutex
	controls map[uuid.UUID]*entry
	// writeMu admits one write at a time from reading the current value to
	// storing the merged one. Two attribute writes to a composite control, a
	// dashboard setting brightness beside a flow setting colour, would each
	// merge into the same stale value and the later one would erase the
	// earlier.
	writeMu sync.Mutex

	watchMu  sync.RWMutex
	watchers map[int]func(Change)
	nextID   int

	publish Publisher
}

// SetPublisher wires the broker the store announces changes on.
func (s *Store) SetPublisher(p Publisher) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publish = p
}

// Load reads every stored control into the cache, replacing what is there.
// A control whose stored options or value will not decode still loads: it is
// better to show it with its initial value than to hide it from the panel that
// could fix it.
func (s *Store) Load() error {
	rows, err := models.ListControls()
	if err != nil {
		return err
	}
	next := make(map[uuid.UUID]*entry, len(rows))
	for _, row := range rows {
		next[row.ID] = &entry{snapshot: snapshotOf(row)}
	}

	s.mu.Lock()
	s.controls = next
	s.mu.Unlock()
	return nil
}

// List returns every control ordered by name.
func (s *Store) List() []Snapshot {
	s.mu.RLock()
	out := make([]Snapshot, 0, len(s.controls))
	for _, e := range s.controls {
		out = append(out, e.snapshot)
	}
	s.mu.RUnlock()

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Get returns one control by ID.
func (s *Store) Get(id uuid.UUID) (Snapshot, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.controls[id]
	if !ok {
		return Snapshot{}, false
	}
	return e.snapshot, true
}

// Resolve looks a control up by ID or, failing that, by name. Flows reference
// controls by ID so a rename cannot break a graph, but a name is what an
// operator types, so both are accepted everywhere a reference is read.
func (s *Store) Resolve(ref string) (Snapshot, bool) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return Snapshot{}, false
	}
	if id, err := uuid.Parse(ref); err == nil {
		if snap, ok := s.Get(id); ok {
			return snap, true
		}
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, e := range s.controls {
		if strings.EqualFold(e.snapshot.Name, ref) {
			return e.snapshot, true
		}
	}
	return Snapshot{}, false
}

// Create defines a new control, seeding it with the kind's initial value.
func (s *Store) Create(name, kind, icon string, options map[string]any, allowPublic bool) (Snapshot, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Snapshot{}, fmt.Errorf("name is required")
	}
	options, err := NormalizeOptions(kind, options)
	if err != nil {
		return Snapshot{}, err
	}

	row := &models.Control{
		Name:        name,
		Kind:        kind,
		Icon:        icon,
		Options:     encodeJSON(options),
		State:       encodeJSON(InitialValue(kind, options)),
		AllowPublic: allowPublic,
	}
	if err := models.CreateControl(row); err != nil {
		return Snapshot{}, err
	}

	snap := snapshotOf(row)
	s.mu.Lock()
	s.controls[row.ID] = &entry{snapshot: snap}
	s.mu.Unlock()

	s.announce(Change{Control: snap, Old: snap.Value, Source: SourceAPI, At: row.UpdatedAt, Definition: true})
	return snap, nil
}

// Update rewrites a control's definition. The value is kept when the kind is
// unchanged and re-seeded when it is not, because a value from the old kind is
// not one the new kind can render.
func (s *Store) Update(id uuid.UUID, name, kind, icon string, options map[string]any, allowPublic bool) (Snapshot, error) {
	snap, change, err := s.redefine(id, name, kind, icon, options, allowPublic)
	if err != nil {
		return Snapshot{}, err
	}
	s.announce(change)
	return snap, nil
}

// redefine reads, rewrites, and saves a control's row under the write lock.
// The save writes the whole row, value included, so a write landing between
// the read and the save would be replaced by the value read before it.
func (s *Store) redefine(id uuid.UUID, name, kind, icon string, options map[string]any, allowPublic bool) (Snapshot, Change, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	row, err := models.FindControlByID(id)
	if err != nil {
		return Snapshot{}, Change{}, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return Snapshot{}, Change{}, fmt.Errorf("name is required")
	}
	options, err = NormalizeOptions(kind, options)
	if err != nil {
		return Snapshot{}, Change{}, err
	}

	kindChanged := row.Kind != kind
	row.Name = name
	row.Kind = kind
	row.Icon = icon
	row.Options = encodeJSON(options)
	row.AllowPublic = allowPublic
	if kindChanged {
		row.State = encodeJSON(InitialValue(kind, options))
	} else if _, err := Coerce(kind, options, decodeValue(row.State)); err != nil {
		// The settings that bound the value changed under it — a select that
		// dropped an option, a number whose range moved. Re-seed rather than
		// leave a value the control now refuses.
		row.State = encodeJSON(InitialValue(kind, options))
	}
	if err := models.SaveControl(row); err != nil {
		return Snapshot{}, Change{}, err
	}

	snap := snapshotOf(row)
	s.mu.Lock()
	old := any(nil)
	if e, ok := s.controls[id]; ok {
		old = e.snapshot.Value
	}
	s.controls[id] = &entry{snapshot: snap}
	s.mu.Unlock()

	return snap, Change{Control: snap, Old: old, Source: SourceAPI, At: row.UpdatedAt, Definition: true}, nil
}

// Delete removes a control. Placements pointing at it keep resolving to
// nothing rather than erroring, the same way a dashboard row survives a
// widget whose plugin unloaded. The deletion is announced on the control's
// topic so an open dashboard learns the control is gone rather than drawing
// its last value forever; flow watchers are not told, because a deletion is
// not a change a control-changed trigger should fire on.
func (s *Store) Delete(id uuid.UUID) error {
	// The write lock keeps out a write that already read the control and
	// would store it again once the row and the cached copy were gone.
	s.writeMu.Lock()
	snap, existed := s.Get(id)
	if err := models.DeleteControl(id); err != nil {
		s.writeMu.Unlock()
		return err
	}
	s.mu.Lock()
	delete(s.controls, id)
	publish := s.publish
	s.mu.Unlock()
	s.writeMu.Unlock()
	if existed && publish != nil {
		publish("control:"+id.String(), "control_deleted",
			map[string]any{"id": id.String(), "name": snap.Name, "deleted": true})
	}
	return nil
}

// Set writes a control's value. The value is coerced to the kind first, so a
// refused write leaves the stored value alone, and watchers see the change
// only after it is persisted.
//
// A composite control merges rather than replaces: writing {on: true} to a
// light leaves its brightness where it was, the same bargain an MQTT device's
// state script makes. A scalar written to a composite control lands on the
// kind's primary attribute, so setting a light to true still turns it on.
func (s *Store) Set(id uuid.UUID, value any, w Writer) (Snapshot, error) {
	return s.write(id, "", value, w)
}

// SetAttribute writes one part of a control, leaving the rest alone. It is how
// a dashboard sets a light's brightness without touching its colour and how a
// flow addresses one attribute of a thermostat.
func (s *Store) SetAttribute(id uuid.UUID, attribute string, value any, w Writer) (Snapshot, error) {
	return s.write(id, strings.TrimSpace(attribute), value, w)
}

// write is the one path a value takes into the store: coerce, persist, then
// announce. Nothing announces before the row is written, so a watcher can
// never read back a value a restart would lose.
func (s *Store) write(id uuid.UUID, attribute string, value any, w Writer) (Snapshot, error) {
	s.writeMu.Lock()
	next, change, err := s.apply(id, attribute, value, w)
	s.writeMu.Unlock()
	if err != nil || change == nil {
		return next, err
	}
	s.announce(*change)
	return next, nil
}

// apply coerces, persists, and stores one write, returning the change to
// announce, or nil when the write changed nothing. The caller holds writeMu.
func (s *Store) apply(id uuid.UUID, attribute string, value any, w Writer) (Snapshot, *Change, error) {
	s.mu.RLock()
	e, ok := s.controls[id]
	s.mu.RUnlock()
	if !ok {
		return Snapshot{}, nil, fmt.Errorf("control not found")
	}
	current := e.snapshot
	def, known := FindKind(current.Kind)

	// Coerce, and for a composite kind fold the result into what is there.
	now := time.Now()
	var coerced any
	switch {
	case attribute != "":
		one, err := CoerceAttribute(current.Kind, current.Options, attribute, value)
		if err != nil {
			return Snapshot{}, nil, err
		}
		if err := CheckAgainstValue(current.Kind, current.Value, attribute, one); err != nil {
			return Snapshot{}, nil, err
		}
		if attr, _ := def.Attribute(attribute); attr.WriteOnly {
			// An action is announced with the value it acted on and stored
			// nowhere: a player is never left "skipping", and a later read
			// finds the value as it was.
			announced := current
			announced.Value = MergeAttribute(current.Value, attribute, one)
			return current, &Change{
				Control:   announced,
				Old:       current.Value,
				Attribute: attribute,
				Source:    w.Source,
				FlowID:    w.FlowID,
				RunID:     w.RunID,
				Depth:     w.Depth,
				Chain:     w.Chain,
				Actor:     w.Actor,
				At:        now,
			}, nil
		}
		if known && def.Composite() {
			coerced = MergeAttribute(current.Value, attribute, one)
		} else {
			// A scalar kind has one attribute and it is the value itself;
			// wrapping it in a one-key object would corrupt the control.
			coerced = one
			attribute = ""
		}

	case known && def.Composite():
		fields, isObject := value.(map[string]any)
		if !isObject {
			attribute = def.PrimaryAttribute().ID
			one, err := CoerceAttribute(current.Kind, current.Options, attribute, value)
			if err != nil {
				return Snapshot{}, nil, err
			}
			coerced = MergeAttribute(current.Value, attribute, one)
			break
		}
		checked, err := Coerce(current.Kind, current.Options, fields)
		if err != nil {
			return Snapshot{}, nil, err
		}
		merged := map[string]any{}
		if fields, ok := current.Value.(map[string]any); ok {
			for k, v := range fields {
				merged[k] = v
			}
		}
		for k, v := range checked.(map[string]any) {
			merged[k] = v
		}
		coerced = merged

	default:
		var err error
		if coerced, err = Coerce(current.Kind, current.Options, value); err != nil {
			return Snapshot{}, nil, err
		}
	}

	if current.Kind == KindLocation {
		coerced = StampSince(current.Value, coerced, now)
	}

	// Attributes are checked one at a time, so the merged value can still hold
	// a combination no attribute alone is guilty of.
	if err := ValidateValue(current.Kind, coerced); err != nil {
		return Snapshot{}, nil, err
	}

	// A write that changes nothing is not a change. A press-only kind is the
	// exception: pressing Up twice is two presses, and a store that swallowed
	// the second one would leave the flow behind it unrun.
	if !PressOnly(current.Kind) && SameValue(current.Value, coerced) {
		return current, nil, nil
	}

	if err := models.UpdateControlState(id, encodeJSON(coerced)); err != nil {
		return Snapshot{}, nil, err
	}

	next := current
	next.Value = coerced
	next.UpdatedAt = now

	s.mu.Lock()
	s.controls[id] = &entry{snapshot: next}
	s.mu.Unlock()

	return next, &Change{
		Control:   next,
		Old:       current.Value,
		Attribute: attribute,
		Source:    w.Source,
		FlowID:    w.FlowID,
		RunID:     w.RunID,
		Depth:     w.Depth,
		Chain:     w.Chain,
		Actor:     w.Actor,
		At:        now,
	}, nil
}

// Watch registers a change handler and returns the function that removes it.
// Handlers run on the writer's goroutine, so a watcher that blocks blocks the
// write; the control trigger hands off to the engine and returns.
func (s *Store) Watch(fn func(Change)) func() {
	s.watchMu.Lock()
	id := s.nextID
	s.nextID++
	s.watchers[id] = fn
	s.watchMu.Unlock()

	return func() {
		s.watchMu.Lock()
		delete(s.watchers, id)
		s.watchMu.Unlock()
	}
}

// announce publishes a change on the broker and hands it to every watcher.
func (s *Store) announce(c Change) {
	s.mu.RLock()
	publish := s.publish
	s.mu.RUnlock()
	if publish != nil {
		publish("control:"+c.Control.ID.String(), "control", c)
	}

	s.watchMu.RLock()
	handlers := make([]func(Change), 0, len(s.watchers))
	for _, fn := range s.watchers {
		handlers = append(handlers, fn)
	}
	s.watchMu.RUnlock()

	for _, fn := range handlers {
		fn(c)
	}
}

// snapshotOf decodes a stored row into the form everything else reads.
func snapshotOf(row *models.Control) Snapshot {
	opts := map[string]any{}
	if row.Options != "" {
		_ = json.Unmarshal([]byte(row.Options), &opts)
	}
	value := decodeValue(row.State)
	if value == nil {
		value = InitialValue(row.Kind, opts)
	}
	return Snapshot{
		ID:          row.ID,
		Name:        row.Name,
		Kind:        row.Kind,
		Icon:        row.Icon,
		Options:     opts,
		Value:       value,
		AllowPublic: row.AllowPublic,
		UpdatedAt:   row.UpdatedAt,
	}
}

// decodeValue reads a stored JSON value, yielding nil for anything unreadable
// so the caller can fall back to the kind's initial value.
func decodeValue(raw string) any {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil
	}
	return v
}

// encodeJSON serializes a stored value, falling back to null so a write is
// never left half-formed.
func encodeJSON(v any) string {
	raw, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(raw)
}

// SameValue compares two coerced values. A scalar kind has already been
// normalized to bool, float64, or string, so equality is enough; a composite
// kind holds a map, which is not comparable, so the encoded form stands in.
// encoding/json sorts object keys, so two equal lights encode identically
// whatever order their attributes were written in.
func SameValue(a, b any) bool {
	_, aMap := a.(map[string]any)
	_, bMap := b.(map[string]any)
	if aMap || bMap {
		return encodeJSON(a) == encodeJSON(b)
	}
	return a == b
}

// StampSince sets a location value's since from the value before it: the time
// of the write when state changed, the previous since when it did not. A since
// the writer supplied is never kept, because coerceObject passes read-only
// attributes through and the time of the last move is not the caller's to set.
// The MQTT relay applies the same rule to a device's decoded state, where a
// script may return state as a list or object, so states compare by their
// encoded form; == on two slices panics.
func StampSince(before, after any, now time.Time) any {
	next, ok := after.(map[string]any)
	if !ok {
		return after
	}
	prev, _ := before.(map[string]any)
	out := make(map[string]any, len(next))
	for k, v := range next {
		out[k] = v
	}
	delete(out, "since")
	state, hasState := out["state"]
	switch {
	case hasState && (prev == nil || encodeJSON(prev["state"]) != encodeJSON(state)):
		out["since"] = now.UTC().Format(time.RFC3339)
	case prev != nil && prev["since"] != nil:
		out["since"] = prev["since"]
	}
	return out
}
