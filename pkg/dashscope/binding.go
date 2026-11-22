package dashscope

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/models"
)

// Binding is one host data source a widget's config points at: the shape
// widgets/shared/entitySource.ts writes. The host reads it rather than the
// whole config because a widget's config is otherwise opaque — the binding is
// the part that says which of the house's data the widget is asking for, and
// therefore the only part authorization can be built on.
type Binding struct {
	Source       string `json:"source"`
	ControlID    string `json:"controlId"`
	ConnectionID string `json:"connectionId"`
	EntityID     string `json:"entityId"`
	DeviceID     string `json:"deviceId"`
	FlowID       string `json:"flowId"`
	Trigger      string `json:"trigger"`
	// Variable names the dashboard variable a variable binding sets, and
	// Scope says whether it sets it for this screen or for every screen.
	Variable string `json:"variable"`
	// Camera names an NVR camera of the connection, or * for every camera.
	Camera string `json:"camera"`
	Scope  string `json:"scope"`
	// DisplayOnly is the author saying this binding shows a reading and nothing
	// more. It is a declaration rather than a derivation because the host
	// cannot know whether a Home Assistant entity can be written, and because
	// what a dashboard may operate is not a question presentation should
	// answer.
	DisplayOnly bool `json:"displayOnly"`
	// Control is the widget face a row stored before kinds were declared at the
	// source. The only value still read from it is "value", which was how an
	// author used to say display-only; a deployed snapshot cannot be rewritten,
	// so the old spelling has to keep meaning what it meant.
	Control string `json:"control"`
	// Triggers are the several entry points one row can press, each granting its
	// own capability. A row with none is the single trigger named above it.
	Triggers []TriggerButton `json:"triggers"`
}

// TriggerButton is one press of a flow-trigger binding: which flow's named
// entry point it starts. Every button is granted separately, so a row cannot
// reach a trigger it does not draw.
type TriggerButton struct {
	FlowID  string `json:"flowId"`
	Trigger string `json:"trigger"`
}

// Binding sources, matching SourceKind in widgets/shared/entitySource.ts.
const (
	SourceControl = "control"
	SourceHA      = "ha"
	SourceMQTT    = "mqtt"
	SourceTrigger = "flow-trigger"
	// SourceVariable is a widget that sets a dashboard variable for every
	// viewer, such as Variable buttons set to every screen.
	SourceVariable = "variable"
	// SourceNVR is one camera of a network video recorder: its images,
	// stream, and events. It is only ever read.
	SourceNVR = "nvr"
)

// VariableScopeDashboard is the scope of a variable binding that sets the
// dashboard-wide value. Any other scope switches only the viewer's screen.
const VariableScopeDashboard = "dashboard"

// controlValue is the face an author used to pick to say a binding only
// displays, before Display only was a checkbox of its own.
const controlValue = "value"

// bindingsFrom pulls a placement's bindings out of its stored config. The path
// comes from the widget's manifest: "." when the config is itself one binding,
// otherwise the field holding them. A config that does not parse yields none,
// which denies rather than guesses.
func bindingsFrom(configJSON, path string) []Binding {
	bindings, _ := ParseBindings(configJSON, path)
	return bindings
}

// ParseBindings reads the manifest-declared binding field for authoring diagnostics.
func ParseBindings(configJSON, path string) ([]Binding, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nil
	}
	if configJSON == "" {
		configJSON = "{}"
	}
	raw := json.RawMessage(configJSON)
	if path == "." {
		var binding Binding
		if err := json.Unmarshal(raw, &binding); err != nil {
			return nil, err
		}
		return []Binding{binding}, nil
	}
	if path != "." {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return nil, err
		}
		var ok bool
		raw, ok = object[path]
		if !ok {
			return nil, nil
		}
	}
	if string(raw) == "null" {
		return nil, fmt.Errorf("binding must be an object or array")
	}
	var list []Binding
	if err := json.Unmarshal(raw, &list); err == nil {
		return list, nil
	}
	var binding Binding
	if err := json.Unmarshal(raw, &binding); err != nil {
		return nil, err
	}
	return []Binding{binding}, nil
}

// writable reports whether pressing this binding is part of what the dashboard
// shows. A row the author marked display only shows a reading and nothing more,
// so it grants no write however the dashboard's control policy is set.
func (b Binding) writable() bool {
	return !b.DisplayOnly && b.Control != controlValue
}

// capabilities turns one binding into what it authorizes. Read comes with the
// binding; write is separate and still has to clear the dashboard's control
// policy before it means anything.
func (b Binding) capabilities() []Capability {
	switch b.Source {
	case SourceControl:
		if b.ControlID == "" {
			return nil
		}
		caps := []Capability{{Kind: CapControl, Ref: b.ControlID}}
		if b.writable() {
			caps = append(caps, Capability{Kind: CapControl, Ref: b.ControlID, Write: true})
		}
		return caps

	case SourceHA:
		if b.ConnectionID == "" || b.EntityID == "" {
			return nil
		}
		caps := []Capability{{Kind: CapHA, Owner: b.ConnectionID, Ref: b.EntityID}}
		if b.writable() {
			caps = append(caps, Capability{Kind: CapHA, Owner: b.ConnectionID, Ref: b.EntityID, Write: true})
		}
		return caps

	case SourceMQTT:
		if b.DeviceID == "" {
			return nil
		}
		caps := []Capability{{Kind: CapDevice, Ref: b.DeviceID}}
		if b.writable() {
			caps = append(caps, Capability{Kind: CapDevice, Ref: b.DeviceID, Write: true})
		}
		return caps

	case SourceNVR:
		if b.ConnectionID == "" || b.Camera == "" {
			return nil
		}
		return []Capability{{Kind: CapNVR, Owner: b.ConnectionID, Ref: b.Camera}}

	case SourceVariable:
		// Choosing a value for every viewer is the only thing the binding
		// does, so like a trigger it grants a write or nothing. A widget that
		// switches only its own screen is display only and needs no grant.
		if b.Variable == "" || b.Scope != VariableScopeDashboard || !b.writable() {
			return nil
		}
		return []Capability{{Kind: CapVariable, Ref: b.Variable, Write: true}}

	case SourceTrigger:
		// A trigger has nothing to read: the only thing a row does with one is
		// press it, so the binding grants a write or nothing. A row of buttons
		// grants one capability per button, because that is what it draws.
		if !b.writable() {
			return nil
		}
		presses := b.Triggers
		if len(presses) == 0 {
			presses = []TriggerButton{{FlowID: b.FlowID, Trigger: b.Trigger}}
		}
		var caps []Capability
		for _, press := range presses {
			if press.FlowID == "" || press.Trigger == "" {
				continue
			}
			caps = append(caps, Capability{
				Kind: CapTrigger, Owner: press.FlowID, Ref: press.Trigger, Write: true,
			})
		}
		return caps
	}
	return nil
}

// ConditionBindings returns the sources a placement's visibility and cue
// conditions read, visibility conditions first. Each is display-only: a condition shows or hides a widget and never
// presses anything, so it grants a read and no write.
func ConditionBindings(d *models.WidgetDisplay) []Binding {
	if d == nil {
		return nil
	}
	var out []Binding
	for _, c := range d.AllConditions() {
		if c.Kind != models.ConditionState {
			continue
		}
		out = append(out, Binding{
			Source:       c.Source,
			ControlID:    c.ControlID,
			ConnectionID: c.ConnectionID,
			EntityID:     c.EntityID,
			DeviceID:     c.DeviceID,
			DisplayOnly:  true,
		})
	}
	return out
}
