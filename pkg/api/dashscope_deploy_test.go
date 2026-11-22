package api_test

import (
	"net/http"
	"strings"
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestUnreadableDeploymentServesNoDraft pins the regression where a deployed
// version that could not be read made the server fall back to the draft: the
// layout response carried unpublished placements and scope granted their
// bindings to anonymous viewers. A public dashboard is deployed showing one
// entity, a second entity is added to the draft only, and the deployed
// snapshot is then made unparseable. Viewers must get neither the draft
// layout nor the draft's entity.
func TestUnreadableDeploymentServesNoDraft(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetState("light.lamps", "off", nil)
	fake.SetState("lock.front_door", "locked", nil)

	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Entity Control", PackageName: "entity-control", TagName: "entity-control-widget",
		Version: "0.1.0", Scope: widgets.WidgetScope{Bindings: "."},
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Hallway", "is_public": true})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]
	place := func(entity string) {
		t.Helper()
		status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
			"widget_id": widgetID, "x": 0, "y": 0, "width": 3, "height": 3,
			"config": `{"source":"ha","displayOnly":true,"connectionId":"` + connID + `","entityId":"` + entity + `"}`,
		})
		require.Equalf(t, http.StatusCreated, status, "the placement must be created: %v", resp)
	}

	place("light.lamps")
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the dashboard must deploy: %v", resp)
	place("lock.front_door")

	require.NoError(t, db.DB.Model(&models.DashboardVersion{}).Where("dashboard_id = ?", dashID).
		Update("snapshot", "{not json").Error, "the deployed snapshot must be corrupted")

	status, resp = doAnon(t, ts, dashID, http.MethodGet, "/api/connections/"+connID+"/ha/states", nil)
	if status == http.StatusOK {
		for _, s := range resp["data"].([]any) {
			assert.NotEqual(t, "lock.front_door", s.(map[string]any)["entity_id"],
				"an entity bound only in the draft must not be readable when the deployment cannot be read")
		}
	}

	status, resp = doAnon(t, ts, "", http.MethodGet, "/api/dashboards/"+dashID, nil)
	assert.NotEqual(t, http.StatusOK, status, "an unreadable deployment must fail rather than serve the draft")
	body, _ := resp["data"].(map[string]any)
	assert.False(t, strings.Contains(stringify(body), "lock.front_door"),
		"the layout response must not carry the unpublished placement")
}

// stringify flattens a decoded response for a containment check.
func stringify(v any) string {
	if v == nil {
		return ""
	}
	var b strings.Builder
	var walk func(any)
	walk = func(v any) {
		switch t := v.(type) {
		case map[string]any:
			for _, x := range t {
				walk(x)
			}
		case []any:
			for _, x := range t {
				walk(x)
			}
		case string:
			b.WriteString(t)
			b.WriteByte(' ')
		}
	}
	walk(v)
	return b.String()
}
