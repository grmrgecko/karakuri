package api_test

import (
	"context"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/dashchart"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/tsdb"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestChartDataStaysInsidePlacement pins spec 028 user story 2 through the
// real router: an anonymous viewer of a public dashboard gets the series of
// the chart's saved query, and nothing it sends can widen that. A variable
// value outside the options is refused, a placement that is not a chart or
// not on the dashboard is not found, a private dashboard is invisible, and
// query text in the body is never run. Ten concurrent viewers cost the
// source one query (SC-004).
func TestChartDataStaysInsidePlacement(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	now := float64(time.Now().Add(-time.Hour).Unix())
	fake.SetState("sensor.kitchen_temp", "21", map[string]any{"friendly_name": "Kitchen", "unit_of_measurement": "°C"})
	fake.SetHistory("sensor.kitchen_temp", []map[string]any{{"s": "20.5", "lu": now}, {"s": "21", "lu": now + 60}})
	fake.SetHistory("sensor.attic_temp", []map[string]any{{"s": "30", "lu": now}})

	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Chart", PackageName: "chart", TagName: "chart-widget", Version: "0.1.0"},
		{Name: "Image", PackageName: "image", TagName: "image-widget", Version: "0.1.0"},
	}), "the widget packages must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Chart wall", "is_public": true})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{
		"display": map[string]any{"variables": []map[string]any{
			{"name": "room", "options": []map[string]any{{"value": "kitchen"}}},
			{"name": "range", "type": "time_range"},
		}},
	})
	require.Equalf(t, http.StatusOK, status, "the variables must save: %v", resp)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equalf(t, http.StatusOK, status, "the widget catalog must list the packages: %v", resp)
	catalog := map[string]any{}
	for _, it := range resp["data"].([]any) {
		w := it.(map[string]any)
		catalog[w["package_name"].(string)] = w["id"]
	}
	place := func(pkg, config string) string {
		status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
			"widget_id": catalog[pkg], "x": 0, "y": 0, "width": 6, "height": 4, "config": config,
		})
		require.Equalf(t, http.StatusCreated, status, "the %s placement must be created: %v", pkg, resp)
		return resp["data"].(map[string]any)["id"].(string)
	}
	chartID := place("chart", `{"timeRange":"${range}","queries":[{"refId":"A","connectionId":"`+connID+
		`","language":"ha_history","ha":{"entities":["sensor.${room}_temp"]}}]}`)
	imageID := place("image", `{}`)

	path := "/api/dashboards/" + dashID + "/widgets/" + chartID + "/chart"
	status, resp = doAnon(t, ts, dashID, http.MethodPost, path, map[string]any{
		"vars": map[string]any{"room": "kitchen", "range": "now-3h,now"},
		// A viewer cannot supply a query; unknown fields are ignored.
		"queries": []map[string]any{{"language": "ha_history", "ha": map[string]any{"entities": []string{"sensor.attic_temp"}}}},
	})
	require.Equalf(t, http.StatusOK, status, "an anonymous viewer of a public chart must get its data: %v", resp)
	results := resp["data"].(map[string]any)["results"].([]any)
	require.Len(t, results, 1, "only the placement's own query runs")
	series := results[0].(map[string]any)["result"].(map[string]any)["series"].([]any)
	require.Len(t, series, 1, "the saved query names one entity")
	labels := series[0].(map[string]any)["labels"].(map[string]any)
	assert.Equal(t, "sensor.kitchen_temp", labels["entity_id"], "the entity comes from the saved query with the option substituted, never from the body")

	status, _ = doAnon(t, ts, dashID, http.MethodPost, path, map[string]any{"vars": map[string]any{"room": "attic"}})
	assert.Equal(t, http.StatusBadRequest, status, "a value outside the variable's options must be refused, or it could name any entity")
	status, _ = doAnon(t, ts, dashID, http.MethodPost, path, map[string]any{"vars": map[string]any{"range": "yesterday-ish"}})
	assert.Equal(t, http.StatusBadRequest, status, "a time range that does not parse must be refused")
	status, _ = doAnon(t, ts, dashID, http.MethodPost, "/api/dashboards/"+dashID+"/widgets/"+imageID+"/chart", map[string]any{})
	assert.Equal(t, http.StatusNotFound, status, "a placement that is not a chart has no queries to run")

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Private"})
	require.Equalf(t, http.StatusCreated, status, "the private dashboard must be created: %v", resp)
	privateID := resp["data"].(map[string]any)["id"].(string)
	status, _ = doAnon(t, ts, privateID, http.MethodPost, "/api/dashboards/"+privateID+"/widgets/"+chartID+"/chart", map[string]any{})
	assert.Equal(t, http.StatusNotFound, status, "a dashboard the viewer cannot read is reported as missing")

	// Concurrent viewers of the same range share one database query.
	var calls atomic.Int32
	orig := dashchart.Querier
	dashchart.Querier = func(ctx context.Context, src *tsdbops.Source, p *tsdbops.Prepared) (*tsdb.Result, error) {
		calls.Add(1)
		time.Sleep(50 * time.Millisecond)
		return orig(ctx, src, p)
	}
	t.Cleanup(func() { dashchart.Querier = orig })
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			doAnon(t, ts, dashID, http.MethodPost, path, map[string]any{"vars": map[string]any{"range": "now-2h,now"}})
		}()
	}
	wg.Wait()
	assert.Equal(t, int32(1), calls.Load(), "ten viewers asking for the same chart within one interval must cost one query")
}

// TestChartQueriesNeedConnectionRead pins that a dashboard editor who may
// not read connections cannot save chart queries, which would otherwise let
// them read any connection through the chart, while placing an empty chart
// and restyling or moving one stay open to them.
func TestChartQueriesNeedConnectionRead(t *testing.T) {
	ts, _ := setupTestServer(t)
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Chart", PackageName: "chart", TagName: "chart-widget", Version: "0.1.0"},
	}), "the chart package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	editor := &models.User{ID: uuid.New(), Username: "editor", Email: "editor@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(editor).Error)
	role := &models.Role{Name: "Dashboard editor"}
	require.NoError(t, role.Create())
	for _, p := range []string{permissions.PermDashboardsRead, permissions.PermDashboardsWrite} {
		require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: p}).Error)
	}
	require.NoError(t, db.DB.Create(&models.RoleAssignment{RoleID: role.ID, UserID: &editor.ID}).Error)
	tokens, err := auth.CreateSession(editor, "127.0.0.1", "test")
	require.NoError(t, err)
	tok := tokens.AccessToken

	status, resp := doJSON(t, ts, tok, http.MethodPost, "/api/dashboards", map[string]any{"name": "Editor's"})
	require.Equalf(t, http.StatusCreated, status, "the editor must be able to create a dashboard: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, tok, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status)
	chartWidget := resp["data"].([]any)[0].(map[string]any)["id"]

	query := `{"viz":"stat","queries":[{"refId":"A","connectionId":"` + uuid.NewString() + `","language":"sql","text":"SELECT secret FROM vault"}]}`
	status, _ = doJSON(t, ts, tok, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": chartWidget, "x": 0, "y": 0, "width": 4, "height": 3, "config": query,
	})
	assert.Equal(t, http.StatusForbidden, status, "a chart with queries needs connection read")

	status, resp = doJSON(t, ts, tok, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": chartWidget, "x": 0, "y": 0, "width": 4, "height": 3, "config": `{"viz":"stat","queries":[{"refId":"A"}]}`,
	})
	require.Equalf(t, http.StatusCreated, status, "an empty chart reads nothing and may be placed: %v", resp)
	placement := "/api/dashboards/" + dashID + "/widgets/" + resp["data"].(map[string]any)["id"].(string)

	status, _ = doJSON(t, ts, tok, http.MethodPut, placement, map[string]any{"config": query})
	assert.Equal(t, http.StatusForbidden, status, "adding queries to a chart needs connection read")
	status, _ = doJSON(t, ts, tok, http.MethodPut, placement, map[string]any{"x": 4, "config": `{"viz":"gauge","queries":[{"refId":"A"}]}`})
	assert.Equal(t, http.StatusOK, status, "moving and restyling change no query")
}

// TestVariableQueriesNeedConnectionRead pins that a time-series query
// variable runs its text against a connection and serves the results as
// options, so saving or previewing a new one needs connection read the way a
// chart query does. Home Assistant entity queries and edits that keep an
// existing query stay open to a dashboard editor.
func TestVariableQueriesNeedConnectionRead(t *testing.T) {
	ts, admin := setupTestServer(t)
	editor := sessionWith(t, "editor", permissions.PermDashboardsRead, permissions.PermDashboardsWrite)

	status, resp := doJSON(t, ts, editor, http.MethodPost, "/api/dashboards", map[string]any{"name": "Editor's"})
	require.Equalf(t, http.StatusCreated, status, "the editor must be able to create a dashboard: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)

	sqlVar := map[string]any{"name": "host", "type": "query", "query": map[string]any{
		"source": "sql_query", "connection_id": uuid.NewString(), "text": "SELECT secret FROM vault",
	}}
	haVar := map[string]any{"name": "person", "type": "query", "query": map[string]any{
		"source": "ha_entities", "connection_id": uuid.NewString(), "pattern": "person.*",
	}}
	display := func(vars ...map[string]any) map[string]any {
		return map[string]any{"display": map[string]any{"variables": vars}}
	}

	status, _ = doJSON(t, ts, editor, http.MethodPut, "/api/dashboards/"+dashID, display(sqlVar))
	assert.Equal(t, http.StatusForbidden, status, "saving a SQL variable needs connection read")
	status, _ = doJSON(t, ts, editor, http.MethodPost, "/api/dashboards/"+dashID+"/variables/preview", map[string]any{"variable": sqlVar})
	assert.Equal(t, http.StatusForbidden, status, "previewing a SQL variable needs connection read")
	status, _ = doJSON(t, ts, editor, http.MethodPost, "/api/dashboards", map[string]any{"name": "Planted", "display": display(sqlVar)["display"]})
	assert.Equal(t, http.StatusForbidden, status, "creating a dashboard with a SQL variable needs connection read")
	status, resp = doJSON(t, ts, editor, http.MethodPut, "/api/dashboards/"+dashID, display(haVar))
	require.Equalf(t, http.StatusOK, status, "an entity query stays inside the layout grant and is open to editors: %v", resp)

	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/dashboards/"+dashID, display(sqlVar))
	require.Equalf(t, http.StatusOK, status, "a user with connection read saves the SQL variable: %v", resp)
	status, resp = doJSON(t, ts, editor, http.MethodPut, "/api/dashboards/"+dashID, display(haVar, sqlVar))
	assert.Equalf(t, http.StatusOK, status, "keeping an existing SQL query while editing other variables needs nothing more: %v", resp)
}

// TestDashboardImportChecksChartQueriesBeforeWriting pins that importing a
// dashboard holding a chart query decides the importer's connection access
// before the transaction that writes the rows. The check ran inside it, where
// the permission lookup waited on the connection the transaction held, so the
// import never answered on a one-connection pool, and a refusal would have
// surfaced as an internal error.
func TestDashboardImportChecksChartQueriesBeforeWriting(t *testing.T) {
	ts, bearer := setupTestServer(t)
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Chart", PackageName: "chart", TagName: "chart-widget", Version: "0.1.0"},
	}), "the chart package must register")
	t.Cleanup(func() { widgets.Unregister("core") })
	author := sessionWith(t, "author", permissions.PermDashboardsRead, permissions.PermDashboardsWrite)

	doc := map[string]any{"document": map[string]any{
		"karakuri_dashboard": 1,
		"name":               "Imported charts",
		"grid":               map[string]any{"rows": 12, "cols": 12},
		"widgets": []map[string]any{{
			"package": "chart", "x": 0, "y": 0, "width": 6, "height": 4,
			"config": `{"queries":[{"refId":"A","connectionId":"` + uuid.NewString() + `","language":"promql","text":"up"}]}`,
		}},
	}}
	cases := []struct {
		name  string
		token string
		want  int
	}{
		{name: "an importer who may read connections", token: bearer, want: http.StatusCreated},
		{name: "an importer who may not read connections", token: author, want: http.StatusForbidden},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			answered := make(chan int, 1)
			go func() {
				status, _ := doJSON(t, ts, tc.token, http.MethodPost, "/api/dashboards/import", doc)
				answered <- status
			}()
			select {
			case status := <-answered:
				assert.Equal(t, tc.want, status, "the chart query decides the import by the importer's connection access")
			case <-time.After(10 * time.Second):
				require.FailNow(t, "the import never answered; the permission check is waiting on the transaction's connection")
			}
		})
	}
}

// TestChartEnergySelectionNeedsConnectionRead locks the energy half of a
// chart query into the rule that changing what a chart reads needs connection
// read. The comparison that decides whether a query changed covered its
// connection, language, text, and Home Assistant selection but not its energy
// selection, so an editor without connection read could repoint an energy
// chart from one plug to the whole house and read figures nobody placed.
func TestChartEnergySelectionNeedsConnectionRead(t *testing.T) {
	ts, admin := setupTestServer(t)
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Chart", PackageName: "chart", TagName: "chart-widget", Version: "0.1.0"},
	}), "the chart package must register")
	t.Cleanup(func() { widgets.Unregister("core") })
	editor := sessionWith(t, "editor", permissions.PermDashboardsRead, permissions.PermDashboardsWrite)

	status, resp := doJSON(t, ts, editor, http.MethodPost, "/api/dashboards", map[string]any{"name": "Editor's"})
	require.Equalf(t, http.StatusCreated, status, "the editor must be able to create a dashboard: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, admin, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	chartWidget := resp["data"].([]any)[0].(map[string]any)["id"]

	energy := func(viz, target string) string {
		return `{"viz":"` + viz + `","queries":[{"refId":"A","connectionId":"energy","language":"energy","energy":{"targets":["` + target + `"],"measure":"usage"}}]}`
	}
	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": chartWidget, "x": 0, "y": 0, "width": 4, "height": 3, "config": energy("bar", "source:"+uuid.NewString()),
	})
	require.Equalf(t, http.StatusCreated, status, "a user with connection read places the energy chart: %v", resp)
	placement := "/api/dashboards/" + dashID + "/widgets/" + resp["data"].(map[string]any)["id"].(string)

	status, _ = doJSON(t, ts, editor, http.MethodPut, placement, map[string]any{"config": energy("bar", "house")})
	assert.Equal(t, http.StatusForbidden, status, "choosing another energy target changes what the chart reads, which needs connection read")
}
