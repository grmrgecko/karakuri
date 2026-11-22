package main

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStockConfigOmittedDefaults pins what a graph saved over MCP or REST
// gets when it leaves fields out: the catalog defaults of a threshold of 1
// and polling only while the market is open. Absolute mode with a zero
// threshold fired on every poll, unchanged price included.
func TestStockConfigOmittedDefaults(t *testing.T) {
	var cfg stockNodeConfig
	require.NoError(t, json.Unmarshal([]byte(`{"symbol":"AAPL","threshold_mode":"absolute"}`), &cfg))

	assert.True(t, cfg.marketHoursOnly(), "an omitted market_hours_only must default to true")
	moved, _, _ := quoteMoved(100, 100, true, cfg)
	assert.False(t, moved, "an unchanged price must not fire")
	moved, _, _ = quoteMoved(100, 100.5, true, cfg)
	assert.False(t, moved, "0.5 is under the default threshold of 1")
	moved, _, _ = quoteMoved(100, 101, true, cfg)
	assert.True(t, moved, "a move of exactly 1 clears the default threshold")

	require.NoError(t, json.Unmarshal([]byte(`{"market_hours_only":false}`), &cfg))
	assert.False(t, cfg.marketHoursOnly(), "an explicit false must still poll around the clock")
}

// TestMarketStatusEmitOn pins which transitions start a run. A graph saved
// over MCP or REST leaves emit_on out, and the catalog default is both; read
// as the empty string it matched neither transition, so the trigger never
// fired.
func TestMarketStatusEmitOn(t *testing.T) {
	cases := []struct {
		name      string
		cfg       string
		open, end bool
	}{
		{"omitted", `{}`, true, true},
		{"both", `{"emit_on":"both"}`, true, true},
		{"open only", `{"emit_on":"open"}`, true, false},
		{"close only", `{"emit_on":"close"}`, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var cfg marketStatusConfig
			require.NoError(t, json.Unmarshal([]byte(tc.cfg), &cfg))
			assert.Equal(t, tc.open, cfg.emits("open"), "the open transition must follow emit_on")
			assert.Equal(t, tc.end, cfg.emits("close"), "the close transition must follow emit_on")
		})
	}
}

// TestAlphaVantageTimestamps pins the zone of Alpha Vantage series keys.
// Intraday keys are US Eastern wall-clock times; read as UTC, every candle
// sat four or five hours early on the chart. Daily keys carry no time and
// stay at midnight UTC.
func TestAlphaVantageTimestamps(t *testing.T) {
	cases := []struct {
		name, key string
		want      time.Time
	}{
		{"winter close", "2026-11-20 16:00:00", time.Date(2026, 11, 20, 21, 0, 0, 0, time.UTC)},
		{"summer open", "2026-07-01 09:30:00", time.Date(2026, 7, 1, 13, 30, 0, 0, time.UTC)},
		{"daily", "2026-11-20", time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want.Unix(), avParseTime(tc.key), "the key must be read in the zone the provider reports it in")
		})
	}
	assert.Zero(t, avParseTime("not a time"), "an unreadable key must not invent a timestamp")
}
