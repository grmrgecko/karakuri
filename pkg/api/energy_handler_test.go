package api_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/energy"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestEnergyRoutesAndPreset covers the energy service through REST: sources,
// a tariff refused for a gap in the week and then saved, rollups with cost,
// the price, and the preset dashboard, whose charts an anonymous viewer of
// the public dashboard reads without any connection permission.
func TestEnergyRoutesAndPreset(t *testing.T) {
	ts, bearer := setupTestServer(t)
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "UTC", WeekStart: 1}))
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Chart", PackageName: "chart", TagName: "chart-widget", Version: "0.1.0"},
	}))
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/energy/sources", map[string]any{
		"name": "House", "source_kind": "control", "source_id": uuid.NewString(),
		"measurement": "energy", "unit": "kWh", "role": "house",
	})
	require.Equalf(t, http.StatusOK, status, "create source: %v", resp)
	house := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/sources", map[string]any{
		"name": "Second house", "source_kind": "control", "source_id": uuid.NewString(),
		"measurement": "energy", "unit": "kWh", "role": "house",
	})
	assert.Equalf(t, http.StatusBadRequest, status, "a second house source is refused: %v", resp)
	status, _ = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/sources", map[string]any{
		"name": "Plug", "source_kind": "device", "source_id": uuid.NewString(),
		"measurement": "energy", "unit": "litres", "role": "consumer",
	})
	assert.Equal(t, http.StatusBadRequest, status, "an unknown unit is refused")

	// Two hours of usage yesterday.
	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
	id := uuid.MustParse(house)
	require.NoError(t, models.UpsertEnergyHours(db.DB, []models.EnergyHour{
		{SourceID: id, Hour: day.Add(10 * time.Hour), LocalDate: day.Format("2006-01-02"), KWh: 1.5, Quality: "live"},
		{SourceID: id, Hour: day.Add(18 * time.Hour), LocalDate: day.Format("2006-01-02"), KWh: 2, Quality: "live"},
	}))

	tariff := map[string]any{"name": "Day and night", "currency": "usd", "effective_from": day.AddDate(0, 0, -7).Format("2006-01-02"),
		"daily_charge": 0.5, "import": map[string]any{"periods": []any{
			map[string]any{"name": "day", "days": []int{1, 2, 3, 4, 5, 6, 7}, "start": "07:00", "end": "23:00", "rate": 0.3},
		}}}
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/tariffs", tariff)
	require.Equal(t, http.StatusBadRequest, status)
	assert.Contains(t, resp["message"], "Monday 00:00 to 07:00 has no rate")
	tariff["import"] = map[string]any{"periods": []any{
		map[string]any{"name": "night", "days": []int{1, 2, 3, 4, 5, 6, 7}, "start": "00:00", "end": "07:00", "rate": 0.1},
		map[string]any{"name": "day", "days": []int{1, 2, 3, 4, 5, 6, 7}, "start": "07:00", "end": "00:00", "rate": 0.3},
	}}
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/tariffs", tariff)
	require.Equalf(t, http.StatusOK, status, "save tariff: %v", resp)
	// Every hour from the first recorded one to now is repriced; how many
	// that is depends on the time of day the test runs.
	since := float64(int(time.Since(day.Add(10 * time.Hour)).Hours()))
	assert.GreaterOrEqual(t, resp["data"].(map[string]any)["hours_recomputed"], since+1)

	from := day.Format(time.RFC3339)
	to := day.Add(24 * time.Hour).Format(time.RFC3339)
	status, resp = doJSON(t, ts, bearer, http.MethodGet,
		"/api/energy/rollups?target=house&measures=usage,total_cost&period=day&from="+from+"&to="+to, nil)
	require.Equalf(t, http.StatusOK, status, "rollups: %v", resp)
	rows := resp["data"].(map[string]any)["rows"].([]any)
	require.Len(t, rows, 1)
	values := rows[0].(map[string]any)["values"].(map[string]any)
	assert.InDelta(t, 3.5, values["usage"], 1e-9)
	assert.InDelta(t, 3.5*0.3+0.5, values["total_cost"], 1e-9)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/energy/price", nil)
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "USD", resp["data"].(map[string]any)["currency"])

	// The energy language answers through the time-series query route.
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/connections/energy/timeseries/query", map[string]any{
		"query": map[string]any{"language": "energy", "from": from, "to": to, "format": "table",
			"energy": map[string]any{"targets": []string{"house"}, "measure": "usage", "period": "hour"}},
	})
	require.Equalf(t, http.StatusOK, status, "energy query: %v", resp)
	series := resp["data"].(map[string]any)["series"].([]any)
	fields := series[0].(map[string]any)["fields"].([]any)
	assert.Equal(t, []any{"House"}, fields[0].(map[string]any)["values"])
	assert.InDelta(t, 3.5, fields[1].(map[string]any)["values"].([]any)[0], 1e-9)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/preset", nil)
	require.Equalf(t, http.StatusOK, status, "preset: %v", resp)
	dash := resp["data"].(map[string]any)
	dashID := dash["id"].(string)
	assert.Equal(t, "energy", dash["slug"])

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/dashboards/"+dashID, nil)
	require.Equal(t, http.StatusOK, status)
	placements := resp["data"].(map[string]any)["widgets"].([]any)
	assert.Len(t, placements, 5, "three stats, the 30-day bars, and the breakdown; no sources table or solar without consumers or solar")

	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"name": "Energy", "is_public": true})
	require.Equal(t, http.StatusOK, status)
	stat := placements[0].(map[string]any)["id"].(string)
	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/dashboards/"+dashID+"/widgets/"+stat+"/chart", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "a public viewer reads the preset's chart: %v", resp)
	results := resp["data"].(map[string]any)["results"].([]any)
	require.Len(t, results, 2)
	assert.Nil(t, results[0].(map[string]any)["error"])
}

// TestEnergyImportFromHomeAssistant covers importing a Home Assistant
// energy sensor's past from its hourly statistics: each hour's change
// becomes that hour's usage, and an hour recorded live is kept.
func TestEnergyImportFromHomeAssistant(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	require.NoError(t, energy.SaveSettings(energy.Settings{Timezone: "UTC", WeekStart: 1}))
	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equal(t, http.StatusCreated, status)
	connID := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	day := time.Now().UTC().Truncate(24*time.Hour).AddDate(0, 0, -1)
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
	require.NoError(t, models.UpsertEnergyHours(db.DB, []models.EnergyHour{{SourceID: uuid.MustParse(id),
		Hour: day.Add(9 * time.Hour), LocalDate: day.Format("2006-01-02"), KWh: 0.7, Quality: "live"}}))

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/energy/sources/"+id+"/import", map[string]any{"days": 2})
	require.Equalf(t, http.StatusOK, status, "import: %v", resp)
	require.Eventually(t, func() bool {
		p, ok := energy.Imports()[uuid.MustParse(id)]
		return ok && !p.Running
	}, 5*time.Second, 20*time.Millisecond)
	assert.Empty(t, energy.Imports()[uuid.MustParse(id)].Error)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/energy/rollups?target=source:"+id+"&period=hour&from="+
		day.Add(8*time.Hour).Format(time.RFC3339)+"&to="+day.Add(10*time.Hour).Format(time.RFC3339), nil)
	require.Equal(t, http.StatusOK, status)
	rows := resp["data"].(map[string]any)["rows"].([]any)
	require.Len(t, rows, 2)
	assert.InDelta(t, 1.25, rows[0].(map[string]any)["values"].(map[string]any)["usage"], 1e-9, "filled from statistics")
	assert.InDelta(t, 0.7, rows[1].(map[string]any)["values"].(map[string]any)["usage"], 1e-9, "a live hour is kept")
}
