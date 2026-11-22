package hadiscovery

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
)

// colorHelpers convert between the colour forms lights report and the hex a
// light's colour key holds.
const colorHelpers = `const __hex = (r, g, b) => "#" + [r, g, b].map((c) => Math.max(0, Math.min(255, Math.round(Number(c)))).toString(16).padStart(2, "0")).join("");
const __rgb = (hex) => { const h = String(hex).replace("#", ""); return [parseInt(h.slice(0, 2), 16), parseInt(h.slice(2, 4), 16), parseInt(h.slice(4, 6), 16)]; };
const __hs2hex = (h, s) => { h = Number(h) / 60; s = Number(s) / 100; const c = s, x = c * (1 - Math.abs((h % 2) - 1)), m = 1 - c; const t = [[c, x, 0], [x, c, 0], [0, c, x], [0, x, c], [x, 0, c], [c, 0, x]][Math.floor(h) % 6] || [0, 0, 0]; return __hex((t[0] + m) * 255, (t[1] + m) * 255, (t[2] + m) * 255); };
const __hex2hs = (hex) => { const [r, g, b] = __rgb(hex).map((c) => c / 255); const max = Math.max(r, g, b), min = Math.min(r, g, b), d = max - min; let h = 0; if (d) { h = max === r ? ((g - b) / d) % 6 : max === g ? (b - r) / d + 2 : (r - g) / d + 4; } h = Math.round(h * 60); if (h < 0) h += 360; return [h, max ? Math.round((d / max) * 100) : 0]; };
const __xy2hex = (x, y) => { const z = 1 - x - y; const Y = 1, X = (Y / y) * x, Z = (Y / y) * z; let r = X * 1.656492 - Y * 0.354851 - Z * 0.255038; let g = -X * 0.707196 + Y * 1.655397 + Z * 0.036152; let b = X * 0.051713 - Y * 0.121364 + Z * 1.01153; const m = Math.max(r, g, b, 1e-9); const gm = (c) => (c <= 0.0031308 ? 12.92 * c : 1.055 * Math.pow(c, 1 / 2.4) - 0.055); return __hex(gm(Math.max(0, r / m)) * 255, gm(Math.max(0, g / m)) * 255, gm(Math.max(0, b / m)) * 255); };
`

// Mapped is the karakuri device a discovery config becomes.
type Mapped struct {
	Definition mqttrelay.Definition
	// Activity lists the topics whose live messages show the device is
	// alive: its state topics and its availability topic.
	Activity []string
	// AvailabilityTopic and Online decide which availability messages count
	// as activity.
	AvailabilityTopic string
	Online            string
}

// read is one value the state script reads: a karakuri key from one topic,
// through a template, converted by a JavaScript expression over v.
type read struct {
	topic   string
	key     string
	tpl     string
	convert string
}

// write is one key the payload script commands: a JavaScript expression over
// value producing the value a command template sees, published on topic.
type write struct {
	key   string
	topic string
	value string
	tpl   string
}

// builder accumulates a mapping.
type builder struct {
	cfg     Config
	kind    string
	options map[string]any
	reads   []read
	writes  []write
	// jsonState reads every key from one JSON state topic, for the light JSON
	// schema, as JavaScript statements over d.
	jsonTopic string
	jsonReads []string
	// payload builds a command by field for schemas whose commands are one
	// document, as JavaScript statements returning the result.
	jsonWrites map[string]string
	colors     bool
}

// Map turns a discovery config into a device definition, or reports why it
// cannot be one.
func Map(cfg Config, connID uuid.UUID) (Mapped, error) {
	b := &builder{cfg: cfg, options: map[string]any{}}
	var err error
	switch cfg.Component {
	case "switch":
		err = b.binary(controls.KindBoolean, true)
	case "binary_sensor":
		err = b.binary(controls.KindBoolean, false)
	case "sensor":
		err = b.sensor()
	case "light":
		switch cfg.String("schema", "default") {
		case "default", "basic":
			err = b.lightDefault()
		case "json":
			err = b.lightJSON()
		default:
			err = fmt.Errorf("light schema %q is not supported", cfg.String("schema", ""))
		}
	case "cover":
		err = b.cover()
	case "lock":
		err = b.lock()
	case "fan":
		err = b.fan()
	case "climate":
		err = b.climate()
	case "number":
		err = b.number()
	case "select":
		err = b.selectEntity()
	case "button":
		err = b.button()
	case "text":
		err = b.text()
	default:
		err = fmt.Errorf("component %q is not supported", cfg.Component)
	}
	if err != nil {
		return Mapped{}, err
	}
	return b.finish(connID)
}

// template translates a template field, defaulting to the raw value.
func (b *builder) template(field string) (string, error) {
	tpl := b.cfg.String(field, "")
	if strings.TrimSpace(tpl) == "" {
		return "value", nil
	}
	js, err := TranslateTemplate(tpl)
	if err != nil {
		return "", fmt.Errorf("%s: %w", field, err)
	}
	return js, nil
}

// addRead adds a read from topicField through tplField.
func (b *builder) addRead(topicField, tplField, key, convert string) error {
	topic := b.cfg.String(topicField, "")
	if topic == "" {
		return nil
	}
	tpl, err := b.template(tplField)
	if err != nil {
		return err
	}
	b.reads = append(b.reads, read{topic: topic, key: key, tpl: tpl, convert: convert})
	return nil
}

// addWrite adds a command for key on topicField, with value the HA-level
// value and tplField an optional command template.
func (b *builder) addWrite(topicField, tplField, key, value string) error {
	topic := b.cfg.String(topicField, "")
	if topic == "" {
		return nil
	}
	tpl := ""
	if tplField != "" && strings.TrimSpace(b.cfg.String(tplField, "")) != "" {
		js, err := TranslateTemplate(b.cfg.String(tplField, ""))
		if err != nil {
			return fmt.Errorf("%s: %w", tplField, err)
		}
		tpl = js
	}
	b.writes = append(b.writes, write{key: key, topic: topic, value: value, tpl: tpl})
	return nil
}

// stateOrCommand reads the primary key from the state topic, or from the
// command topic when the entity has no state topic, so a device that only
// takes commands still shows the last one sent.
func (b *builder) stateTopicField(field string) string {
	if b.cfg.String(field, "") != "" {
		return field
	}
	return "command_topic"
}

// binary maps a switch or binary sensor.
func (b *builder) binary(kind string, commands bool) error {
	b.kind = kind
	on := b.cfg.String("payload_on", "ON")
	off := b.cfg.String("payload_off", "OFF")
	stateOn := b.cfg.String("state_on", on)
	stateOff := b.cfg.String("state_off", off)
	topicField := "state_topic"
	if commands {
		topicField = b.stateTopicField("state_topic")
	}
	if b.cfg.Component == "binary_sensor" {
		if labels := binaryLabels(b.cfg.String("device_class", "")); labels != nil {
			b.options["labels"] = labels
		}
	}
	convert := "(__s(v) === " + jsQuote(stateOn) + " ? true : __s(v) === " + jsQuote(stateOff) + " ? false : undefined)"
	if err := b.addRead(topicField, "value_template", "on", convert); err != nil {
		return err
	}
	if commands {
		return b.addWrite("command_topic", "command_template", "on", "(value ? "+jsQuote(on)+" : "+jsQuote(off)+")")
	}
	return nil
}

// binaryLabels names a binary sensor's states by device class.
func binaryLabels(class string) map[string]any {
	pair := func(on, off string) map[string]any { return map[string]any{"true": on, "false": off} }
	switch class {
	case "door", "garage_door", "opening", "window":
		return pair("Open", "Closed")
	case "motion", "occupancy", "presence", "moving", "sound", "vibration", "smoke", "gas", "carbon_monoxide", "problem", "safety", "tamper":
		return pair("Detected", "Clear")
	case "moisture":
		return pair("Wet", "Dry")
	case "lock":
		return pair("Unlocked", "Locked")
	case "connectivity":
		return pair("Connected", "Disconnected")
	case "battery":
		return pair("Low", "Normal")
	case "plug", "power", "running", "light":
		return pair("On", "Off")
	}
	return nil
}

// numericClasses are sensor device classes whose state is a number.
var numericClasses = map[string]bool{
	"apparent_power": true, "aqi": true, "atmospheric_pressure": true, "battery": true, "carbon_dioxide": true,
	"carbon_monoxide": true, "current": true, "data_rate": true, "data_size": true, "distance": true, "duration": true,
	"energy": true, "frequency": true, "gas": true, "humidity": true, "illuminance": true, "irradiance": true,
	"moisture": true, "monetary": true, "nitrogen_dioxide": true, "pm1": true, "pm10": true, "pm25": true, "power": true,
	"power_factor": true, "precipitation": true, "pressure": true, "reactive_power": true, "signal_strength": true,
	"sound_pressure": true, "speed": true, "temperature": true, "volatile_organic_compounds": true, "voltage": true,
	"volume": true, "water": true, "weight": true, "wind_speed": true,
}

// sensor maps a sensor: a number when it has a unit, a state class, or a
// numeric device class, otherwise text.
func (b *builder) sensor() error {
	unit := b.cfg.String("unit_of_measurement", "")
	if unit != "" || b.cfg.Has("state_class") || numericClasses[b.cfg.String("device_class", "")] {
		b.kind = controls.KindNumber
		b.options["min"], b.options["max"] = -1e9, 1e9
		b.options["display"] = "box"
		if unit != "" {
			b.options["unit"] = unit
		}
		return b.addRead("state_topic", "value_template", "value", "(() => { const n = Number(v); return v === \"\" || v === null || isNaN(n) ? undefined : n; })()")
	}
	b.kind = controls.KindText
	return b.addRead("state_topic", "value_template", "value", "(v === undefined || v === null ? undefined : __s(v))")
}

// mireds turns a light's colour temperature range into kelvin options.
func (b *builder) kelvinRange() {
	if b.cfg.Bool("color_temp_kelvin", false) {
		b.options["min_kelvin"] = b.cfg.Number("min_kelvin", 2000)
		b.options["max_kelvin"] = b.cfg.Number("max_kelvin", 6535)
		return
	}
	b.options["min_kelvin"] = math.Round(1e6 / b.cfg.Number("max_mireds", 500))
	b.options["max_kelvin"] = math.Round(1e6 / b.cfg.Number("min_mireds", 153))
}

// lightDefault maps a light using the default schema: one topic per feature.
func (b *builder) lightDefault() error {
	b.kind = controls.KindLight
	b.options["brightness"] = false
	on := b.cfg.String("payload_on", "ON")
	off := b.cfg.String("payload_off", "OFF")
	convert := "(__s(v) === " + jsQuote(on) + " ? true : __s(v) === " + jsQuote(off) + " ? false : undefined)"
	if err := b.addRead(b.stateTopicField("state_topic"), "state_value_template", "on", convert); err != nil {
		return err
	}
	if err := b.addWrite("command_topic", "", "on", "(value ? "+jsQuote(on)+" : "+jsQuote(off)+")"); err != nil {
		return err
	}

	scale := b.cfg.Number("brightness_scale", 255)
	if b.cfg.Has("brightness_state_topic") || b.cfg.Has("brightness_command_topic") {
		b.options["brightness"] = true
		if err := b.addRead("brightness_state_topic", "brightness_value_template", "brightness",
			"Math.round((Number(v) / "+num(scale)+") * 100)"); err != nil {
			return err
		}
		if err := b.addWrite("brightness_command_topic", "brightness_command_template", "brightness",
			"Math.round((value / 100) * "+num(scale)+")"); err != nil {
			return err
		}
	}
	if b.cfg.Has("color_temp_state_topic") || b.cfg.Has("color_temp_command_topic") {
		b.options["color_temp"] = true
		b.kelvinRange()
		read, write := "Math.round(1000000 / Number(v))", "Math.round(1000000 / value)"
		if b.cfg.Bool("color_temp_kelvin", false) {
			read, write = "Math.round(Number(v))", "Math.round(value)"
		}
		if err := b.addRead("color_temp_state_topic", "color_temp_value_template", "color_temp", read); err != nil {
			return err
		}
		if err := b.addWrite("color_temp_command_topic", "color_temp_command_template", "color_temp", write); err != nil {
			return err
		}
	}
	switch {
	case b.cfg.Has("rgb_state_topic") || b.cfg.Has("rgb_command_topic"):
		if b.cfg.String("rgb_command_template", "") != "" {
			return fmt.Errorf("rgb_command_template is not supported")
		}
		b.options["color"] = true
		b.colors = true
		if err := b.addRead("rgb_state_topic", "rgb_value_template", "color",
			"(() => { const p = __s(v).split(\",\"); return p.length >= 3 ? __hex(p[0], p[1], p[2]) : undefined; })()"); err != nil {
			return err
		}
		if err := b.addWrite("rgb_command_topic", "", "color", "__rgb(value).join(\",\")"); err != nil {
			return err
		}
	case b.cfg.Has("hs_state_topic") || b.cfg.Has("hs_command_topic"):
		b.options["color"] = true
		b.colors = true
		if err := b.addRead("hs_state_topic", "hs_value_template", "color",
			"(() => { const p = __s(v).split(\",\"); return p.length >= 2 ? __hs2hex(p[0], p[1]) : undefined; })()"); err != nil {
			return err
		}
		if err := b.addWrite("hs_command_topic", "", "color", "__hex2hs(value).join(\",\")"); err != nil {
			return err
		}
	case b.cfg.Has("xy_state_topic"):
		b.options["color"] = true
		b.colors = true
		if err := b.addRead("xy_state_topic", "xy_value_template", "color",
			"(() => { const p = __s(v).split(\",\"); return p.length >= 2 ? __xy2hex(Number(p[0]), Number(p[1])) : undefined; })()"); err != nil {
			return err
		}
	}
	if list := b.cfg.Strings("effect_list", nil); len(list) > 0 {
		b.options["effects"] = true
		b.options["effect_list"] = choices(list)
		if err := b.addRead("effect_state_topic", "effect_value_template", "effect", "(v === undefined ? undefined : __s(v))"); err != nil {
			return err
		}
		if err := b.addWrite("effect_command_topic", "effect_command_template", "effect", "value"); err != nil {
			return err
		}
	}
	return nil
}

// lightJSON maps a light using the JSON schema: one state document and one
// command document.
func (b *builder) lightJSON() error {
	b.kind = controls.KindLight
	b.colors = true
	modes := b.cfg.Strings("supported_color_modes", nil)
	has := func(names ...string) bool {
		for _, m := range modes {
			for _, n := range names {
				if m == n {
					return true
				}
			}
		}
		return false
	}
	brightness := b.cfg.Bool("brightness", false) || has("brightness", "color_temp", "hs", "xy", "rgb", "rgbw", "rgbww", "white")
	colorTemp := b.cfg.Bool("color_temp", false) || has("color_temp")
	color := b.cfg.Bool("rgb", false) || b.cfg.Bool("hs", false) || b.cfg.Bool("xy", false) || has("hs", "xy", "rgb", "rgbw", "rgbww")
	effects := b.cfg.Strings("effect_list", nil)
	b.options["brightness"] = brightness
	scale := b.cfg.Number("brightness_scale", 255)
	kelvin := b.cfg.Bool("color_temp_kelvin", false)

	b.jsonTopic = b.cfg.String("state_topic", "")
	b.jsonReads = append(b.jsonReads, `if ("state" in d) out.on = String(d.state).toUpperCase() === "ON";`)
	b.jsonWrites = map[string]string{
		"on": `({ state: value ? "ON" : "OFF" })`,
	}
	if brightness {
		b.jsonReads = append(b.jsonReads, `if (typeof d.brightness === "number") out.brightness = Math.round((d.brightness / `+num(scale)+`) * 100);`)
		b.jsonWrites["brightness"] = `({ state: "ON", brightness: Math.round((value / 100) * ` + num(scale) + `) })`
	}
	if colorTemp {
		b.options["color_temp"] = true
		b.kelvinRange()
		if kelvin {
			b.jsonReads = append(b.jsonReads, `if (typeof d.color_temp === "number") out.color_temp = Math.round(d.color_temp);`)
			b.jsonWrites["color_temp"] = `({ state: "ON", color_temp: Math.round(value) })`
		} else {
			b.jsonReads = append(b.jsonReads, `if (typeof d.color_temp === "number" && d.color_temp > 0) out.color_temp = Math.round(1000000 / d.color_temp);`)
			b.jsonWrites["color_temp"] = `({ state: "ON", color_temp: Math.round(1000000 / value) })`
		}
	}
	if color {
		b.options["color"] = true
		b.jsonReads = append(b.jsonReads, `if (d.color && typeof d.color === "object") {
  const c = d.color;
  if (c.r !== undefined) out.color = __hex(c.r, c.g, c.b);
  else if (c.h !== undefined) out.color = __hs2hex(c.h, c.s);
  else if (c.x !== undefined) out.color = __xy2hex(c.x, c.y);
}`)
		write := `(() => { const [r, g, b] = __rgb(value); return { state: "ON", color: { r, g, b } }; })()`
		if !has("rgb", "rgbw", "rgbww") && has("hs") {
			write = `(() => { const [h, s] = __hex2hs(value); return { state: "ON", color: { h, s } }; })()`
		}
		b.jsonWrites["color"] = write
	}
	if len(effects) > 0 {
		b.options["effects"] = true
		b.options["effect_list"] = choices(effects)
		b.jsonReads = append(b.jsonReads, `if (typeof d.effect === "string") out.effect = d.effect;`)
		b.jsonWrites["effect"] = `({ state: "ON", effect: value })`
	}
	if b.jsonTopic == "" {
		// Without a state topic the light shows the last command sent.
		b.jsonTopic = b.cfg.String("command_topic", "")
	}
	if b.jsonTopic == "" {
		return fmt.Errorf("light has neither a state nor a command topic")
	}
	return nil
}

// cover maps a cover.
func (b *builder) cover() error {
	b.kind = controls.KindCover
	b.options["position"], b.options["tilt"] = false, false
	// A null payload_stop says the cover cannot stop.
	_, stopSet := b.cfg.Fields["payload_stop"]
	b.options["stop"] = b.cfg.String("command_topic", "") != "" && (!stopSet || b.cfg.Has("payload_stop"))
	states := map[string]string{
		b.cfg.String("state_open", "open"):       "open",
		b.cfg.String("state_opening", "opening"): "opening",
		b.cfg.String("state_closed", "closed"):   "closed",
		b.cfg.String("state_closing", "closing"): "closing",
		b.cfg.String("state_stopped", "stopped"): "stopped",
	}
	keys := make([]string, 0, len(states))
	for k := range states {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var table []string
	for _, k := range keys {
		table = append(table, jsQuote(k)+": "+jsQuote(states[k]))
	}
	if err := b.addRead("state_topic", "value_template", "state", "({"+strings.Join(table, ", ")+"})[__s(v)]"); err != nil {
		return err
	}
	open := b.cfg.String("payload_open", "OPEN")
	closePayload := b.cfg.String("payload_close", "CLOSE")
	stop := b.cfg.String("payload_stop", "STOP")
	if err := b.addWrite("command_topic", "", "state",
		"({ open: "+jsQuote(open)+", closed: "+jsQuote(closePayload)+", stopped: "+jsQuote(stop)+" })[value]"); err != nil {
		return err
	}

	posOpen := b.cfg.Number("position_open", 100)
	posClosed := b.cfg.Number("position_closed", 0)
	span := posOpen - posClosed
	if span == 0 {
		span = 100
	}
	if b.cfg.Has("position_topic") || b.cfg.Has("set_position_topic") {
		b.options["position"] = true
		if err := b.addRead("position_topic", "position_template", "position",
			"Math.round(((Number(v) - "+num(posClosed)+") / "+num(span)+") * 100)"); err != nil {
			return err
		}
		if !b.cfg.Has("state_topic") {
			// Without a state topic, the position says whether it is open.
			if err := b.addRead("position_topic", "position_template", "state",
				"(Number(v) === "+num(posClosed)+" ? \"closed\" : \"open\")"); err != nil {
				return err
			}
		}
		if err := b.addWrite("set_position_topic", "set_position_template", "position",
			"Math.round("+num(posClosed)+" + (value / 100) * "+num(span)+")"); err != nil {
			return err
		}
	}
	if b.cfg.Has("tilt_status_topic") || b.cfg.Has("tilt_command_topic") {
		b.options["tilt"] = true
		tmin, tmax := b.cfg.Number("tilt_min", 0), b.cfg.Number("tilt_max", 100)
		tspan := tmax - tmin
		if tspan == 0 {
			tspan = 100
		}
		if err := b.addRead("tilt_status_topic", "tilt_status_template", "tilt",
			"Math.round(((Number(v) - "+num(tmin)+") / "+num(tspan)+") * 100)"); err != nil {
			return err
		}
		if err := b.addWrite("tilt_command_topic", "tilt_command_template", "tilt",
			"Math.round("+num(tmin)+" + (value / 100) * "+num(tspan)+")"); err != nil {
			return err
		}
	}
	return nil
}

// lock maps a lock.
func (b *builder) lock() error {
	b.kind = controls.KindLock
	states := []struct{ field, def, state string }{
		{"state_locked", "LOCKED", "locked"},
		{"state_unlocked", "UNLOCKED", "unlocked"},
		{"state_locking", "LOCKING", "locking"},
		{"state_unlocking", "UNLOCKING", "unlocking"},
		{"state_jammed", "JAMMED", "jammed"},
	}
	var table []string
	for _, s := range states {
		table = append(table, jsQuote(b.cfg.String(s.field, s.def))+": "+jsQuote(s.state))
	}
	lock := b.cfg.String("payload_lock", "LOCK")
	unlock := b.cfg.String("payload_unlock", "UNLOCK")
	// A lock with no state topic echoes its commands.
	table = append(table, jsQuote(lock)+": \"locked\"", jsQuote(unlock)+": \"unlocked\"")
	if err := b.addRead(b.stateTopicField("state_topic"), "value_template", "state", "({"+strings.Join(table, ", ")+"})[__s(v)]"); err != nil {
		return err
	}
	return b.addWrite("command_topic", "command_template", "state",
		"(value === \"locked\" ? "+jsQuote(lock)+" : value === \"unlocked\" ? "+jsQuote(unlock)+" : undefined)")
}

// fan maps a fan.
func (b *builder) fan() error {
	b.kind = controls.KindFan
	on := b.cfg.String("payload_on", "ON")
	off := b.cfg.String("payload_off", "OFF")
	convert := "(__s(v) === " + jsQuote(on) + " ? true : __s(v) === " + jsQuote(off) + " ? false : undefined)"
	if err := b.addRead(b.stateTopicField("state_topic"), "state_value_template", "on", convert); err != nil {
		return err
	}
	if err := b.addWrite("command_topic", "command_template", "on", "(value ? "+jsQuote(on)+" : "+jsQuote(off)+")"); err != nil {
		return err
	}
	b.options["speed"] = false
	if b.cfg.Has("percentage_state_topic") || b.cfg.Has("percentage_command_topic") {
		b.options["speed"] = true
		lo, hi := b.cfg.Number("speed_range_min", 1), b.cfg.Number("speed_range_max", 100)
		count := hi - lo + 1
		if err := b.addRead("percentage_state_topic", "percentage_value_template", "percentage",
			"(Number(v) <= 0 ? 0 : Math.round(((Number(v) - "+num(lo)+" + 1) / "+num(count)+") * 100))"); err != nil {
			return err
		}
		if err := b.addWrite("percentage_command_topic", "percentage_command_template", "percentage",
			"(value <= 0 ? 0 : Math.ceil((value * "+num(count)+") / 100) + "+num(lo)+" - 1)"); err != nil {
			return err
		}
	}
	if presets := b.cfg.Strings("preset_modes", nil); len(presets) > 0 {
		b.options["presets"] = true
		b.options["preset_list"] = choices(presets)
		if err := b.addRead("preset_mode_state_topic", "preset_mode_value_template", "preset", "(v === undefined ? undefined : __s(v))"); err != nil {
			return err
		}
		if err := b.addWrite("preset_mode_command_topic", "preset_mode_command_template", "preset", "value"); err != nil {
			return err
		}
	}
	if b.cfg.Has("oscillation_state_topic") || b.cfg.Has("oscillation_command_topic") {
		b.options["oscillate"] = true
		oon := b.cfg.String("payload_oscillation_on", "oscillate_on")
		ooff := b.cfg.String("payload_oscillation_off", "oscillate_off")
		if err := b.addRead("oscillation_state_topic", "oscillation_value_template", "oscillating",
			"(__s(v) === "+jsQuote(oon)+" ? true : __s(v) === "+jsQuote(ooff)+" ? false : undefined)"); err != nil {
			return err
		}
		if err := b.addWrite("oscillation_command_topic", "oscillation_command_template", "oscillating",
			"(value ? "+jsQuote(oon)+" : "+jsQuote(ooff)+")"); err != nil {
			return err
		}
	}
	if b.cfg.Has("direction_state_topic") || b.cfg.Has("direction_command_topic") {
		b.options["direction"] = true
		if err := b.addRead("direction_state_topic", "direction_value_template", "direction", "__s(v)"); err != nil {
			return err
		}
		if err := b.addWrite("direction_command_topic", "direction_command_template", "direction", "value"); err != nil {
			return err
		}
	}
	return nil
}

// climate maps an HVAC entity.
func (b *builder) climate() error {
	b.kind = controls.KindClimate
	modes := b.cfg.Strings("modes", []string{"auto", "off", "cool", "heat", "dry", "fan_only"})
	b.options["modes"] = choices(modes)
	b.options["min_temp"] = b.cfg.Number("min_temp", 7)
	b.options["max_temp"] = b.cfg.Number("max_temp", 35)
	b.options["step"] = b.cfg.Number("temp_step", 0.5)
	if unit := b.cfg.String("temperature_unit", ""); unit != "" {
		b.options["unit"] = "°" + strings.TrimPrefix(unit, "°")
	}
	text := "(v === undefined || v === null ? undefined : __s(v))"
	number := "(() => { const n = Number(v); return v === \"\" || v === null || isNaN(n) ? undefined : n; })()"
	steps := []struct {
		state, stateTpl, command, commandTpl, key, convert string
	}{
		{"mode_state_topic", "mode_state_template", "mode_command_topic", "mode_command_template", "mode", text},
		{"temperature_state_topic", "temperature_state_template", "temperature_command_topic", "temperature_command_template", "target", number},
		{"temperature_low_state_topic", "temperature_low_state_template", "temperature_low_command_topic", "temperature_low_command_template", "target_low", number},
		{"temperature_high_state_topic", "temperature_high_state_template", "temperature_high_command_topic", "temperature_high_command_template", "target_high", number},
		{"fan_mode_state_topic", "fan_mode_state_template", "fan_mode_command_topic", "fan_mode_command_template", "fan_mode", text},
	}
	for _, s := range steps {
		if err := b.addRead(s.state, s.stateTpl, s.key, s.convert); err != nil {
			return err
		}
		if err := b.addWrite(s.command, s.commandTpl, s.key, "value"); err != nil {
			return err
		}
	}
	if b.cfg.Has("temperature_low_command_topic") || b.cfg.Has("temperature_high_command_topic") {
		b.options["range"] = true
	}
	if b.cfg.Has("fan_mode_state_topic") || b.cfg.Has("fan_mode_command_topic") {
		b.options["fan"] = true
		b.options["fan_modes"] = choices(b.cfg.Strings("fan_modes", []string{"auto", "low", "medium", "high"}))
	}
	if err := b.addRead("current_temperature_topic", "current_temperature_template", "current", number); err != nil {
		return err
	}
	return b.addRead("action_topic", "action_template", "action", text)
}

// number maps a number entity.
func (b *builder) number() error {
	b.kind = controls.KindNumber
	b.options["min"] = b.cfg.Number("min", 1)
	b.options["max"] = b.cfg.Number("max", 100)
	b.options["step"] = b.cfg.Number("step", 1)
	if unit := b.cfg.String("unit_of_measurement", ""); unit != "" {
		b.options["unit"] = unit
	}
	if b.cfg.String("mode", "auto") == "box" {
		b.options["display"] = "box"
	}
	if err := b.addRead(b.stateTopicField("state_topic"), "value_template", "value",
		"(() => { const n = Number(v); return v === \"\" || v === null || isNaN(n) ? undefined : n; })()"); err != nil {
		return err
	}
	return b.addWrite("command_topic", "command_template", "value", "value")
}

// selectEntity maps a select entity.
func (b *builder) selectEntity() error {
	b.kind = controls.KindSelect
	options := b.cfg.Strings("options", nil)
	if len(options) == 0 {
		return fmt.Errorf("select has no options")
	}
	b.options["options"] = choices(options)
	if err := b.addRead(b.stateTopicField("state_topic"), "value_template", "value", "(v === undefined ? undefined : __s(v))"); err != nil {
		return err
	}
	return b.addWrite("command_topic", "command_template", "value", "value")
}

// button maps a button: one press, nothing to read.
func (b *builder) button() error {
	b.kind = controls.KindButtons
	press := b.cfg.String("payload_press", "PRESS")
	label := "Press"
	if name, ok := b.cfg.Fields["name"].(string); ok && strings.TrimSpace(name) != "" {
		label = strings.TrimSpace(name)
	}
	b.options["buttons"] = []any{map[string]any{"value": press, "label": label}}
	if b.cfg.String("command_topic", "") == "" {
		return fmt.Errorf("button has no command topic")
	}
	return b.addWrite("command_topic", "command_template", "value", "value")
}

// text maps a text entity.
func (b *builder) text() error {
	b.kind = controls.KindText
	b.options["max_length"] = b.cfg.Number("max", 255)
	if err := b.addRead(b.stateTopicField("state_topic"), "value_template", "value", "(v === undefined || v === null ? undefined : __s(v))"); err != nil {
		return err
	}
	return b.addWrite("command_topic", "command_template", "value", "value")
}

// finish writes the scripts and the definition.
func (b *builder) finish(connID uuid.UUID) (Mapped, error) {
	topics := []string{}
	seen := map[string]bool{}
	addTopic := func(t string) {
		if t != "" && !seen[t] {
			seen[t] = true
			topics = append(topics, t)
		}
	}
	for _, r := range b.reads {
		addTopic(r.topic)
	}
	addTopic(b.jsonTopic)
	attrTopic := b.cfg.String("json_attributes_topic", "")
	if attrTopic != "" && b.cfg.String("json_attributes_template", "") == "" {
		addTopic(attrTopic)
	} else {
		attrTopic = ""
	}
	activity := append([]string{}, topics...)
	if len(topics) == 0 {
		// A device with nothing to read, such as a button, listens on its
		// command topic so it has a topic to run on; its script ignores it.
		addTopic(b.cfg.String("command_topic", ""))
	}
	if len(topics) == 0 {
		return Mapped{}, fmt.Errorf("config has no state or command topic")
	}

	def := mqttrelay.Definition{
		Name:          b.cfg.Name(),
		ConnectionID:  connID,
		Topics:        topics,
		StateScript:   b.stateScript(attrTopic),
		Control:       b.kind,
		Options:       b.options,
		PayloadScript: b.payloadScript(),
		Description:   b.cfg.Description(),
		CommandRetain: b.cfg.Bool("retain", false),
	}
	if qos := int(b.cfg.Number("qos", 0)); qos >= 0 && qos <= 2 {
		def.StateQoS, def.CommandQoS = qos, qos
	}
	m := Mapped{Definition: def, Activity: activity}
	if topic, online, offline, ok := b.availability(); ok {
		m.Definition.AvailabilityTopic = topic
		m.Definition.AvailabilityOnline = online
		m.Definition.AvailabilityOffline = offline
		m.AvailabilityTopic, m.Online = topic, online
		m.Activity = append(m.Activity, topic)
	}
	return m, nil
}

// availability reads the first availability topic and its payloads. An
// entry whose template is anything but value_json.state is left out, since
// the device's availability matching reads raw payloads and a JSON state.
func (b *builder) availability() (topic, online, offline string, ok bool) {
	online = b.cfg.String("payload_available", "online")
	offline = b.cfg.String("payload_not_available", "offline")
	if t := b.cfg.String("availability_topic", ""); t != "" {
		if tplOK(b.cfg.String("availability_template", "")) {
			return t, online, offline, true
		}
		return "", "", "", false
	}
	list, _ := b.cfg.Fields["availability"].([]any)
	for _, entry := range list {
		m, isMap := entry.(map[string]any)
		if !isMap {
			continue
		}
		t, _ := m["topic"].(string)
		if t == "" {
			continue
		}
		tpl, _ := m["value_template"].(string)
		if !tplOK(tpl) {
			continue
		}
		on, off := online, offline
		if v, isStr := m["payload_available"].(string); isStr {
			on = v
		}
		if v, isStr := m["payload_not_available"].(string); isStr {
			off = v
		}
		return t, on, off, true
	}
	return "", "", "", false
}

// tplOK reports whether an availability template is one the device's
// availability matching already covers.
func tplOK(tpl string) bool {
	t := strings.ReplaceAll(strings.TrimSpace(tpl), " ", "")
	return t == "" || t == "{{value}}" || t == "{{value_json.state}}" || t == "{{value_json['state']}}" || t == `{{value_json["state"]}}`
}

// stateScript writes the state script: it parses the payload once and reads
// each key from the topics that carry it.
func (b *builder) stateScript(attrTopic string) string {
	var s strings.Builder
	s.WriteString("// Generated from the device's Home Assistant MQTT discovery config.\n")
	s.WriteString(jsHelpers)
	if b.colors {
		s.WriteString(colorHelpers)
	}
	s.WriteString("let value_json;\n")
	s.WriteString("try { value_json = JSON.parse(payload); } catch (e) { value_json = undefined; }\n")
	s.WriteString("const value = payload;\n")
	s.WriteString("const out = {};\n")
	for _, r := range b.reads {
		s.WriteString("if (topic === " + jsQuote(r.topic) + ") {\n")
		s.WriteString("  try { const v = " + r.tpl + "; const r = " + r.convert + "; if (r !== undefined && r !== null && !(typeof r === \"number\" && isNaN(r))) out." + r.key + " = r; } catch (e) {}\n")
		s.WriteString("}\n")
	}
	if b.jsonTopic != "" {
		s.WriteString("if (topic === " + jsQuote(b.jsonTopic) + " && value_json && typeof value_json === \"object\") {\n")
		s.WriteString("  const d = value_json;\n")
		for _, line := range b.jsonReads {
			s.WriteString("  " + strings.ReplaceAll(line, "\n", "\n  ") + "\n")
		}
		s.WriteString("}\n")
	}
	if attrTopic != "" {
		s.WriteString("if (topic === " + jsQuote(attrTopic) + " && value_json && typeof value_json === \"object\" && !Array.isArray(value_json)) {\n")
		s.WriteString("  for (const k of Object.keys(value_json)) if (!(k in out)) out[k] = value_json[k];\n")
		s.WriteString("}\n")
	}
	s.WriteString("return Object.keys(out).length ? out : null;\n")
	return s.String()
}

// payloadScript writes the payload script, or "" for a device that takes no
// commands.
func (b *builder) payloadScript() string {
	if len(b.writes) == 0 && len(b.jsonWrites) == 0 {
		return ""
	}
	var s strings.Builder
	s.WriteString("// Generated from the device's Home Assistant MQTT discovery config.\n")
	s.WriteString(jsHelpers)
	if b.colors {
		s.WriteString(colorHelpers)
	}
	s.WriteString("switch (field) {\n")
	for _, w := range b.writes {
		s.WriteString("  case " + jsQuote(w.key) + ": {\n")
		s.WriteString("    const __v = " + w.value + ";\n")
		s.WriteString("    if (__v === undefined) return {};\n")
		if w.tpl != "" {
			s.WriteString("    { const value = __v; const value_json = undefined; return { topic: " + jsQuote(w.topic) + ", payload: __s(" + w.tpl + ") }; }\n")
		} else {
			s.WriteString("    return { topic: " + jsQuote(w.topic) + ", payload: __s(__v) };\n")
		}
		s.WriteString("  }\n")
	}
	if len(b.jsonWrites) > 0 {
		topic := b.cfg.String("command_topic", "")
		keys := make([]string, 0, len(b.jsonWrites))
		for k := range b.jsonWrites {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			s.WriteString("  case " + jsQuote(k) + ": return { topic: " + jsQuote(topic) + ", payload: " + b.jsonWrites[k] + " };\n")
		}
	}
	s.WriteString("}\n")
	s.WriteString("return {};\n")
	return s.String()
}

// choices renders values as a kind's choices option.
func choices(values []string) []any {
	out := make([]any, 0, len(values))
	for _, v := range values {
		out = append(out, map[string]any{"value": v, "label": v})
	}
	return out
}

// num renders a number for a script.
func num(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}
