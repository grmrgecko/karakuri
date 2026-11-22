package openhab

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
)

// Proposal is the device an item becomes.
type Proposal struct {
	Kind     string
	Options  map[string]any
	Writable bool
	// Reason is why the item cannot be a device, empty when it can.
	Reason string
}

// Writable reports whether items of a type take commands from a device.
func Writable(itemType string) bool {
	switch baseOf(itemType) {
	case "Switch", "Dimmer", "Color", "Rollershutter", "Number", "String", "DateTime", "Player":
		return true
	}
	return false
}

// baseOf strips a dimension, and reads a group's base type from the
// Group:Switch form views use.
func baseOf(t string) string {
	t = strings.TrimPrefix(t, "Group:")
	if i := strings.IndexByte(t, ':'); i >= 0 {
		t = t[:i]
	}
	return t
}

// Propose maps an item to a device kind and its options. The state
// description's bounds and options become the kind's, so the kind's own
// checks hold a command to them: openHAB itself accepts a number past its
// maximum or a string outside its options.
func Propose(it Item) Proposal {
	p := Proposal{Options: map[string]any{}}
	desc := it.StateDescription
	readOnly := desc != nil && desc.ReadOnly
	base := it.BaseType()
	if it.Type == "Group" && it.GroupType == "" {
		p.Reason = "a group without a base type has no state of its own; trigger on its members instead"
		return p
	}
	switch base {
	case "Switch":
		p.Kind, p.Writable = controls.KindBoolean, true
	case "Contact":
		p.Kind = controls.KindBoolean
		p.Options["labels"] = map[string]any{"true": "Open", "false": "Closed"}
	case "Dimmer":
		p.Kind, p.Writable = controls.KindLight, true
		p.Options["brightness"], p.Options["color"], p.Options["color_temp"] = true, false, false
	case "Color":
		p.Kind, p.Writable = controls.KindLight, true
		p.Options["brightness"], p.Options["color"], p.Options["color_temp"] = true, true, false
	case "Rollershutter":
		p.Kind, p.Writable = controls.KindCover, true
		p.Options["position"], p.Options["tilt"], p.Options["stop"] = true, false, true
	case "Number":
		p.Kind, p.Writable = controls.KindNumber, true
		p.Options["min"], p.Options["max"] = -1e12, 1e12
		p.Options["display"] = "box"
		if desc != nil {
			if desc.Minimum != nil {
				p.Options["min"] = *desc.Minimum
			}
			if desc.Maximum != nil {
				p.Options["max"] = *desc.Maximum
			}
			if desc.Step != nil && *desc.Step > 0 {
				p.Options["step"] = *desc.Step
			}
			if desc.Minimum != nil && desc.Maximum != nil {
				p.Options["display"] = "slider"
			}
		}
		unit := strings.TrimSpace(it.UnitSymbol)
		if unit == "" {
			// openHAB before 4 reports the unit only inside the state.
			_, unit = splitQuantity(it.State)
		}
		if unit != "" {
			p.Options["unit"] = " " + unit
		}
	case "String":
		if desc != nil && len(desc.Options) > 0 {
			p.Kind, p.Writable = controls.KindSelect, true
			choices := make([]any, 0, len(desc.Options))
			for _, o := range desc.Options {
				label := o.Label
				if label == "" {
					label = o.Value
				}
				choices = append(choices, map[string]any{"value": o.Value, "label": label})
			}
			p.Options["options"] = choices
			break
		}
		p.Kind, p.Writable = controls.KindText, true
		p.Options["max_length"] = float64(0)
	case "DateTime":
		p.Kind, p.Writable = controls.KindDateTime, true
		p.Options["mode"] = "datetime"
	case "Player":
		p.Kind, p.Writable = controls.KindMedia, true
		for k, v := range map[string]bool{"volume": false, "mute": false, "skip": true, "stop": false,
			"seek": false, "source": false, "power": false} {
			p.Options[k] = v
		}
	case "Location":
		p.Kind = controls.KindLocation
	default:
		p.Reason = it.Type + " items are not supported"
		return p
	}
	if readOnly {
		p.Writable = false
	}
	return p
}

// Values is an item's state as its device kind holds it, or nil while the
// item has no value (NULL or UNDEF).
func Values(it Item) map[string]any {
	if !HasValue(it.State) {
		return nil
	}
	s := it.State
	switch it.BaseType() {
	case "Switch":
		return map[string]any{"on": s == "ON"}
	case "Contact":
		return map[string]any{"on": s == "OPEN"}
	case "Dimmer":
		n, ok := percent(s)
		if !ok {
			return nil
		}
		return map[string]any{"on": n > 0, "brightness": n}
	case "Color":
		h, sat, b, ok := hsb(s)
		if !ok {
			return nil
		}
		return map[string]any{"on": b > 0, "brightness": math.Round(b), "color": hsvToHex(h, sat, 100)}
	case "Rollershutter":
		n, ok := percent(s)
		if !ok {
			return nil
		}
		// openHAB counts 0 as open; karakuri's cover counts 100 as open.
		position := 100 - n
		state := "closed"
		if position > 0 {
			state = "open"
		}
		return map[string]any{"state": state, "position": position}
	case "Number":
		num, _ := splitQuantity(s)
		n, err := strconv.ParseFloat(num, 64)
		if err != nil {
			return nil
		}
		return map[string]any{"value": n}
	case "String":
		return map[string]any{"value": s}
	case "DateTime":
		for _, layout := range []string{"2006-01-02T15:04:05.000-0700", "2006-01-02T15:04:05-0700", time.RFC3339Nano} {
			if t, err := time.Parse(layout, s); err == nil {
				return map[string]any{"value": t.UTC().Format(time.RFC3339)}
			}
		}
		return nil
	case "Player":
		state := "idle"
		switch s {
		case "PLAY":
			state = "playing"
		case "PAUSE":
			state = "paused"
		}
		return map[string]any{"state": state}
	case "Location":
		parts := strings.Split(s, ",")
		if len(parts) < 2 {
			return nil
		}
		lat, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		lon, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err1 != nil || err2 != nil {
			return nil
		}
		return map[string]any{"latitude": lat, "longitude": lon}
	}
	return nil
}

// Command builds the openHAB command for one device write. The value has
// already been coerced by the kind, so a brightness is a percentage within
// bounds and a colour a hex string.
func Command(it Item, attribute string, value any) (string, error) {
	if it.StateDescription != nil && it.StateDescription.ReadOnly {
		return "", fmt.Errorf("openHAB marks %s read only", it.Name)
	}
	onOff := func(v any) string {
		if b, ok := v.(bool); ok && b {
			return "ON"
		}
		return "OFF"
	}
	switch it.BaseType() {
	case "Switch":
		return onOff(value), nil
	case "Dimmer":
		if attribute == "brightness" {
			return formatNumber(value), nil
		}
		return onOff(value), nil
	case "Color":
		switch attribute {
		case "brightness":
			return formatNumber(value), nil
		case "color":
			h, s, ok := hexToHS(fmt.Sprint(value))
			if !ok {
				return "", fmt.Errorf("colour %v is not a hex colour", value)
			}
			b := 100.0
			if _, _, cur, ok := hsb(it.State); ok && cur > 0 {
				b = cur
			}
			return fmt.Sprintf("%s,%s,%s", trim(h), trim(s), trim(b)), nil
		}
		return onOff(value), nil
	case "Rollershutter":
		if attribute == "position" {
			n, ok := toFloat(value)
			if !ok {
				return "", fmt.Errorf("position %v is not a number", value)
			}
			return trim(100 - n), nil
		}
		switch fmt.Sprint(value) {
		case "open":
			return "UP", nil
		case "closed":
			return "DOWN", nil
		case "stopped":
			return "STOP", nil
		}
		return "", fmt.Errorf("a blind can be told open, closed, or stopped, not %v", value)
	case "Number":
		return formatNumber(value), nil
	case "String", "DateTime":
		return fmt.Sprint(value), nil
	case "Player":
		if attribute == "command" {
			switch value {
			case "next":
				return "NEXT", nil
			case "previous":
				return "PREVIOUS", nil
			}
		}
		switch value {
		case "playing":
			return "PLAY", nil
		case "paused":
			return "PAUSE", nil
		}
		return "", fmt.Errorf("an openHAB player can be told playing, paused, next, or previous, not %v", value)
	}
	return "", fmt.Errorf("%s items take no commands", it.Type)
}

// splitQuantity splits a dimensioned number's state, "21.5 °C", into its
// number and unit.
func splitQuantity(s string) (string, string) {
	num, unit, ok := strings.Cut(strings.TrimSpace(s), " ")
	if !ok {
		return num, ""
	}
	return num, strings.TrimSpace(unit)
}

func percent(s string) (float64, bool) {
	n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return 0, false
	}
	return math.Round(n), true
}

func hsb(s string) (float64, float64, float64, bool) {
	parts := strings.Split(s, ",")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	var out [3]float64
	for i, p := range parts {
		n, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return 0, 0, 0, false
		}
		out[i] = n
	}
	return out[0], out[1], out[2], true
}

// hsvToHex renders hue (0-360), saturation, and value (0-100) as #rrggbb.
func hsvToHex(h, s, v float64) string {
	s, v = s/100, v/100
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var r, g, b float64
	switch {
	case h < 60:
		r, g, b = c, x, 0
	case h < 120:
		r, g, b = x, c, 0
	case h < 180:
		r, g, b = 0, c, x
	case h < 240:
		r, g, b = 0, x, c
	case h < 300:
		r, g, b = x, 0, c
	default:
		r, g, b = c, 0, x
	}
	byteOf := func(f float64) int { return int(math.Round((f + m) * 255)) }
	return fmt.Sprintf("#%02x%02x%02x", byteOf(r), byteOf(g), byteOf(b))
}

// hexToHS reads a hex colour's hue and saturation (0-100). The hue is at
// least 0 and below 360: openHAB's HSBType refuses 360, so a hue that rounds
// up to it wraps to 0, the same red.
func hexToHS(hex string) (float64, float64, bool) {
	hex = strings.TrimPrefix(strings.TrimSpace(hex), "#")
	if len(hex) != 6 {
		return 0, 0, false
	}
	n, err := strconv.ParseUint(hex, 16, 32)
	if err != nil {
		return 0, 0, false
	}
	r, g, b := float64(n>>16&0xff)/255, float64(n>>8&0xff)/255, float64(n&0xff)/255
	mx, mn := math.Max(r, math.Max(g, b)), math.Min(r, math.Min(g, b))
	d := mx - mn
	var h float64
	switch {
	case d == 0:
		h = 0
	case mx == r:
		h = 60 * math.Mod((g-b)/d, 6)
	case mx == g:
		h = 60 * ((b-r)/d + 2)
	default:
		h = 60 * ((r-g)/d + 4)
	}
	if h < 0 {
		h += 360
	}
	s := 0.0
	if mx > 0 {
		s = d / mx
	}
	return math.Mod(math.Round(h), 360), math.Round(s * 100), true
}

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int:
		return float64(t), true
	case string:
		n, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
		return n, err == nil
	}
	return 0, false
}

func formatNumber(v any) string {
	if n, ok := toFloat(v); ok {
		return trim(n)
	}
	return fmt.Sprint(v)
}

// trim renders a number without a trailing .0.
func trim(n float64) string {
	return strconv.FormatFloat(n, 'f', -1, 64)
}
