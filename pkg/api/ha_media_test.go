package api_test

import (
	"io"
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/middleware"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestHAMediaRoutesFollowDashboardBindings covers the read routes feature 024
// adds for shapes get_states cannot carry. A public dashboard binding one
// camera and one to-do list lets an anonymous viewer read exactly those: the
// camera's image (fetched with the connection's token, which the fake checks
// and the viewer never holds), its history, and the list's items. A second
// camera and list on the same connection stay out of reach, and a camera
// route refuses an entity of another domain.
func TestHAMediaRoutesFollowDashboardBindings(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetState("camera.door", "idle", nil)
	fake.SetState("camera.yard", "idle", nil)
	fake.SetState("todo.shopping", "1", nil)
	fake.SetState("todo.chores", "2", nil)
	fake.SetImage("/api/camera_proxy/camera.door", []byte("door-jpeg"))
	fake.SetImage("/api/camera_proxy/camera.yard", []byte("yard-jpeg"))
	fake.SetTodoItems("todo.shopping", []map[string]any{{"uid": "1", "summary": "Milk", "status": "needs_action"}})
	fake.SetTodoItems("todo.chores", []map[string]any{{"uid": "2", "summary": "Mop", "status": "needs_action"}})
	// Home Assistant's compressed minimal history: s is the state and lu the
	// update time in fractional Unix seconds, so 1700000000.5 is t=1700000000500.
	fake.SetHistory("camera.door", []map[string]any{{"s": "idle", "lu": 1700000000.5}, {"s": "recording", "lu": 1700000060.0}})

	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{
		{Name: "Camera", PackageName: "camera", TagName: "camera-widget", Version: "0.1.0", Scope: widgets.WidgetScope{Bindings: "."}},
		{Name: "To-do List", PackageName: "todo-list", TagName: "todo-list-widget", Version: "0.1.0", Scope: widgets.WidgetScope{Bindings: "."}},
	}), "the widget packages must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Hall", "is_public": true})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	ids := map[string]any{}
	for _, item := range resp["data"].([]any) {
		w := item.(map[string]any)
		ids[w["package_name"].(string)] = w["id"]
	}
	for pkg, entity := range map[string]string{"camera": "camera.door", "todo-list": "todo.shopping"} {
		status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
			"widget_id": ids[pkg], "x": 0, "y": 0, "width": 3, "height": 3,
			"config": `{"source":"ha","displayOnly":true,"connectionId":"` + connID + `","entityId":"` + entity + `"}`,
		})
		require.Equalf(t, http.StatusCreated, status, "the %s placement must be created: %v", pkg, resp)
	}

	raw := func(path string) (int, string) {
		t.Helper()
		req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
		require.NoError(t, err)
		req.Header.Set(middleware.DashboardHeader, dashID)
		res, err := ts.Client().Do(req)
		require.NoError(t, err)
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		return res.StatusCode, string(body)
	}
	base := "/api/connections/" + connID + "/ha"

	status, body := raw(base + "/camera/camera.door/snapshot")
	assert.Equal(t, http.StatusOK, status, "the bound camera's snapshot must relay")
	assert.Equal(t, "door-jpeg", body, "the relay must carry Home Assistant's image, fetched with the connection's token")
	status, _ = raw(base + "/camera/camera.yard/snapshot")
	assert.Equal(t, http.StatusForbidden, status, "a camera the dashboard does not bind stays out of reach")
	status, _ = raw(base + "/camera/todo.shopping/snapshot")
	assert.Equal(t, http.StatusBadRequest, status, "a camera route must refuse an entity of another domain")

	status, resp = doAnon(t, ts, dashID, http.MethodGet, base+"/todo/todo.shopping", nil)
	require.Equalf(t, http.StatusOK, status, "the bound list must be readable: %v", resp)
	items := resp["data"].(map[string]any)["items"].([]any)
	require.Len(t, items, 1, "the list carries its one item")
	assert.Equal(t, "Milk", items[0].(map[string]any)["summary"], "items come from todo/item/list")
	status, _ = doAnon(t, ts, dashID, http.MethodGet, base+"/todo/todo.chores", nil)
	assert.Equal(t, http.StatusForbidden, status, "a list the dashboard does not bind stays out of reach")

	status, resp = doAnon(t, ts, dashID, http.MethodGet, base+"/history/camera.door?hours=24", nil)
	require.Equalf(t, http.StatusOK, status, "the bound entity's history must be readable: %v", resp)
	points := resp["data"].(map[string]any)["points"].([]any)
	require.Len(t, points, 2, "both recorded states come back")
	assert.Equal(t, float64(1700000000500), points[0].(map[string]any)["t"], "fractional seconds become Unix milliseconds")
	status, _ = doAnon(t, ts, dashID, http.MethodGet, base+"/history/camera.yard?hours=24", nil)
	assert.Equal(t, http.StatusForbidden, status, "history of an unbound entity stays out of reach")
}
