// Command stock is a karakuri plugin bringing stock market data to dashboards
// and flows. It serves an HTTP API the stock widget reads, polls only the
// tickers something is watching, and contributes the flow nodes in nodes.go so
// a quote move or a market open can start a run.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/swaggest/rest/nethttp"
	"github.com/swaggest/rest/web"

	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk/httpsdk"
)

const defaultPollInterval = 60 // seconds

func main() { sdk.Serve(&StockPlugin{}) }

// StockPlugin is the stock market data provider.
type StockPlugin struct {
	host            plugin.HostAPI
	scheduler       *Scheduler
	providers       map[string]Provider
	service         *web.Service
	cache           *gocache.Cache
	pollInterval    time.Duration
	marketHoursOnly bool
}

// Manifest identifies the plugin.
func (p *StockPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "stock",
		Version:     "0.1.0",
		Description: "Stock quotes for dashboards and flows, via Yahoo Finance, Alpha Vantage, or Finnhub.",
		Author:      "James Coleman",
	}
}

// Register contributes the plugin's flow nodes.
func (p *StockPlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(&StockQuoteChangedNode{p: p})
	r.RegisterNode(&StockMarketStatusNode{p: p})
	r.RegisterNode(&StockQuoteNode{p: p})
	return nil
}

// SettingsSchema declares the plugin's configuration. The two API keys are
// what decide which providers exist: a key that is not set means that
// provider is not offered at all.
func (p *StockPlugin) SettingsSchema() string {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"poll_interval": map[string]any{
				"type":        "number",
				"title":       "Poll interval (seconds)",
				"description": "How often a watched ticker is refetched during market hours.",
				"default":     defaultPollInterval,
			},
			"market_hours_only": map[string]any{
				"type":        "boolean",
				"title":       "Poll only during market hours",
				"description": "Stops polling when US markets are closed.",
				"default":     true,
			},
			"alphavantage_api_key": map[string]any{
				"type":        "string",
				"title":       "Alpha Vantage API key",
				"description": "Enables the Alpha Vantage provider when set.",
				"format":      "password",
			},
			"finnhub_api_key": map[string]any{
				"type":        "string",
				"title":       "Finnhub API key",
				"description": "Enables the Finnhub provider when set.",
				"format":      "password",
			},
		},
	}
	b, _ := json.Marshal(schema)
	return string(b)
}

// Init reads settings, builds whichever providers are configured, and
// registers the permission, provider entries, and topic subscription.
func (p *StockPlugin) Init(ctx context.Context, host plugin.HostAPI) error {
	p.host = host

	pollSec := defaultPollInterval
	if val, found, _ := host.GetConfig(ctx, "poll_interval"); found {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			pollSec = n
		}
	}
	p.pollInterval = time.Duration(pollSec) * time.Second

	p.marketHoursOnly = true
	if val, found, _ := host.GetConfig(ctx, "market_hours_only"); found && (val == "false" || val == "0") {
		p.marketHoursOnly = false
	}

	// Yahoo needs no credentials; the other two exist only when keyed.
	p.providers = map[string]Provider{"yahoo": NewYahooProvider()}
	if val, found, _ := host.GetConfig(ctx, "alphavantage_api_key"); found && val != "" {
		p.providers["alphavantage"] = NewAlphaVantageProvider(val)
	}
	if val, found, _ := host.GetConfig(ctx, "finnhub_api_key"); found && val != "" {
		p.providers["finnhub"] = NewFinnhubProvider(val)
	}

	p.cache = gocache.New(p.pollInterval*2, p.pollInterval*3)
	p.scheduler = NewScheduler(host, p.providers, p.cache, p.pollInterval, p.marketHoursOnly)

	if err := host.RegisterPermission(ctx, "api:stock", []string{"read"}, "Stock market data access"); err != nil {
		return fmt.Errorf("registering permission: %w", err)
	}
	if err := host.SeedRoleDefaults(ctx, []plugin.RoleDefault{
		{Permission: "api:stock:read", AllRoles: true},
	}); err != nil {
		return fmt.Errorf("seeding role defaults: %w", err)
	}

	displayNames := map[string]string{
		"yahoo":        "Yahoo Finance",
		"alphavantage": "Alpha Vantage",
		"finnhub":      "Finnhub",
	}
	for name := range p.providers {
		if err := host.RegisterProvider(ctx, "stock", name,
			"/plugins/stock/"+name, displayNames[name]); err != nil {
			return fmt.Errorf("registering provider %s: %w", name, err)
		}
	}
	if err := host.RegisterTopicPermission(ctx, "market:stock:*", "api:stock:read"); err != nil {
		return fmt.Errorf("registering topic permission: %w", err)
	}
	if err := host.Subscribe(ctx, "topic:market:stock:*:count"); err != nil {
		return fmt.Errorf("subscribing to count topic: %w", err)
	}

	p.service = newService(p, host)
	return nil
}

// Shutdown stops polling and withdraws what Init registered.
func (p *StockPlugin) Shutdown(ctx context.Context) error {
	if p.scheduler != nil {
		p.scheduler.Stop()
	}
	if p.host != nil {
		_ = p.host.UnregisterPermission(ctx, "api:stock")
		for name := range p.providers {
			_ = p.host.UnregisterProvider(ctx, "stock", name)
		}
		_ = p.host.UnregisterTopicPermission(ctx, "market:stock:*")
		_ = p.host.Unsubscribe(ctx, "topic:market:stock:*:count")
	}
	return nil
}

// HandleHTTP routes a proxied request through the plugin's own API.
func (p *StockPlugin) HandleHTTP(_ context.Context, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, error) {
	return httpsdk.ServeHTTP(p.service, req)
}

// OpenAPISpec publishes the plugin's routes in the host's API document.
func (p *StockPlugin) OpenAPISpec() string { return httpsdk.OpenAPISpec(p.service) }

// HandleMessage starts and stops polling as dashboards subscribe to and leave
// ticker topics.
func (p *StockPlugin) HandleMessage(_ context.Context, _, msgType string, payload []byte) error {
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

	// Topic form: market:stock:{provider}:{TICKER}.
	parts := strings.Split(msg.Topic, ":")
	if len(parts) < 4 || parts[0] != "market" || parts[1] != "stock" {
		return nil
	}
	provider, ticker := parts[2], strings.ToUpper(parts[3])

	if msg.Count > 0 {
		p.scheduler.EnsurePolling(provider, ticker)
	} else {
		p.scheduler.StopPolling(provider, ticker)
	}
	return nil
}

// newService builds the stock plugin's own HTTP API.
func newService(p *StockPlugin, host plugin.HostAPI) *web.Service {
	s := httpsdk.NewService("Stock Plugin API", "0.1.0", "Stock market data endpoints.")

	read := httpsdk.RequirePermission(host, "api:stock:read")
	s.With(read).Method(http.MethodGet, "/{provider}/{ticker}",
		nethttp.NewHandler(p.quoteInteractor()))
	s.With(read).Method(http.MethodGet, "/{provider}/{ticker}/ohlc",
		nethttp.NewHandler(p.ohlcInteractor()))
	s.With(read).Method(http.MethodGet, "/{provider}/search",
		nethttp.NewHandler(p.searchInteractor()))
	s.With(read).Method(http.MethodGet, "/{provider}/trending",
		nethttp.NewHandler(p.trendingInteractor()))

	return s
}
