package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// folderWeather is where the plugin's nodes sit in the palette. Folders are
// free-form display strings, so a plugin brings its own without registering
// anything with the host.
const folderWeather = "Weather"

// providerField is the provider picker every weather node shares.
func providerField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:      "provider",
		Label:   "Provider",
		Type:    flow.FieldSelect,
		Default: "openmeteo",
		Options: []engine.Option{
			{Value: "openmeteo", Label: "Open-Meteo"},
			{Value: "openweathermap", Label: "OpenWeatherMap"},
		},
		Help: "OpenWeatherMap needs the plugin's API key; Open-Meteo needs none.",
	}
}

func latitudeField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "latitude", Label: "Latitude", Type: flow.FieldNumber, Required: true,
		Placeholder: "47.6062",
	}
}

func longitudeField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "longitude", Label: "Longitude", Type: flow.FieldNumber, Required: true,
		Placeholder: "-122.3321",
	}
}

// resolveProvider looks up a configured provider by name, defaulting to the
// one that needs no credentials.
func (p *WeatherPlugin) resolveProvider(name string) (Provider, error) {
	if name == "" {
		name = "openmeteo"
	}
	prov, ok := p.providers[name]
	if !ok {
		return nil, fmt.Errorf("unknown weather provider %q", name)
	}
	return prov, nil
}

// ---- weather-changed ----

// WeatherChangedNode polls a location and starts a run when the forecast
// changes. What counts as a change is configurable, so the node is a genuine
// event source rather than an interval timer with weather attached.
type WeatherChangedNode struct{ p *WeatherPlugin }

func (WeatherChangedNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "weather-changed",
		Label:             "Weather Changed",
		Category:          flow.CategoryTrigger,
		Folder:            folderWeather,
		Description:       "Starts a run when a location's forecast changes",
		Icon:              "las la-cloud-sun-rain",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			providerField(),
			latitudeField(),
			longitudeField(),
			{
				ID: "watch", Label: "Emit on", Type: flow.FieldSelect, Default: "condition",
				Options: []engine.Option{
					{Value: "condition", Label: "Condition change"},
					{Value: "temperature", Label: "Temperature change"},
					{Value: "any", Label: "Any observation change"},
				},
				Help: "Which part of the current observation has to move for a run to start.",
			},
			{
				ID: "threshold", Label: "Temperature threshold", Type: flow.FieldNumber, Default: 0.5,
				Help: "Degrees the temperature must move to count as a change. Ignored unless watching temperature.",
			},
			{
				ID: "interval_s", Label: "Poll interval (seconds)", Type: flow.FieldNumber, Default: 600,
				Help: "How often the forecast is fetched. Providers rate-limit; below 60 seconds is unkind.",
			},
			{
				ID: "units", Label: "Units", Type: flow.FieldSelect, Default: "metric",
				Options: []engine.Option{
					{Value: "metric", Label: "Metric"},
					{Value: "imperial", Label: "Imperial"},
				},
			},
		},
	}
}

// weatherNodeConfig is the decoded configuration shared by the weather nodes.
type weatherNodeConfig struct {
	Provider  string  `json:"provider"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Units     string  `json:"units"`
	Watch     string  `json:"watch"`
	Threshold float64 `json:"threshold"`
	IntervalS float64 `json:"interval_s"`
}

// Execute passes the run's seed message through, like every trigger.
func (WeatherChangedNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{}}
	}
	return engine.One("out", msg), nil
}

// Start polls until disarmed, emitting only when the watched aspect moved.
// Comparison is against the last emitted observation, so a slow temperature
// drift eventually crosses the threshold instead of never firing. The wait
// goes through the host clock, so a fake clock drives it in tests.
func (n *WeatherChangedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg weatherNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return err
	}
	interval := time.Duration(cfg.IntervalS * float64(time.Second))
	if interval <= 0 {
		interval = time.Duration(defaultPollInterval) * time.Second
	}

	var previous *CurrentWeather
	for {
		data, err := prov.FetchForecast(ctx, roundCoord(cfg.Latitude), roundCoord(cfg.Longitude), n.p.language)
		if err != nil {
			// A provider hiccup is not a reason to disarm; the next tick
			// tries again. A trigger has no error branch to route to.
			ec.Debug(map[string]any{"error": err.Error()}, "weather fetch failed")
		} else if data.Current != nil {
			data = n.p.withOverride(data, cfg.Latitude, cfg.Longitude)
			if cfg.Units == "imperial" {
				data = convertToImperial(data)
			}
			if changed, fields := weatherChanged(previous, data.Current, cfg); changed {
				emit(&engine.Msg{
					Topic: fmt.Sprintf("weather:%s:%s:%s", cfg.Provider,
						formatCoord(roundCoord(cfg.Latitude)), formatCoord(roundCoord(cfg.Longitude))),
					Payload: map[string]any{
						"current":  data.Current,
						"previous": previous,
						"changed":  fields,
						"daily":    data.Daily,
						"units":    cfg.Units,
					},
				})
				previous = data.Current
			}
		}

		select {
		case <-ec.Clock().After(interval):
		case <-ctx.Done():
			return nil
		}
	}
}

// weatherChanged reports whether the watched aspect moved, and which fields
// did. The first observation always counts as a change: a flow that starts
// mid-afternoon should learn the weather rather than wait for it to shift.
func weatherChanged(prev, cur *CurrentWeather, cfg weatherNodeConfig) (bool, []string) {
	if prev == nil {
		return true, []string{"initial"}
	}
	switch cfg.Watch {
	case "temperature":
		threshold := cfg.Threshold
		if threshold <= 0 {
			threshold = 0.5
		}
		if math.Abs(cur.Temp-prev.Temp) >= threshold {
			return true, []string{"temp"}
		}
		return false, nil
	case "any":
		var fields []string
		if cur.Temp != prev.Temp {
			fields = append(fields, "temp")
		}
		if cur.WeatherCode != prev.WeatherCode {
			fields = append(fields, "weather_code")
		}
		if cur.Humidity != prev.Humidity {
			fields = append(fields, "humidity")
		}
		if cur.WindSpeed != prev.WindSpeed {
			fields = append(fields, "wind_speed")
		}
		if cur.Pressure != prev.Pressure {
			fields = append(fields, "pressure")
		}
		return len(fields) > 0, fields
	default: // condition
		if cur.WeatherCode != prev.WeatherCode {
			return true, []string{"weather_code"}
		}
		return false, nil
	}
}

// ---- weather-forecast ----

// WeatherForecastNode is a read-only lookup: no input port, no error branch,
// one fetch per run however many consumers pull it.
type WeatherForecastNode struct{ p *WeatherPlugin }

func (WeatherForecastNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "weather-forecast",
		Label:             "Weather Forecast",
		Folder:            folderWeather,
		Description:       "Current conditions and forecast for a location, fetched when a consumer needs it",
		Icon:              "las la-cloud-sun",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			providerField(),
			latitudeField(),
			longitudeField(),
			{
				ID: "units", Label: "Units", Type: flow.FieldSelect, Default: "metric",
				Options: []engine.Option{
					{Value: "metric", Label: "Metric"},
					{Value: "imperial", Label: "Imperial"},
				},
			},
		},
	}
}

func (n *WeatherForecastNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg weatherNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return nil, err
	}

	data, err := prov.FetchForecast(ctx, roundCoord(cfg.Latitude), roundCoord(cfg.Longitude), n.p.language)
	if err != nil {
		return nil, fmt.Errorf("fetching forecast: %w", err)
	}
	data = n.p.withOverride(data, cfg.Latitude, cfg.Longitude)
	if cfg.Units == "imperial" {
		data = convertToImperial(data)
	}

	return engine.One("out", &engine.Msg{
		Topic: fmt.Sprintf("weather:%s:%s:%s", cfg.Provider,
			formatCoord(roundCoord(cfg.Latitude)), formatCoord(roundCoord(cfg.Longitude))),
		Payload: map[string]any{
			"current":  data.Current,
			"hourly":   data.Hourly,
			"daily":    data.Daily,
			"alerts":   data.Alerts,
			"units":    cfg.Units,
			"fetched":  data.FetchedAt,
			"provider": cfg.Provider,
			"override": data.Override,
		},
	}), nil
}

// ---- weather-geocode ----

// WeatherGeocodeNode resolves a place name to coordinates, or coordinates
// back to a place name. Its query field is value-edge bindable, so an upstream
// node can supply the search term.
type WeatherGeocodeNode struct{ p *WeatherPlugin }

func (WeatherGeocodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "weather-geocode",
		Label:             "Geocode",
		Folder:            folderWeather,
		Description:       "Resolves a place name to coordinates, or coordinates to a place name",
		Icon:              "las la-map-marker-alt",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			providerField(),
			{
				ID: "mode", Label: "Direction", Type: flow.FieldSelect, Default: "search",
				Options: []engine.Option{
					{Value: "search", Label: "Name to coordinates"},
					{Value: "reverse", Label: "Coordinates to name"},
				},
			},
			{
				ID: "query", Label: "Place name", Type: flow.FieldString,
				Placeholder: "Seattle, WA",
				Help:        "Searched when the direction is name to coordinates.",
			},
			{
				ID: "latitude", Label: "Latitude", Type: flow.FieldNumber,
				Help: "Used when the direction is coordinates to name.",
			},
			{
				ID: "longitude", Label: "Longitude", Type: flow.FieldNumber,
				Help: "Used when the direction is coordinates to name.",
			},
		},
	}
}

// geocodeConfig is the decoded weather-geocode configuration.
type geocodeConfig struct {
	Provider  string  `json:"provider"`
	Mode      string  `json:"mode"`
	Query     string  `json:"query"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

func (n *WeatherGeocodeNode) Execute(ctx context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg geocodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	prov, err := n.p.resolveProvider(cfg.Provider)
	if err != nil {
		return nil, err
	}

	var results []GeoResult
	if cfg.Mode == "reverse" {
		results, err = prov.ReverseGeo(ctx, cfg.Latitude, cfg.Longitude, n.p.language)
	} else {
		query := strings.TrimSpace(cfg.Query)
		if query == "" {
			return nil, fmt.Errorf("a place name is required to search")
		}
		results, err = prov.GeoSearch(ctx, query, n.p.language)
	}
	if err != nil {
		return nil, fmt.Errorf("geocoding: %w", err)
	}
	if results == nil {
		results = []GeoResult{}
	}

	// The first result is the one a downstream node almost always wants, so
	// it is lifted out rather than left for a Change node to dig for.
	payload := map[string]any{"results": results, "count": len(results)}
	if len(results) > 0 {
		payload["name"] = results[0].Name
		payload["latitude"] = results[0].Lat
		payload["longitude"] = results[0].Lon
		payload["country"] = results[0].Country
	}
	return engine.One("out", &engine.Msg{Topic: "geocode", Payload: payload}), nil
}

// ---- weather-override ----

// Override defaults. A station reporting every minute stays live through a
// few missed reports under the default age, and the default radius covers a
// town's postal code from its centre.
const (
	overrideDefaultMaxAge = 15 * time.Minute
	overrideMaxMaxAge     = 24 * time.Hour
	overrideDefaultRadius = 10.0
	overrideMaxRadius     = 200.0
)

// WeatherOverrideNode stores local station readings for a location. Forecasts
// served for coordinates within the override's radius show those readings in
// place of the provider's current observation until each reading reaches the
// maximum age, and then return to the provider's values.
type WeatherOverrideNode struct{ p *WeatherPlugin }

func (WeatherOverrideNode) Meta() engine.NodeMeta {
	fields := []engine.FieldSpec{
		{
			ID: "operation", Label: "Operation", Type: flow.FieldSelect, Default: "set",
			Options: []engine.Option{
				{Value: "set", Label: "Set readings"},
				{Value: "clear", Label: "Clear override"},
			},
			Help: "Clear drops every reading for the location at once, such as when the station goes offline.",
		},
		{
			ID: "location", Label: "Location", Type: flow.FieldSelect, Default: "postal",
			Options: []engine.Option{
				{Value: "postal", Label: "Postal code"},
				{Value: "coordinates", Label: "Coordinates"},
			},
		},
		{
			ID: "postal_code", Label: "Postal code", Type: flow.FieldString, Placeholder: "35757",
			ShowIf: &flow.ShowIf{Field: "location", Equals: "postal"},
			Help:   "Resolved to the centre of the place carrying the code. {{ }} segments are supported.",
		},
		{
			ID: "country", Label: "Country code", Type: flow.FieldString, Placeholder: "US",
			ShowIf: &flow.ShowIf{Field: "location", Equals: "postal"},
			Help:   "ISO country code. Recommended, since the same postal code exists in several countries.",
		},
		{
			ID: "latitude", Label: "Latitude", Type: flow.FieldNumber, Geo: flow.GeoLatitude,
			Placeholder: "34.7304",
			ShowIf:      &flow.ShowIf{Field: "location", Equals: "coordinates"},
		},
		{
			ID: "longitude", Label: "Longitude", Type: flow.FieldNumber, Geo: flow.GeoLongitude,
			Placeholder: "-86.5846",
			ShowIf:      &flow.ShowIf{Field: "location", Equals: "coordinates"},
		},
		{
			ID: "radius_km", Label: "Radius (km)", Type: flow.FieldNumber, Default: overrideDefaultRadius,
			ShowIf: &flow.ShowIf{Field: "operation", Equals: "set"},
			Help:   "Forecasts for coordinates this close use the readings. The nearest override wins where radii overlap.",
		},
		{
			ID: "max_age", Label: "Maximum age", Type: flow.FieldDuration, Default: "15m", Placeholder: "15m",
			ShowIf: &flow.ShowIf{Field: "operation", Equals: "set"},
			Help:   "How long each reading replaces the provider's after it was last set. Up to 24h.",
		},
		{
			ID: "units", Label: "Reading units", Type: flow.FieldSelect, Default: "metric",
			ShowIf: &flow.ShowIf{Field: "operation", Equals: "set"},
			Options: []engine.Option{
				{Value: "metric", Label: "Metric (°C, m/s, hPa)"},
				{Value: "imperial", Label: "Imperial (°F, mph, inHg)"},
			},
		},
	}
	for _, m := range overrideMetrics {
		help := "Leave blank to keep the provider's value. Blank, null, unknown, and unavailable results are skipped."
		switch {
		case m.Metric == "":
		case m.Imperial != m.Metric:
			help = "In " + m.Metric + " or " + m.Imperial + ", per the reading units. " + help
		default:
			help = "In " + m.Metric + ". " + help
		}
		fields = append(fields, engine.FieldSpec{
			ID: m.ID, Label: m.Label, Type: flow.FieldString,
			Placeholder: "{{ payload." + m.ID + " }}",
			ShowIf:      &flow.ShowIf{Field: "operation", Equals: "set"},
			Help:        help,
		})
	}

	return engine.NodeMeta{
		Type:        "weather-override",
		Label:       "Weather Override",
		Folder:      folderWeather,
		Description: "Replaces a location's current conditions with local station readings until they expire",
		Icon:        "las la-broadcast-tower",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
			flow.ErrorPort(),
		},
		ConfigSpec: fields,
	}
}

// overrideConfig is the decoded weather-override configuration. Metric
// fields are read from the raw config because a value edge can supply a
// number where the editor stores text.
type overrideConfig struct {
	Operation  string  `json:"operation"`
	Location   string  `json:"location"`
	PostalCode string  `json:"postal_code"`
	Country    string  `json:"country"`
	Latitude   float64 `json:"latitude"`
	Longitude  float64 `json:"longitude"`
	RadiusKm   float64 `json:"radius_km"`
	MaxAge     string  `json:"max_age"`
	Units      string  `json:"units"`
}

func (n *WeatherOverrideNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg overrideConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	var raw map[string]any
	if err := ec.Config(&raw); err != nil {
		return nil, err
	}
	msg := inputs["in"]

	if cfg.Location == "coordinates" && (raw["latitude"] == nil || raw["longitude"] == nil) {
		return nil, fmt.Errorf("latitude and longitude are required")
	}
	label, lat, lon, err := n.resolveLocation(ctx, ec, cfg, msg)
	if err != nil {
		return nil, err
	}

	if cfg.Operation == "clear" {
		entry, held := n.p.overrides.Clear(lat, lon)
		if held {
			n.p.notifyOverride(entry.Lat, entry.Lon, entry.RadiusKm)
		}
		return engine.One("out", &engine.Msg{
			Topic: "weather-override",
			Payload: map[string]any{
				"operation": "clear", "source": label,
				"latitude": roundCoord(lat), "longitude": roundCoord(lon),
				"cleared": held,
			},
		}), nil
	}

	maxAge := overrideDefaultMaxAge
	if s := strings.TrimSpace(cfg.MaxAge); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil || d <= 0 || d > overrideMaxMaxAge {
			return nil, fmt.Errorf("maximum age %q must be a duration above zero and at most 24h", cfg.MaxAge)
		}
		maxAge = d
	}
	radius := cfg.RadiusKm
	if radius == 0 {
		radius = overrideDefaultRadius
	}
	if radius < 0 || radius > overrideMaxRadius || math.IsNaN(radius) {
		return nil, fmt.Errorf("radius %v km must be between 0 and %v", cfg.RadiusKm, overrideMaxRadius)
	}

	values := map[string]float64{}
	for _, m := range overrideMetrics {
		v, ok, err := overrideReading(ec, raw[m.ID], msg)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", m.Label, err)
		}
		if !ok {
			continue
		}
		if cfg.Units == "imperial" && m.toMetric != nil {
			v = m.toMetric(v)
		}
		if v < m.Min || v > m.Max {
			reading := strings.TrimSpace(fmt.Sprintf("%.2f %s", v, m.Metric))
			return nil, fmt.Errorf("%s %s is outside %v to %v", m.Label, reading, m.Min, m.Max)
		}
		values[m.ID] = v
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("no readings to set: every metric field was blank or unavailable")
	}

	entry := n.p.overrides.Set(label, lat, lon, radius, values, maxAge)
	n.p.notifyOverride(entry.Lat, entry.Lon, entry.RadiusKm)

	return engine.One("out", &engine.Msg{
		Topic: "weather-override",
		Payload: map[string]any{
			"operation": "set", "source": label,
			"latitude": entry.Lat, "longitude": entry.Lon, "radius_km": entry.RadiusKm,
			"units":   "metric",
			"set":     values,
			"metrics": entry.Values,
		},
	}), nil
}

// resolveLocation returns the override's label and coordinates.
func (n *WeatherOverrideNode) resolveLocation(ctx context.Context, ec engine.ExecContext, cfg overrideConfig, msg *engine.Msg) (string, float64, float64, error) {
	if cfg.Location == "coordinates" {
		if cfg.Latitude < -90 || cfg.Latitude > 90 || cfg.Longitude < -180 || cfg.Longitude > 180 {
			return "", 0, 0, fmt.Errorf("coordinates %v, %v are out of range", cfg.Latitude, cfg.Longitude)
		}
		return fmt.Sprintf("%s,%s", formatCoord(roundCoord(cfg.Latitude)), formatCoord(roundCoord(cfg.Longitude))),
			cfg.Latitude, cfg.Longitude, nil
	}

	code, err := ec.Interpolate(strings.TrimSpace(cfg.PostalCode), msg)
	if err != nil {
		return "", 0, 0, fmt.Errorf("postal code: %w", err)
	}
	code = strings.TrimSpace(code)
	if code == "" {
		return "", 0, 0, fmt.Errorf("a postal code is required")
	}
	country := strings.ToUpper(strings.TrimSpace(cfg.Country))
	place, err := n.p.resolvePostal(ctx, code, country)
	if err != nil {
		return "", 0, 0, err
	}
	label := code
	if country != "" {
		label = country + " " + code
	}
	return label, place.Lat, place.Lon, nil
}

// resolvePostal resolves a postal code once and remembers the answer. A
// failed lookup is not remembered, so the next report tries again.
func (p *WeatherPlugin) resolvePostal(ctx context.Context, code, country string) (GeoResult, error) {
	key := country + ":" + strings.ToUpper(code)
	p.postalMu.Lock()
	place, ok := p.postal[key]
	p.postalMu.Unlock()
	if ok {
		return place, nil
	}
	place, err := p.openMeteo.PostalSearch(ctx, code, country)
	if err != nil {
		return GeoResult{}, fmt.Errorf("resolving postal code: %w", err)
	}
	p.postalMu.Lock()
	p.postal[key] = place
	p.postalMu.Unlock()
	return place, nil
}

// overrideReading turns one metric field into a number. A literal number
// arrives from a value edge; text is interpolated against the message. Home
// Assistant reports a missing sensor as unknown or unavailable, and a missing
// message field renders as null, so those leave the provider's value in place.
func overrideReading(ec engine.ExecContext, field any, msg *engine.Msg) (float64, bool, error) {
	var s string
	switch v := field.(type) {
	case nil:
		return 0, false, nil
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, false, fmt.Errorf("reading is not a finite number")
		}
		return v, true, nil
	case string:
		out, err := ec.Interpolate(strings.TrimSpace(v), msg)
		if err != nil {
			return 0, false, err
		}
		s = strings.TrimSpace(out)
	default:
		return 0, false, fmt.Errorf("reading must be a number or text, not %T", field)
	}
	switch strings.ToLower(strings.Trim(s, `"`)) {
	case "", "null", "unknown", "unavailable", "none":
		return 0, false, nil
	}
	f, err := strconv.ParseFloat(strings.Trim(s, `"`), 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false, fmt.Errorf("%q is not a number", s)
	}
	return f, true, nil
}
