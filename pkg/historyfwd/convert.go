package historyfwd

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
)

// Default metric names from spec 027 FR-016.
const (
	metricControl = "karakuri_control"
	metricDevice  = "karakuri_mqtt_device"
	metricHA      = "karakuri_ha_entity"
)

// Value kinds a sample carries.
const (
	valNumber = iota
	valText
	valEnum
)

// sample is one recordable attribute value before it is shaped for a
// destination.
type sample struct {
	labels  map[string]string
	at      time.Time
	kind    int
	num     float64
	text    string
	options []string
	// series identifies the source attribute, for tracking the previous
	// value of an enumeration whose options are unknown.
	series string
}

// enumAttributes are the string attributes of control kinds whose values are
// a small fixed set, recorded as state-sets for numeric-only destinations.
// Others, such as a light's hex colour, have no numeric form.
var enumAttributes = map[string]bool{
	controls.KindSelect + ".value":   true,
	controls.KindLock + ".state":     true,
	controls.KindCover + ".state":    true,
	controls.KindClimate + ".mode":   true,
	"climate.fan_mode":               true,
	"climate.action":                 true,
	controls.KindLight + ".effect":   true,
	controls.KindFan + ".preset":     true,
	controls.KindFan + ".direction":  true,
	controls.KindLocation + ".state": true,
	controls.KindMedia + ".state":    true,
}

// controlSamples turns a control change into one sample per attribute.
func controlSamples(c controls.Change) []sample {
	snap := c.Control
	base := map[string]string{"id": snap.ID.String(), "name": snap.Name}
	kind, ok := controls.FindKind(snap.Kind)
	at := c.At
	if at.IsZero() {
		at = time.Now()
	}
	if !ok || kind.PressOnly {
		return nil
	}
	if attr, found := kind.Attribute(c.Attribute); found && attr.WriteOnly {
		// An action such as a skip moved nothing the history records.
		return nil
	}
	values := map[string]any{}
	if kind.Composite() {
		obj, _ := snap.Value.(map[string]any)
		for k, v := range obj {
			values[k] = v
		}
	} else {
		values[kind.PrimaryAttribute().ID] = snap.Value
	}
	var out []sample
	for _, attr := range kind.Attributes {
		v, ok := values[attr.ID]
		if !ok || v == nil {
			continue
		}
		labels := withLabel(base, "attribute", attr.ID)
		s := sample{labels: labels, at: at, series: "control:" + snap.ID.String() + ":" + attr.ID}
		key := snap.Kind + "." + attr.ID
		switch {
		case snap.Kind == controls.KindLocation && attr.ID == "since":
			// since repeats the time of the state sample it belongs to.
			continue
		case attr.WriteOnly:
			continue
		case snap.Kind == controls.KindDateTime:
			n, ok := dateTimeNumber(fmt.Sprint(v))
			if !ok {
				continue
			}
			s.kind, s.num = valNumber, n
		case enumAttributes[key]:
			s.kind, s.text = valEnum, fmt.Sprint(v)
			s.options = controlOptions(snap, attr.ID)
		default:
			if !fill(&s, v, attr.Type) {
				continue
			}
		}
		out = append(out, s)
	}
	return out
}

// controlOptions returns the known values of a control's enumeration.
func controlOptions(snap controls.Snapshot, attribute string) []string {
	key := ""
	switch {
	case snap.Kind == controls.KindSelect:
		key = "options"
	case snap.Kind == controls.KindLocation && attribute == "state":
		key = "zones"
	case snap.Kind == controls.KindClimate && attribute == "mode":
		key = "modes"
	case snap.Kind == controls.KindClimate && attribute == "fan_mode":
		key = "fan_modes"
	case snap.Kind == controls.KindLight && attribute == "effect":
		key = "effect_list"
	case snap.Kind == controls.KindFan && attribute == "preset":
		key = "preset_list"
	case snap.Kind == controls.KindFan && attribute == "direction":
		return []string{"forward", "reverse"}
	case snap.Kind == controls.KindLock:
		return []string{"locked", "unlocked", "locking", "unlocking", "jammed", "open"}
	case snap.Kind == controls.KindCover:
		return []string{"open", "closed", "opening", "closing", "stopped"}
	case snap.Kind == controls.KindMedia && attribute == "state":
		return []string{"off", "on", "idle", "playing", "paused", "buffering"}
	}
	return choiceList(snap.Options[key])
}

// choiceList reads a "choices" option: strings, or objects with a value.
func choiceList(raw any) []string {
	list, _ := raw.([]any)
	var out []string
	for _, item := range list {
		switch v := item.(type) {
		case string:
			out = append(out, v)
		case map[string]any:
			if s, ok := v["value"].(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

// availabilityValue records a device's availability as a number: online 1 and
// offline 0. Unknown records nothing, since it says nothing about the device.
func availabilityValue(availability string) (float64, bool) {
	switch availability {
	case mqttrelay.AvailabilityOnline:
		return 1, true
	case mqttrelay.AvailabilityOffline:
		return 0, true
	}
	return 0, false
}

// deviceSamples turns the values one MQTT message carried into samples.
func deviceSamples(id, name, kindName string, values map[string]any, at time.Time) []sample {
	base := map[string]string{"id": id, "name": name}
	kind, hasKind := controls.FindKind(kindName)
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var out []sample
	for _, k := range keys {
		v := values[k]
		if v == nil {
			continue
		}
		s := sample{labels: withLabel(base, "attribute", k), at: at, series: "device:" + id + ":" + k}
		typ := ""
		if hasKind {
			if attr, ok := kind.Attribute(k); ok {
				typ = attr.Type
			}
			if enumAttributes[kindName+"."+k] {
				s.kind, s.text = valEnum, fmt.Sprint(v)
				out = append(out, s)
				continue
			}
		}
		if !fill(&s, v, typ) {
			continue
		}
		out = append(out, s)
	}
	return out
}

// haSamples turns a Home Assistant state into samples: the state, then the
// listed attributes or every numeric one.
func haSamples(connName, entityID, state string, attrs map[string]any, at time.Time, listed []string, all bool) []sample {
	if state == "unavailable" || state == "unknown" || state == "" {
		return nil
	}
	base := map[string]string{"entity_id": entityID, "connection": connName}
	if domain, _, ok := strings.Cut(entityID, "."); ok {
		base["domain"] = domain
	}
	if name, ok := attrs["friendly_name"].(string); ok {
		base["friendly_name"] = name
	}
	stateLabels := withLabel(base, "attribute", "state")
	if unit, ok := attrs["unit_of_measurement"].(string); ok && unit != "" {
		stateLabels["unit"] = unit
	}
	s := sample{labels: stateLabels, at: at, series: "ha:" + entityID + ":state"}
	if v, ok := homeassistant.NumericState(state); ok {
		s.kind, s.num = valNumber, v
	} else if opts := choiceList(attrs["options"]); len(opts) > 0 {
		s.kind, s.text, s.options = valEnum, state, opts
	} else {
		s.kind, s.text = valText, state
	}
	out := []sample{s}

	var names []string
	if all {
		for k := range attrs {
			names = append(names, k)
		}
	} else {
		names = append(names, listed...)
	}
	sort.Strings(names)
	for _, name := range names {
		v, ok := attrs[name]
		if !ok || v == nil {
			continue
		}
		a := sample{labels: withLabel(base, "attribute", name), at: at, series: "ha:" + entityID + ":" + name}
		if all {
			// All attributes means the numeric ones; text attributes such as
			// icons and friendly names are not history.
			n, ok := number(v)
			if !ok {
				continue
			}
			a.kind, a.num = valNumber, n
		} else if !fill(&a, v, "") {
			continue
		}
		out = append(out, a)
	}
	return out
}

// fill sets a sample from a value of an optional declared type, reporting
// whether the value is recordable.
func fill(s *sample, v any, typ string) bool {
	if typ == flow.PortString {
		s.kind, s.text = valText, fmt.Sprint(v)
		return true
	}
	if n, ok := number(v); ok {
		s.kind, s.num = valNumber, n
		return true
	}
	switch x := v.(type) {
	case string:
		s.kind, s.text = valText, x
		return true
	case map[string]any, []any:
		return false
	}
	return false
}

// number reads numbers and booleans as float64.
func number(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, !math.IsNaN(x) && !math.IsInf(x, 0)
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case bool:
		if x {
			return 1, true
		}
		return 0, true
	case string:
		if n, err := strconv.ParseFloat(x, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
	}
	return 0, false
}

// dateTimeNumber reads a date and time control's value as a number: Unix
// seconds for a timestamp or a date, and seconds since midnight for a time
// of day, which has no date to count from.
func dateTimeNumber(s string) (float64, bool) {
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.Parse(layout, s); err == nil {
			return float64(t.Hour()*3600 + t.Minute()*60 + t.Second()), true
		}
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return float64(t.Unix()), true
		}
	}
	return 0, false
}

func withLabel(base map[string]string, k, v string) map[string]string {
	out := make(map[string]string, len(base)+2)
	for bk, bv := range base {
		out[bk] = bv
	}
	out[k] = v
	return out
}

// shape turns a sample into the points one destination stores. Numeric-only
// destinations get numbers, a state-set for an enumeration, and nothing for
// text; text-capable ones store enumerations and text as states. prev is the
// enumeration's previous value, used when its options are unknown.
func shape(s sample, metric string, extra map[string]string, numericOnly bool, prev string) []tsdb.Point {
	labels := make(map[string]string, len(s.labels)+len(extra))
	for k, v := range extra {
		labels[k] = v
	}
	for k, v := range s.labels {
		labels[k] = v
	}
	at := s.at.UnixMilli()
	switch s.kind {
	case valNumber:
		return []tsdb.Point{{Time: at, Metric: metric, Labels: labels, Value: tsdb.Float(s.num)}}
	case valText:
		if numericOnly {
			return nil
		}
		return []tsdb.Point{{Time: at, Metric: metric, Labels: labels, State: tsdb.Text(s.text)}}
	case valEnum:
		if !numericOnly {
			return []tsdb.Point{{Time: at, Metric: metric, Labels: labels, State: tsdb.Text(s.text)}}
		}
		options := s.options
		if len(options) == 0 {
			options = []string{s.text}
			if prev != "" && prev != s.text {
				options = append(options, prev)
			}
		} else if !contains(options, s.text) {
			options = append(append([]string{}, options...), s.text)
		}
		out := make([]tsdb.Point, 0, len(options))
		for _, o := range options {
			l := withLabel(labels, "state", o)
			v := 0.0
			if o == s.text {
				v = 1
			}
			out = append(out, tsdb.Point{Time: at, Metric: metric, Labels: l, Value: tsdb.Float(v)})
		}
		return out
	}
	return nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// seriesKey identifies one series at one destination.
func seriesKey(dest string, p tsdb.Point) string {
	keys := make([]string, 0, len(p.Labels))
	for k := range p.Labels {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(dest)
	b.WriteByte('|')
	b.WriteString(p.Metric)
	for _, k := range keys {
		b.WriteByte('|')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(p.Labels[k])
	}
	return b.String()
}
