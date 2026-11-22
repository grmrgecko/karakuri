package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk/httpsdk"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"
	"github.com/swaggest/usecase"
	"github.com/swaggest/usecase/status"
)

// statusError is a simple error type for returning user-facing messages.
type statusError string

func (e statusError) Error() string { return string(e) }

func errProviderNotFound(name string) error {
	return statusError("unknown provider: " + name)
}

// Input/output types for the forecast endpoint.

type forecastInput struct {
	Provider string  `path:"provider" description:"Weather provider."`
	Lat      float64 `path:"lat" description:"Latitude."`
	Lon      float64 `path:"lon" description:"Longitude."`
	Units    string  `query:"units" description:"Unit system (metric or imperial)."`
	Lang     string  `query:"lang" description:"Language code for descriptions."`
}

type forecastOutput = apiutil.Success[*ForecastData]

func (p *WeatherPlugin) forecastInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in forecastInput, out *forecastOutput) error {
		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		lat := in.Lat
		lon := in.Lon

		lang := in.Lang
		if lang == "" {
			lang = p.language
		}

		// Check local cache first. Polling refreshes the location's entry in
		// the plugin's language, so another language keeps its own entry.
		polledKey := fmt.Sprintf("forecast:%s", locationKey(in.Provider, lat, lon))
		cacheKey := polledKey
		if lang != p.language {
			cacheKey = polledKey + ":lang:" + lang
		}
		var data *ForecastData

		if cached, found := p.cache.Get(cacheKey); found {
			data, _ = cached.(*ForecastData)
		}
		// Nothing refreshes another language's entry, so it is read again
		// once polling has fetched the location after it.
		if data != nil && cacheKey != polledKey {
			if cached, found := p.cache.Get(polledKey); found {
				if polled, _ := cached.(*ForecastData); polled != nil && polled.FetchedAt > data.FetchedAt {
					data = nil
				}
			}
		}

		// Cache miss: perform an immediate fetch.
		if data == nil {
			fetched, fetchErr := prov.FetchForecast(ctx, roundCoord(lat), roundCoord(lon), lang)
			if fetchErr != nil {
				return status.Wrap(fetchErr, status.Internal)
			}
			data = fetched
			ttl := p.pollInterval * 2
			p.cache.Set(cacheKey, data, ttl)
		}

		// Ensure polling is active for this location.
		p.scheduler.EnsurePolling(in.Provider, lat, lon)

		// Station readings replace the provider's on a copy, so an expired
		// reading reverts without refetching.
		data = p.withOverride(data, lat, lon)

		// Unit conversion on egress.
		if in.Units == "imperial" {
			data = convertToImperial(data)
		}

		out.Data = data
		return nil
	})
	u.SetTitle("Get Forecast")
	u.SetDescription("Returns weather forecast data for the given location from the specified provider.")
	u.SetTags("Weather")
	return u
}

// Input/output types for the geo search endpoint.

type geoSearchInput struct {
	Provider string `path:"provider" description:"Weather provider."`
	Q        string `query:"q" required:"true" description:"Search query for location lookup."`
	Lang     string `query:"lang" description:"Language code for results."`
}

type geoSearchOutput = apiutil.Success[[]GeoResult]

func (p *WeatherPlugin) geoSearchInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in geoSearchInput, out *geoSearchOutput) error {
		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		if in.Q == "" {
			return status.Wrap(errors.New("query parameter 'q' is required"), status.InvalidArgument)
		}

		lang := in.Lang
		if lang == "" {
			lang = p.language
		}

		results, err := prov.GeoSearch(ctx, in.Q, lang)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		out.Data = results
		return nil
	})
	u.SetTitle("Geo Search")
	u.SetDescription("Searches for locations by name using the specified provider's geocoding service.")
	u.SetTags("Weather")
	return u
}

// Input/output types for the reverse geocoding endpoint.

type reverseGeoInput struct {
	Provider string  `path:"provider" description:"Weather provider."`
	Lat      float64 `path:"lat" description:"Latitude."`
	Lon      float64 `path:"lon" description:"Longitude."`
	Lang     string  `query:"lang" description:"Language code for results."`
}

type reverseGeoOutput = apiutil.Success[[]GeoResult]

func (p *WeatherPlugin) reverseGeoInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in reverseGeoInput, out *reverseGeoOutput) error {
		prov, ok := p.providers[in.Provider]
		if !ok {
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		lang := in.Lang
		if lang == "" {
			lang = p.language
		}

		results, err := prov.ReverseGeo(ctx, in.Lat, in.Lon, lang)
		if err != nil {
			return status.Wrap(err, status.Internal)
		}

		out.Data = results
		return nil
	})
	u.SetTitle("Reverse Geocode")
	u.SetDescription("Resolves geographic coordinates to a place name using the specified provider.")
	u.SetTags("Weather")
	return u
}

// Input/output types for the config schema endpoint.

type configSchemaInput struct {
	Provider string `path:"provider" description:"Weather provider."`
}

// ConfigSchemaData holds the JSON schema for a provider's configuration.
type ConfigSchemaData struct {
	Type       string                 `json:"type" description:"JSON schema type."`
	Properties map[string]interface{} `json:"properties" description:"JSON schema properties for the provider config."`
}

type configSchemaOutput = apiutil.Success[ConfigSchemaData]

func (p *WeatherPlugin) configSchemaInteractor() usecase.Interactor {
	u := usecase.NewInteractor(func(ctx context.Context, in configSchemaInput, out *configSchemaOutput) error {
		var data ConfigSchemaData
		data.Type = "object"

		switch in.Provider {
		case "openweathermap":
			data.Properties = map[string]interface{}{
				"latitude": map[string]interface{}{
					"type":  "number",
					"title": "Latitude",
				},
				"longitude": map[string]interface{}{
					"type":  "number",
					"title": "Longitude",
				},
				"locationName": map[string]interface{}{
					"type":  "string",
					"title": "Location Name",
				},
				"units": map[string]interface{}{
					"type":  "string",
					"title": "Units",
					"enum":  []string{"metric", "imperial"},
				},
				"language": map[string]interface{}{
					"type":    "string",
					"title":   "Language",
					"default": "en",
				},
			}
		case "openmeteo":
			data.Properties = map[string]interface{}{
				"latitude": map[string]interface{}{
					"type":  "number",
					"title": "Latitude",
				},
				"longitude": map[string]interface{}{
					"type":  "number",
					"title": "Longitude",
				},
				"locationName": map[string]interface{}{
					"type":  "string",
					"title": "Location Name",
				},
				"units": map[string]interface{}{
					"type":  "string",
					"title": "Units",
					"enum":  []string{"metric", "imperial"},
				},
				"language": map[string]interface{}{
					"type":    "string",
					"title":   "Language",
					"default": "en",
				},
			}
		default:
			return status.Wrap(errProviderNotFound(in.Provider), status.NotFound)
		}

		out.Data = data
		return nil
	})
	u.SetTitle("Get Config Schema")
	u.SetDescription("Returns a JSON schema describing the per-widget configuration fields for a provider.")
	u.SetTags("Weather")
	return u
}

// newService builds the weather plugin's own HTTP API.
func newService(p *WeatherPlugin, host plugin.HostAPI) *web.Service {
	s := httpsdk.NewService("Weather Plugin API", "0.1.0",
		"Weather data provider supporting OpenWeatherMap and Open-Meteo.")

	// Config schema endpoint has no permission requirement.
	s.Method(http.MethodGet, "/{provider}/config_schema",
		nethttp.NewHandler(p.configSchemaInteractor()))

	// Weather data read endpoints.
	s.With(httpsdk.RequirePermission(host, "api:weather:read")).
		Method(http.MethodGet, "/{provider}/forecast/{lat}:{lon}",
			nethttp.NewHandler(p.forecastInteractor()))
	s.With(httpsdk.RequirePermission(host, "api:weather:read")).
		Method(http.MethodGet, "/{provider}/geo",
			nethttp.NewHandler(p.geoSearchInteractor()))
	s.With(httpsdk.RequirePermission(host, "api:weather:read")).
		Method(http.MethodGet, "/{provider}/geo/reverse/{lat}:{lon}",
			nethttp.NewHandler(p.reverseGeoInteractor()))

	return s
}
