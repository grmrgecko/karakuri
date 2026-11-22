package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

// OpenWeatherMapProvider implements the Provider interface using the OWM One Call API 3.0.
type OpenWeatherMapProvider struct {
	apiKey     string
	httpClient *http.Client
}

// NewOpenWeatherMapProvider creates a provider configured with the given API key.
func NewOpenWeatherMapProvider(apiKey string) *OpenWeatherMapProvider {
	return &OpenWeatherMapProvider{
		apiKey: apiKey,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchForecast retrieves the full forecast from the One Call API 3.0, always in metric units.
func (p *OpenWeatherMapProvider) FetchForecast(ctx context.Context, lat, lon float64, lang string) (*ForecastData, error) {
	if lang == "" {
		lang = "en"
	}

	u := fmt.Sprintf(
		"https://api.openweathermap.org/data/3.0/onecall?lat=%f&lon=%f&units=metric&lang=%s&appid=%s",
		lat, lon, url.QueryEscape(lang), url.QueryEscape(p.apiKey),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching forecast: %w", sdk.WithoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("owm API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw owmOneCallResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	return mapOWMResponse(&raw), nil
}

// GeoSearch queries the OWM Geocoding API for location matches.
func (p *OpenWeatherMapProvider) GeoSearch(ctx context.Context, query, lang string) ([]GeoResult, error) {
	u := fmt.Sprintf(
		"https://api.openweathermap.org/geo/1.0/direct?q=%s&limit=10&appid=%s",
		url.QueryEscape(query), url.QueryEscape(p.apiKey),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching geocoding: %w", sdk.WithoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("owm geocoding API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw []owmGeoResult
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding geocoding response: %w", err)
	}

	results := make([]GeoResult, 0, len(raw))
	for _, r := range raw {
		results = append(results, GeoResult(r))
	}

	return results, nil
}

// ReverseGeo resolves coordinates to a place name using the OWM reverse geocoding endpoint.
func (p *OpenWeatherMapProvider) ReverseGeo(ctx context.Context, lat, lon float64, lang string) ([]GeoResult, error) {
	u := fmt.Sprintf(
		"https://api.openweathermap.org/geo/1.0/reverse?lat=%f&lon=%f&limit=1&appid=%s",
		lat, lon, url.QueryEscape(p.apiKey),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching reverse geocoding: %w", sdk.WithoutURL(err))
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("owm reverse geocoding API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw []owmGeoResult
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding reverse geocoding response: %w", err)
	}

	results := make([]GeoResult, 0, len(raw))
	for _, r := range raw {
		results = append(results, GeoResult(r))
	}

	return results, nil
}

// OWM API response types for JSON deserialization.

type owmOneCallResponse struct {
	Current  owmCurrent    `json:"current"`
	Minutely []owmMinutely `json:"minutely"`
	Hourly   []owmHourly   `json:"hourly"`
	Daily    []owmDaily    `json:"daily"`
	Alerts   []owmAlert    `json:"alerts"`
}

type owmMinutely struct {
	Dt            int64   `json:"dt"`
	Precipitation float64 `json:"precipitation"`
}

type owmWeatherEntry struct {
	ID          int    `json:"id"`
	Main        string `json:"main"`
	Description string `json:"description"`
	Icon        string `json:"icon"`
}

type owmCurrent struct {
	Dt         int64             `json:"dt"`
	Sunrise    int64             `json:"sunrise"`
	Sunset     int64             `json:"sunset"`
	Temp       float64           `json:"temp"`
	FeelsLike  float64           `json:"feels_like"`
	Humidity   int               `json:"humidity"`
	WindSpeed  float64           `json:"wind_speed"`
	WindDeg    int               `json:"wind_deg"`
	UVI        float64           `json:"uvi"`
	Clouds     int               `json:"clouds"`
	Visibility int               `json:"visibility"`
	Pressure   int               `json:"pressure"`
	DewPoint   float64           `json:"dew_point"`
	Weather    []owmWeatherEntry `json:"weather"`
}

type owmHourly struct {
	Dt        int64             `json:"dt"`
	Temp      float64           `json:"temp"`
	FeelsLike float64           `json:"feels_like"`
	Humidity  int               `json:"humidity"`
	WindSpeed float64           `json:"wind_speed"`
	WindDeg   int               `json:"wind_deg"`
	Pop       float64           `json:"pop"`
	Weather   []owmWeatherEntry `json:"weather"`
}

type owmDaily struct {
	Dt        int64             `json:"dt"`
	Sunrise   int64             `json:"sunrise"`
	Sunset    int64             `json:"sunset"`
	Moonrise  int64             `json:"moonrise"`
	Moonset   int64             `json:"moonset"`
	MoonPhase float64           `json:"moon_phase"`
	Humidity  int               `json:"humidity"`
	WindSpeed float64           `json:"wind_speed"`
	Pop       float64           `json:"pop"`
	Summary   string            `json:"summary"`
	Temp      owmDailyTemp      `json:"temp"`
	Weather   []owmWeatherEntry `json:"weather"`
}

type owmDailyTemp struct {
	Min float64 `json:"min"`
	Max float64 `json:"max"`
}

type owmAlert struct {
	Event       string `json:"event"`
	Description string `json:"description"`
	Start       int64  `json:"start"`
	End         int64  `json:"end"`
}

type owmGeoResult struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Country string  `json:"country"`
	State   string  `json:"state"`
}

// mapOWMResponse converts the raw OWM response into the normalized ForecastData model.
func mapOWMResponse(raw *owmOneCallResponse) *ForecastData {
	data := &ForecastData{
		FetchedAt: time.Now().Unix(),
	}

	// Current conditions.
	c := raw.Current
	current := &CurrentWeather{
		Dt:         c.Dt,
		Sunrise:    c.Sunrise,
		Sunset:     c.Sunset,
		Temp:       c.Temp,
		FeelsLike:  c.FeelsLike,
		Humidity:   c.Humidity,
		WindSpeed:  c.WindSpeed,
		WindDeg:    c.WindDeg,
		UVI:        c.UVI,
		Clouds:     c.Clouds,
		Visibility: c.Visibility,
		Pressure:   c.Pressure,
		DewPoint:   c.DewPoint,
	}
	if len(c.Weather) > 0 {
		w := c.Weather[0]
		current.WeatherCode = fmt.Sprintf("%d", w.ID)
		current.Description = w.Description
		current.Icon = w.Icon
	}
	data.Current = current

	// Minutely precipitation (up to 61 entries).
	for _, m := range raw.Minutely {
		data.Minutely = append(data.Minutely, MinutelyWeather(m))
	}

	// Hourly forecast (up to 48 entries).
	for _, h := range raw.Hourly {
		entry := HourlyWeather{
			Dt:        h.Dt,
			Temp:      h.Temp,
			FeelsLike: h.FeelsLike,
			Humidity:  h.Humidity,
			WindSpeed: h.WindSpeed,
			WindDeg:   h.WindDeg,
			Pop:       h.Pop,
		}
		if len(h.Weather) > 0 {
			w := h.Weather[0]
			entry.WeatherCode = fmt.Sprintf("%d", w.ID)
			entry.Description = w.Description
			entry.Icon = w.Icon
		}
		data.Hourly = append(data.Hourly, entry)
	}

	// Daily forecast (up to 8 entries).
	for _, d := range raw.Daily {
		entry := DailyWeather{
			Dt:        d.Dt,
			Sunrise:   d.Sunrise,
			Sunset:    d.Sunset,
			Moonrise:  d.Moonrise,
			Moonset:   d.Moonset,
			MoonPhase: d.MoonPhase,
			TempMin:   d.Temp.Min,
			TempMax:   d.Temp.Max,
			Humidity:  d.Humidity,
			WindSpeed: d.WindSpeed,
			Pop:       d.Pop,
			Summary:   d.Summary,
		}
		if len(d.Weather) > 0 {
			w := d.Weather[0]
			entry.WeatherCode = fmt.Sprintf("%d", w.ID)
			entry.Description = w.Description
			entry.Icon = w.Icon
		}
		data.Daily = append(data.Daily, entry)
	}

	// Alerts.
	for _, a := range raw.Alerts {
		data.Alerts = append(data.Alerts, WeatherAlert(a))
	}

	return data
}
