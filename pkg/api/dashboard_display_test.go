package api_test

import (
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestVisibilityConditionGrantsReadOnly covers the scope a visibility
// condition adds: a clock hidden unless a door is open must let an anonymous
// viewer of the public dashboard read that one entity to decide, and must
// never let them press it, even with public control on. The runtime state
// that decides visibility is readable by the same viewer, while commanding
// it is reserved to editors.
func TestVisibilityConditionGrantsReadOnly(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetState("binary_sensor.garage", "on", nil)
	fake.SetState("lock.front_door", "locked", nil)

	// A widget that declares no scope of its own: everything this placement
	// grants comes from its condition.
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Digital Clock", PackageName: "digital-clock", TagName: "digital-clock-widget", Version: "0.1.0",
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{
		"name": "Garage Panel", "is_public": true, "public_control": true,
	})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": widgetID, "x": 0, "y": 0, "width": 3, "height": 2, "config": `{}`,
		"display": map[string]any{"hidden": true, "conditions": []any{map[string]any{
			"kind": "state", "source": "ha", "connectionId": connID, "entityId": "binary_sensor.garage",
			"operator": "equals", "value": "on",
		}}},
	})
	require.Equalf(t, http.StatusCreated, status, "the placement must be created: %v", resp)
	placementID, _ := resp["data"].(map[string]any)["id"].(string)

	status, resp = doAnon(t, ts, dashID, http.MethodGet, "/api/connections/"+connID+"/ha/states", nil)
	require.Equalf(t, http.StatusOK, status, "the condition's source must be readable to decide visibility: %v", resp)
	states, _ := resp["data"].([]any)
	require.Len(t, states, 1, "only the condition's entity may come back")
	assert.Equal(t, "binary_sensor.garage", states[0].(map[string]any)["entity_id"], "the condition grants its own entity and no other")

	status, _ = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "homeassistant", "service": "turn_off",
		"target": map[string]any{"entity_id": "binary_sensor.garage"},
	})
	assert.Equal(t, http.StatusForbidden, status, "a condition reads its source and never grants a write, whatever the control policy")

	status, _ = doAnon(t, ts, "", http.MethodPost, "/api/dashboards/"+dashID+"/commands", map[string]any{
		"action": "show", "targets": []string{placementID}, "lock": "start",
	})
	assert.Equal(t, http.StatusUnauthorized, status, "an anonymous viewer cannot command the dashboard")

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/commands", map[string]any{
		"action": "show", "targets": []string{placementID}, "lock": "start",
	})
	require.Equalf(t, http.StatusOK, status, "the owner may command the dashboard: %v", resp)

	status, resp = doAnon(t, ts, "", http.MethodGet, "/api/dashboards/"+dashID+"/live", nil)
	require.Equalf(t, http.StatusOK, status, "a public dashboard's runtime state is readable by its anonymous viewers: %v", resp)
	locks, _ := resp["data"].(map[string]any)["locks"].(map[string]any)
	assert.Equal(t, []any{}, locks[placementID], "releasing start leaves the hidden-on-start placement with no locks")
}

// TestStatusCueGrantsReadAndValidates covers a status cue's two server
// contracts: its state source is readable by an anonymous viewer of a public
// dashboard, as a visibility condition's is, and never writable; and a write
// carrying a malformed style or cue is refused without storing it, while an
// empty style stores as none so the placement compares equal to one saved
// before styles existed.
func TestStatusCueGrantsReadAndValidates(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetState("cover.garage", "open", nil)
	fake.SetState("lock.front_door", "locked", nil)

	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Digital Clock", PackageName: "digital-clock", TagName: "digital-clock-widget", Version: "0.1.0",
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{
		"name": "Garage Panel", "is_public": true, "public_control": true,
	})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]

	place := func(display map[string]any) (int, map[string]any) {
		return doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
			"widget_id": widgetID, "x": 0, "y": 0, "width": 3, "height": 2, "config": `{}`, "display": display,
		})
	}

	refused := []struct {
		name    string
		display map[string]any
	}{
		{"an unknown animation", map[string]any{"cues": []any{map[string]any{"animation": "spin"}}}},
		{"a border too wide", map[string]any{"style": map[string]any{"border_width": 17}}},
		{"a radius too large", map[string]any{"style": map[string]any{"radius": 65}}},
		{"a color that closes the declaration", map[string]any{"style": map[string]any{"background": "red; position:fixed"}}},
		{"a color that loads a resource", map[string]any{"style": map[string]any{"background": "url(http://example.com/x)"}}},
		{"a cue condition of an unknown kind", map[string]any{"cues": []any{map[string]any{"conditions": []any{map[string]any{"kind": "weather"}}}}}},
		{"too many cues", map[string]any{"cues": []any{
			map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{},
			map[string]any{}, map[string]any{}, map[string]any{}, map[string]any{},
		}}},
	}
	for _, tc := range refused {
		status, _ := place(tc.display)
		assert.Equalf(t, http.StatusBadRequest, status, "%s must be refused", tc.name)
	}

	status, resp = place(map[string]any{"name": "Clock", "style": map[string]any{}})
	require.Equalf(t, http.StatusCreated, status, "a placement with an empty style must be created: %v", resp)
	display, _ := resp["data"].(map[string]any)["display"].(map[string]any)
	assert.NotContains(t, display, "style", "an empty style stores as none")

	status, resp = place(map[string]any{
		"style": map[string]any{"background": "transparent", "radius": 0},
		"cues": []any{map[string]any{"color": "#f38ba8", "animation": "blink", "conditions": []any{map[string]any{
			"kind": "state", "source": "ha", "connectionId": connID, "entityId": "cover.garage",
			"operator": "equals", "value": "open",
		}}}},
	})
	require.Equalf(t, http.StatusCreated, status, "a placement with a style and a cue must be created: %v", resp)
	display, _ = resp["data"].(map[string]any)["display"].(map[string]any)
	style, _ := display["style"].(map[string]any)
	assert.Equal(t, float64(0), style["radius"], "a square radius is kept rather than dropped as unset")

	status, resp = doAnon(t, ts, dashID, http.MethodGet, "/api/connections/"+connID+"/ha/states", nil)
	require.Equalf(t, http.StatusOK, status, "the cue's source must be readable to decide the cue: %v", resp)
	states, _ := resp["data"].([]any)
	require.Len(t, states, 1, "only the cue's entity may come back")
	assert.Equal(t, "cover.garage", states[0].(map[string]any)["entity_id"], "the cue grants its own entity and no other")

	status, _ = doAnon(t, ts, dashID, http.MethodPost, "/api/connections/"+connID+"/ha/service", map[string]any{
		"domain": "cover", "service": "close_cover",
		"target": map[string]any{"entity_id": "cover.garage"},
	})
	assert.Equal(t, http.StatusForbidden, status, "a cue reads its source and never grants a write")
}
