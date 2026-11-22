package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// doAnon performs a request with no credentials, optionally naming the
// dashboard it is being made from — exactly what the browser sends for a
// visitor who has only opened a public dashboard's URL.
func doAnon(t *testing.T, srv *httptest.Server, dashboard, method, path string, body any) (int, map[string]any) {
	t.Helper()

	var reqBody bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&reqBody).Encode(body), "the request body must encode")
	}
	req, err := http.NewRequest(method, srv.URL+path, &reqBody)
	require.NoErrorf(t, err, "the request to %s %s must build", method, path)
	req.Header.Set("Content-Type", "application/json")
	if dashboard != "" {
		req.Header.Set(middleware.DashboardHeader, dashboard)
	}

	resp, err := srv.Client().Do(req)
	require.NoErrorf(t, err, "%s %s must reach the server", method, path)
	defer func() { _ = resp.Body.Close() }()

	var decoded map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&decoded)
	return resp.StatusCode, decoded
}

// TestPublicDashboardExposesOnlyItsWidgets is the whole security boundary end
// to end: a real router, a real database, a real Home Assistant driver against
// a fake server speaking the real protocol, and an anonymous caller holding no
// permission at all.
//
// A dashboard's deployed layout is the grant. The house here has two entities
// and the dashboard shows one, so the visitor may read that one and no other —
// the alternative, handing out the whole entity list to render a single tile,
// is what makes a public dashboard unpublishable. Control is the second half:
// a published dashboard shows the house without giving out the switches until
// its author says otherwise, and even then only for the entities on it and
// through services of their own domain.
func TestPublicDashboardExposesOnlyItsWidgets(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetState("light.lamps", "off", map[string]any{"friendly_name": "Lamps"})
	fake.SetState("lock.front_door", "locked", nil)

	// The widget package has to be registered for its placement to authorize
	// anything: an unloaded plugin cannot serve the widget, so it must not
	// grant the widget's data either.
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name:        "Entity Control",
		PackageName: "entity-control",
		TagName:     "entity-control-widget",
		Version:     "0.1.0",
		Scope:       widgets.WidgetScope{Bindings: "."},
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant",
		"kind": "homeassistant",
		"config": map[string]any{
			"base_url": fake.URL,
			"token":    haTestToken,
		},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, connID, "the id addresses every request below")
	waitForConnected(t, mgr, connID)

	// A public dashboard holding one tile, bound to one of the two entities.
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{
		"name": "Hallway Panel", "is_public": true,
	})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, dashID, "the dashboard id is what a widget's request names")

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equalf(t, http.StatusOK, status, "the widget catalog must list the registered package: %v", resp)
	items, _ := resp["data"].([]any)
	require.NotEmpty(t, items, "the registered package must appear in the catalog")
	widgetID := items[0].(map[string]any)["id"]

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": widgetID,
		"x":         0, "y": 0, "width": 3, "height": 3,
		"config": `{"source":"ha","control":"auto","connectionId":"` + connID + `","entityId":"light.lamps"}`,
	})
	require.Equalf(t, http.StatusCreated, status, "the placement must be created: %v", resp)

	// A visitor who names no dashboard is just an anonymous caller.
	status, _ = doAnon(t, ts, "", http.MethodGet, "/api/connections/"+connID+"/ha/states", nil)
	assert.Equal(t, http.StatusUnauthorized, status,
		"without a dashboard behind it there is nothing to authorize an anonymous read of a connection")

	// Naming the dashboard grants exactly what the dashboard shows.
	status, resp = doAnon(t, ts, dashID, http.MethodGet, "/api/connections/"+connID+"/ha/states", nil)
	require.Equalf(t, http.StatusOK, status, "the dashboard's own tile must be able to read its entity: %v", resp)
	states, ok := resp["data"].([]any)
	require.True(t, ok, "the state list is an array")
	require.Lenf(t, states, 1, "only the entity the dashboard shows may come back, not the house: %v", states)
	assert.Equal(t, "light.lamps", states[0].(map[string]any)["entity_id"],
		"the one entity a widget binds to is the whole of what a public dashboard exposes on that connection")

	// Control is off by default, so the tile renders but does not press.
	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "light", "service": "turn_on",
		"target": map[string]any{"entity_id": "light.lamps"},
	})
	assert.Equalf(t, http.StatusForbidden, status,
		"publishing a dashboard shows the house; operating it is a separate decision: %v", resp)

	// The author opens it for operating.
	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{
		"public_control": true,
	})
	require.Equal(t, http.StatusOK, status, "the author must be able to permit anonymous control")

	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "light", "service": "turn_on",
		"target": map[string]any{"entity_id": "light.lamps"},
	})
	assert.Equalf(t, http.StatusOK, status, "with control permitted the tile on the page presses: %v", resp)

	// Permitting control does not widen which entities are reachable.
	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "lock", "service": "unlock",
		"target": map[string]any{"entity_id": "lock.front_door"},
	})
	assert.Equalf(t, http.StatusForbidden, status,
		"an entity no widget on the dashboard binds to stays out of reach however the dashboard is published: %v", resp)

	// A call with no target is the dangerous shape: in Home Assistant it acts
	// on every entity of the domain, which no widget on a dashboard is asking
	// for.
	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "light", "service": "turn_off",
	})
	assert.Equalf(t, http.StatusForbidden, status,
		"a service call naming no entity would reach every light, so a dashboard grant must refuse it: %v", resp)

	// Home Assistant acts on the union of everything a call addresses, so a
	// granted entity smuggled in beside an area is an area-wide call wearing
	// the dashboard's grant. Each of these carries light.lamps, which the
	// dashboard does control.
	for _, key := range []string{"area_id", "device_id", "floor_id", "label_id"} {
		status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
			"domain": "light", "service": "turn_off",
			"target": map[string]any{"entity_id": "light.lamps", key: "everything"},
		})
		assert.Equalf(t, http.StatusForbidden, status,
			"%s beside a granted entity widens the call past the dashboard and must be refused: %v", key, resp)

		status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
			"domain": "light", "service": "turn_off",
			"service_data": map[string]any{"entity_id": "light.lamps", key: "everything"},
		})
		assert.Equalf(t, http.StatusForbidden, status,
			"%s in service_data addresses just as widely as in target: %v", key, resp)
	}

	// Naming a bound entity does not make any service about it. A script,
	// shell_command, or rest_command service runs whatever it is and takes
	// entity_id as data, so the service must be the entity's own domain.
	for _, call := range []struct{ domain, service string }{
		{"script", "unlock_front_door"}, {"shell_command", "wipe"}, {"homeassistant", "turn_off"},
	} {
		status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
			"domain": call.domain, "service": call.service,
			"service_data": map[string]any{"entity_id": "light.lamps"},
		})
		assert.Equalf(t, http.StatusForbidden, status,
			"%s.%s does not act on light.lamps, so binding the light must not authorize it: %v", call.domain, call.service, resp)
	}

	// Every script is also a service of its own, script.<object_id>, which
	// runs that script whatever the call targets. A bound script starts
	// through turn_on and grants no other script.
	fake.SetState("script.night", "off", nil)
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": widgetID,
		"x":         3, "y": 0, "width": 3, "height": 3,
		"config": `{"source":"ha","control":"auto","connectionId":"` + connID + `","entityId":"script.night"}`,
	})
	require.Equalf(t, http.StatusCreated, status, "the script placement must be created: %v", resp)
	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "script", "service": "turn_on", "target": map[string]any{"entity_id": "script.night"},
	})
	assert.Equalf(t, http.StatusOK, status, "the bound script starts through turn_on, as the widget calls it: %v", resp)
	status, resp = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "script", "service": "unlock_front_door", "target": map[string]any{"entity_id": "script.night"},
	})
	assert.Equalf(t, http.StatusForbidden, status, "another script's own service must not ride on the bound script: %v", resp)

	// The dashboard is the grant, not the connection: another public dashboard
	// with nothing on it grants nothing on the same connection.
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{
		"name": "Empty Panel", "is_public": true,
	})
	require.Equalf(t, http.StatusCreated, status, "the second dashboard must be created: %v", resp)
	emptyID, _ := resp["data"].(map[string]any)["id"].(string)

	status, _ = doAnon(t, ts, emptyID, http.MethodGet, "/api/connections/"+connID+"/ha/states", nil)
	assert.Equal(t, http.StatusForbidden, status,
		"a dashboard that shows nothing on a connection exposes nothing on it")
}
