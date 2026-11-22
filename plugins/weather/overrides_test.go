package main

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMatchPostal decodes a real Open-Meteo geocoding answer for US postal
// code 35757, fetched on 2026-11-11 from geocoding-api.open-meteo.com/v1/search
// with name=35757 and countryCode=US. Open-Meteo answers with the populated
// place whose postcodes list the code (Madison, Alabama, carrying 35756-35758),
// so the override sits at the town's centre rather than the code's centroid.
// A code the place does not list must not borrow its coordinates.
func TestMatchPostal(t *testing.T) {
	body, err := os.ReadFile("testdata/openmeteo-geocoding-postal-35757.json")
	require.NoError(t, err)
	var raw omGeoResponse
	require.NoError(t, json.Unmarshal(body, &raw))

	place, ok := matchPostal(&raw, "35757")
	require.True(t, ok, "Madison lists 35757 among its postcodes")
	assert.Equal(t, "Madison", place.Name)
	assert.Equal(t, 34.69926, place.Lat)
	assert.Equal(t, -86.74833, place.Lon)

	_, ok = matchPostal(&raw, "35801")
	assert.False(t, ok, "a code missing from every result's postcodes is not resolved")
}

// TestOverrideStoreLookup locks the two rules that decide what a forecast
// shows: each reading expires on its own maximum age, and a forecast takes
// the nearest live override within that override's radius. The coordinates
// are Madison, Alabama (34.6993, -86.7483) and a point about 6.8 km north
// west of it (34.7579, -86.7680), both in postal code 35757.
func TestOverrideStoreLookup(t *testing.T) {
	now := time.Date(2026, 11, 11, 18, 0, 0, 0, time.UTC)
	s := newOverrideStore(nil)
	s.now = func() time.Time { return now }
	t.Cleanup(s.Stop)

	s.Set("US 35757", 34.69926, -86.74833, 10, map[string]float64{"temp": 13.4, "humidity": 91}, 15*time.Minute)
	now = now.Add(10 * time.Minute)
	s.Set("US 35757", 34.69926, -86.74833, 10, map[string]float64{"temp": 13.6}, 15*time.Minute)

	ov := s.Lookup(34.7579, -86.7680)
	require.NotNil(t, ov, "a point 6.8 km away sits inside a 10 km radius")
	assert.Equal(t, 13.6, ov.Values["temp"].Value, "a newer reading replaces the older one")
	assert.Contains(t, ov.Values, "humidity", "humidity set 10 minutes ago is still within 15 minutes")

	now = now.Add(6 * time.Minute)
	ov = s.Lookup(34.7579, -86.7680)
	require.NotNil(t, ov)
	assert.NotContains(t, ov.Values, "humidity", "humidity set 16 minutes ago has expired")
	assert.Contains(t, ov.Values, "temp", "temperature set 6 minutes ago is still live")

	s.Set("station", 34.7579, -86.7680, 1, map[string]float64{"temp": 12.9}, 15*time.Minute)
	assert.Equal(t, "station", s.Lookup(34.7579, -86.7680).Label, "the nearest override wins where radii overlap")
	assert.Equal(t, "US 35757", s.Lookup(34.69926, -86.74833).Label, "an override does not reach past its own 1 km radius")

	now = now.Add(15 * time.Minute)
	assert.Nil(t, s.Lookup(34.7579, -86.7680), "once every reading expires the provider's values return")
}
