package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// OpenMeteoProvider implements the Provider interface using the Open-Meteo API.
type OpenMeteoProvider struct {
	httpClient *http.Client
}

// NewOpenMeteoProvider creates a provider for the Open-Meteo weather API.
func NewOpenMeteoProvider() *OpenMeteoProvider {
	return &OpenMeteoProvider{
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// FetchForecast retrieves the full forecast from the Open-Meteo API in metric units.
func (p *OpenMeteoProvider) FetchForecast(ctx context.Context, lat, lon float64, lang string) (*ForecastData, error) {
	const (
		currentVars = "temperature_2m,relative_humidity_2m,apparent_temperature,weather_code,cloud_cover,pressure_msl,wind_speed_10m,wind_direction_10m,uv_index,visibility,dew_point_2m,is_day"
		hourlyVars  = "temperature_2m,relative_humidity_2m,apparent_temperature,precipitation_probability,weather_code,wind_speed_10m,wind_direction_10m,is_day"
		dailyVars   = "weather_code,temperature_2m_max,temperature_2m_min,sunrise,sunset,precipitation_probability_max,wind_speed_10m_max,uv_index_max"
	)

	u := fmt.Sprintf(
		"https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f&current=%s&hourly=%s&daily=%s&wind_speed_unit=ms&timeformat=unixtime&timezone=auto&forecast_days=8&forecast_hours=48",
		lat, lon, currentVars, hourlyVars, dailyVars,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching forecast: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open-meteo API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw omForecastResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding response: %w", err)
	}

	data := mapOMResponse(&raw)

	// Air quality comes from a separate API with its own outages, and a
	// forecast without it is still worth serving.
	aq, err := p.fetchAirQuality(ctx, lat, lon)
	if err != nil {
		log.Printf("weather: air quality fetch for %.4f,%.4f: %v", lat, lon, err)
	} else {
		data.AirQuality = aq
	}

	return data, nil
}

// fetchAirQuality retrieves current air quality, pollen, and the next 24
// hours of AQI from the Open-Meteo air quality API.
func (p *OpenMeteoProvider) fetchAirQuality(ctx context.Context, lat, lon float64) (*AirQuality, error) {
	const (
		currentVars = "us_aqi,european_aqi,pm2_5,pm10,ozone,nitrogen_dioxide,carbon_monoxide,sulphur_dioxide,uv_index,alder_pollen,birch_pollen,grass_pollen,mugwort_pollen,olive_pollen,ragweed_pollen"
		hourlyVars  = "us_aqi,european_aqi"
	)

	u := fmt.Sprintf(
		"https://air-quality-api.open-meteo.com/v1/air-quality?latitude=%f&longitude=%f&current=%s&hourly=%s&forecast_hours=24&timeformat=unixtime&timezone=auto",
		lat, lon, currentVars, hourlyVars,
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching air quality: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open-meteo air quality API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw omAirQualityResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding air quality response: %w", err)
	}

	return mapOMAirQuality(&raw), nil
}

// GeoSearch queries the Open-Meteo Geocoding API for location matches.
func (p *OpenMeteoProvider) GeoSearch(ctx context.Context, query, lang string) ([]GeoResult, error) {
	if lang == "" {
		lang = "en"
	}

	u := fmt.Sprintf(
		"https://geocoding-api.open-meteo.com/v1/search?name=%s&count=10&language=%s",
		url.QueryEscape(query), url.QueryEscape(lang),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching geocoding: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("open-meteo geocoding API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw omGeoResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding geocoding response: %w", err)
	}

	if raw.Results == nil {
		return []GeoResult{}, nil
	}

	results := make([]GeoResult, 0, len(raw.Results))
	for _, r := range raw.Results {
		results = append(results, GeoResult{
			Name:    r.Name,
			Lat:     r.Latitude,
			Lon:     r.Longitude,
			Country: r.Country,
			State:   r.Admin1,
		})
	}

	return results, nil
}

// PostalSearch resolves a postal code to the coordinates of the place that
// carries it. Open-Meteo answers a postal code with the populated place whose
// postcodes include it, so the result is the town's centre rather than the
// code's own centroid; country narrows codes shared between countries.
func (p *OpenMeteoProvider) PostalSearch(ctx context.Context, code, country string) (GeoResult, error) {
	u := fmt.Sprintf(
		"https://geocoding-api.open-meteo.com/v1/search?name=%s&count=10&language=en",
		url.QueryEscape(code),
	)
	if country != "" {
		u += "&countryCode=" + url.QueryEscape(country)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return GeoResult{}, fmt.Errorf("creating request: %w", err)
	}
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return GeoResult{}, fmt.Errorf("fetching geocoding: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return GeoResult{}, fmt.Errorf("open-meteo geocoding API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw omGeoResponse
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return GeoResult{}, fmt.Errorf("decoding geocoding response: %w", err)
	}
	match, ok := matchPostal(&raw, code)
	if !ok {
		return GeoResult{}, fmt.Errorf("no place found for postal code %q", code)
	}
	return match, nil
}

// matchPostal picks the first result listing the postal code. A name search
// also matches places merely named like the code, which must not be taken
// for it.
func matchPostal(raw *omGeoResponse, code string) (GeoResult, bool) {
	for _, r := range raw.Results {
		for _, pc := range r.Postcodes {
			if strings.EqualFold(pc, code) {
				return GeoResult{Name: r.Name, Lat: r.Latitude, Lon: r.Longitude, Country: r.Country, State: r.Admin1}, true
			}
		}
	}
	return GeoResult{}, false
}

// ReverseGeo resolves coordinates to a place name using the Nominatim reverse geocoding API,
// since Open-Meteo does not provide a native reverse geocoding endpoint.
func (p *OpenMeteoProvider) ReverseGeo(ctx context.Context, lat, lon float64, lang string) ([]GeoResult, error) {
	if lang == "" {
		lang = "en"
	}

	u := fmt.Sprintf(
		"https://nominatim.openstreetmap.org/reverse?lat=%f&lon=%f&format=json&zoom=10&accept-language=%s",
		lat, lon, url.QueryEscape(lang),
	)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", "Karakuri/1.0")

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching reverse geocoding: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("nominatim API returned %d: %s", resp.StatusCode, string(body))
	}

	var raw nominatimReverseResult
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding reverse geocoding response: %w", err)
	}

	// Determine the best display name from the address hierarchy.
	name := raw.Address.City
	if name == "" {
		name = raw.Address.Town
	}
	if name == "" {
		name = raw.Address.Village
	}
	if name == "" {
		name = raw.Name
	}

	parsedLat, _ := strconv.ParseFloat(raw.Lat, 64)
	parsedLon, _ := strconv.ParseFloat(raw.Lon, 64)

	return []GeoResult{{
		Name:    name,
		Lat:     parsedLat,
		Lon:     parsedLon,
		Country: raw.Address.CountryCode,
		State:   raw.Address.State,
	}}, nil
}

// nominatimReverseResult represents the JSON response from the Nominatim reverse geocoding API.
type nominatimReverseResult struct {
	Name    string `json:"name"`
	Lat     string `json:"lat"`
	Lon     string `json:"lon"`
	Address struct {
		City        string `json:"city"`
		Town        string `json:"town"`
		Village     string `json:"village"`
		State       string `json:"state"`
		Country     string `json:"country"`
		CountryCode string `json:"country_code"`
	} `json:"address"`
}

// Open-Meteo API response types. The forecast API returns columnar arrays
// rather than per-entry objects.

type omForecastResponse struct {
	Current *omCurrent `json:"current"`
	Hourly  *omHourly  `json:"hourly"`
	Daily   *omDaily   `json:"daily"`
}

type omCurrent struct {
	Time               int64   `json:"time"`
	Temperature2m      float64 `json:"temperature_2m"`
	RelativeHumidity2m int     `json:"relative_humidity_2m"`
	ApparentTemp       float64 `json:"apparent_temperature"`
	WeatherCode        int     `json:"weather_code"`
	CloudCover         int     `json:"cloud_cover"`
	PressureMSL        float64 `json:"pressure_msl"`
	WindSpeed10m       float64 `json:"wind_speed_10m"`
	WindDirection10m   int     `json:"wind_direction_10m"`
	UVIndex            float64 `json:"uv_index"`
	Visibility         float64 `json:"visibility"`
	DewPoint2m         float64 `json:"dew_point_2m"`
	IsDay              int     `json:"is_day"`
}

type omHourly struct {
	Time                     []int64   `json:"time"`
	Temperature2m            []float64 `json:"temperature_2m"`
	RelativeHumidity2m       []int     `json:"relative_humidity_2m"`
	ApparentTemp             []float64 `json:"apparent_temperature"`
	PrecipitationProbability []int     `json:"precipitation_probability"`
	WeatherCode              []int     `json:"weather_code"`
	WindSpeed10m             []float64 `json:"wind_speed_10m"`
	WindDirection10m         []int     `json:"wind_direction_10m"`
	IsDay                    []int     `json:"is_day"`
}

type omDaily struct {
	Time                        []int64   `json:"time"`
	WeatherCode                 []int     `json:"weather_code"`
	Temperature2mMax            []float64 `json:"temperature_2m_max"`
	Temperature2mMin            []float64 `json:"temperature_2m_min"`
	Sunrise                     []int64   `json:"sunrise"`
	Sunset                      []int64   `json:"sunset"`
	PrecipitationProbabilityMax []int     `json:"precipitation_probability_max"`
	WindSpeed10mMax             []float64 `json:"wind_speed_10m_max"`
	UVIndexMax                  []float64 `json:"uv_index_max"`
}

type omAirQualityResponse struct {
	Current *omAirQualityCurrent `json:"current"`
	Hourly  *omAirQualityHourly  `json:"hourly"`
}

// omAirQualityCurrent uses pointers because the API sends null for readings
// it has no model for at the location, which is every pollen field outside
// Europe.
type omAirQualityCurrent struct {
	Time            int64    `json:"time"`
	USAQI           *float64 `json:"us_aqi"`
	EuropeanAQI     *float64 `json:"european_aqi"`
	PM25            *float64 `json:"pm2_5"`
	PM10            *float64 `json:"pm10"`
	Ozone           *float64 `json:"ozone"`
	NitrogenDioxide *float64 `json:"nitrogen_dioxide"`
	CarbonMonoxide  *float64 `json:"carbon_monoxide"`
	SulphurDioxide  *float64 `json:"sulphur_dioxide"`
	UVIndex         *float64 `json:"uv_index"`
	AlderPollen     *float64 `json:"alder_pollen"`
	BirchPollen     *float64 `json:"birch_pollen"`
	GrassPollen     *float64 `json:"grass_pollen"`
	MugwortPollen   *float64 `json:"mugwort_pollen"`
	OlivePollen     *float64 `json:"olive_pollen"`
	RagweedPollen   *float64 `json:"ragweed_pollen"`
}

type omAirQualityHourly struct {
	Time        []int64    `json:"time"`
	USAQI       []*float64 `json:"us_aqi"`
	EuropeanAQI []*float64 `json:"european_aqi"`
}

type omGeoResponse struct {
	Results []omGeoResult `json:"results"`
}

type omGeoResult struct {
	Name      string   `json:"name"`
	Latitude  float64  `json:"latitude"`
	Longitude float64  `json:"longitude"`
	Country   string   `json:"country"`
	Admin1    string   `json:"admin1"`
	Postcodes []string `json:"postcodes"`
}

// mapOMResponse converts the Open-Meteo columnar response into the normalized ForecastData model.
func mapOMResponse(raw *omForecastResponse) *ForecastData {
	data := &ForecastData{
		FetchedAt: time.Now().Unix(),
	}

	// Pull sunrise/sunset from the first daily entry for current conditions.
	var todaySunrise, todaySunset int64
	if raw.Daily != nil && len(raw.Daily.Sunrise) > 0 {
		todaySunrise = raw.Daily.Sunrise[0]
		todaySunset = raw.Daily.Sunset[0]
	}

	// Current conditions.
	if raw.Current != nil {
		c := raw.Current
		data.Current = &CurrentWeather{
			Dt:          c.Time,
			Sunrise:     todaySunrise,
			Sunset:      todaySunset,
			Temp:        c.Temperature2m,
			FeelsLike:   c.ApparentTemp,
			Humidity:    c.RelativeHumidity2m,
			WindSpeed:   c.WindSpeed10m,
			WindDeg:     c.WindDirection10m,
			UVI:         c.UVIndex,
			Clouds:      c.CloudCover,
			Visibility:  int(c.Visibility),
			Pressure:    int(c.PressureMSL),
			DewPoint:    c.DewPoint2m,
			WeatherCode: fmt.Sprintf("%d", c.WeatherCode),
			Description: wmoDescription(c.WeatherCode),
			Icon:        wmoIcon(c.WeatherCode, c.IsDay == 1),
		}
	}

	// Hourly forecast (up to 48 entries).
	if raw.Hourly != nil {
		h := raw.Hourly
		n := len(h.Time)
		data.Hourly = make([]HourlyWeather, n)
		for i := 0; i < n; i++ {
			wc := indexInt(h.WeatherCode, i)
			isDay := indexInt(h.IsDay, i) == 1
			data.Hourly[i] = HourlyWeather{
				Dt:          h.Time[i],
				Temp:        indexFloat(h.Temperature2m, i),
				FeelsLike:   indexFloat(h.ApparentTemp, i),
				Humidity:    indexInt(h.RelativeHumidity2m, i),
				WindSpeed:   indexFloat(h.WindSpeed10m, i),
				WindDeg:     indexInt(h.WindDirection10m, i),
				Pop:         float64(indexInt(h.PrecipitationProbability, i)) / 100.0,
				WeatherCode: fmt.Sprintf("%d", wc),
				Description: wmoDescription(wc),
				Icon:        wmoIcon(wc, isDay),
			}
		}
	}

	// Daily forecast (up to 8 entries).
	if raw.Daily != nil {
		d := raw.Daily
		n := len(d.Time)
		data.Daily = make([]DailyWeather, n)
		for i := 0; i < n; i++ {
			wc := indexInt(d.WeatherCode, i)
			data.Daily[i] = DailyWeather{
				Dt:          d.Time[i],
				Sunrise:     indexInt64(d.Sunrise, i),
				Sunset:      indexInt64(d.Sunset, i),
				TempMax:     indexFloat(d.Temperature2mMax, i),
				TempMin:     indexFloat(d.Temperature2mMin, i),
				WindSpeed:   indexFloat(d.WindSpeed10mMax, i),
				Pop:         float64(indexInt(d.PrecipitationProbabilityMax, i)) / 100.0,
				WeatherCode: fmt.Sprintf("%d", wc),
				Description: wmoDescription(wc),
				Icon:        wmoIcon(wc, true),
			}
		}
	}

	return data
}

// mapOMAirQuality converts the air quality response, keeping null readings
// absent and leaving out the pollen block when the location has none.
func mapOMAirQuality(raw *omAirQualityResponse) *AirQuality {
	aq := &AirQuality{}

	if c := raw.Current; c != nil {
		aq.Dt = c.Time
		aq.USAQI = c.USAQI
		aq.EuropeanAQI = c.EuropeanAQI
		aq.PM25 = c.PM25
		aq.PM10 = c.PM10
		aq.Ozone = c.Ozone
		aq.NitrogenDioxide = c.NitrogenDioxide
		aq.CarbonMonoxide = c.CarbonMonoxide
		aq.SulphurDioxide = c.SulphurDioxide
		aq.UVIndex = c.UVIndex

		pollen := Pollen{
			Alder:   c.AlderPollen,
			Birch:   c.BirchPollen,
			Grass:   c.GrassPollen,
			Mugwort: c.MugwortPollen,
			Olive:   c.OlivePollen,
			Ragweed: c.RagweedPollen,
		}
		if pollen != (Pollen{}) {
			aq.Pollen = &pollen
		}
	}

	if h := raw.Hourly; h != nil {
		aq.Hourly = make([]AirQualityHour, 0, len(h.Time))
		for i, t := range h.Time {
			aq.Hourly = append(aq.Hourly, AirQualityHour{
				Dt:          t,
				USAQI:       indexFloatPtr(h.USAQI, i),
				EuropeanAQI: indexFloatPtr(h.EuropeanAQI, i),
			})
		}
	}

	return aq
}

// Safe index helpers for columnar arrays that may differ in length.

func indexFloatPtr(s []*float64, i int) *float64 {
	if i < len(s) {
		return s[i]
	}
	return nil
}

func indexFloat(s []float64, i int) float64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func indexInt(s []int, i int) int {
	if i < len(s) {
		return s[i]
	}
	return 0
}

func indexInt64(s []int64, i int) int64 {
	if i < len(s) {
		return s[i]
	}
	return 0
}

// wmoDescription returns a human-readable description for a WMO weather code.
func wmoDescription(code int) string {
	switch code {
	case 0:
		return "Clear sky"
	case 1:
		return "Mainly clear"
	case 2:
		return "Partly cloudy"
	case 3:
		return "Overcast"
	case 45:
		return "Foggy"
	case 48:
		return "Depositing rime fog"
	case 51:
		return "Light drizzle"
	case 53:
		return "Moderate drizzle"
	case 55:
		return "Dense drizzle"
	case 56:
		return "Light freezing drizzle"
	case 57:
		return "Dense freezing drizzle"
	case 61:
		return "Slight rain"
	case 63:
		return "Moderate rain"
	case 65:
		return "Heavy rain"
	case 66:
		return "Light freezing rain"
	case 67:
		return "Heavy freezing rain"
	case 71:
		return "Slight snowfall"
	case 73:
		return "Moderate snowfall"
	case 75:
		return "Heavy snowfall"
	case 77:
		return "Snow grains"
	case 80:
		return "Slight rain showers"
	case 81:
		return "Moderate rain showers"
	case 82:
		return "Violent rain showers"
	case 85:
		return "Slight snow showers"
	case 86:
		return "Heavy snow showers"
	case 95:
		return "Thunderstorm"
	case 96:
		return "Thunderstorm with slight hail"
	case 99:
		return "Thunderstorm with heavy hail"
	default:
		return "Unknown"
	}
}

// wmoIcon maps a WMO weather code to an OWM-compatible icon string for
// consistent frontend rendering.
func wmoIcon(code int, isDay bool) string {
	suffix := "d"
	if !isDay {
		suffix = "n"
	}

	var base string
	switch {
	case code == 0:
		base = "01"
	case code == 1:
		base = "02"
	case code == 2:
		base = "03"
	case code == 3:
		base = "04"
	case code == 45 || code == 48:
		base = "50"
	case code >= 51 && code <= 57:
		base = "09"
	case code >= 61 && code <= 65:
		base = "10"
	case code == 66 || code == 67:
		base = "13"
	case code >= 71 && code <= 77:
		base = "13"
	case code >= 80 && code <= 82:
		base = "09"
	case code == 85 || code == 86:
		base = "13"
	case code >= 95:
		base = "11"
	default:
		base = "03"
	}

	return base + suffix
}
