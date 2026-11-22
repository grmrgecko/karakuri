package zigbee2mqtt

import (
	"math"
	"sort"

	"github.com/grmrgecko/karakuri/pkg/controls"
)

// Proposal is the karakuri device a Zigbee2MQTT device becomes.
type Proposal struct {
	Kind    string
	Options map[string]any
	// Fields are the other properties the device reports, kept in its state
	// under their own names: battery, power, link quality.
	Fields []string
	// Mapped are the device properties the kind's keys are read from.
	Mapped []string
	// ReadOnly is set when nothing the kind draws can be commanded.
	ReadOnly bool

	keys []keyMap
	// colorXY is set when colour arrives as CIE xy and the state script needs
	// the conversion helper.
	colorXY bool
}

// keyMap reads one of the kind's keys from a device property and, when the
// key can be commanded, writes it back. Read is a JavaScript expression over
// v, the property's value; Write is one over value and on producing the
// value to send for Prop.
type keyMap struct {
	Key   string
	Prop  string
	Read  string
	Write string
}

// binarySensors are binary properties that make a good main reading, with
// the labels a dashboard shows for true and false. Zigbee2MQTT reports a
// contact sensor's contact as true when closed, so a contact is labelled the
// other way round.
var binarySensors = map[string]map[string]any{
	"contact":    {"true": "Closed", "false": "Open"},
	"occupancy":  {"true": "Detected", "false": "Clear"},
	"presence":   {"true": "Present", "false": "Away"},
	"water_leak": {"true": "Leak", "false": "Dry"},
	"smoke":      {"true": "Smoke", "false": "Clear"},
	"gas":        {"true": "Gas", "false": "Clear"},
	"tamper":     {"true": "Tampered", "false": "OK"},
	"vibration":  {"true": "Vibrating", "false": "Still"},
}

// Propose maps a device's exposes to a kind. The main expose decides the
// kind: a specific type first, then a binary sensor, then a button's action,
// then the first reading Zigbee2MQTT does not mark as a setting or a
// diagnostic, then anything at all. Every other property is kept as a field.
func Propose(d Device) Proposal {
	var exposes []Expose
	if d.Definition != nil {
		exposes = d.Definition.Exposes
	}
	p := Proposal{Options: map[string]any{}}

	if e, ok := firstExpose(exposes, func(e Expose) bool {
		switch e.Type {
		case "light", "switch", "cover", "lock", "fan", "climate":
			return true
		}
		return false
	}); ok {
		p.fromSpecific(e, exposes)
	} else if e, ok := firstExpose(exposes, func(e Expose) bool {
		_, known := binarySensors[e.Property]
		return e.Type == "binary" && known
	}); ok {
		p.Kind = controls.KindBoolean
		p.ReadOnly = true
		p.Options["labels"] = binarySensors[e.Property]
		p.keys = []keyMap{{Key: "on", Prop: e.Property, Read: "v === true"}}
	} else if e, ok := firstExpose(exposes, func(e Expose) bool {
		return e.Property == "action" && e.Type == "enum"
	}); ok {
		p.Kind = controls.KindText
		p.ReadOnly = true
		// Zigbee2MQTT clears action to an empty string after each press, so
		// the reading keeps the last press instead.
		p.keys = []keyMap{{Key: "value", Prop: e.Property, Read: "v || undefined"}}
	} else if e, ok := firstExpose(exposes, func(e Expose) bool {
		return e.Category == "" && e.Property != "" && e.Access&accessState != 0
	}); ok {
		p.fromReading(e)
	} else if e, ok := firstExpose(exposes, func(e Expose) bool { return e.Property != "" }); ok {
		p.fromReading(e)
	} else {
		p.Kind = controls.KindText
		p.ReadOnly = true
	}

	mapped := map[string]bool{}
	for _, k := range p.keys {
		mapped[k.Prop] = true
	}
	for prop := range mapped {
		p.Mapped = append(p.Mapped, prop)
	}
	sort.Strings(p.Mapped)
	for _, prop := range properties(exposes) {
		if !mapped[prop] {
			p.Fields = append(p.Fields, prop)
		}
	}
	if !p.ReadOnly {
		p.ReadOnly = true
		for _, k := range p.keys {
			if k.Write != "" {
				p.ReadOnly = false
				break
			}
		}
	}
	return p
}

// fromSpecific maps a light, switch, cover, lock, fan, or climate expose.
func (p *Proposal) fromSpecific(e Expose, all []Expose) {
	feature := func(names ...string) (Expose, bool) {
		for _, f := range e.Features {
			for _, n := range names {
				if f.Name == n || f.Property == n {
					return f, true
				}
			}
		}
		return Expose{}, false
	}
	settable := func(f Expose) bool { return f.Access&accessSet != 0 }

	switch e.Type {
	case "light":
		p.Kind = controls.KindLight
		p.Options["brightness"] = false
		if f, ok := feature("state"); ok {
			p.keys = append(p.keys, binaryKey("on", f))
		}
		if f, ok := feature("brightness"); ok {
			max := valueOr(f.ValueMax, 254)
			p.Options["brightness"] = true
			k := keyMap{Key: "brightness", Prop: f.Property, Read: "Math.round((v / " + num(max) + ") * 100)"}
			if settable(f) {
				k.Write = "Math.round((value / 100) * " + num(max) + ")"
			}
			p.keys = append(p.keys, k)
		}
		if f, ok := feature("color_temp"); ok {
			p.Options["color_temp"] = true
			// Mireds and kelvin run in opposite directions, so the warmest
			// kelvin comes from the largest mired value.
			p.Options["min_kelvin"] = math.Round(1e6 / valueOr(f.ValueMax, 500))
			p.Options["max_kelvin"] = math.Round(1e6 / valueOr(f.ValueMin, 153))
			k := keyMap{Key: "color_temp", Prop: f.Property, Read: "v ? Math.round(1000000 / v) : undefined"}
			if settable(f) {
				k.Write = "Math.round(1000000 / value)"
			}
			p.keys = append(p.keys, k)
		}
		if f, ok := feature("color_xy", "color_hs"); ok {
			p.Options["color"] = true
			p.colorXY = f.Name == "color_xy"
			read := "v && v.hex ? v.hex : undefined"
			if p.colorXY {
				read = "v && v.x !== undefined ? xyToHex(v.x, v.y) : (v && v.hex ? v.hex : undefined)"
			}
			k := keyMap{Key: "color", Prop: f.Property, Read: read}
			if settable(f) {
				k.Write = "({ hex: value })"
			}
			p.keys = append(p.keys, k)
		}
		if f, ok := firstExpose(all, func(x Expose) bool { return x.Property == "effect" && x.Type == "enum" }); ok {
			p.Options["effects"] = true
			p.Options["effect_list"] = choices(f.Values)
			k := keyMap{Key: "effect", Prop: f.Property, Read: "v"}
			if settable(f) {
				k.Write = "value"
			}
			p.keys = append(p.keys, k)
		}

	case "switch":
		p.Kind = controls.KindBoolean
		if f, ok := feature("state"); ok {
			p.keys = append(p.keys, binaryKey("on", f))
		}

	case "cover":
		p.Kind = controls.KindCover
		p.Options["position"], p.Options["tilt"], p.Options["stop"] = false, false, true
		if f, ok := feature("state"); ok {
			k := keyMap{Key: "state", Prop: f.Property,
				Read: `({ OPEN: "open", CLOSE: "closed", STOP: "stopped", OPENING: "opening", CLOSING: "closing" })[String(v).toUpperCase()]`}
			if settable(f) {
				k.Write = `({ open: "OPEN", closed: "CLOSE", stopped: "STOP" })[value]`
			}
			p.keys = append(p.keys, k)
		}
		for _, name := range []string{"position", "tilt"} {
			if f, ok := feature(name); ok {
				p.Options[name] = true
				k := keyMap{Key: name, Prop: f.Property, Read: "v"}
				if settable(f) {
					k.Write = "value"
				}
				p.keys = append(p.keys, k)
			}
		}

	case "lock":
		p.Kind = controls.KindLock
		if f, ok := feature("lock_state"); ok {
			p.keys = append(p.keys, keyMap{Key: "state", Prop: f.Property,
				Read: `({ locked: "locked", unlocked: "unlocked", not_fully_locked: "jammed" })[v]`})
		}
		if f, ok := feature("state"); ok {
			read := `v === ` + jsLiteral(f.ValueOn, "LOCK") + ` ? "locked" : "unlocked"`
			k := keyMap{Key: "state", Prop: f.Property, Read: read}
			if settable(f) {
				// Only locked and unlocked name an action. The states a lock
				// reports in between write nothing; falling through to the
				// off value would unlock the door for jammed.
				k.Write = `value === "locked" ? ` + jsLiteral(f.ValueOn, "LOCK") +
					` : value === "unlocked" ? ` + jsLiteral(f.ValueOff, "UNLOCK") + ` : undefined`
			}
			// lock_state, when present, is the better reading; state then only
			// commands.
			if len(p.keys) > 0 {
				k.Read = ""
			}
			p.keys = append(p.keys, k)
		}

	case "fan":
		p.Kind = controls.KindFan
		p.Options["speed"], p.Options["presets"] = false, false
		if f, ok := feature("state"); ok {
			p.keys = append(p.keys, binaryKey("on", f))
		}
		if f, ok := feature("mode", "fan_mode"); ok {
			p.Options["presets"] = true
			p.Options["preset_list"] = choices(f.Values)
			k := keyMap{Key: "preset", Prop: f.Property, Read: "v"}
			if settable(f) {
				k.Write = "value"
			}
			p.keys = append(p.keys, k)
		}

	case "climate":
		p.Kind = controls.KindClimate
		if f, ok := feature("system_mode"); ok {
			p.Options["modes"] = choices(f.Values)
			k := keyMap{Key: "mode", Prop: f.Property, Read: "v"}
			if settable(f) {
				k.Write = "value"
			}
			p.keys = append(p.keys, k)
		}
		if f, ok := feature("occupied_heating_setpoint", "current_heating_setpoint", "occupied_cooling_setpoint"); ok {
			p.Options["min_temp"] = valueOr(f.ValueMin, 7)
			p.Options["max_temp"] = valueOr(f.ValueMax, 35)
			p.Options["step"] = valueOr(f.ValueStep, 0.5)
			if f.Unit != "" {
				p.Options["unit"] = f.Unit
			}
			k := keyMap{Key: "target", Prop: f.Property, Read: "v"}
			if settable(f) {
				k.Write = "value"
			}
			p.keys = append(p.keys, k)
		}
		if f, ok := feature("local_temperature"); ok {
			p.keys = append(p.keys, keyMap{Key: "current", Prop: f.Property, Read: "v"})
		}
		if f, ok := feature("running_state"); ok {
			p.keys = append(p.keys, keyMap{Key: "action", Prop: f.Property, Read: "v"})
		}
		if f, ok := feature("fan_mode"); ok {
			p.Options["fan"] = true
			p.Options["fan_modes"] = choices(f.Values)
			k := keyMap{Key: "fan_mode", Prop: f.Property, Read: "v"}
			if settable(f) {
				k.Write = "value"
			}
			p.keys = append(p.keys, k)
		}
		if _, ok := p.Options["modes"]; !ok {
			p.Options["modes"] = choices([]string{"off", "heat"})
		}
	}
}

// fromReading maps one generic expose to a read-only reading.
func (p *Proposal) fromReading(e Expose) {
	p.ReadOnly = true
	switch e.Type {
	case "numeric":
		p.Kind = controls.KindNumber
		if e.Unit != "" {
			p.Options["unit"] = e.Unit
		}
		if e.ValueMin != nil && e.ValueMax != nil && *e.ValueMin < *e.ValueMax {
			p.Options["min"], p.Options["max"] = *e.ValueMin, *e.ValueMax
		} else {
			// A reading has no slider, but a number control still holds its
			// value to bounds; wide ones keep any reading.
			p.Options["min"], p.Options["max"] = -1e9, 1e9
		}
		p.Options["display"] = "box"
		p.keys = []keyMap{{Key: "value", Prop: e.Property, Read: "v"}}
	case "binary":
		p.Kind = controls.KindBoolean
		p.keys = []keyMap{{Key: "on", Prop: e.Property, Read: "v === " + jsLiteral(e.ValueOn, true)}}
	default:
		p.Kind = controls.KindText
		p.keys = []keyMap{{Key: "value", Prop: e.Property, Read: "v"}}
	}
}

// binaryKey reads and writes a binary feature's on and off values.
func binaryKey(key string, f Expose) keyMap {
	on, off := jsLiteral(f.ValueOn, "ON"), jsLiteral(f.ValueOff, "OFF")
	k := keyMap{Key: key, Prop: f.Property, Read: "v === " + on}
	if f.Access&accessSet != 0 {
		k.Write = "value ? " + on + " : " + off
	}
	return k
}

// firstExpose finds the first expose matching ok.
func firstExpose(exposes []Expose, ok func(Expose) bool) (Expose, bool) {
	for _, e := range exposes {
		if ok(e) {
			return e, true
		}
	}
	return Expose{}, false
}

// properties lists every property a device reports, features included, in
// expose order and without repeats.
func properties(exposes []Expose) []string {
	seen := map[string]bool{}
	var out []string
	var walk func([]Expose)
	walk = func(list []Expose) {
		for _, e := range list {
			if e.Property != "" && (e.Access == 0 || e.Access&accessState != 0) && !seen[e.Property] {
				seen[e.Property] = true
				out = append(out, e.Property)
			}
			// A composite's features live inside its own property, not beside it.
			if e.Type != "composite" {
				walk(e.Features)
			}
		}
	}
	walk(exposes)
	return out
}

// choices renders a list of values as a kind's choices option.
func choices(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, map[string]any{"value": v, "label": v})
	}
	return out
}

// valueOr dereferences an optional number.
func valueOr(v *float64, def float64) float64 {
	if v == nil {
		return def
	}
	return *v
}
