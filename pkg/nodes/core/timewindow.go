package core

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sunevent"
)

// TimeWindowNode classifies incoming events by local clock or solar boundaries.
type TimeWindowNode struct{}

func (TimeWindowNode) Meta() engine.NodeMeta {
	fields := []engine.FieldSpec{
		{ID: "weekdays", Label: "Weekdays", Type: flow.FieldString, Control: "tokens", Options: weekdayOptions(), Help: "Empty selects every day. An overnight window belongs to the day it starts."},
		{ID: "location", Label: "Time zone", Type: flow.FieldTimezone, Help: "Empty uses the server's local time. Clock windows follow local wall time, including repeated daylight saving hours."},
		{ID: "time", Label: "Timestamp property", Type: flow.FieldString, Placeholder: "payload.timestamp", Help: "Empty uses the current time. Otherwise read a timestamp property from the message."},
	}
	for _, side := range []struct{ id, label, clock, event string }{{"start", "Start", "08:00", "sunrise"}, {"end", "End", "18:00", "sunset"}} {
		fields = append(fields,
			engine.FieldSpec{ID: side.id + "_type", Label: side.label + " boundary", Type: flow.FieldSelect, Default: "clock", Options: []engine.Option{{Value: "clock", Label: "Clock time"}, {Value: "sun", Label: "Sun event"}}},
			engine.FieldSpec{ID: side.id + "_time", Label: side.label + " time", Type: flow.FieldString, Default: side.clock, Placeholder: "HH:MM", ShowIf: &flow.ShowIf{Field: side.id + "_type", Equals: "clock"}},
			engine.FieldSpec{ID: side.id + "_event", Label: side.label + " event", Type: flow.FieldSelect, Default: side.event, Options: windowSunOptions(), ShowIf: &flow.ShowIf{Field: side.id + "_type", Equals: "sun"}},
			engine.FieldSpec{ID: side.id + "_offset", Label: side.label + " offset", Type: flow.FieldDuration, ShowIf: &flow.ShowIf{Field: side.id + "_type", Equals: "sun"}, Help: "Elapsed time before or after the sun event, between -12h and 12h."},
		)
	}
	fields = append(fields,
		engine.FieldSpec{ID: "latitude", Label: "Latitude", Type: flow.FieldNumber, Geo: flow.GeoLatitude, Help: "Solar boundaries use these coordinates, or the server's configured location."},
		engine.FieldSpec{ID: "longitude", Label: "Longitude", Type: flow.FieldNumber, Geo: flow.GeoLongitude},
	)
	return engine.NodeMeta{
		Type: "time-window", Label: "Time window", Folder: flow.FolderTime, Icon: "las la-business-time",
		Description: "Routes events inside or outside selected local clock or sun-event windows",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs: []engine.PortSpec{
			{ID: "inside", Label: "Inside", Type: flow.PortAny, TypeFrom: "in"},
			{ID: "outside", Label: "Outside", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: fields,
	}
}

// weekdayOptions names the values accepted by window weekday selection.
func weekdayOptions() []engine.Option {
	var out []engine.Option
	for _, day := range []time.Weekday{time.Monday, time.Tuesday, time.Wednesday, time.Thursday, time.Friday, time.Saturday, time.Sunday} {
		out = append(out, engine.Option{Value: strings.ToLower(day.String()), Label: day.String()})
	}
	return out
}

// windowSunOptions excludes custom elevations, which require additional inputs.
func windowSunOptions() []engine.Option {
	return []engine.Option{
		{Value: "sunrise", Label: "Sunrise"}, {Value: "sunset", Label: "Sunset"},
		{Value: "solar_noon", Label: "Solar noon"}, {Value: "dawn", Label: "Dawn"}, {Value: "dusk", Label: "Dusk"},
		{Value: "nautical_dawn", Label: "Nautical dawn"}, {Value: "nautical_dusk", Label: "Nautical dusk"},
		{Value: "astronomical_dawn", Label: "Astronomical dawn"}, {Value: "astronomical_dusk", Label: "Astronomical dusk"},
	}
}

// timeWindowConfig separates the two endpoint definitions from message time.
type timeWindowConfig struct {
	Weekdays    string   `json:"weekdays"`
	Location    string   `json:"location"`
	Time        string   `json:"time"`
	StartType   string   `json:"start_type"`
	StartTime   string   `json:"start_time"`
	StartEvent  string   `json:"start_event"`
	StartOffset string   `json:"start_offset"`
	EndType     string   `json:"end_type"`
	EndTime     string   `json:"end_time"`
	EndEvent    string   `json:"end_event"`
	EndOffset   string   `json:"end_offset"`
	Latitude    *float64 `json:"latitude"`
	Longitude   *float64 `json:"longitude"`
}

// windowBoundary resolves a nominal local date to a civil comparison timestamp.
type windowBoundary struct {
	kind   string
	hour   int
	minute int
	event  sunevent.Event
	offset time.Duration
}

// parseWindowBoundary validates one active endpoint without reading stale fields.
func parseWindowBoundary(kind, clock, event, offset, defaultClock, defaultEvent string) (windowBoundary, error) {
	if kind == "" {
		kind = "clock"
	}
	b := windowBoundary{kind: kind}
	switch kind {
	case "clock":
		if clock == "" {
			clock = defaultClock
		}
		t, err := time.Parse("15:04", clock)
		if err != nil || t.Format("15:04") != clock {
			return b, fmt.Errorf("clock time %q must use HH:MM from 00:00 to 23:59", clock)
		}
		b.hour, b.minute = t.Hour(), t.Minute()
	case "sun":
		if event == "" {
			event = defaultEvent
		}
		var err error
		b.event, err = sunevent.ParseEvent(event)
		if err != nil || b.event == sunevent.CustomElevation {
			return b, fmt.Errorf("unsupported window sun event %q", event)
		}
		b.offset, err = parseDurationDefault(offset, 0)
		if err != nil || b.offset < -12*time.Hour || b.offset > 12*time.Hour {
			return b, fmt.Errorf("sun offset %q must be between -12h and 12h", offset)
		}
	default:
		return b, fmt.Errorf("unknown window boundary type %q", kind)
	}
	return b, nil
}

// civilTime preserves wall-clock fields without choosing an ambiguous DST instant.
func civilTime(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// on finds a solar event on the requested local date, which may be a neighboring
// UTC date near the date line. Returned timestamps are civil comparison values.
func (b windowBoundary) on(date time.Time, loc *time.Location, lat, lon float64) (time.Time, bool, error) {
	if b.kind == "clock" {
		return time.Date(date.Year(), date.Month(), date.Day(), b.hour, b.minute, 0, 0, time.UTC), true, nil
	}
	opts := sunevent.Options{Event: b.event, Latitude: lat, Longitude: lon}
	for shift := -1; shift <= 1; shift++ {
		at, err := sunevent.On(date.AddDate(0, 0, shift), opts)
		if err != nil {
			var noEvent *sunevent.ErrNoEvent
			if errors.As(err, &noEvent) {
				continue
			}
			return time.Time{}, false, err
		}
		local := at.In(loc)
		if local.Year() == date.Year() && local.Month() == date.Month() && local.Day() == date.Day() {
			return civilTime(at.Add(b.offset).In(loc)), true, nil
		}
	}
	return time.Time{}, false, nil
}

// Execute classifies one message without waiting or starting another run.
func (TimeWindowNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg timeWindowConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	loc, err := waitLocation(cfg.Location)
	if err != nil {
		return nil, err
	}
	start, err := parseWindowBoundary(cfg.StartType, cfg.StartTime, cfg.StartEvent, cfg.StartOffset, "08:00", "sunrise")
	if err != nil {
		return nil, fmt.Errorf("start boundary: %w", err)
	}
	end, err := parseWindowBoundary(cfg.EndType, cfg.EndTime, cfg.EndEvent, cfg.EndOffset, "18:00", "sunset")
	if err != nil {
		return nil, fmt.Errorf("end boundary: %w", err)
	}
	if start == end {
		return nil, fmt.Errorf("window start and end must differ")
	}
	selected := make(map[time.Weekday]bool)
	if strings.TrimSpace(cfg.Weekdays) != "" {
		for _, raw := range strings.Split(cfg.Weekdays, ",") {
			day := strings.ToLower(strings.TrimSpace(raw))
			found := false
			for d := time.Sunday; d <= time.Saturday; d++ {
				if strings.ToLower(d.String()) == day {
					selected[d] = true
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("unknown weekday %q", raw)
			}
		}
	}
	var lat, lon float64
	if start.kind == "sun" || end.kind == "sun" {
		lat, lon, err = resolveCoordinates(cfg.Latitude, cfg.Longitude)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(lat) || math.IsInf(lat, 0) || math.IsNaN(lon) || math.IsInf(lon, 0) || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
			return nil, fmt.Errorf("sun coordinates must be finite latitude -90..90 and longitude -180..180")
		}
	}
	msg := inbound(ec, inputs)
	at := ec.Clock().Now()
	if path := strings.TrimSpace(cfg.Time); path != "" {
		value, found := resolveProperty(msg, path)
		if !found {
			return nil, fmt.Errorf("timestamp property %q is not present on the message", path)
		}
		if number, ok := toNumber(value); ok && (math.IsNaN(number) || math.IsInf(number, 0)) {
			return nil, fmt.Errorf("timestamp property %q must be finite", path)
		}
		at, err = parseTimeValue(value, loc)
		if err != nil {
			return nil, fmt.Errorf("timestamp property %q: %w", path, err)
		}
	}
	now := civilTime(at.In(loc))
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	missingSun := false
	// Offsets can move a start into tomorrow or an end two dates past its
	// nominal start. These four anchors cover the bounded twelve-hour offsets.
	for shift := -2; shift <= 1; shift++ {
		date := today.AddDate(0, 0, shift)
		if len(selected) > 0 && !selected[date.Weekday()] {
			continue
		}
		from, found, err := start.on(date, loc, lat, lon)
		if err != nil {
			return nil, err
		}
		if !found {
			missingSun = true
			continue
		}
		if now.Before(from) {
			continue
		}
		for endShift := 0; endShift <= 2; endShift++ {
			until, found, err := end.on(date.AddDate(0, 0, endShift), loc, lat, lon)
			if err != nil {
				return nil, err
			}
			if !found {
				missingSun = true
				break
			}
			if !until.After(from) {
				continue
			}
			if now.Before(until) {
				return engine.One("inside", msg), nil
			}
			break
		}
	}
	if missingSun {
		msg = passThrough(msg)
		if msg.Meta == nil {
			msg.Meta = make(map[string]any)
		}
		msg.Meta["time_window_reason"] = "no_sun_event"
	}
	return engine.One("outside", msg), nil
}
