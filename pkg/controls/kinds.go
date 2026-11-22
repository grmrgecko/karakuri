// Package controls is the catalog and store of controls: named, typed values
// the system owns, which a dashboard operates and a flow reads or triggers on.
//
// A control is the counterpart to a device entity. A device's state belongs to
// the device and karakuri only observes it; a control's state belongs here, so
// a dashboard row can set it and any number of flows can watch it without
// either side knowing about the other. That indirection is what a fire-and-
// forget dashboard button cannot express: a button has no state to show, so a
// dashboard built out of buttons can never report what the house is doing.
//
// The kind catalog is also the vocabulary every other source declares itself
// in. An MQTT device names a kind, a Home Assistant entity is derived into
// one, and a dashboard widget draws whatever kind it is handed. One catalog
// means a light bound over MQTT and a light bound through Home Assistant get
// the same face, the same attributes, and the same options, and a widget never
// has to ask the author what it is looking at.
package controls

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Control kind names. Each names a value shape, not a use: what a control is
// for is the author's business, and the kind only decides how the value is
// rendered, coerced, and checked.
const (
	KindBoolean  = "boolean"
	KindNumber   = "number"
	KindSelect   = "select"
	KindText     = "text"
	KindDateTime = "datetime"
	// KindButtons is a set of actions rather than a state. Its value is the
	// last thing pressed, and every press is a change even when the value
	// repeats, which is what makes an otherwise stateless press observable.
	KindButtons = "buttons"
	KindLock    = "lock"
	KindLight   = "light"
	KindCover   = "cover"
	KindClimate = "climate"
	KindFan     = "fan"
	// KindLocation is where a person or a tracked thing is: a zone name or
	// home/away, with optional coordinates. Its since attribute is set by
	// whoever writes the value, on a change of state only.
	KindLocation = "location"
	KindMedia    = "media"
)

// Attribute is one addressable part of a kind's value. A scalar kind has
// exactly one, and its ID is the key a device's state script returns and the
// field a payload script is told is being commanded. A composite kind has
// several, which is what lets a light's brightness be set without disturbing
// its colour and lets a flow bind one of them to a config field.
type Attribute struct {
	ID    string `json:"id" description:"Key in the value object, and the field name a command names."`
	Label string `json:"label" description:"Display name."`
	Type  string `json:"type" description:"Payload type this attribute carries."`
	// Primary marks the attribute a bare set writes when the caller names
	// none. Every kind has exactly one.
	Primary bool `json:"primary,omitempty" description:"Whether a set naming no attribute writes this one."`
	// ReadOnly marks an attribute the device reports but nothing can command,
	// such as a thermostat's current temperature.
	ReadOnly bool `json:"read_only,omitempty" description:"Whether the attribute is reported but cannot be set."`
	// RequiresOption names a boolean kind option that must be on for this
	// attribute to exist, so a light without colour support has no colour
	// attribute rather than one nothing will accept.
	RequiresOption string `json:"requires_option,omitempty" description:"Kind option that must be on for this attribute to exist."`
	// WriteOnly marks an attribute that is an action rather than a state,
	// such as a player's next track: a write is announced to watchers and
	// never stored, so no later read finds a player still "skipping".
	WriteOnly bool `json:"write_only,omitempty" description:"Whether a write is announced as an action and never stored."`
}

// Kind describes one control kind: how it presents, what settings it takes,
// what its value holds, and which parts of it are addressable. The settings
// schema is FieldSpec so both the controls panel and the MQTT device panel
// render it with the same machinery as a node's config, and a new kind needs
// no frontend change.
type Kind struct {
	Name        string           `json:"name" description:"Kind identifier."`
	Label       string           `json:"label" description:"Display name."`
	Description string           `json:"description" description:"What the kind is for."`
	Icon        string           `json:"icon" description:"Line-awesome icon class."`
	ValueType   string           `json:"value_type" description:"Payload type the whole value carries."`
	Attributes  []Attribute      `json:"attributes" description:"Addressable parts of the value."`
	Options     []flow.FieldSpec `json:"options" description:"Kind-specific settings schema."`
	// PressOnly marks a kind whose value is an action rather than a state.
	// Nothing subscribes to it for a reading, and every write announces a
	// change even when the value did not move.
	PressOnly bool `json:"press_only,omitempty" description:"Whether a write is a press rather than a state change."`
}

// Composite reports whether the kind's value is an object of attributes rather
// than a single value.
func (k Kind) Composite() bool {
	return k.ValueType == flow.PortObject
}

// Attribute returns one of the kind's attributes by ID.
func (k Kind) Attribute(id string) (Attribute, bool) {
	for _, a := range k.Attributes {
		if a.ID == id {
			return a, true
		}
	}
	return Attribute{}, false
}

// PrimaryAttribute returns the attribute a set writes when the caller names
// none.
func (k Kind) PrimaryAttribute() Attribute {
	for _, a := range k.Attributes {
		if a.Primary {
			return a
		}
	}
	if len(k.Attributes) > 0 {
		return k.Attributes[0]
	}
	return Attribute{}
}

// Kinds returns the catalog in the order the settings panels offer it. A kind
// with no settings still carries an empty list rather than a null one, so a
// client can render the schema without checking which kind it came from.
func Kinds() []Kind {
	out := kinds()
	for i := range out {
		if out[i].Options == nil {
			out[i].Options = []flow.FieldSpec{}
		}
		if out[i].Attributes == nil {
			out[i].Attributes = []Attribute{}
		}
	}
	return out
}

// kinds is the catalog itself.
func kinds() []Kind {
	return []Kind{
		{
			Name:        KindBoolean,
			Label:       "Toggle",
			Description: "An on/off value. Renders as a switch and triggers flows on either edge.",
			Icon:        "las la-toggle-on",
			ValueType:   flow.PortBool,
			Attributes: []Attribute{
				{ID: "on", Label: "On", Type: flow.PortBool, Primary: true},
			},
		},
		{
			Name:        KindNumber,
			Label:       "Number",
			Description: "A numeric value with bounds, shown as a slider or a box.",
			Icon:        "las la-sliders-h",
			ValueType:   flow.PortNumber,
			Attributes: []Attribute{
				{ID: "value", Label: "Value", Type: flow.PortNumber, Primary: true},
			},
			Options: append([]flow.FieldSpec{
				{ID: "min", Label: "Minimum", Type: flow.FieldNumber, Default: 0,
					Help: "Lowest value the control accepts. A set below it is refused rather than clamped."},
				{ID: "max", Label: "Maximum", Type: flow.FieldNumber, Default: 100,
					Help: "Highest value the control accepts."},
				{ID: "step", Label: "Step", Type: flow.FieldNumber, Default: 1,
					Help: "Increment the slider and the stepper buttons move by."},
				{ID: "display", Label: "Display", Type: flow.FieldSelect, Default: "slider",
					Options: []flow.Option{
						{Value: "slider", Label: "Slider"},
						{Value: "box", Label: "Number box"},
					}},
			}, formatOptions()...),
		},
		{
			Name:        KindSelect,
			Label:       "Dropdown",
			Description: "One of a fixed list of options, and the choice sticks: a house mode, a fan speed, an input.",
			Icon:        "las la-list",
			ValueType:   flow.PortString,
			Attributes: []Attribute{
				{ID: "value", Label: "Value", Type: flow.PortString, Primary: true},
			},
			Options: []flow.FieldSpec{
				{ID: "options", Label: "Options", Type: flow.FieldJSON, Control: "choices", Required: true,
					Help: "Choices in the order they are offered. The first is the initial value. " +
						"A label is what the dashboard shows; the value is what flows read."},
				{ID: "display", Label: "Display", Type: flow.FieldSelect, Default: "dropdown",
					Options: []flow.Option{
						{Value: "dropdown", Label: "Dropdown"},
						{Value: "segmented", Label: "Segmented row"},
					},
					Help: "A segmented row puts every option on screen at once, which suits two or " +
						"three short ones on a wall panel."},
			},
		},
		{
			Name:  KindButtons,
			Label: "Buttons",
			Description: "A set of actions with no state behind them: Up, Stop, Down, or the codes " +
				"an infrared blaster sends. One button draws as one button.",
			Icon:      "las la-hand-pointer",
			ValueType: flow.PortString,
			PressOnly: true,
			Attributes: []Attribute{
				{ID: "value", Label: "Pressed", Type: flow.PortString, Primary: true},
			},
			Options: []flow.FieldSpec{
				{ID: "buttons", Label: "Buttons", Type: flow.FieldJSON, Control: "choices", Required: true,
					Help: "Actions in the order they are drawn. The value is what a flow reads and " +
						"what a device's payload script is handed."},
			},
		},
		{
			Name:        KindText,
			Label:       "Text",
			Description: "A text value. It reads as a value when the source cannot be written and as an input when it can.",
			Icon:        "las la-i-cursor",
			ValueType:   flow.PortString,
			Attributes: []Attribute{
				{ID: "value", Label: "Value", Type: flow.PortString, Primary: true},
			},
			Options: append([]flow.FieldSpec{
				{ID: "max_length", Label: "Maximum length", Type: flow.FieldNumber, Default: 255,
					Help: "Longest value accepted. Zero means no limit."},
				{ID: "multiline", Label: "Multi-line", Type: flow.FieldBoolean, Default: false,
					Help: "Render as a text area instead of a single-line input."},
			}, formatOptions()...),
		},
		{
			Name:        KindDateTime,
			Label:       "Date and time",
			Description: "A date, a time of day, or both. The value a schedule reads its next run from.",
			Icon:        "las la-clock",
			ValueType:   flow.PortString,
			Attributes: []Attribute{
				{ID: "value", Label: "Value", Type: flow.PortString, Primary: true},
			},
			Options: []flow.FieldSpec{
				{ID: "mode", Label: "Holds", Type: flow.FieldSelect, Default: "datetime",
					Options: []flow.Option{
						{Value: "datetime", Label: "Date and time"},
						{Value: "date", Label: "Date only"},
						{Value: "time", Label: "Time only"},
					},
					Help: "Which parts the value carries. It is stored as text in that shape."},
			},
		},
		{
			Name:        KindLock,
			Label:       "Lock",
			Description: "A lock. It reports locked, unlocked, or jammed, and the action offered is the one that changes it.",
			Icon:        "las la-lock",
			ValueType:   flow.PortString,
			Attributes: []Attribute{
				{ID: "state", Label: "State", Type: flow.PortString, Primary: true},
			},
			Options: []flow.FieldSpec{
				{ID: "confirm", Label: "Confirm before acting", Type: flow.FieldBoolean, Default: false,
					Help: "Require a second tap. A wall tablet by the door is worth protecting from a sleeve."},
			},
		},
		{
			Name:  KindLight,
			Label: "Light",
			Description: "A light: on and off, and whichever of brightness, colour, colour temperature, " +
				"and effects it supports.",
			Icon:      "las la-lightbulb",
			ValueType: flow.PortObject,
			Attributes: []Attribute{
				{ID: "on", Label: "On", Type: flow.PortBool, Primary: true},
				{ID: "brightness", Label: "Brightness", Type: flow.PortNumber, RequiresOption: "brightness"},
				{ID: "color", Label: "Colour", Type: flow.PortString, RequiresOption: "color"},
				{ID: "color_temp", Label: "Colour temperature", Type: flow.PortNumber, RequiresOption: "color_temp"},
				{ID: "effect", Label: "Effect", Type: flow.PortString, RequiresOption: "effects"},
			},
			Options: []flow.FieldSpec{
				{ID: "brightness", Label: "Has brightness", Type: flow.FieldBoolean, Default: true,
					Help: "Brightness is a percentage from 0 to 100. A device that counts in 0 to 254 " +
						"converts in its own scripts, so every light on a dashboard reads the same way."},
				{ID: "color", Label: "Has colour", Type: flow.FieldBoolean, Default: false,
					Help: "Colour is a hex string such as #ff8800."},
				{ID: "color_temp", Label: "Has colour temperature", Type: flow.FieldBoolean, Default: false,
					Help: "Colour temperature is in kelvin."},
				{ID: "min_kelvin", Label: "Warmest temperature", Type: flow.FieldNumber, Default: 2000,
					ShowIf: &flow.ShowIf{Field: "color_temp", Equals: true}},
				{ID: "max_kelvin", Label: "Coolest temperature", Type: flow.FieldNumber, Default: 6500,
					ShowIf: &flow.ShowIf{Field: "color_temp", Equals: true}},
				{ID: "effects", Label: "Has effects", Type: flow.FieldBoolean, Default: false},
				{ID: "effect_list", Label: "Effects", Type: flow.FieldJSON, Control: "choices",
					ShowIf: &flow.ShowIf{Field: "effects", Equals: true},
					Help:   "Effects the light accepts, in the order they are offered."},
			},
		},
		{
			Name:        KindCover,
			Label:       "Cover",
			Description: "A blind, shutter, curtain, or garage door: open, stop, and close, with a position when it has one.",
			Icon:        "las la-window-maximize",
			ValueType:   flow.PortObject,
			Attributes: []Attribute{
				{ID: "state", Label: "State", Type: flow.PortString, Primary: true},
				{ID: "position", Label: "Position", Type: flow.PortNumber, RequiresOption: "position"},
				{ID: "tilt", Label: "Tilt", Type: flow.PortNumber, RequiresOption: "tilt"},
			},
			Options: []flow.FieldSpec{
				{ID: "position", Label: "Has position", Type: flow.FieldBoolean, Default: false,
					Help: "Position is a percentage, 100 fully open, matching how Home Assistant reports it."},
				{ID: "tilt", Label: "Has tilt", Type: flow.FieldBoolean, Default: false},
				{ID: "stop", Label: "Has stop", Type: flow.FieldBoolean, Default: true,
					Help: "Draw a stop button between open and close."},
			},
		},
		{
			Name:        KindClimate,
			Label:       "Thermostat",
			Description: "A thermostat: the mode it is in, the temperature it is aiming for, and what it currently reads.",
			Icon:        "las la-thermometer-half",
			ValueType:   flow.PortObject,
			Attributes: []Attribute{
				{ID: "mode", Label: "Mode", Type: flow.PortString, Primary: true},
				{ID: "target", Label: "Target temperature", Type: flow.PortNumber},
				{ID: "target_low", Label: "Target low", Type: flow.PortNumber, RequiresOption: "range"},
				{ID: "target_high", Label: "Target high", Type: flow.PortNumber, RequiresOption: "range"},
				{ID: "fan_mode", Label: "Fan mode", Type: flow.PortString, RequiresOption: "fan"},
				{ID: "current", Label: "Current temperature", Type: flow.PortNumber, ReadOnly: true},
				{ID: "action", Label: "Action", Type: flow.PortString, ReadOnly: true},
			},
			Options: []flow.FieldSpec{
				{ID: "modes", Label: "Modes", Type: flow.FieldJSON, Control: "choices", Required: true,
					Default: []any{
						map[string]any{"value": "off", "label": "Off"},
						map[string]any{"value": "heat", "label": "Heat"},
						map[string]any{"value": "cool", "label": "Cool"},
					},
					Help: "Modes the thermostat accepts, in the order they are offered."},
				{ID: "min_temp", Label: "Minimum temperature", Type: flow.FieldNumber, Default: 7},
				{ID: "max_temp", Label: "Maximum temperature", Type: flow.FieldNumber, Default: 35},
				{ID: "step", Label: "Step", Type: flow.FieldNumber, Default: 0.5},
				{ID: "unit", Label: "Unit", Type: flow.FieldString, Default: "°C",
					Help: "Suffix shown after every temperature. Display only; nothing is converted."},
				{ID: "range", Label: "Aims at a range", Type: flow.FieldBoolean, Default: false,
					Help: "Two setpoints instead of one, for a thermostat that heats below a floor and cools above a ceiling."},
				{ID: "fan", Label: "Has fan modes", Type: flow.FieldBoolean, Default: false},
				{ID: "fan_modes", Label: "Fan modes", Type: flow.FieldJSON, Control: "choices",
					ShowIf: &flow.ShowIf{Field: "fan", Equals: true}},
			},
		},
		{
			Name:        KindFan,
			Label:       "Fan",
			Description: "A fan: on and off, plus whichever of speed, presets, oscillation, and direction it has.",
			Icon:        "las la-fan",
			ValueType:   flow.PortObject,
			Attributes: []Attribute{
				{ID: "on", Label: "On", Type: flow.PortBool, Primary: true},
				{ID: "percentage", Label: "Speed", Type: flow.PortNumber, RequiresOption: "speed"},
				{ID: "preset", Label: "Preset", Type: flow.PortString, RequiresOption: "presets"},
				{ID: "oscillating", Label: "Oscillating", Type: flow.PortBool, RequiresOption: "oscillate"},
				{ID: "direction", Label: "Direction", Type: flow.PortString, RequiresOption: "direction"},
			},
			Options: []flow.FieldSpec{
				{ID: "speed", Label: "Has speed", Type: flow.FieldBoolean, Default: true,
					Help: "Speed is a percentage from 0 to 100."},
				{ID: "speed_step", Label: "Speed step", Type: flow.FieldNumber, Default: 1,
					ShowIf: &flow.ShowIf{Field: "speed", Equals: true},
					Help:   "A three-speed fan steps by 33 rather than by 1."},
				{ID: "presets", Label: "Has presets", Type: flow.FieldBoolean, Default: false},
				{ID: "preset_list", Label: "Presets", Type: flow.FieldJSON, Control: "choices",
					ShowIf: &flow.ShowIf{Field: "presets", Equals: true}},
				{ID: "oscillate", Label: "Has oscillation", Type: flow.FieldBoolean, Default: false},
				{ID: "direction", Label: "Has direction", Type: flow.FieldBoolean, Default: false},
			},
		},
		{
			Name:  KindLocation,
			Label: "Location",
			Description: "Where a person or a tracked thing is: home, away, or a named zone, with " +
				"coordinates when the source reports them and how long it has been there.",
			Icon:      "las la-map-marker",
			ValueType: flow.PortObject,
			Attributes: []Attribute{
				{ID: "state", Label: "State", Type: flow.PortString, Primary: true},
				{ID: "latitude", Label: "Latitude", Type: flow.PortNumber},
				{ID: "longitude", Label: "Longitude", Type: flow.PortNumber},
				{ID: "accuracy", Label: "Accuracy", Type: flow.PortNumber},
				{ID: "source", Label: "Source", Type: flow.PortString},
				{ID: "since", Label: "Since", Type: flow.PortString, ReadOnly: true},
			},
			Options: []flow.FieldSpec{
				{ID: "zones", Label: "Zones", Type: flow.FieldJSON, Control: "choices",
					Default: []any{
						map[string]any{"value": "home", "label": "Home"},
						map[string]any{"value": "not_home", "label": "Away"},
					},
					Help: "States in the order the picker offers them. The value is what a source " +
						"reports and a flow compares; the label is what the dashboard shows."},
				{ID: "restrict_zones", Label: "Only listed zones", Type: flow.FieldBoolean, Default: false,
					Help: "Refuse any state the zones do not list. Off, any zone name a source reports is kept."},
				{ID: "manual", Label: "Pick by hand", Type: flow.FieldBoolean, Default: false,
					Help: "Draw the zones as a picker on dashboards. A state picked by hand stands " +
						"until the next source writes one."},
				{ID: "show_coordinates", Label: "Show coordinates", Type: flow.FieldBoolean, Default: false,
					Help: "Show latitude, longitude, and accuracy under the state when they are known."},
			},
		},
		{
			Name:  KindMedia,
			Label: "Media player",
			Description: "A speaker or player: whether it is playing, what is playing, its progress, " +
				"volume, and source, with the transport controls the player has.",
			Icon:      "las la-music",
			ValueType: flow.PortObject,
			Attributes: []Attribute{
				{ID: "state", Label: "State", Type: flow.PortString, Primary: true},
				{ID: "volume", Label: "Volume", Type: flow.PortNumber, RequiresOption: "volume"},
				{ID: "muted", Label: "Muted", Type: flow.PortBool, RequiresOption: "mute"},
				{ID: "source", Label: "Source", Type: flow.PortString, RequiresOption: "source"},
				{ID: "position", Label: "Position", Type: flow.PortNumber, RequiresOption: "seek"},
				{ID: "command", Label: "Skip", Type: flow.PortString, RequiresOption: "skip", WriteOnly: true},
				{ID: "title", Label: "Title", Type: flow.PortString, ReadOnly: true},
				{ID: "artist", Label: "Artist", Type: flow.PortString, ReadOnly: true},
				{ID: "album", Label: "Album", Type: flow.PortString, ReadOnly: true},
				{ID: "art", Label: "Art", Type: flow.PortString, ReadOnly: true},
				{ID: "duration", Label: "Duration", Type: flow.PortNumber, ReadOnly: true},
				{ID: "position_at", Label: "Position reported at", Type: flow.PortString, ReadOnly: true},
				{ID: "sources", Label: "Sources", Type: flow.PortAny, ReadOnly: true},
			},
			Options: []flow.FieldSpec{
				{ID: "volume", Label: "Has volume", Type: flow.FieldBoolean, Default: true,
					Help: "Volume is a percentage from 0 to 100."},
				{ID: "mute", Label: "Has mute", Type: flow.FieldBoolean, Default: true},
				{ID: "skip", Label: "Has next and previous", Type: flow.FieldBoolean, Default: true,
					Help: "Next and previous are written to command and announced, never stored."},
				{ID: "stop", Label: "Has stop", Type: flow.FieldBoolean, Default: true,
					Help: "Stopping sets the state to idle."},
				{ID: "seek", Label: "Has seek", Type: flow.FieldBoolean, Default: false,
					Help: "Position is in seconds from the start of what is playing."},
				{ID: "power", Label: "Has power", Type: flow.FieldBoolean, Default: false,
					Help: "Offer turning the player on and off."},
				{ID: "source", Label: "Has sources", Type: flow.FieldBoolean, Default: false},
				{ID: "source_list", Label: "Sources", Type: flow.FieldJSON, Control: "choices",
					ShowIf: &flow.ShowIf{Field: "source", Equals: true},
					Help:   "Sources the player accepts. Empty takes the list the player reports in sources."},
			},
		},
	}
}

// formatOptions are the display settings a kind that shows a plain reading
// carries. They exist for the sources with nowhere else to format: a Home
// Assistant sensor and a karakuri control have no script between the value and
// the screen, while an MQTT device formats in its state script and can leave
// these alone.
func formatOptions() []flow.FieldSpec {
	return []flow.FieldSpec{
		{ID: "unit", Label: "Unit", Type: flow.FieldString, Placeholder: "°C",
			Help: "Suffix shown after the value. Display only; nothing is converted."},
		{ID: "prefix", Label: "Prefix", Type: flow.FieldString, Placeholder: "$",
			Help: "Text shown before the value."},
		{ID: "precision", Label: "Decimal places", Type: flow.FieldNumber,
			Help: "Round the displayed value to this many decimals. Empty shows it as it arrives."},
		{ID: "labels", Label: "Value labels", Type: flow.FieldJSON, Control: "map",
			Help: "Show these values as something else: heat as Heating, 0 as Idle. Only the display changes."},
	}
}

// FindKind returns the definition of one kind.
func FindKind(name string) (Kind, bool) {
	for _, k := range Kinds() {
		if k.Name == name {
			return k, true
		}
	}
	return Kind{}, false
}

// ValueType returns the payload type a kind's value carries, or the wildcard
// for a kind that is not registered.
func ValueType(kind string) string {
	if k, ok := FindKind(kind); ok {
		return k.ValueType
	}
	return flow.PortAny
}

// PressOnly reports whether a write to this kind is a press rather than a
// state change, so the store announces every one of them.
func PressOnly(kind string) bool {
	k, ok := FindKind(kind)
	return ok && k.PressOnly
}

// InitialValue is the value a newly created control starts at, chosen so a
// control is usable the moment it exists rather than sitting empty until it is
// first set.
func InitialValue(kind string, opts map[string]any) any {
	switch kind {
	case KindBoolean:
		return false
	case KindNumber:
		if min, ok := optFloat(opts, "min"); ok {
			return min
		}
		return float64(0)
	case KindSelect:
		if list := choiceValues(opts, "options"); len(list) > 0 {
			return list[0]
		}
		return ""
	case KindButtons:
		// Nothing has been pressed yet. A buttons control's value is a record
		// of the last press, not a selection, so it starts empty.
		return ""
	case KindLock:
		return "locked"
	case KindLight:
		return map[string]any{"on": false}
	case KindCover:
		return map[string]any{"state": "closed"}
	case KindClimate:
		value := map[string]any{"mode": "off"}
		if min, ok := optFloat(opts, "min_temp"); ok {
			value["target"] = min
		}
		return value
	case KindFan:
		return map[string]any{"on": false}
	case KindLocation:
		// Nothing has reported yet. Home or away would each be a claim, and a
		// flow acting on arrival or departure would act on it.
		return map[string]any{}
	case KindMedia:
		return map[string]any{"state": "idle"}
	default:
		return ""
	}
}

// NormalizeOptions fills a kind's declared defaults into missing settings and
// rejects settings the kind cannot work with. The settings panel seeds
// defaults and enforces required fields itself, but a definition written over
// the API arrives as whatever the caller sent — and a select with no options
// or a slider whose floor sits above its ceiling would refuse every write
// while looking perfectly healthy in the panel.
func NormalizeOptions(kind string, opts map[string]any) (map[string]any, error) {
	def, ok := FindKind(kind)
	if !ok {
		return nil, fmt.Errorf("unknown control kind %q", kind)
	}
	out := make(map[string]any, len(opts))
	for k, v := range opts {
		out[k] = v
	}
	for _, f := range def.Options {
		if _, present := out[f.ID]; !present && f.Default != nil {
			out[f.ID] = f.Default
		}
	}

	// Required settings must hold something; a required choices list must hold
	// at least one entry or the control can never be set.
	for _, f := range def.Options {
		if !f.Required {
			continue
		}
		if f.Control == "choices" {
			if len(choiceValues(out, f.ID)) == 0 {
				return nil, fmt.Errorf("a %s needs at least one entry in %s", strings.ToLower(def.Label), strings.ToLower(f.Label))
			}
			continue
		}
		if _, present := out[f.ID]; !present {
			return nil, fmt.Errorf("a %s requires %s", strings.ToLower(def.Label), strings.ToLower(f.Label))
		}
	}

	// A range that holds no values refuses every write later; refuse the
	// definition instead.
	for _, pair := range [][2]string{{"min", "max"}, {"min_temp", "max_temp"}, {"min_kelvin", "max_kelvin"}} {
		lo, okLo := optFloat(out, pair[0])
		hi, okHi := optFloat(out, pair[1])
		if okLo && okHi && lo >= hi {
			return nil, fmt.Errorf("%s must be below %s", pair[0], pair[1])
		}
	}
	if kind == KindLocation && optBool(out, "restrict_zones") && len(choiceValues(out, "zones")) == 0 {
		return nil, fmt.Errorf("a location restricted to its zones needs at least one zone")
	}
	for _, key := range []string{"step", "speed_step"} {
		if n, ok := optFloat(out, key); ok && n <= 0 {
			return nil, fmt.Errorf("%s must be above zero", key)
		}
	}
	return out, nil
}

// ValidateValue checks the invariants a merged composite value must hold that
// no single attribute can be blamed for: a range thermostat's floor sitting
// above its ceiling. It runs after the merge, because either setpoint alone
// can be the write that breaks the pair.
func ValidateValue(kind string, value any) error {
	if kind != KindClimate {
		return nil
	}
	fields, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	lo, okLo := fields["target_low"].(float64)
	hi, okHi := fields["target_high"].(float64)
	if okLo && okHi && lo > hi {
		return fmt.Errorf("target low %v is above target high %v", lo, hi)
	}
	return nil
}

// Coerce converts a set request's value into the control's stored form and
// rejects what the kind cannot hold. Coercion is deliberately narrow: a
// dashboard row sends the right shape already, and accepting a loose one here
// would let a flow write a value the row cannot render.
func Coerce(kind string, opts map[string]any, value any) (any, error) {
	def, ok := FindKind(kind)
	if !ok {
		return nil, fmt.Errorf("unknown control kind %q", kind)
	}
	if def.Composite() {
		return coerceObject(def, opts, value)
	}
	return CoerceAttribute(kind, opts, def.PrimaryAttribute().ID, value)
}

// CoerceAttribute converts one attribute's value. A scalar kind has a single
// attribute, so this is also where a plain set lands; a composite kind reaches
// it once per key.
func CoerceAttribute(kind string, opts map[string]any, attribute string, value any) (any, error) {
	return coerceAttribute(kind, opts, attribute, value, false)
}

// coerceAttribute converts one attribute's value. A report is a whole value a
// player or a mirroring flow states about itself, which may hold what nobody
// can command, such as a media player buffering; a write is a command.
func coerceAttribute(kind string, opts map[string]any, attribute string, value any, report bool) (any, error) {
	def, ok := FindKind(kind)
	if !ok {
		return nil, fmt.Errorf("unknown control kind %q", kind)
	}
	attr, ok := def.Attribute(attribute)
	if !ok {
		return nil, fmt.Errorf("%s has no %q to set", def.Label, attribute)
	}
	if attr.ReadOnly {
		return nil, fmt.Errorf("%s reports %s but cannot be told it", def.Label, attr.Label)
	}
	if attr.RequiresOption != "" && !optBool(opts, attr.RequiresOption) {
		return nil, fmt.Errorf("this %s has no %s", strings.ToLower(def.Label), strings.ToLower(attr.Label))
	}

	switch kind {
	case KindBoolean:
		return coerceBool(value)

	case KindNumber:
		return coerceRange(value, opts, "min", "max")

	case KindSelect:
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		return matchChoice(s, choiceValues(opts, "options"), "the control's options")

	case KindButtons:
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		list := choiceValues(opts, "buttons")
		if len(list) == 0 {
			// A control with no buttons declared is the single unlabelled
			// action it is, so any press lands on it.
			return s, nil
		}
		return matchChoice(s, list, "the control's buttons")

	case KindText:
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		if max, ok := optFloat(opts, "max_length"); ok && max > 0 && float64(len([]rune(s))) > max {
			return nil, fmt.Errorf("value is longer than the maximum of %v characters", max)
		}
		return s, nil

	case KindDateTime:
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		return coerceDateTime(optString(opts, "mode"), s)

	case KindLock:
		return coerceLock(value)

	case KindLight:
		return coerceLightAttribute(attribute, value, opts)

	case KindCover:
		return coerceCoverAttribute(attribute, value)

	case KindClimate:
		return coerceClimateAttribute(attribute, value, opts)

	case KindFan:
		return coerceFanAttribute(attribute, value, opts)

	case KindLocation:
		return coerceLocationAttribute(attribute, value, opts)

	case KindMedia:
		return coerceMediaAttribute(attribute, value, opts, report)
	}
	return nil, fmt.Errorf("unknown control kind %q", kind)
}

// coerceObject checks a whole composite value, one attribute at a time. Keys
// the kind does not know are refused rather than stored: a light that quietly
// accepted a "temperature" key would report it to every widget as if it meant
// something.
func coerceObject(def Kind, opts map[string]any, value any) (any, error) {
	fields, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("value must be an object of %s attributes", strings.ToLower(def.Label))
	}
	out := make(map[string]any, len(fields))
	for key, raw := range fields {
		attr, ok := def.Attribute(key)
		if !ok {
			return nil, fmt.Errorf("%s has no %q attribute", def.Label, key)
		}
		if attr.ReadOnly {
			// A reported attribute rides along untouched: it is how a device
			// tells the store what it currently reads.
			out[key] = raw
			continue
		}
		if attr.WriteOnly {
			return nil, fmt.Errorf("%s is an action; write it on its own rather than as part of a value", key)
		}
		coerced, err := coerceAttribute(def.Name, opts, key, raw, true)
		if err != nil {
			return nil, err
		}
		out[key] = coerced
	}
	return out, nil
}

// MergeAttribute folds one attribute into a composite control's current value,
// so setting a light's brightness leaves its colour where it was.
func MergeAttribute(current any, attribute string, value any) map[string]any {
	out := map[string]any{}
	if fields, ok := current.(map[string]any); ok {
		for k, v := range fields {
			out[k] = v
		}
	}
	out[attribute] = value
	return out
}

// coerceRange reads a number and holds it to the kind's bounds.
func coerceRange(value any, opts map[string]any, minKey, maxKey string) (any, error) {
	n, err := coerceFloat(value)
	if err != nil {
		return nil, err
	}
	if min, ok := optFloat(opts, minKey); ok && n < min {
		return nil, fmt.Errorf("value %v is below the minimum of %v", n, min)
	}
	if max, ok := optFloat(opts, maxKey); ok && n > max {
		return nil, fmt.Errorf("value %v is above the maximum of %v", n, max)
	}
	return n, nil
}

// coercePercent reads a 0 to 100 reading, which is what brightness, cover
// position, and fan speed all are here whatever the device counts in.
func coercePercent(value any) (any, error) {
	n, err := coerceFloat(value)
	if err != nil {
		return nil, err
	}
	if n < 0 || n > 100 {
		return nil, fmt.Errorf("value %v is not a percentage from 0 to 100", n)
	}
	return n, nil
}

// coerceLightAttribute checks one part of a light.
func coerceLightAttribute(attribute string, value any, opts map[string]any) (any, error) {
	switch attribute {
	case "on":
		return coerceBool(value)
	case "brightness":
		return coercePercent(value)
	case "color":
		return coerceHexColor(value)
	case "color_temp":
		return coerceRange(value, opts, "min_kelvin", "max_kelvin")
	case "effect":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		list := choiceValues(opts, "effect_list")
		if len(list) == 0 {
			return s, nil
		}
		return matchChoice(s, list, "the light's effects")
	}
	return nil, fmt.Errorf("a light has no %q", attribute)
}

// coverStates are the readings a cover reports, matching what Home Assistant
// calls them so an entity's state passes through unchanged.
var coverStates = []string{"open", "opening", "closed", "closing", "stopped"}

// coerceCoverAttribute checks one part of a cover.
func coerceCoverAttribute(attribute string, value any) (any, error) {
	switch attribute {
	case "state":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		return matchChoice(strings.ToLower(strings.TrimSpace(s)), coverStates, "a cover's states")
	case "position", "tilt":
		return coercePercent(value)
	}
	return nil, fmt.Errorf("a cover has no %q", attribute)
}

// coerceClimateAttribute checks one part of a thermostat.
func coerceClimateAttribute(attribute string, value any, opts map[string]any) (any, error) {
	switch attribute {
	case "mode":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		return matchChoice(s, choiceValues(opts, "modes"), "the thermostat's modes")
	case "target", "target_low", "target_high":
		return coerceRange(value, opts, "min_temp", "max_temp")
	case "fan_mode":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		list := choiceValues(opts, "fan_modes")
		if len(list) == 0 {
			return s, nil
		}
		return matchChoice(s, list, "the thermostat's fan modes")
	}
	return nil, fmt.Errorf("a thermostat has no %q", attribute)
}

// coerceFanAttribute checks one part of a fan.
func coerceFanAttribute(attribute string, value any, opts map[string]any) (any, error) {
	switch attribute {
	case "on", "oscillating":
		return coerceBool(value)
	case "percentage":
		return coercePercent(value)
	case "preset":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		list := choiceValues(opts, "preset_list")
		if len(list) == 0 {
			return s, nil
		}
		return matchChoice(s, list, "the fan's presets")
	case "direction":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		return matchChoice(strings.ToLower(strings.TrimSpace(s)),
			[]string{"forward", "reverse"}, "a fan's directions")
	}
	return nil, fmt.Errorf("a fan has no %q", attribute)
}

// coerceLocationAttribute checks one part of a location.
func coerceLocationAttribute(attribute string, value any, opts map[string]any) (any, error) {
	switch attribute {
	case "state":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil, fmt.Errorf("a location state cannot be empty")
		}
		if !optBool(opts, "restrict_zones") {
			return s, nil
		}
		zones := choiceValues(opts, "zones")
		return matchChoice(s, zones, "the control's zones: "+strings.Join(zones, ", "))
	case "latitude":
		return coerceBounded(value, -90, 90)
	case "longitude":
		return coerceBounded(value, -180, 180)
	case "accuracy":
		n, err := coerceFloat(value)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("value %v is below the minimum of 0", n)
		}
		return n, nil
	case "source":
		return coerceString(value)
	}
	return nil, fmt.Errorf("a location has no %q", attribute)
}

// mediaStates are the states a media player reports, matching what Home
// Assistant calls them so an entity's state passes through unchanged.
var mediaStates = []string{"off", "on", "idle", "playing", "paused", "buffering"}

// mediaCommands are the actions a media player's command attribute takes.
var mediaCommands = []string{"next", "previous"}

// coerceMediaAttribute checks one part of a media player. A write is held to
// the features the options give the player, so a press the player cannot do
// is refused before anything is sent; a report states what the player is
// doing and only has to be a state.
func coerceMediaAttribute(attribute string, value any, opts map[string]any, report bool) (any, error) {
	switch attribute {
	case "state":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		s = strings.ToLower(strings.TrimSpace(s))
		if _, err := matchChoice(s, mediaStates, "a media player's states: "+strings.Join(mediaStates, ", ")); err != nil {
			return nil, err
		}
		if report {
			return s, nil
		}
		switch s {
		case "buffering":
			return nil, fmt.Errorf("buffering is reported by a player and cannot be set")
		case "idle":
			if !optBool(opts, "stop") {
				return nil, fmt.Errorf("this player has no stop")
			}
		case "on", "off":
			if !optBool(opts, "power") {
				return nil, fmt.Errorf("this player has no power control")
			}
		}
		return s, nil
	case "volume":
		return coerceBounded(value, 0, 100)
	case "muted":
		return coerceBool(value)
	case "source":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		if list := choiceValues(opts, "source_list"); len(list) > 0 {
			return matchChoice(s, list, "the player's sources: "+strings.Join(list, ", "))
		}
		return s, nil
	case "position":
		n, err := coerceFloat(value)
		if err != nil {
			return nil, err
		}
		if n < 0 {
			return nil, fmt.Errorf("position %v is below the start", n)
		}
		return n, nil
	case "command":
		s, err := coerceString(value)
		if err != nil {
			return nil, err
		}
		return matchChoice(strings.ToLower(strings.TrimSpace(s)), mediaCommands, "a media player's commands: next, previous")
	}
	return nil, fmt.Errorf("a media player has no %q", attribute)
}

// CheckAgainstValue holds a write to what the value it lands on reports: a
// media player's source must be one the player lists, and a seek must fall
// within what is playing. Coercion cannot see the value, so the store and a
// device command call this with the value they hold.
func CheckAgainstValue(kind string, current any, attribute string, value any) error {
	if kind != KindMedia {
		return nil
	}
	fields, _ := current.(map[string]any)
	switch attribute {
	case "source":
		list := stringList(fields["sources"])
		if len(list) == 0 {
			return nil
		}
		s, _ := value.(string)
		_, err := matchChoice(s, list, "the player's sources: "+strings.Join(list, ", "))
		return err
	case "position":
		n, _ := value.(float64)
		if d, err := coerceFloat(fields["duration"]); err == nil && d > 0 && n > d {
			return fmt.Errorf("position %v is past the end at %v", n, d)
		}
	}
	return nil
}

// stringList reads a reported list of names, such as a player's sources.
func stringList(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if list, ok := v.([]string); ok {
			return list
		}
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if s, ok := item.(string); ok && s != "" {
			out = append(out, s)
		}
	}
	return out
}

// coerceBounded reads a number held to fixed bounds.
func coerceBounded(value any, min, max float64) (any, error) {
	n, err := coerceFloat(value)
	if err != nil {
		return nil, err
	}
	if n < min {
		return nil, fmt.Errorf("value %v is below the minimum of %v", n, min)
	}
	if n > max {
		return nil, fmt.Errorf("value %v is above the maximum of %v", n, max)
	}
	return n, nil
}

// matchChoice holds a value to a declared list, naming the list in the error so
// an operator knows where to go and fix it.
func matchChoice(value string, list []string, what string) (any, error) {
	for _, item := range list {
		if item == value {
			return value, nil
		}
	}
	return nil, fmt.Errorf("%q is not one of %s", value, what)
}

// coerceHexColor normalizes a colour to #rrggbb. Hex is the storage form
// because it survives JSON, a script reads it without a library, and every
// widget already speaks it.
func coerceHexColor(value any) (any, error) {
	s, err := coerceString(value)
	if err != nil {
		return nil, err
	}
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 {
		return nil, fmt.Errorf("colour must be a hex string such as #ff8800")
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return nil, fmt.Errorf("colour must be a hex string such as #ff8800")
		}
	}
	return "#" + s, nil
}

// lockStates are the readings a lock reports. Locking and unlocking are the
// moments in between, which a device that reports them should be able to say.
var lockStates = []string{"locked", "unlocked", "locking", "unlocking", "jammed"}

// coerceLock reads a lock state, accepting the booleans a flow and an MQTT
// payload reasonably use for it.
func coerceLock(value any) (any, error) {
	if b, ok := value.(bool); ok {
		if b {
			return "locked", nil
		}
		return "unlocked", nil
	}
	s, err := coerceString(value)
	if err != nil {
		return nil, err
	}
	return matchChoice(strings.ToLower(strings.TrimSpace(s)), lockStates, "a lock's states")
}

// coerceDateTime checks a date/time value against the shape the control holds
// and returns it normalized, so two writes of the same instant compare equal.
func coerceDateTime(mode, s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", nil
	}
	switch mode {
	case "date":
		t, err := time.Parse("2006-01-02", s)
		if err != nil {
			return nil, fmt.Errorf("value must be a date as YYYY-MM-DD")
		}
		return t.Format("2006-01-02"), nil
	case "time":
		for _, layout := range []string{"15:04:05", "15:04"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t.Format("15:04:05"), nil
			}
		}
		return nil, fmt.Errorf("value must be a time as HH:MM or HH:MM:SS")
	default:
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return nil, fmt.Errorf("value must be a timestamp in RFC 3339 form")
		}
		return t.Format(time.RFC3339), nil
	}
}

// coerceBool accepts the forms a dashboard, an MQTT payload, and a flow all
// reasonably call true or false.
func coerceBool(value any) (any, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case float64:
		return v != 0, nil
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "on", "1", "yes", "open", "unlocked":
			return true, nil
		case "false", "off", "0", "no", "closed", "locked":
			return false, nil
		}
	}
	return nil, fmt.Errorf("value must be true or false")
}

// coerceFloat accepts a number or the text of one, which is what an MQTT
// payload or an expression usually yields.
func coerceFloat(value any) (float64, error) {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("value must be a finite number")
		}
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("value must be a number")
		}
		// ParseFloat reads "nan" and "inf" as numbers.
		if math.IsNaN(n) || math.IsInf(n, 0) {
			return 0, fmt.Errorf("value must be a finite number")
		}
		return n, nil
	}
	return 0, fmt.Errorf("value must be a number")
}

// coerceString accepts scalars, because a number or boolean written into a
// text control reads as its printed form.
func coerceString(value any) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64), nil
	case bool:
		return strconv.FormatBool(v), nil
	case nil:
		return "", nil
	}
	return "", fmt.Errorf("value must be text")
}

// optFloat reads a numeric kind setting.
func optFloat(opts map[string]any, key string) (float64, bool) {
	switch v := opts[key].(type) {
	case float64:
		return v, true
	case int:
		return float64(v), true
	case string:
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

// optString reads a text kind setting.
func optString(opts map[string]any, key string) string {
	if s, ok := opts[key].(string); ok {
		return s
	}
	return ""
}

// optBool reads a boolean kind setting. An option that was never stored reads
// as its declared default, so a light created before colour existed does not
// silently lose brightness.
func optBool(opts map[string]any, key string) bool {
	switch v := opts[key].(type) {
	case bool:
		return v
	case string:
		return v == "true"
	case float64:
		return v != 0
	}
	return defaultBoolOption(key)
}

// defaultBoolOption answers what a missing feature flag means. Only the ones
// declared on by default are listed; everything else is off until asked for.
func defaultBoolOption(key string) bool {
	switch key {
	case "brightness", "stop", "speed", "volume", "mute", "skip":
		return true
	}
	return false
}

// choiceValues reads a choices option into the values it holds, tolerating the
// plain string list an older control or a hand-written import may carry.
func choiceValues(opts map[string]any, key string) []string {
	raw, ok := opts[key].([]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		switch v := item.(type) {
		case map[string]any:
			if s, err := coerceString(v["value"]); err == nil && s != "" {
				out = append(out, s)
			}
		default:
			if s, err := coerceString(item); err == nil && s != "" {
				out = append(out, s)
			}
		}
	}
	return out
}
