// Command crypto is a karakuri plugin bringing cryptocurrency market data to
// dashboards and flows. It serves an HTTP API the crypto widget reads, polls
// only the pairs something is watching, and contributes the flow nodes in
// nodes.go so a price move can start a run.
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

const defaultPollInterval = 45 // seconds

func main() { sdk.Serve(&CryptoPlugin{}) }

// CryptoPlugin is the cryptocurrency market data provider.
type CryptoPlugin struct {
	host         plugin.HostAPI
	scheduler    *Scheduler
	providers    map[string]Provider
	service      *web.Service
	cache        *gocache.Cache
	pollInterval time.Duration
}

// Manifest identifies the plugin.
func (p *CryptoPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "crypto",
		Version:     "0.1.0",
		Description: "Cryptocurrency prices for dashboards and flows, via Binance, Coinbase, or CoinGecko.",
		Author:      "James Coleman",
	}
}

// Register contributes the plugin's flow nodes.
func (p *CryptoPlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(&CryptoPriceChangedNode{p: p})
	r.RegisterNode(&CryptoPriceNode{p: p})
	return nil
}

// SettingsSchema declares the plugin's configuration.
func (p *CryptoPlugin) SettingsSchema() string {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"poll_interval": map[string]any{
				"type":        "number",
				"title":       "Poll interval (seconds)",
				"description": "How often a watched pair's price is refetched.",
				"default":     defaultPollInterval,
			},
			"coingecko_api_key": map[string]any{
				"type":        "string",
				"title":       "CoinGecko Pro API key",
				"description": "Raises the rate limit. Leave empty for the free tier.",
				"format":      "password",
			},
			"binance_base_url": map[string]any{
				"type":        "string",
				"title":       "Binance base URL",
				"description": "Defaults to https://api.binance.us; use https://api.binance.com for the global endpoint.",
			},
		},
	}
	b, _ := json.Marshal(schema)
	return string(b)
}

// Init reads settings, builds the providers, and registers the permission,
// provider entries, and topic subscription polling depends on.
func (p *CryptoPlugin) Init(ctx context.Context, host plugin.HostAPI) error {
	p.host = host

	pollSec := defaultPollInterval
	if val, found, _ := host.GetConfig(ctx, "poll_interval"); found {
		if n, err := strconv.Atoi(val); err == nil && n > 0 {
			pollSec = n
		}
	}
	p.pollInterval = time.Duration(pollSec) * time.Second

	binanceURL, _, _ := host.GetConfig(ctx, "binance_base_url")
	cgKey, _, _ := host.GetConfig(ctx, "coingecko_api_key")
	p.providers = map[string]Provider{
		"binance":   NewBinanceProvider(binanceURL),
		"coinbase":  NewCoinbaseProvider(),
		"coingecko": NewCoinGeckoProvider(cgKey),
	}

	p.cache = gocache.New(p.pollInterval*2, p.pollInterval*3)
	p.scheduler = NewScheduler(host, p.providers, p.cache, p.pollInterval)

	if err := host.RegisterPermission(ctx, "api:crypto", []string{"read"}, "Cryptocurrency market data access"); err != nil {
		return fmt.Errorf("registering permission: %w", err)
	}
	if err := host.SeedRoleDefaults(ctx, []plugin.RoleDefault{
		{Permission: "api:crypto:read", AllRoles: true},
	}); err != nil {
		return fmt.Errorf("seeding role defaults: %w", err)
	}

	displayNames := map[string]string{
		"binance":   "Binance",
		"coinbase":  "Coinbase",
		"coingecko": "CoinGecko",
	}
	for name := range p.providers {
		if err := host.RegisterProvider(ctx, "crypto", name,
			"/plugins/crypto/"+name, displayNames[name]); err != nil {
			return fmt.Errorf("registering provider %s: %w", name, err)
		}
	}
	if err := host.RegisterTopicPermission(ctx, "market:crypto:*", "api:crypto:read"); err != nil {
		return fmt.Errorf("registering topic permission: %w", err)
	}

	// Polling follows demand, the same as the weather plugin: a pair with no
	// subscribers stops being fetched.
	if err := host.Subscribe(ctx, "topic:market:crypto:*:count"); err != nil {
		return fmt.Errorf("subscribing to count topic: %w", err)
	}

	p.service = newService(p, host)
	return nil
}

// Shutdown stops polling and withdraws what Init registered.
func (p *CryptoPlugin) Shutdown(ctx context.Context) error {
	if p.scheduler != nil {
		p.scheduler.Stop()
	}
	if p.host != nil {
		_ = p.host.UnregisterPermission(ctx, "api:crypto")
		for name := range p.providers {
			_ = p.host.UnregisterProvider(ctx, "crypto", name)
		}
		_ = p.host.UnregisterTopicPermission(ctx, "market:crypto:*")
		_ = p.host.Unsubscribe(ctx, "topic:market:crypto:*:count")
	}
	return nil
}

// HandleHTTP routes a proxied request through the plugin's own API.
func (p *CryptoPlugin) HandleHTTP(_ context.Context, req *plugin.HTTPRequestParams) (*plugin.HTTPResponseResult, error) {
	return httpsdk.ServeHTTP(p.service, req)
}

// OpenAPISpec publishes the plugin's routes in the host's API document.
func (p *CryptoPlugin) OpenAPISpec() string { return httpsdk.OpenAPISpec(p.service) }

// HandleMessage starts and stops polling as dashboards subscribe to and leave
// pair topics.
func (p *CryptoPlugin) HandleMessage(_ context.Context, _, msgType string, payload []byte) error {
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

	// Topic form: market:crypto:{provider}:{PAIR}.
	parts := strings.Split(msg.Topic, ":")
	if len(parts) < 4 || parts[0] != "market" || parts[1] != "crypto" {
		return nil
	}
	provider, pair := parts[2], strings.ToUpper(parts[3])

	if msg.Count > 0 {
		p.scheduler.EnsurePolling(provider, pair)
	} else {
		p.scheduler.StopPolling(provider, pair)
	}
	return nil
}

// newService builds the crypto plugin's own HTTP API.
func newService(p *CryptoPlugin, host plugin.HostAPI) *web.Service {
	s := httpsdk.NewService("Crypto Plugin API", "0.1.0",
		"Cryptocurrency market data endpoints.")

	read := httpsdk.RequirePermission(host, "api:crypto:read")
	s.With(read).Method(http.MethodGet, "/{provider}/{base}/{quote}",
		nethttp.NewHandler(p.quoteInteractor()))
	s.With(read).Method(http.MethodGet, "/{provider}/{base}/{quote}/history",
		nethttp.NewHandler(p.historyInteractor()))
	s.With(read).Method(http.MethodGet, "/{provider}/search",
		nethttp.NewHandler(p.searchInteractor()))
	s.With(read).Method(http.MethodGet, "/{provider}/trending",
		nethttp.NewHandler(p.trendingInteractor()))

	return s
}
