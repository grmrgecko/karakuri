package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestEnergyImportInHalfHourZone locks that Home Assistant statistics show
// in the rollups of a zone offset by half an hour. Statistic hours start on
// the UTC hour and energy hours on the local one, so in Asia/Kolkata
// (UTC+5:30) each statistic hour is shared between two energy hours. The
// rows were stored under the UTC hour starts, which no rollup there reads,
// and an import that reported hours written showed nothing. The 1.25 kWh
// at 08:00 UTC and 0.75 kWh at 09:00 UTC fall in the local hours starting
// 07:30, 08:30, and 09:30 UTC as 0.625, 1.0, and 0.375.
func TestEnergyImportInHalfHourZone(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "Asia/Kolkata", WeekStart: 1}))
	t.Cleanup(func() { _ = energy.SaveSettings(energy.Settings{Timezone: "UTC", WeekStart: 1}) })
	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equal(t, http.StatusCreated, status)
	connID := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -2)
	fake.SetStatistics("sensor.house_energy", []map[string]any{
		{"start": day.Add(8 * time.Hour).UnixMilli(), "change": 1.25},
		{"start": day.Add(9 * time.Hour).UnixMilli(), "change": 0.75},
	})
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/sources", map[string]any{
		"name": "House", "source_kind": "ha", "source_id": connID, "entity_id": "sensor.house_energy",
		"measurement": "energy", "unit": "kWh", "role": "house",
	})
	require.Equalf(t, http.StatusOK, status, "source: %v", resp)
	id := resp["data"].(map[string]any)["id"].(string)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/sources/"+id+"/import", map[string]any{"days": 4})
	require.Equalf(t, http.StatusOK, status, "import: %v", resp)
	require.Eventually(t, func() bool {
		p, ok := energy.Imports()[uuid.MustParse(id)]
		return ok && !p.Running
	}, 5*time.Second, 20*time.Millisecond)
	require.Empty(t, energy.Imports()[uuid.MustParse(id)].Error)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/energy/rollups?target=source:"+id+"&period=hour&from="+
		day.Add(7*time.Hour+30*time.Minute).Format(time.RFC3339)+"&to="+day.Add(10*time.Hour+30*time.Minute).Format(time.RFC3339), nil)
	require.Equal(t, http.StatusOK, status)
	rows := resp["data"].(map[string]any)["rows"].([]any)
	require.Len(t, rows, 3, "07:30 to 10:30 UTC is three local hours")
	for i, want := range []float64{0.625, 1.0, 0.375} {
		assert.InDelta(t, want, rows[i].(map[string]any)["values"].(map[string]any)["usage"], 1e-9,
			"local hour %d holds its share of the statistic hours it overlaps", i)
	}
}
