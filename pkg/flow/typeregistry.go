package flow

import "sync"

// Port types are registered rather than hard-coded so a plugin can bring its
// own. A type is data: its name, how the editor describes it, which types a
// value of it also satisfies, and whether it can stand as a value parameter.
// The compatibility relations (Compatible, MessageCompatible) and the editor's
// labels, tooltips, and rotating-hue colors are all derived from this registry,
// so registering a type is the whole job — nothing else needs a case added.

// PortTypeDef describes one payload type a port can declare.
type PortTypeDef struct {
	// Name is the identifier ports carry, e.g. "string".
	Name string `json:"name" description:"Type identifier used on ports."`
	// Label is the display name shown on port badges.
	Label string `json:"label" description:"Display name shown on port badges."`
	// Accepts is the human phrase for what the type takes, shown in editor
	// tooltips ("text", "true or false").
	Accepts string `json:"accepts,omitempty" description:"Human phrase for what the type takes."`
	// WidensTo lists types a value of this type also satisfies: an integer
	// widens to float and number, binary widens to string. The relation is
	// directional and not transitive; list every target explicitly.
	WidensTo []string `json:"widens_to,omitempty" description:"Types a value of this type also satisfies."`
	// Value marks a type usable as a subflow value parameter, pulled over a
	// value edge rather than delivered as a message.
	Value bool `json:"value,omitempty" description:"Whether the type can stand as a value parameter."`
	// Script marks a type an author may declare on a script node's ports. The
	// data types all allow it; a capability type like an AI tool set describes
	// a payload with an enforced shape a script cannot promise, so it stays
	// out of the picker and off ValidScriptPortType.
	Script bool `json:"script,omitempty" description:"Whether the type may be declared on a script node's ports."`
}

// portTypeRegistry holds registered port types in registration order. The
// order is part of the contract: the editor assigns rotating-hue colors by
// position, so appending never recolors an existing type.
var (
	portTypeMu    sync.RWMutex
	portTypeOrder []PortTypeDef
	portTypeByID  = map[string]int{}
)

// RegisterPortType registers a payload type, replacing an existing definition
// with the same name in place so its color position holds.
func RegisterPortType(def PortTypeDef) {
	portTypeMu.Lock()
	defer portTypeMu.Unlock()
	if i, ok := portTypeByID[def.Name]; ok {
		portTypeOrder[i] = def
		return
	}
	portTypeByID[def.Name] = len(portTypeOrder)
	portTypeOrder = append(portTypeOrder, def)
}

// PortTypes returns every registered type in registration order.
func PortTypes() []PortTypeDef {
	portTypeMu.RLock()
	defer portTypeMu.RUnlock()
	out := make([]PortTypeDef, len(portTypeOrder))
	copy(out, portTypeOrder)
	return out
}

// PortTypeFor returns a registered type by name.
func PortTypeFor(name string) (PortTypeDef, bool) {
	portTypeMu.RLock()
	defer portTypeMu.RUnlock()
	i, ok := portTypeByID[name]
	if !ok {
		return PortTypeDef{}, false
	}
	return portTypeOrder[i], true
}

// The built-in types register at package init so validation works without any
// plugin loaded; plugins append theirs through the same registry. Order is
// load-bearing (see portTypeRegistry) — append only.
func init() {
	for _, def := range []PortTypeDef{
		{Name: PortAny, Label: "Any", Accepts: "any payload", Script: true},
		{Name: PortString, Label: "String", Accepts: "text", Value: true, Script: true},
		{Name: PortNumber, Label: "Number", Accepts: "a whole or decimal number", Value: true, Script: true},
		{Name: PortInteger, Label: "Integer", Accepts: "a whole number",
			WidensTo: []string{PortNumber, PortFloat}, Value: true, Script: true},
		{Name: PortFloat, Label: "Float", Accepts: "a decimal number",
			WidensTo: []string{PortNumber}, Value: true, Script: true},
		{Name: PortBool, Label: "Boolean", Accepts: "true or false", Value: true, Script: true},
		{Name: PortObject, Label: "Object", Accepts: "a decoded object, array, or scalar", Value: true, Script: true},
		{Name: PortBinary, Label: "Binary", Accepts: "raw bytes",
			WidensTo: []string{PortString}, Value: true, Script: true},
	} {
		RegisterPortType(def)
	}
}
