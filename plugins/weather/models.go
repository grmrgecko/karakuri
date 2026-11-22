package main

// ForecastData holds the complete weather forecast for a location.
type ForecastData struct {
	Current  *CurrentWeather   `json:"current"`
	Hourly   []HourlyWeather   `json:"hourly"`
	Daily    []DailyWeather    `json:"daily"`
	Minutely []MinutelyWeather `json:"minutely"`
	Alerts   []WeatherAlert    `json:"alerts"`
	// AirQuality is nil when the provider has no air quality data or its
	// fetch failed; the forecast is still served without it.
	AirQuality *AirQuality `json:"air_quality,omitempty"`
	// Override is set when local station readings replaced part of Current.
	Override  *ForecastOverride `json:"override,omitempty"`
	FetchedAt int64             `json:"fetched_at"`
}

// AirQuality holds current air quality and pollen readings with a short
// hourly AQI series. Each reading is a pointer because the source publishes
// null where it has no model coverage (pollen outside Europe), and a missing
// reading must not read as a clean zero. Concentrations are in µg/m³, pollen
// in grains/m³; none of them change with the unit system.
type AirQuality struct {
	Dt              int64    `json:"dt"`
	USAQI           *float64 `json:"us_aqi,omitempty"`
	EuropeanAQI     *float64 `json:"european_aqi,omitempty"`
	PM25            *float64 `json:"pm2_5,omitempty"`
	PM10            *float64 `json:"pm10,omitempty"`
	Ozone           *float64 `json:"ozone,omitempty"`
	NitrogenDioxide *float64 `json:"nitrogen_dioxide,omitempty"`
	CarbonMonoxide  *float64 `json:"carbon_monoxide,omitempty"`
	SulphurDioxide  *float64 `json:"sulphur_dioxide,omitempty"`
	UVIndex         *float64 `json:"uv_index,omitempty"`
	// Pollen is nil when the source has no pollen reading for the location.
	Pollen *Pollen `json:"pollen,omitempty"`
	// Hourly covers the next 24 hours, starting with the current hour.
	Hourly []AirQualityHour `json:"hourly,omitempty"`
}

// Pollen holds current pollen counts in grains/m³.
type Pollen struct {
	Alder   *float64 `json:"alder,omitempty"`
	Birch   *float64 `json:"birch,omitempty"`
	Grass   *float64 `json:"grass,omitempty"`
	Mugwort *float64 `json:"mugwort,omitempty"`
	Olive   *float64 `json:"olive,omitempty"`
	Ragweed *float64 `json:"ragweed,omitempty"`
}

// AirQualityHour holds one hour of the AQI forecast.
type AirQualityHour struct {
	Dt          int64    `json:"dt"`
	USAQI       *float64 `json:"us_aqi,omitempty"`
	EuropeanAQI *float64 `json:"european_aqi,omitempty"`
}

// MinutelyWeather holds precipitation data for a single minute.
type MinutelyWeather struct {
	Dt            int64   `json:"dt"`
	Precipitation float64 `json:"precipitation"`
}

// CurrentWeather holds real-time weather observations.
type CurrentWeather struct {
	Dt          int64   `json:"dt"`
	Sunrise     int64   `json:"sunrise"`
	Sunset      int64   `json:"sunset"`
	Temp        float64 `json:"temp"`
	FeelsLike   float64 `json:"feels_like"`
	Humidity    int     `json:"humidity"`
	WindSpeed   float64 `json:"wind_speed"`
	WindDeg     int     `json:"wind_deg"`
	UVI         float64 `json:"uvi"`
	Clouds      int     `json:"clouds"`
	Visibility  int     `json:"visibility"`
	Pressure    int     `json:"pressure"`
	DewPoint    float64 `json:"dew_point"`
	WeatherCode string  `json:"weather_code"`
	Description string  `json:"description"`
	Icon        string  `json:"icon"`
}

// HourlyWeather holds a single hour's forecast data.
type HourlyWeather struct {
	Dt          int64   `json:"dt"`
	Temp        float64 `json:"temp"`
	FeelsLike   float64 `json:"feels_like"`
	Humidity    int     `json:"humidity"`
	WindSpeed   float64 `json:"wind_speed"`
	WindDeg     int     `json:"wind_deg"`
	WeatherCode string  `json:"weather_code"`
	Description string  `json:"description"`
	Icon        string  `json:"icon"`
	Pop         float64 `json:"pop"`
}

// DailyWeather holds a single day's forecast data.
type DailyWeather struct {
	Dt          int64   `json:"dt"`
	Sunrise     int64   `json:"sunrise"`
	Sunset      int64   `json:"sunset"`
	Moonrise    int64   `json:"moonrise"`
	Moonset     int64   `json:"moonset"`
	MoonPhase   float64 `json:"moon_phase"`
	TempMin     float64 `json:"temp_min"`
	TempMax     float64 `json:"temp_max"`
	Humidity    int     `json:"humidity"`
	WindSpeed   float64 `json:"wind_speed"`
	WeatherCode string  `json:"weather_code"`
	Description string  `json:"description"`
	Icon        string  `json:"icon"`
	Pop         float64 `json:"pop"`
	Summary     string  `json:"summary"`
}

// WeatherAlert holds a weather warning from the data source.
type WeatherAlert struct {
	Event       string `json:"event"`
	Description string `json:"description"`
	Start       int64  `json:"start"`
	End         int64  `json:"end"`
}

// GeoResult holds a geocoding search result.
type GeoResult struct {
	Name    string  `json:"name"`
	Lat     float64 `json:"lat"`
	Lon     float64 `json:"lon"`
	Country string  `json:"country"`
	State   string  `json:"state"`
}
