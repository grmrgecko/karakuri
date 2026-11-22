// Command weather is a karakuri plugin bringing weather data to both
// dashboards and flows. It serves an HTTP API the weather widget reads,
// polls forecasts for the locations something is actually watching, and
// contributes the flow nodes in nodes.go so a forecast change can start a run.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/swaggest/rest/web"

	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk/httpsdk"
)

const defaultPollInterval = 600 // seconds

func main() { sdk.Serve(&WeatherPlugin{}) }

// WeatherPlugin is the weather data provider.
type WeatherPlugin struct {
	host         plugin.HostAPI
	scheduler    *Scheduler
	providers    map[string]Provider
	service      *web.Service
	cache        *gocache.Cache
	pollInterval time.Duration
	language     string
	overrides    *overrideStore
	openMeteo    *OpenMeteoProvider

	// postalMu guards postal, the resolved coordinates of each postal code
	// an override node has used. A station reports every minute or so, and
	// a code's place does not move between reports.
	postalMu sync.Mutex
	postal   map[string]GeoResult
}

// Manifest identifies the plugin.
func (p *WeatherPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "weather",
		Version:     "0.1.0",
		Description: "Weather forecasts for dashboards and flows, via OpenWeatherMap or Open-Meteo.",
		Author:      "James Coleman",
	}
}

// Register contributes the plugin's flow nodes. Everything else it brings —
// the HTTP API, settings, providers — arrives through the optional interfaces
// below rather than the node registrar.
func (p *WeatherPlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(&WeatherChangedNode{p: p})
	r.RegisterNode(&WeatherForecastNode{p: p})
	r.RegisterNode(&WeatherGeocodeNode{p: p})
	r.RegisterNode(&WeatherOverrideNode{p: p})
	return nil
}

// SettingsSchema declares the plugin's configuration.
func (p *WeatherPlugin) SettingsSchema() string {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"api_key": map[string]any{
				"type":        "string",
				"title":       "OpenWeatherMap API key",
				"description": "Required for the OpenWeatherMap provider; Open-Meteo needs none.",
				"format":      "password",
			},
			"poll_interval": map[string]any{
				"type":        "number",
				"title":       "Poll interval (seconds)",
				"description": "How often a watched location's forecast is refetched.",
				"default":     defaultPollInterval,
			},
			"language": map[string]any{
				"type":        "string",
				"title":       "Language",
				"description": "Language code for weather descriptions.",
				"default":     "en",
			},
		},
	}
	b, _ := json.Marshal(schema)
	return string(b)
}

// Init reads settings, builds the providers, and registers what the host needs
// to know about: the permissions gating the API, the providers a widget can
// point at, and the topic whose subscriber count drives polling.
func (p *WeatherPlugin) Init(ctx context.Context, host plugin.HostAPI) error {
	p.host = host

	apiKey, _, _ := host.GetConfig(ctx, "api_key")

	pollSec := defaultPollInterval
	if val, found, _ := host.GetConfig(ctx, "poll_interval"); found {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			pollSec = n
		}
	}
	p.pollInterval = time.Duration(pollSec) * time.Second

	p.language = "en"
	if val, found, _ := host.GetConfig(ctx, "language"); found && val != "" {
		p.language = val
	}

	p.openMeteo = NewOpenMeteoProvider()
	p.providers = map[string]Provider{
		"openweathermap": NewOpenWeatherMapProvider(apiKey),
		"openmeteo":      p.openMeteo,
	}
	p.postal = map[string]GeoResult{}
	p.overrides = newOverrideStore(p.notifyOverride)
	p.cache = gocache.New(10*time.Minute, 15*time.Minute)
	p.scheduler = NewScheduler(host, p.providers, p.cache, p.pollInterval, p.language)

	if err := host.RegisterPermission(ctx, "api:weather", []string{"read"}, "Weather data access"); err != nil {
		return fmt.Errorf("registering permission: %w", err)
	}
	// Reading weather is harmless, so every role gets it. Station readings
	// arrive through the Weather Override node, which flow permissions gate.
	if err := host.SeedRoleDefaults(ctx, []plugin.RoleDefault{
		{Permission: "api:weather:read", AllRoles: true},
	}); err != nil {
		return fmt.Errorf("seeding role defaults: %w", err)
	}

	if err := host.RegisterProvider(ctx, "weather", "openmeteo", "/plugins/weather/openmeteo", "Open-Meteo"); err != nil {
		return fmt.Errorf("registering openmeteo provider: %w", err)
	}
	if err := host.RegisterProvider(ctx, "weather", "openweathermap", "/plugins/weather/openweathermap", "OpenWeatherMap"); err != nil {
		return fmt.Errorf("registering openweathermap provider: %w", err)
	}
	if err := host.RegisterTopicPermission(ctx, "weather:*", "api:weather:read"); err != nil {
		return fmt.Errorf("registering topic permission: %w", err)
	}

	// Polling follows demand: the broker reports how many clients are
	// subscribed to each forecast topic, and a location with no watchers
	// stops being fetched.
	if err := host.Subscribe(ctx, "topic:weather:forecast:*:count"); err != nil {
		return fmt.Errorf("subscribing to count topic: %w", err)
	}

	p.service = newService(p, host)
	return nil
}

// Shutdown stops polling and withdraws what Init registered.
func (p *WeatherPlugin) Shutdown(ctx context.Context) error {
	if p.scheduler != nil {
		p.scheduler.Stop()
	}
	if p.overrides != nil {
		p.overrides.Stop()
	}
	if p.host != nil {
		_ = p.host.UnregisterPermission(ctx, "api:weather")
		_ = p.host.UnregisterProvider(ctx, "weather", "openweathermap")
		_ = p.host.UnregisterProvider(ctx, "weather", "openmeteo")
		_ = p.host.UnregisterTopicPermission(ctx, "weather:*")
		_ = p.host.Unsubscribe(ctx, "topic:weather:forecast:*:count")
	}
	return nil
}

// HandleHTTP routes a proxied request through the plugin's own API.
func (p *WeatherPlugin) HandleHTTP(_ context.Context, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, error) {
	return httpsdk.ServeHTTP(p.service, req)
}

// OpenAPISpec publishes the plugin's routes in the host's API document.
func (p *WeatherPlugin) OpenAPISpec() string { return httpsdk.OpenAPISpec(p.service) }

// HandleMessage starts and stops polling as dashboards subscribe to and leave
// forecast topics.
func (p *WeatherPlugin) HandleMessage(_ context.Context, _, msgType string, payload []byte) error {
	if msgType != "subscription_count" {
		return nil
	}
	var msg struct {
		Topic string `json:"topic"`
		Count int    `json:"count"`
	}
	if err := json.Unmarshal(payload, &msg); err != nil {
		return nil
	}

	// Topic form: weather:forecast:{provider}:{lat}:{lon}.
	parts := strings.Split(msg.Topic, ":")
	if len(parts) < 5 || parts[0] != "weather" || parts[1] != "forecast" {
		return nil
	}
	provider := parts[2]
	lat, errLat := strconv.ParseFloat(parts[3], 64)
	lon, errLon := strconv.ParseFloat(parts[4], 64)
	if errLat != nil || errLon != nil {
		return nil
	}

	if msg.Count > 0 {
		p.scheduler.EnsurePolling(provider, lat, lon)
	} else {
		p.scheduler.StopPolling(provider, lat, lon)
	}
	return nil
}
