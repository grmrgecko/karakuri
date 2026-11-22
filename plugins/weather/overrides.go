package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"sync"
	"time"
)

// overrideMetric names one current observation an override can replace. The
// order of overrideMetrics is the order fields appear in the node and in
// published results.
type overrideMetric struct {
	ID    string
	Label string
	// Metric and Imperial name the unit a value is read in for each unit
	// system, for help text. Both are empty for a unitless index.
	Metric, Imperial string
	// Min and Max bound a value after conversion to metric; NaN leaves a
	// side open.
	Min, Max float64
	// toMetric converts a value read in imperial units.
	toMetric func(float64) float64
}

func fahrenheitToCelsius(f float64) float64 { return (f - 32) * 5 / 9 }
func mphToMs(mph float64) float64           { return mph / 2.23694 }
func inHgToHPa(inHg float64) float64        { return inHg * 33.8639 }

var overrideMetrics = []overrideMetric{
	{ID: "temp", Label: "Temperature", Metric: "°C", Imperial: "°F", Min: -90, Max: 70, toMetric: fahrenheitToCelsius},
	{ID: "feels_like", Label: "Feels like", Metric: "°C", Imperial: "°F", Min: -110, Max: 90, toMetric: fahrenheitToCelsius},
	{ID: "dew_point", Label: "Dew point", Metric: "°C", Imperial: "°F", Min: -100, Max: 50, toMetric: fahrenheitToCelsius},
	{ID: "humidity", Label: "Humidity", Metric: "%", Imperial: "%", Min: 0, Max: 100},
	{ID: "pressure", Label: "Sea-level pressure", Metric: "hPa", Imperial: "inHg", Min: 850, Max: 1090, toMetric: inHgToHPa},
	{ID: "wind_speed", Label: "Wind speed", Metric: "m/s", Imperial: "mph", Min: 0, Max: 120, toMetric: mphToMs},
	{ID: "wind_deg", Label: "Wind direction", Metric: "degrees", Imperial: "degrees", Min: 0, Max: 360},
	{ID: "uvi", Label: "UV index", Metric: "", Imperial: "", Min: 0, Max: 25},
}

// overrideValue is one metric reading held by an override, in metric units.
type overrideValue struct {
	Value     float64   `json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// stationOverride holds the readings pushed for one location. Each metric
// expires on its own, because a station often reports its sensors on
// separate messages and one failed sensor must not keep the others stale.
type stationOverride struct {
	Label    string
	Lat, Lon float64
	RadiusKm float64
	Values   map[string]overrideValue
}

// ForecastOverride describes the readings an override replaced in a served
// forecast, so a consumer can tell station data from provider data. The
// values themselves are in Current, in the units the forecast was asked for.
type ForecastOverride struct {
	Source    string                    `json:"source"`
	Latitude  float64                   `json:"latitude"`
	Longitude float64                   `json:"longitude"`
	Metrics   map[string]overrideWindow `json:"metrics"`
}

// overrideWindow is when a served reading was set and when it expires.
type overrideWindow struct {
	UpdatedAt time.Time `json:"updated_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// overrideStore holds station overrides in memory. Overrides are short-lived
// by design, so a plugin restart dropping them only returns the provider's
// values until the station's next report.
type overrideStore struct {
	mu      sync.Mutex
	entries map[string]*stationOverride
	timer   *time.Timer
	now     func() time.Time
	// expired is called, outside the lock, with each location whose
	// metrics expired, so watchers of nearby forecasts can refetch.
	expired func(lat, lon, radiusKm float64)
}

func newOverrideStore(expired func(lat, lon, radiusKm float64)) *overrideStore {
	return &overrideStore{entries: map[string]*stationOverride{}, now: time.Now, expired: expired}
}

func overrideKey(lat, lon float64) string {
	return formatCoord(roundCoord(lat)) + ":" + formatCoord(roundCoord(lon))
}

// Set merges metric values into the override for a location. Values are in
// metric units; each one expires maxAge after this call.
func (s *overrideStore) Set(label string, lat, lon, radiusKm float64, values map[string]float64, maxAge time.Duration) *stationOverride {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	key := overrideKey(lat, lon)
	entry := s.entries[key]
	if entry == nil {
		entry = &stationOverride{Values: map[string]overrideValue{}}
		s.entries[key] = entry
	}
	entry.Label = label
	entry.Lat, entry.Lon = roundCoord(lat), roundCoord(lon)
	entry.RadiusKm = radiusKm
	for id, v := range values {
		entry.Values[id] = overrideValue{Value: v, UpdatedAt: now, ExpiresAt: now.Add(maxAge)}
	}
	s.pruneLocked(now)
	s.scheduleLocked(now)
	return cloneOverride(entry)
}

// Clear removes a location's override and reports whether one was held.
func (s *overrideStore) Clear(lat, lon float64) (*stationOverride, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := overrideKey(lat, lon)
	entry, ok := s.entries[key]
	delete(s.entries, key)
	s.scheduleLocked(s.now())
	return entry, ok
}

// Lookup returns the live override nearest a location within its radius, or
// nil when none covers it.
func (s *overrideStore) Lookup(lat, lon float64) *stationOverride {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	var best *stationOverride
	bestDist := math.Inf(1)
	for _, entry := range s.entries {
		d := distanceKm(lat, lon, entry.Lat, entry.Lon)
		if d > entry.RadiusKm || d >= bestDist {
			continue
		}
		live := false
		for _, v := range entry.Values {
			if now.Before(v.ExpiresAt) {
				live = true
				break
			}
		}
		if live {
			best, bestDist = entry, d
		}
	}
	if best == nil {
		return nil
	}
	out := cloneOverride(best)
	for id, v := range out.Values {
		if !now.Before(v.ExpiresAt) {
			delete(out.Values, id)
		}
	}
	return out
}

// Stop cancels the pending expiry callback.
func (s *overrideStore) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
}

// pruneLocked drops expired metrics and empty entries, returning the
// locations that lost metrics.
func (s *overrideStore) pruneLocked(now time.Time) []*stationOverride {
	var changed []*stationOverride
	for key, entry := range s.entries {
		dropped := false
		for id, v := range entry.Values {
			if !now.Before(v.ExpiresAt) {
				delete(entry.Values, id)
				dropped = true
			}
		}
		if dropped {
			changed = append(changed, cloneOverride(entry))
		}
		if len(entry.Values) == 0 {
			delete(s.entries, key)
		}
	}
	return changed
}

// scheduleLocked arms the timer for the earliest expiry still pending.
func (s *overrideStore) scheduleLocked(now time.Time) {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	var next time.Time
	for _, entry := range s.entries {
		for _, v := range entry.Values {
			if next.IsZero() || v.ExpiresAt.Before(next) {
				next = v.ExpiresAt
			}
		}
	}
	if next.IsZero() {
		return
	}
	s.timer = time.AfterFunc(next.Sub(now), s.expire)
}

func (s *overrideStore) expire() {
	s.mu.Lock()
	now := s.now()
	changed := s.pruneLocked(now)
	s.scheduleLocked(now)
	s.mu.Unlock()

	if s.expired == nil {
		return
	}
	for _, entry := range changed {
		s.expired(entry.Lat, entry.Lon, entry.RadiusKm)
	}
}

func cloneOverride(entry *stationOverride) *stationOverride {
	out := *entry
	out.Values = make(map[string]overrideValue, len(entry.Values))
	for id, v := range entry.Values {
		out.Values[id] = v
	}
	return &out
}

// distanceKm is the great-circle distance between two coordinates.
func distanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371.0
	rad := math.Pi / 180
	dLat := (lat2 - lat1) * rad
	dLon := (lon2 - lon1) * rad
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(lat1*rad)*math.Cos(lat2*rad)*math.Sin(dLon/2)*math.Sin(dLon/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// applyOverride returns a copy of data with the override's live readings in
// place of the provider's current observation. The cached forecast is never
// modified, so an expired reading reverts on the next request.
func applyOverride(data *ForecastData, ov *stationOverride) *ForecastData {
	if data == nil || data.Current == nil || ov == nil || len(ov.Values) == 0 {
		return data
	}
	out := *data
	cur := *data.Current
	out.Current = &cur
	for id, v := range ov.Values {
		switch id {
		case "temp":
			cur.Temp = v.Value
		case "feels_like":
			cur.FeelsLike = v.Value
		case "dew_point":
			cur.DewPoint = v.Value
		case "humidity":
			cur.Humidity = int(math.Round(v.Value))
		case "pressure":
			cur.Pressure = int(math.Round(v.Value))
		case "wind_speed":
			cur.WindSpeed = v.Value
		case "wind_deg":
			cur.WindDeg = int(math.Round(v.Value)) % 360
		case "uvi":
			cur.UVI = v.Value
		}
	}
	windows := make(map[string]overrideWindow, len(ov.Values))
	for id, v := range ov.Values {
		windows[id] = overrideWindow{UpdatedAt: v.UpdatedAt, ExpiresAt: v.ExpiresAt}
	}
	out.Override = &ForecastOverride{
		Source: ov.Label, Latitude: ov.Lat, Longitude: ov.Lon, Metrics: windows,
	}
	return &out
}

// withOverride applies the override covering a location, if any.
func (p *WeatherPlugin) withOverride(data *ForecastData, lat, lon float64) *ForecastData {
	if p.overrides == nil {
		return data
	}
	return applyOverride(data, p.overrides.Lookup(roundCoord(lat), roundCoord(lon)))
}

// notifyOverride tells every watched forecast within an override's radius to
// refetch. Widgets ignore the payload and read the forecast again, which now
// carries the new or expired readings.
func (p *WeatherPlugin) notifyOverride(lat, lon, radiusKm float64) {
	if p.host == nil || p.scheduler == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	payload, _ := json.Marshal(map[string]any{"latitude": lat, "longitude": lon})
	for _, w := range p.scheduler.Watched() {
		if distanceKm(lat, lon, w.Lat, w.Lon) > radiusKm {
			continue
		}
		topic := fmt.Sprintf("weather:forecast:%s:%s:%s", w.Provider, formatCoord(w.Lat), formatCoord(w.Lon))
		if err := p.host.Publish(ctx, topic, "override_update", payload); err != nil {
			log.Printf("weather: publishing override update to %s: %v", topic, err)
		}
	}
}
