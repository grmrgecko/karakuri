package main

import "context"

// Provider abstracts a weather data source for forecast retrieval and geocoding.
type Provider interface {
	FetchForecast(ctx context.Context, lat, lon float64, lang string) (*ForecastData, error)
	GeoSearch(ctx context.Context, query, lang string) ([]GeoResult, error)
	ReverseGeo(ctx context.Context, lat, lon float64, lang string) ([]GeoResult, error)
}
