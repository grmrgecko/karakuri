package flow

import "sync"

// Config-field controls. A field's Type is its semantic datatype — what
// validation checks and what a value edge can bind — and its Control names
// the editor it renders with. A field with no Control renders the default
// editor for its type. Controls are registered so a plugin can name its own;
// a frontend that does not implement a control falls back to the editor for
// the control's Base type, so a graph never becomes uneditable over a missing
// widget.

// FieldControlDef describes one config-field editor.
type FieldControlDef struct {
	// Name is the identifier FieldSpec.Control references, e.g. "password".
	Name string `json:"name" description:"Control identifier referenced by config fields."`
	// Base is the built-in field type whose editor stands in when the control
	// itself is not implemented by the frontend rendering the form.
	Base string `json:"base" description:"Field type whose editor is the fallback."`
	// Description says what the control renders, for the catalog.
	Description string `json:"description,omitempty" description:"What the control renders."`
}

var (
	controlMu    sync.RWMutex
	controlOrder []FieldControlDef
	controlByID  = map[string]int{}
)

// RegisterFieldControl registers a control, replacing an existing definition
// with the same name.
func RegisterFieldControl(def FieldControlDef) {
	controlMu.Lock()
	defer controlMu.Unlock()
	if i, ok := controlByID[def.Name]; ok {
		controlOrder[i] = def
		return
	}
	controlByID[def.Name] = len(controlOrder)
	controlOrder = append(controlOrder, def)
}

// FieldControls returns every registered control in registration order.
func FieldControls() []FieldControlDef {
	controlMu.RLock()
	defer controlMu.RUnlock()
	out := make([]FieldControlDef, len(controlOrder))
	copy(out, controlOrder)
	return out
}

// FieldControlFor returns a registered control by name.
func FieldControlFor(name string) (FieldControlDef, bool) {
	controlMu.RLock()
	defer controlMu.RUnlock()
	i, ok := controlByID[name]
	if !ok {
		return FieldControlDef{}, false
	}
	return controlOrder[i], true
}
