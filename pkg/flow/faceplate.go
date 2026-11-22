package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"
)

// NodeTypeFaceplate identifies a faceplate: one named tab of controls whose
// values feed the flow through value outputs.
const NodeTypeFaceplate = "faceplate"

// FieldFaceplateControls is a faceplate's control list: typed settings, each
// of which becomes one value output.
const FieldFaceplateControls = "faceplate_controls"

// Faceplate control value types.
const (
	ControlNumber  = "number"
	ControlBoolean = "boolean"
	ControlString  = "string"
	ControlEntity  = "entity"
	ControlArray   = "array"
	ControlObject  = "object"
)

const (
	// MaxFaceplateControls bounds one tab.
	MaxFaceplateControls = 64
	// MaxFaceplateSource bounds a template or change script.
	MaxFaceplateSource = 256 << 10
)

var controlIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// ControlOption is one choice a string or number control offers.
type ControlOption struct {
	Value any    `json:"value"`
	Label string `json:"label,omitempty"`
}

// FaceplateControl is one typed setting on a faceplate.
type FaceplateControl struct {
	ID          string          `json:"id"`
	Label       string          `json:"label"`
	Type        string          `json:"type"`
	Description string          `json:"description,omitempty"`
	Default     any             `json:"default,omitempty"`
	Min         *float64        `json:"min,omitempty"`
	Max         *float64        `json:"max,omitempty"`
	Step        *float64        `json:"step,omitempty"`
	Options     []ControlOption `json:"options,omitempty"`
	// Template is HTML and JavaScript drawing a custom editor for the value,
	// run isolated in the viewer's browser.
	Template string `json:"template,omitempty"`
}

// FaceplateConfig is a faceplate node's whole config.
type FaceplateConfig struct {
	Name     string             `json:"name"`
	Order    int                `json:"order"`
	Controls []FaceplateControl `json:"controls"`
	Values   map[string]any     `json:"values,omitempty"`
	// Script runs in the viewer's browser when any faceplate value in the
	// flow changes, and may change other controls.
	Script string `json:"script,omitempty"`
}

// EntityValue is the value of an entity control: one Home Assistant entity.
type EntityValue struct {
	ConnectionID string `json:"connection_id"`
	EntityID     string `json:"entity_id"`
}

// ParseFaceplate decodes a faceplate's config. An empty config is an empty
// faceplate.
func ParseFaceplate(raw json.RawMessage) (FaceplateConfig, error) {
	var cfg FaceplateConfig
	if len(raw) == 0 {
		return cfg, nil
	}
	err := json.Unmarshal(raw, &cfg)
	return cfg, err
}

// ControlPortType returns the port type a control's output carries.
func ControlPortType(controlType string) string {
	switch controlType {
	case ControlNumber:
		return PortNumber
	case ControlBoolean:
		return PortBool
	case ControlString:
		return PortString
	case ControlEntity, ControlArray, ControlObject:
		return PortObject
	}
	return PortAny
}

// FaceplatePorts derives one value output per control, in control order.
func FaceplatePorts(raw json.RawMessage) []PortSpec {
	cfg, err := ParseFaceplate(raw)
	if err != nil {
		return nil
	}
	ports := make([]PortSpec, 0, len(cfg.Controls))
	seen := map[string]bool{}
	for _, c := range cfg.Controls {
		if c.ID == "" || seen[c.ID] {
			continue
		}
		seen[c.ID] = true
		label := c.Label
		if label == "" {
			label = c.ID
		}
		ports = append(ports, PortSpec{ID: c.ID, Label: label, Type: ControlPortType(c.Type)})
	}
	return ports
}

// ControlValue returns a control's current value: the stored value, or its
// default when none is stored. A stored null is no value, as it is to
// CheckControlValue.
func (cfg FaceplateConfig) ControlValue(c FaceplateControl) any {
	if v, ok := cfg.Values[c.ID]; ok && v != nil {
		return v
	}
	return c.Default
}

// CheckControlValue reports why a value does not fit a control, or "" when it
// does. A nil value is always allowed: it falls back to the default.
func CheckControlValue(c FaceplateControl, v any) string {
	if v == nil {
		return ""
	}
	switch c.Type {
	case ControlNumber:
		n, ok := v.(float64)
		if !ok {
			return "must be a number"
		}
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return "must be a finite number"
		}
		if c.Min != nil && n < *c.Min {
			return fmt.Sprintf("must be at least %g", *c.Min)
		}
		if c.Max != nil && n > *c.Max {
			return fmt.Sprintf("must be at most %g", *c.Max)
		}
	case ControlBoolean:
		if _, ok := v.(bool); !ok {
			return "must be true or false"
		}
	case ControlString:
		if _, ok := v.(string); !ok {
			return "must be text"
		}
	case ControlEntity:
		m, ok := v.(map[string]any)
		if !ok {
			return "must be an entity with connection_id and entity_id"
		}
		if conn, _ := m["connection_id"].(string); conn == "" {
			return "must name a connection"
		}
		if entity, _ := m["entity_id"].(string); entity == "" {
			return "must name an entity"
		}
	case ControlArray:
		if _, ok := v.([]any); !ok {
			return "must be a list"
		}
	case ControlObject:
		if _, ok := v.(map[string]any); !ok {
			return "must be an object"
		}
	default:
		return fmt.Sprintf("has unknown type %q", c.Type)
	}
	if len(c.Options) > 0 {
		for _, o := range c.Options {
			if fmt.Sprint(o.Value) == fmt.Sprint(v) {
				return ""
			}
		}
		return "must be one of its options"
	}
	return ""
}

// ValidateFaceplate reports problems with a faceplate's config. Lenient
// validation, for drafts, allows unfinished controls; every stored value must
// still fit its control, so a bad value never reaches a version.
func ValidateFaceplate(cfg FaceplateConfig, lenient bool) []string {
	var problems []string
	if !lenient && strings.TrimSpace(cfg.Name) == "" {
		problems = append(problems, "the faceplate needs a tab name")
	}
	if len(cfg.Controls) > MaxFaceplateControls {
		problems = append(problems, fmt.Sprintf("a faceplate holds at most %d controls", MaxFaceplateControls))
	}
	if len(cfg.Script) > MaxFaceplateSource {
		problems = append(problems, "the change script is larger than 256 KiB")
	}
	seen := map[string]bool{}
	byID := map[string]FaceplateControl{}
	for _, c := range cfg.Controls {
		if !controlIDPattern.MatchString(c.ID) {
			if !lenient || c.ID != "" {
				problems = append(problems, fmt.Sprintf("control id %q must start with a letter and use lowercase letters, digits, and underscores", c.ID))
			}
		} else if seen[c.ID] {
			problems = append(problems, fmt.Sprintf("control id %q is used twice", c.ID))
		}
		seen[c.ID] = true
		byID[c.ID] = c
		if !lenient && strings.TrimSpace(c.Label) == "" {
			problems = append(problems, fmt.Sprintf("control %q needs a label", c.ID))
		}
		if ControlPortType(c.Type) == PortAny && (!lenient || c.Type != "") {
			problems = append(problems, fmt.Sprintf("control %q has unknown type %q", c.ID, c.Type))
		}
		if c.Min != nil && c.Max != nil && *c.Min > *c.Max {
			problems = append(problems, fmt.Sprintf("control %q has a minimum above its maximum", c.ID))
		}
		if len(c.Template) > MaxFaceplateSource {
			problems = append(problems, fmt.Sprintf("control %q has a template larger than 256 KiB", c.ID))
		}
		if c.Type != "" && ControlPortType(c.Type) != PortAny {
			if why := CheckControlValue(c, c.Default); why != "" {
				problems = append(problems, fmt.Sprintf("control %q default %s", c.ID, why))
			}
		}
	}
	for id, v := range cfg.Values {
		c, ok := byID[id]
		if !ok {
			if !lenient {
				problems = append(problems, fmt.Sprintf("value %q has no control", id))
			}
			continue
		}
		if c.Type == "" {
			continue
		}
		if why := CheckControlValue(c, v); why != "" {
			problems = append(problems, fmt.Sprintf("control %q value %s", c.ID, why))
		}
	}
	return problems
}

func validateFaceplate(p *problems, key string, n *Node, lenient bool) {
	cfg, err := ParseFaceplate(n.Config)
	if err != nil {
		p.add(key, "faceplate config is invalid: %s", err)
		return
	}
	for _, problem := range ValidateFaceplate(cfg, lenient) {
		p.add(key, "%s", problem)
	}
}

// FaceplateControlsSchema describes the control list to catalog and MCP
// consumers.
func FaceplateControlsSchema() map[string]any {
	return map[string]any{
		"type": "array", "maxItems": MaxFaceplateControls,
		"items": map[string]any{
			"type": "object", "required": []string{"id", "label", "type"},
			"properties": map[string]any{
				"id":          map[string]any{"type": "string", "pattern": controlIDPattern.String(), "description": "Stable output port ID."},
				"label":       map[string]any{"type": "string", "description": "Text shown beside the control."},
				"type":        map[string]any{"type": "string", "enum": []string{ControlNumber, ControlBoolean, ControlString, ControlEntity, ControlArray, ControlObject}},
				"description": map[string]any{"type": "string"},
				"default":     map[string]any{"description": "Value used until one is applied."},
				"min":         map[string]any{"type": "number"},
				"max":         map[string]any{"type": "number"},
				"step":        map[string]any{"type": "number"},
				"options": map[string]any{"type": "array", "items": map[string]any{
					"type": "object", "required": []string{"value"},
					"properties": map[string]any{"value": map[string]any{}, "label": map[string]any{"type": "string"}},
				}},
				"template": map[string]any{"type": "string", "description": "HTML and JavaScript for a custom editor, run isolated in the browser."},
			},
		},
	}
}
