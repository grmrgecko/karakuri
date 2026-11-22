package main

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/apiutil"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// langProvider is a forecast service that describes the weather in the
// language it is asked for, the way OpenWeatherMap's lang parameter does.
type langProvider struct {
	mu    sync.Mutex
	langs []string
}

func (p *langProvider) FetchForecast(_ context.Context, _, _ float64, lang string) (*ForecastData, error) {
	p.mu.Lock()
	p.langs = append(p.langs, lang)
	p.mu.Unlock()
	return &ForecastData{
		Current:   &CurrentWeather{Description: "sky in " + lang},
		FetchedAt: time.Now().Unix(),
	}, nil
}

func (p *langProvider) GeoSearch(context.Context, string, string) ([]GeoResult, error) {
	return nil, nil
}

func (p *langProvider) ReverseGeo(context.Context, float64, float64, string) ([]GeoResult, error) {
	return nil, nil
}

// TestForecastKeepsTheRequestedLanguage pins that a forecast is served in the
// language its request names. The cache held one forecast per location
// whatever its language, and polling refreshes it in the plugin's own
// language, so a widget set to German was served English from the first poll
// on, and two widgets in different languages shared whichever was read first.
func TestForecastKeepsTheRequestedLanguage(t *testing.T) {
	prov := &langProvider{}
	providers := map[string]Provider{"fake": prov}
	cache := gocache.New(time.Minute, time.Minute)
	p := &WeatherPlugin{providers: providers, cache: cache, pollInterval: time.Hour, language: "en"}
	p.scheduler = NewScheduler(nil, providers, cache, time.Hour, "en")
	t.Cleanup(p.scheduler.Stop)
	p.service = newService(p, nil)

	describe := func(lang string) string {
		t.Helper()
		// The dashboard grant stands in for the host's permission check.
		resp, err := p.HandleHTTP(context.Background(), &plugin.HTTPRequestParams{
			Method: http.MethodGet, Path: "/fake/forecast/34.7304:-86.5846",
			QueryString: "lang=" + lang, DashboardGrant: "d1",
		})
		require.NoError(t, err, "the forecast route must answer")
		require.Equalf(t, http.StatusOK, resp.StatusCode, "body: %s", resp.Body)
		var out apiutil.Success[*ForecastData]
		require.NoError(t, json.Unmarshal(resp.Body, &out), "the forecast must decode")
		require.NotNil(t, out.Data.Current, "the forecast carries current conditions")
		return out.Data.Current.Description
	}

	assert.Equal(t, "sky in de", describe("de"), "the first read is in the language asked for")
	// The request starts polling, which reads the location in the plugin's
	// language and caches it.
	require.Eventually(t, func() bool {
		prov.mu.Lock()
		defer prov.mu.Unlock()
		return len(prov.langs) >= 2
	}, 5*time.Second, 10*time.Millisecond, "the scheduler must poll the watched location")
	require.Eventually(t, func() bool {
		_, found := cache.Get("forecast:fake:34.7304:-86.5846")
		return found
	}, 5*time.Second, 10*time.Millisecond, "the poll must cache what it read")

	assert.Equal(t, "sky in de", describe("de"), "a later read must not come back in the plugin's language")
	assert.Equal(t, "sky in en", describe("en"), "the plugin's language is still served to a widget that asks for it")
}
