package main

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMapOMAirQuality decodes real Open-Meteo air quality responses, fetched
// on 2026-10-24 from air-quality-api.open-meteo.com/v1/air-quality with the
// variables fetchAirQuality requests, for Berlin (52.52, 13.41) and
// Huntsville, Alabama (34.7304, -86.5846). Open-Meteo models pollen only for
// Europe: Berlin reports zero counts out of season, while Huntsville reports
// null, and only the null must drop the pollen block.
func TestMapOMAirQuality(t *testing.T) {
	for _, tc := range []struct {
		fixture    string
		usAQI      float64
		pm25       float64
		wantPollen bool
	}{
		{"openmeteo-air-quality-berlin.json", 28, 3.9, true},
		{"openmeteo-air-quality-huntsville.json", 35, 2.3, false},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			body, err := os.ReadFile("testdata/" + tc.fixture)
			require.NoError(t, err)
			var raw omAirQualityResponse
			require.NoError(t, json.Unmarshal(body, &raw))

			aq := mapOMAirQuality(&raw)

			require.NotNil(t, aq.USAQI)
			assert.Equal(t, tc.usAQI, *aq.USAQI)
			require.NotNil(t, aq.PM25)
			assert.Equal(t, tc.pm25, *aq.PM25)
			if tc.wantPollen {
				require.NotNil(t, aq.Pollen, "zero pollen counts are a reading and must be kept")
				require.NotNil(t, aq.Pollen.Birch)
				assert.Equal(t, 0.0, *aq.Pollen.Birch)
			} else {
				assert.Nil(t, aq.Pollen, "null pollen means no coverage and must not read as zero")
			}
			assert.Len(t, aq.Hourly, 24, "the hourly series covers the next 24 hours")
			assert.Equal(t, aq.Dt, aq.Hourly[0].Dt, "the series starts at the current hour")
		})
	}
}
