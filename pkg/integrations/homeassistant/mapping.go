package homeassistant

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/controls"
)

// This file is the server's copy of the mapping the dashboard widgets apply in
// plugins/core/widgets/shared/entitySource.ts: which control kind a domain is
// drawn as, that kind's options for one entity, its value, and the service a
// write calls. Devices use it so a device and a card row bound to the same
// entity agree; testdata/mapping.json holds both copies to the same answers.

// domainKinds maps a domain onto the kind it is drawn with. A domain that is
// not listed is a reading.
var domainKinds = map[string]string{
	"light":          controls.KindLight,
	"switch":         controls.KindBoolean,
	"siren":          controls.KindBoolean,
	"humidifier":     controls.KindBoolean,
	"input_boolean":  controls.KindBoolean,
	"fan":            controls.KindFan,
	"cover":          controls.KindCover,
	"climate":        controls.KindClimate,
	"water_heater":   controls.KindClimate,
	"lock":           controls.KindLock,
	"scene":          controls.KindButtons,
	"script":         controls.KindButtons,
	"automation":     controls.KindButtons,
	"button":         controls.KindButtons,
	"input_button":   controls.KindButtons,
	"number":         controls.KindNumber,
	"input_number":   controls.KindNumber,
	"select":         controls.KindSelect,
	"input_select":   controls.KindSelect,
	"text":           controls.KindText,
	"input_text":     controls.KindText,
	"person":         controls.KindLocation,
	"device_tracker": controls.KindLocation,
	"media_player":   controls.KindMedia,
}

// readOnlyDomains are drawn with a kind but take no commands: where someone
// is comes from their phone, not from a dashboard.
var readOnlyDomains = map[string]bool{"person": true, "device_tracker": true}

// Mapping is what one entity is as a control kind.
type Mapping struct {
	Kind     string         `json:"kind"`
	Options  map[string]any `json:"options"`
	Writable bool           `json:"writable"`
}

// Propose maps an entity to its kind, the kind's options for this entity, and
// whether it takes commands.
func Propose(e Entity) Mapping {
	domain := domainOf(e.EntityID)
	kind, known := domainKinds[domain]
	if !known {
		kind = controls.KindText
	}
	return Mapping{Kind: kind, Options: kindOptions(kind, e.Attributes), Writable: known && !readOnlyDomains[domain]}
}

// Writable reports whether entities of a domain take commands.
func Writable(domain string) bool {
	_, known := domainKinds[domain]
	return known && !readOnlyDomains[domain]
}

// attr reads an attribute, treating null as absent as the widgets' ?? does.
func attr(a map[string]any, key string) (any, bool) {
	v, ok := a[key]
	return v, ok && v != nil
}

// num reads a number the way JavaScript's Number() does for the values Home
// Assistant sends: numbers as they are, numeric strings parsed, anything else
// not a number.
func num(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return n, err == nil
	}
	return 0, false
}

// numOr reads a numeric attribute with a fallback for one that is absent.
func numOr(a map[string]any, key string, fallback float64) any {
	v, ok := attr(a, key)
	if !ok {
		return fallback
	}
	if n, ok := num(v); ok {
		return n
	}
	return nil
}

// numIf reads a numeric attribute, absent when Home Assistant sends none.
func numIf(a map[string]any, key string) (any, bool) {
	v, ok := a[key]
	if !ok {
		return nil, false
	}
	if n, ok := num(v); ok {
		return n, true
	}
	return nil, true
}

// stringOr renders an attribute as text with a fallback, as String(x ?? d).
func stringOr(a map[string]any, key, fallback string) string {
	v, ok := attr(a, key)
	if !ok {
		return fallback
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}

// asChoices renders a Home Assistant string list as the catalog's choice shape.
func asChoices(v any) []any {
	list, ok := v.([]any)
	if !ok {
		return []any{}
	}
	out := make([]any, 0, len(list))
	for _, item := range list {
		s := fmt.Sprint(item)
		out = append(out, map[string]any{"value": s, "label": s})
	}
	return out
}

func nonEmptyList(v any) bool {
	list, ok := v.([]any)
	return ok && len(list) > 0
}

func stringsOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// Home Assistant's media_player feature bits.
const (
	mediaSeek          = 2
	mediaVolumeSet     = 4
	mediaVolumeMute    = 8
	mediaPreviousTrack = 16
	mediaNextTrack     = 32
	mediaTurnOn        = 128
	mediaTurnOff       = 256
	mediaSelectSource  = 2048
	mediaStop          = 4096
)

// kindOptions turns an entity's attributes into the kind's settings.
func kindOptions(kind string, a map[string]any) map[string]any {
	switch kind {
	case controls.KindLight:
		modes := stringsOf(a["supported_color_modes"])
		has := func(want ...string) bool {
			for _, m := range modes {
				for _, w := range want {
					if m == w {
						return true
					}
				}
			}
			return false
		}
		brightness := false
		for _, m := range modes {
			if m != "onoff" {
				brightness = true
			}
		}
		return map[string]any{
			"brightness":  brightness,
			"color":       has("hs", "rgb", "rgbw", "rgbww", "xy"),
			"color_temp":  has("color_temp"),
			"min_kelvin":  numOr(a, "min_color_temp_kelvin", 2000),
			"max_kelvin":  numOr(a, "max_color_temp_kelvin", 6500),
			"effects":     nonEmptyList(a["effect_list"]),
			"effect_list": asChoices(a["effect_list"]),
		}
	case controls.KindFan:
		_, pct := a["percentage"]
		_, step := a["percentage_step"]
		_, osc := a["oscillating"]
		_, dir := a["direction"]
		return map[string]any{
			"speed":       pct || step,
			"speed_step":  numOr(a, "percentage_step", 1),
			"presets":     nonEmptyList(a["preset_modes"]),
			"preset_list": asChoices(a["preset_modes"]),
			"oscillate":   osc,
			"direction":   dir,
		}
	case controls.KindCover:
		_, pos := a["current_position"]
		_, tilt := a["current_tilt_position"]
		return map[string]any{"position": pos, "tilt": tilt, "stop": true}
	case controls.KindClimate:
		modes := a["hvac_modes"]
		if modes == nil {
			modes = a["operation_list"]
		}
		_, low := a["target_temp_low"]
		_, high := a["target_temp_high"]
		return map[string]any{
			"modes":     asChoices(modes),
			"min_temp":  numOr(a, "min_temp", 7),
			"max_temp":  numOr(a, "max_temp", 35),
			"step":      numOr(a, "target_temp_step", 0.5),
			"unit":      stringOr(a, "unit_of_measurement", "°"),
			"range":     low || high,
			"fan":       nonEmptyList(a["fan_modes"]),
			"fan_modes": asChoices(a["fan_modes"]),
		}
	case controls.KindNumber:
		display := "slider"
		if a["mode"] == "box" {
			display = "box"
		}
		return map[string]any{
			"min":     numOr(a, "min", 0),
			"max":     numOr(a, "max", 100),
			"step":    numOr(a, "step", 1),
			"unit":    stringOr(a, "unit_of_measurement", ""),
			"display": display,
		}
	case controls.KindSelect:
		return map[string]any{"options": asChoices(a["options"]), "display": "dropdown"}
	case controls.KindLocation:
		return map[string]any{"zones": []any{
			map[string]any{"value": "home", "label": "Home"},
			map[string]any{"value": "not_home", "label": "Away"},
		}}
	case controls.KindMedia:
		features := 0
		if n, ok := num(a["supported_features"]); ok {
			features = int(n)
		}
		has := func(bit int) bool { return features&bit != 0 }
		return map[string]any{
			"volume": has(mediaVolumeSet),
			"mute":   has(mediaVolumeMute),
			"skip":   has(mediaNextTrack) || has(mediaPreviousTrack),
			"seek":   has(mediaSeek),
			"source": has(mediaSelectSource),
			"power":  has(mediaTurnOn) || has(mediaTurnOff),
			"stop":   has(mediaStop),
		}
	default:
		out := map[string]any{"unit": stringOr(a, "unit_of_measurement", "")}
		if v, ok := attr(a, "suggested_display_precision"); ok {
			out["precision"] = v
		}
		return out
	}
}

// Value shapes an entity's state and attributes into what its kind holds:
// an object for a composite kind, a single value otherwise.
func Value(e Entity) any {
	kind := Propose(e).Kind
	a := e.Attributes
	state := e.State
	put := func(m map[string]any, key string, v any, ok bool) {
		if ok && v != nil {
			m[key] = v
		}
	}
	switch kind {
	case controls.KindLocation:
		out := map[string]any{"state": state}
		for key, from := range map[string]string{"latitude": "latitude", "longitude": "longitude", "accuracy": "gps_accuracy"} {
			if n, ok := a[from].(float64); ok {
				out[key] = n
			}
		}
		if v, ok := attr(a, "source"); ok {
			out["source"] = v
		} else if v, ok := attr(a, "source_type"); ok {
			out["source"] = v
		}
		put(out, "since", e.LastChanged, e.LastChanged != "")
		return out
	case controls.KindLight:
		out := map[string]any{"on": state == "on"}
		if v, ok := a["brightness"]; ok {
			if n, ok := num(v); ok {
				out["brightness"] = math.Round(n / 255 * 100)
			}
		}
		if hex := rgbToHex(a["rgb_color"]); hex != "" {
			out["color"] = hex
		}
		v, ok := numIf(a, "color_temp_kelvin")
		put(out, "color_temp", v, ok)
		put(out, "effect", a["effect"], true)
		return out
	case controls.KindFan:
		out := map[string]any{"on": state == "on"}
		v, ok := numIf(a, "percentage")
		put(out, "percentage", v, ok)
		put(out, "preset", a["preset_mode"], true)
		put(out, "oscillating", a["oscillating"], true)
		put(out, "direction", a["direction"], true)
		return out
	case controls.KindCover:
		out := map[string]any{"state": state}
		v, ok := numIf(a, "current_position")
		put(out, "position", v, ok)
		v, ok = numIf(a, "current_tilt_position")
		put(out, "tilt", v, ok)
		return out
	case controls.KindClimate:
		out := map[string]any{"mode": state}
		for key, from := range map[string]string{"target": "temperature", "target_low": "target_temp_low",
			"target_high": "target_temp_high", "current": "current_temperature"} {
			v, ok := numIf(a, from)
			put(out, key, v, ok)
		}
		put(out, "fan_mode", a["fan_mode"], true)
		put(out, "action", a["hvac_action"], true)
		return out
	case controls.KindMedia:
		s := state
		if s == "standby" {
			s = "off"
		}
		out := map[string]any{"state": s}
		if v, ok := a["volume_level"]; ok {
			if n, ok := num(v); ok {
				out["volume"] = math.Round(n * 100)
			}
		}
		for key, from := range map[string]string{"muted": "is_volume_muted", "title": "media_title",
			"artist": "media_artist", "album": "media_album_name", "art": "entity_picture",
			"position_at": "media_position_updated_at", "source": "source"} {
			put(out, key, a[from], true)
		}
		v, ok := numIf(a, "media_position")
		put(out, "position", v, ok)
		v, ok = numIf(a, "media_duration")
		put(out, "duration", v, ok)
		if list, ok := a["source_list"].([]any); ok {
			sources := make([]any, 0, len(list))
			for _, item := range list {
				sources = append(sources, fmt.Sprint(item))
			}
			out["sources"] = sources
		}
		return out
	case controls.KindBoolean:
		return state == "on"
	case controls.KindNumber:
		if n, ok := num(state); ok {
			return n
		}
		return nil
	default:
		return state
	}
}

// Values is an entity's value as a device's state: a composite kind's
// attributes, or a single value under the kind's primary attribute.
func Values(e Entity) map[string]any {
	v := Value(e)
	if m, ok := v.(map[string]any); ok {
		return m
	}
	k, ok := controls.FindKind(Propose(e).Kind)
	if !ok {
		return map[string]any{"value": v}
	}
	return map[string]any{k.PrimaryAttribute().ID: v}
}

// rgbToHex renders Home Assistant's rgb_color triple as hex.
func rgbToHex(v any) string {
	list, ok := v.([]any)
	if !ok || len(list) < 3 {
		return ""
	}
	var b strings.Builder
	b.WriteByte('#')
	for _, item := range list[:3] {
		n, _ := num(item)
		n = math.Max(0, math.Min(255, math.Round(n)))
		fmt.Fprintf(&b, "%02x", int(n))
	}
	return b.String()
}

// hexToRgb is the reverse, for the service call that takes a triple.
func hexToRgb(hex string) []any {
	clean := strings.TrimPrefix(hex, "#")
	if len(clean) == 3 {
		clean = string([]byte{clean[0], clean[0], clean[1], clean[1], clean[2], clean[2]})
	}
	if len(clean) != 6 {
		return []any{255.0, 255.0, 255.0}
	}
	out := make([]any, 0, 3)
	for i := 0; i < 6; i += 2 {
		n, _ := strconv.ParseInt(clean[i:i+2], 16, 64)
		out = append(out, float64(n))
	}
	return out
}

// Call is one service call a write maps to.
type Call struct {
	Domain  string         `json:"domain"`
	Service string         `json:"service"`
	Data    map[string]any `json:"data,omitempty"`
}

// truthy reads a write's value as on or off.
func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case float64:
		return t != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "on", "1", "yes", "open", "unlocked":
			return true
		}
	}
	return false
}

func number(v any) float64 {
	n, _ := num(v)
	return n
}

// Command resolves the service call one write to an entity makes. ok is false
// for a write the entity cannot take.
func Command(e Entity, attribute string, value any) (Call, bool) {
	domain := domainOf(e.EntityID)
	if !Writable(domain) {
		return Call{}, false
	}
	kind := Propose(e).Kind
	if attribute == "" {
		if k, ok := controls.FindKind(kind); ok {
			attribute = k.PrimaryAttribute().ID
		}
	}
	str := fmt.Sprint(value)
	switch kind {
	case controls.KindBoolean:
		if truthy(value) {
			return Call{domain, "turn_on", nil}, true
		}
		return Call{domain, "turn_off", nil}, true
	case controls.KindLock:
		// The states in between and jammed are readings, not actions; calling
		// unlock for them would open a door nobody asked to open.
		switch value {
		case "locked":
			return Call{"lock", "lock", nil}, true
		case "unlocked":
			return Call{"lock", "unlock", nil}, true
		}
	case controls.KindButtons:
		switch domain {
		case "automation":
			return Call{domain, "trigger", nil}, true
		case "button", "input_button":
			return Call{domain, "press", nil}, true
		}
		return Call{domain, "turn_on", nil}, true
	case controls.KindNumber:
		return Call{domain, "set_value", map[string]any{"value": number(value)}}, true
	case controls.KindSelect:
		return Call{domain, "select_option", map[string]any{"option": str}}, true
	case controls.KindText:
		return Call{domain, "set_value", map[string]any{"value": str}}, true
	case controls.KindLight:
		switch attribute {
		case "on":
			if truthy(value) {
				return Call{"light", "turn_on", nil}, true
			}
			return Call{"light", "turn_off", nil}, true
		case "brightness":
			return Call{"light", "turn_on", map[string]any{"brightness_pct": number(value)}}, true
		case "color":
			return Call{"light", "turn_on", map[string]any{"rgb_color": hexToRgb(str)}}, true
		case "color_temp":
			return Call{"light", "turn_on", map[string]any{"color_temp_kelvin": number(value)}}, true
		case "effect":
			return Call{"light", "turn_on", map[string]any{"effect": str}}, true
		}
	case controls.KindFan:
		switch attribute {
		case "on":
			if truthy(value) {
				return Call{"fan", "turn_on", nil}, true
			}
			return Call{"fan", "turn_off", nil}, true
		case "percentage":
			return Call{"fan", "set_percentage", map[string]any{"percentage": number(value)}}, true
		case "preset":
			return Call{"fan", "set_preset_mode", map[string]any{"preset_mode": str}}, true
		case "oscillating":
			return Call{"fan", "oscillate", map[string]any{"oscillating": truthy(value)}}, true
		case "direction":
			return Call{"fan", "set_direction", map[string]any{"direction": str}}, true
		}
	case controls.KindCover:
		switch {
		case attribute == "position":
			return Call{"cover", "set_cover_position", map[string]any{"position": number(value)}}, true
		case attribute == "tilt":
			return Call{"cover", "set_cover_tilt_position", map[string]any{"tilt_position": number(value)}}, true
		case value == "open":
			return Call{"cover", "open_cover", nil}, true
		case value == "closed":
			return Call{"cover", "close_cover", nil}, true
		case value == "stopped":
			return Call{"cover", "stop_cover", nil}, true
		}
	case controls.KindClimate:
		switch attribute {
		case "mode":
			if domain == "water_heater" {
				return Call{domain, "set_operation_mode", map[string]any{"operation_mode": str}}, true
			}
			return Call{domain, "set_hvac_mode", map[string]any{"hvac_mode": str}}, true
		case "fan_mode":
			return Call{domain, "set_fan_mode", map[string]any{"fan_mode": str}}, true
		case "target_low", "target_high":
			current, _ := Value(e).(map[string]any)
			low, high := current["target_low"], current["target_high"]
			if attribute == "target_low" {
				low = value
			} else {
				high = value
			}
			return Call{domain, "set_temperature", map[string]any{
				"target_temp_low": number(low), "target_temp_high": number(high)}}, true
		}
		return Call{domain, "set_temperature", map[string]any{"temperature": number(value)}}, true
	case controls.KindMedia:
		return mediaCall(attribute, value)
	}
	return Call{}, false
}

// mediaStateServices are the media_player services that put a player in each
// state the kind can be told.
var mediaStateServices = map[string]string{
	"playing": "media_play",
	"paused":  "media_pause",
	"idle":    "media_stop",
	"on":      "turn_on",
	"off":     "turn_off",
}

// mediaCall resolves one media write into its media_player service.
func mediaCall(attribute string, value any) (Call, bool) {
	const domain = "media_player"
	switch attribute {
	case "state":
		if service, ok := mediaStateServices[fmt.Sprint(value)]; ok {
			return Call{domain, service, nil}, true
		}
	case "volume":
		return Call{domain, "volume_set", map[string]any{"volume_level": number(value) / 100}}, true
	case "muted":
		return Call{domain, "volume_mute", map[string]any{"is_volume_muted": truthy(value)}}, true
	case "source":
		return Call{domain, "select_source", map[string]any{"source": fmt.Sprint(value)}}, true
	case "position":
		return Call{domain, "media_seek", map[string]any{"seek_position": number(value)}}, true
	case "command":
		switch value {
		case "next":
			return Call{domain, "media_next_track", nil}, true
		case "previous":
			return Call{domain, "media_previous_track", nil}, true
		}
	}
	return Call{}, false
}
