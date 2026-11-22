package main

import "encoding/json"

func celsiusToFahrenheit(c float64) float64 {
	return c*9.0/5.0 + 32.0
}

func msToMph(ms float64) float64 {
	return ms * 2.23694
}

func mmToInches(mm float64) float64 {
	return mm * 0.03937
}

// convertToImperial creates a deep copy of the forecast data with temperatures in
// Fahrenheit and wind speeds in mph.
func convertToImperial(data *ForecastData) *ForecastData {
	// Deep copy via JSON round-trip to avoid mutating the cached original.
	raw, _ := json.Marshal(data)
	var out ForecastData
	_ = json.Unmarshal(raw, &out)

	if out.Current != nil {
		out.Current.Temp = celsiusToFahrenheit(out.Current.Temp)
		out.Current.FeelsLike = celsiusToFahrenheit(out.Current.FeelsLike)
		out.Current.DewPoint = celsiusToFahrenheit(out.Current.DewPoint)
		out.Current.WindSpeed = msToMph(out.Current.WindSpeed)
	}

	for i := range out.Hourly {
		out.Hourly[i].Temp = celsiusToFahrenheit(out.Hourly[i].Temp)
		out.Hourly[i].FeelsLike = celsiusToFahrenheit(out.Hourly[i].FeelsLike)
		out.Hourly[i].WindSpeed = msToMph(out.Hourly[i].WindSpeed)
	}

	for i := range out.Daily {
		out.Daily[i].TempMin = celsiusToFahrenheit(out.Daily[i].TempMin)
		out.Daily[i].TempMax = celsiusToFahrenheit(out.Daily[i].TempMax)
		out.Daily[i].WindSpeed = msToMph(out.Daily[i].WindSpeed)
	}

	for i := range out.Minutely {
		out.Minutely[i].Precipitation = mmToInches(out.Minutely[i].Precipitation)
	}

	return &out
}
