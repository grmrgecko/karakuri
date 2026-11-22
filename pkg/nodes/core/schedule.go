package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/cronspec"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sunevent"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
)

// ScheduleNode is the time trigger: while its flow is deployed and enabled it
// starts a run on every cron tick, or at a solar event such as sunset. Test runs
// need no test_params; the node synthesizes a tick-shaped message on demand.
type ScheduleNode struct{}

func (ScheduleNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "schedule",
		Label:             "Schedule",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderTime,
		Description:       "Starts a run on a cron schedule or at a sun event",
		Icon:              "las la-clock",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Trigger on",
				Type:    flow.FieldSelect,
				Default: "cron",
				Options: []engine.Option{
					{Value: "cron", Label: "A cron schedule"},
					{Value: "sun", Label: "A sun event"},
				},
			},
			{
				ID:          "cron",
				Label:       "Cron",
				Type:        flow.FieldCron,
				Required:    true,
				Default:     "0 8 * * *",
				Placeholder: "0 8 * * *",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "cron"},
			},
			{
				ID:      "event",
				Label:   "Sun event",
				Type:    flow.FieldSelect,
				Default: string(sunevent.Sunset),
				ShowIf:  &flow.ShowIf{Field: "mode", Equals: "sun"},
				Options: []engine.Option{
					{Value: string(sunevent.Sunrise), Label: "Sunrise"},
					{Value: string(sunevent.Sunset), Label: "Sunset"},
					{Value: string(sunevent.SolarNoon), Label: "Solar noon"},
					{Value: string(sunevent.Dawn), Label: "Dawn (civil twilight)"},
					{Value: string(sunevent.Dusk), Label: "Dusk (civil twilight)"},
					{Value: string(sunevent.NauticalDawn), Label: "Nautical dawn"},
					{Value: string(sunevent.NauticalDusk), Label: "Nautical dusk"},
					{Value: string(sunevent.AstronomicalDawn), Label: "Astronomical dawn"},
					{Value: string(sunevent.AstronomicalDusk), Label: "Astronomical dusk"},
					{Value: string(sunevent.CustomElevation), Label: "A specific sun elevation"},
				},
			},
			{
				ID:          "elevation",
				Label:       "Sun elevation",
				Type:        flow.FieldNumber,
				Default:     6,
				ShowIf:      &flow.ShowIf{Field: "event", Equals: string(sunevent.CustomElevation)},
				Help:        "Degrees above the horizon. 6 is the golden hour edge; negative values are below the horizon.",
				Placeholder: "6",
			},
			{
				ID:      "rising",
				Label:   "While the sun is rising",
				Type:    flow.FieldBoolean,
				Default: false,
				ShowIf:  &flow.ShowIf{Field: "event", Equals: string(sunevent.CustomElevation)},
				Help:    "On picks the morning crossing of that elevation, off the evening one.",
			},
			{
				ID:          "offset",
				Label:       "Offset",
				Type:        flow.FieldDuration,
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sun"},
				Placeholder: "-30m",
				Help:        "Shifts the trigger relative to the event. Negative fires before it, for example -30m for half an hour before sunset.",
			},
			{
				ID:          "latitude",
				Label:       "Latitude",
				Type:        flow.FieldNumber,
				Geo:         flow.GeoLatitude,
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sun"},
				Placeholder: "51.4769",
				Help:        "Degrees north. Empty uses the server's configured location.",
			},
			{
				ID:          "longitude",
				Label:       "Longitude",
				Type:        flow.FieldNumber,
				Geo:         flow.GeoLongitude,
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sun"},
				Placeholder: "-0.0005",
				Help:        "Degrees east. Empty uses the server's configured location.",
			},
			{
				ID:    "location",
				Label: "Time zone",
				Type:  flow.FieldTimezone,
				Help:  "IANA time zone the schedule is evaluated in. Empty uses the server's local time. Sun events are computed from latitude and longitude, so this only labels their timestamps.",
			},
		},
	}
}

// scheduleConfig is the decoded schedule node configuration.
type scheduleConfig struct {
	Mode      string   `json:"mode"`
	Cron      string   `json:"cron"`
	Event     string   `json:"event"`
	Elevation *float64 `json:"elevation"`
	Rising    bool     `json:"rising"`
	Offset    string   `json:"offset"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
	Location  string   `json:"location"`
}

// isSun reports whether the node is configured for a solar event.
func (c scheduleConfig) isSun() bool { return c.Mode == "sun" }

// spec returns the effective cron spec with the configured time zone applied.
func (c scheduleConfig) spec() string {
	return cronspec.Spec(c.Cron, c.Location)
}

// sunOptions resolves the solar lookup, falling back to the server's configured
// location when the node leaves latitude and longitude empty.
func (c scheduleConfig) sunOptions() (sunevent.Options, time.Duration, error) {
	// An omitted event takes the field's default of sunset.
	name := c.Event
	if name == "" {
		name = string(sunevent.Sunset)
	}
	event, err := sunevent.ParseEvent(name)
	if err != nil {
		return sunevent.Options{}, 0, err
	}
	lat, lon, err := resolveCoordinates(c.Latitude, c.Longitude)
	if err != nil {
		return sunevent.Options{}, 0, err
	}
	offset, err := parseDurationDefault(c.Offset, 0)
	if err != nil {
		return sunevent.Options{}, 0, fmt.Errorf("invalid offset %q: %w", c.Offset, err)
	}
	// An omitted elevation takes the field's default of 6; 0 is a real
	// elevation, the horizon, so it cannot stand for unset.
	elevation := 6.0
	if c.Elevation != nil {
		elevation = *c.Elevation
	}
	opts := sunevent.Options{
		Event:     event,
		Latitude:  lat,
		Longitude: lon,
		Elevation: elevation,
		Morning:   c.Rising,
	}
	if err := opts.Validate(); err != nil {
		return sunevent.Options{}, 0, err
	}
	return opts, offset, nil
}

// validate checks whichever schedule the node is configured for.
func (c scheduleConfig) validate() error {
	if c.isSun() {
		_, _, err := c.sunOptions()
		return err
	}
	return cronspec.Validate(c.Cron, c.Location)
}

// resolveCoordinates prefers the node's own coordinates and falls back to the
// server configuration, so a deployment with one location does not repeat it on
// every schedule node.
func resolveCoordinates(lat, lon *float64) (float64, float64, error) {
	if lat != nil && lon != nil {
		return *lat, *lon, nil
	}
	if config.C == nil || (config.C.Location.Latitude == 0 && config.C.Location.Longitude == 0) {
		return 0, 0, fmt.Errorf("sun events need a latitude and longitude, either on the node or as location in the server config")
	}
	if lat != nil {
		return *lat, config.C.Location.Longitude, nil
	}
	if lon != nil {
		return config.C.Location.Latitude, *lon, nil
	}
	return config.C.Location.Latitude, config.C.Location.Longitude, nil
}

// stampZone returns the zone a tick's timestamps are written in: the node's
// own, or the clock's when the node names none. A zone that does not load
// also falls back to the clock's; arming rejects it before any tick is built.
func (c scheduleConfig) stampZone(t time.Time) *time.Location {
	name := strings.TrimSpace(c.Location)
	if name == "" {
		return t.Location()
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return t.Location()
	}
	return loc
}

// tickMsg builds the message emitted per schedule tick, stamped in loc.
func tickMsg(t time.Time, spec string, loc *time.Location) *engine.Msg {
	return &engine.Msg{
		Payload: map[string]any{"scheduled_at": t.In(loc).Format(time.RFC3339)},
		Meta:    map[string]any{"cron": spec},
	}
}

// sunTickMsg builds the message emitted for a solar event, carrying both the
// nominal event time and the offset time the trigger actually fired at, both
// stamped in loc.
func sunTickMsg(now, eventAt time.Time, opts sunevent.Options, offset time.Duration, loc *time.Location) *engine.Msg {
	return &engine.Msg{
		Payload: map[string]any{
			"scheduled_at": now.In(loc).Format(time.RFC3339),
			"event":        string(opts.Event),
			"event_at":     eventAt.In(loc).Format(time.RFC3339),
		},
		Meta: map[string]any{
			"sun_event": string(opts.Event),
			"offset":    offset.String(),
			"latitude":  opts.Latitude,
			"longitude": opts.Longitude,
		},
	}
}

// Execute passes the run's initial message through. Test runs arrive with an
// empty payload (the node is TestWithoutParams); those get a synthesized
// tick message so downstream nodes see the deployed shape.
func (ScheduleNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	var cfg scheduleConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}

	var out *engine.Msg
	if cfg.isSun() {
		// Show the next real occurrence so a test run matches what will fire.
		opts, offset, err := cfg.sunOptions()
		if err != nil {
			return nil, err
		}
		now := ec.Clock().Now()
		next, err := sunevent.Next(now, opts, offset)
		if err != nil {
			return nil, err
		}
		out = sunTickMsg(now, next.Add(-offset), opts, offset, cfg.stampZone(now))
	} else {
		now := ec.Clock().Now()
		out = tickMsg(now, cfg.Cron, cfg.stampZone(now))
	}

	if msg != nil {
		out.ID = msg.ID
		if msg.Meta != nil {
			for k, v := range msg.Meta {
				out.Meta[k] = v
			}
		}
	}
	return engine.One("out", out), nil
}

// NextFire implements engine.NextFirer from the same cron and sun rules Start
// arms, so the time it reports is the time the trigger fires.
func (ScheduleNode) NextFire(config json.RawMessage, after time.Time) (time.Time, bool, error) {
	var cfg scheduleConfig
	if len(config) > 0 {
		if err := json.Unmarshal(config, &cfg); err != nil {
			return time.Time{}, false, err
		}
	}
	if cfg.Mode == "" {
		cfg.Mode = "cron"
	}
	if err := cfg.validate(); err != nil {
		return time.Time{}, false, err
	}
	if cfg.isSun() {
		opts, offset, err := cfg.sunOptions()
		if err != nil {
			return time.Time{}, false, err
		}
		// Next fails when the event does not occur within its search span,
		// as in polar night; the caller skips the node either way.
		next, err := sunevent.Next(after, opts, offset)
		if err != nil {
			return time.Time{}, false, err
		}
		return next, true, nil
	}
	times, err := cronspec.Next(cfg.Cron, cfg.Location, after, 1)
	if err != nil {
		return time.Time{}, false, err
	}
	if len(times) == 0 {
		return time.Time{}, false, nil
	}
	return times[0], true, nil
}

// Start implements engine.TriggerType: it emits a message per schedule tick
// until the context is canceled.
func (ScheduleNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg scheduleConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if err := cfg.validate(); err != nil {
		return err
	}
	if cfg.isSun() {
		return startSunSchedule(ctx, ec, cfg, emit)
	}

	c := cron.New(cron.WithParser(cronspec.Parser))
	if _, err := c.AddFunc(cfg.spec(), func() {
		now := ec.Clock().Now()
		emit(tickMsg(now, cfg.Cron, cfg.stampZone(now)))
	}); err != nil {
		return fmt.Errorf("invalid cron spec %q: %w", cfg.Cron, err)
	}
	c.Start()
	ec.Logger().Info("schedule trigger armed",
		zap.String("cron", cfg.Cron), zap.String("location", cfg.Location))

	<-ctx.Done()
	// Stop returns a context that completes when running jobs finish.
	<-c.Stop().Done()
	return nil
}

// startSunSchedule waits for each solar occurrence in turn, recomputing after
// every tick so the schedule tracks the season without drifting. Waits go
// through the engine clock, so tests can drive them.
func startSunSchedule(ctx context.Context, ec engine.ExecContext, cfg scheduleConfig, emit func(*engine.Msg)) error {
	opts, offset, err := cfg.sunOptions()
	if err != nil {
		return err
	}
	ec.Logger().Info("sun schedule trigger armed",
		zap.String("event", string(opts.Event)),
		zap.Duration("offset", offset),
		zap.Float64("latitude", opts.Latitude),
		zap.Float64("longitude", opts.Longitude))

	for {
		now := ec.Clock().Now()
		next, err := sunevent.Next(now, opts, offset)
		if err != nil {
			// The event never happens at this location; log once and stop rather
			// than spinning.
			return err
		}
		if err := ec.Park(ctx, next.Sub(now)); err != nil {
			return nil
		}
		fired := ec.Clock().Now()
		emit(sunTickMsg(fired, next.Add(-offset), opts, offset, cfg.stampZone(fired)))

		// Step past the occurrence just handled so the next search cannot land
		// on it again when the clock has barely moved.
		if !fired.After(next) {
			if err := ec.Park(ctx, time.Second); err != nil {
				return nil
			}
		}
	}
}
