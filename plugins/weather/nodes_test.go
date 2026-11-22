package main

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/pkg/engine"
)

// driftProvider reports the next temperature from a fixed series on every
// forecast fetch.
type driftProvider struct {
	mu    sync.Mutex
	temps []float64
}

func (p *driftProvider) FetchForecast(context.Context, float64, float64, string) (*ForecastData, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	temp := p.temps[0]
	if len(p.temps) > 1 {
		p.temps = p.temps[1:]
	}
	return &ForecastData{Current: &CurrentWeather{Temp: temp}}, nil
}

func (p *driftProvider) GeoSearch(context.Context, string, string) ([]GeoResult, error) {
	return nil, nil
}

func (p *driftProvider) ReverseGeo(context.Context, float64, float64, string) ([]GeoResult, error) {
	return nil, nil
}

// weatherTriggerCtx is the slice of ExecContext the weather trigger uses.
type weatherTriggerCtx struct {
	engine.ExecContext
	cfg   string
	clock *engine.FakeClock
}

func (c *weatherTriggerCtx) Config(out any) error { return json.Unmarshal([]byte(c.cfg), out) }
func (c *weatherTriggerCtx) Clock() engine.Clock  { return c.clock }
func (c *weatherTriggerCtx) Debug(any, string)    {}

// TestWeatherChangedFiresOnSlowDrift pins the baseline to the last emitted
// observation. Polls of 20.0, 20.2, 20.4 and 20.6 against a 0.5 degree
// threshold move 0.2 each, but 20.6 - 20.0 = 0.6 since the first emit, so the
// fourth poll must fire.
func TestWeatherChangedFiresOnSlowDrift(t *testing.T) {
	prov := &driftProvider{temps: []float64{20.0, 20.2, 20.4, 20.6}}
	p := &WeatherPlugin{providers: map[string]Provider{"test": prov}}
	clock := engine.NewFakeClock(time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC))
	ec := &weatherTriggerCtx{
		cfg:   `{"provider":"test","watch":"temperature","threshold":0.5,"interval_s":60}`,
		clock: clock,
	}

	var mu sync.Mutex
	var temps []float64
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = (&WeatherChangedNode{p: p}).Start(ctx, ec, func(m *engine.Msg) {
			mu.Lock()
			defer mu.Unlock()
			temps = append(temps, m.Payload.(map[string]any)["current"].(*CurrentWeather).Temp)
		})
	}()
	for polls := 1; polls <= 3; polls++ {
		require.True(t, clock.BlockUntilAfterCalls(polls, 3*time.Second), "the trigger must wait between polls")
		clock.Advance(time.Minute)
	}
	require.True(t, clock.BlockUntilAfterCalls(4, 3*time.Second), "the fourth poll must complete")
	cancel()
	<-done

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []float64{20.0, 20.6}, temps,
		"the first observation fires, then the drift fires once it clears the threshold from 20.0")
}
