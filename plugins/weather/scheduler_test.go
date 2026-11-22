package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestLocationKey pins the browser subscription format, including western
// longitudes, trailing zeroes, and JavaScript's rounding toward positive ties.
func TestLocationKey(t *testing.T) {
	for _, tc := range []struct {
		name     string
		lat, lon float64
		want     string
	}{
		{"Huntsville", 34.7304, -86.5846, "openmeteo:34.7304:-86.5846"},
		{"trailing zeroes", 40.7128, -74.006, "openmeteo:40.7128:-74.0060"},
		{"southern latitude", -33.8688, 151.2093, "openmeteo:-33.8688:151.2093"},
		{"halfway values", 1.23445, -1.23445, "openmeteo:1.2345:-1.2344"},
		{"near zero", -0.00001, 0, "openmeteo:0.0000:0.0000"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, LocationKey(tc.want), locationKey("openmeteo", tc.lat, tc.lon))
			assert.Equal(t, LocationKey(tc.want), locationKey("openmeteo", roundCoord(tc.lat), roundCoord(tc.lon)), "polling must not shift already rounded coordinates")
		})
	}
}
