package api_test

import (
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestRevisionsRefuseStaleReplacements pins the fix for concurrent dashboard
// edits overwriting each other. Two writers read a placement at config
// revision 0; the first replacement lands and raises it to 1, and the second,
// still naming 0, is refused with 409 instead of erasing the first. A writer
// that names no revision keeps the old last-write-wins behaviour. The
// dashboard's display settings follow the same rule.
func TestRevisionsRefuseStaleReplacements(t *testing.T) {
	ts, bearer := setupTestServer(t)
	require.NoError(t, widgets.Register(zap.NewNop(), "core", t.TempDir(), []widgets.Spec{{
		Name: "Image", PackageName: "image", TagName: "image-widget", Version: "0.1.0",
	}}), "the widget package must register")
	t.Cleanup(func() { widgets.Unregister("core") })

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards", map[string]any{"name": "Shared"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID, _ := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/widgets", nil)
	require.Equal(t, http.StatusOK, status, "the widget catalog must load")
	widgetID := resp["data"].([]any)[0].(map[string]any)["id"]
	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/dashboards/"+dashID+"/widgets", map[string]any{
		"widget_id": widgetID, "x": 0, "y": 0, "width": 2, "height": 2, "config": `{}`,
	})
	require.Equalf(t, http.StatusCreated, status, "the placement must be created: %v", resp)
	placement := "/api/dashboards/" + dashID + "/widgets/" + resp["data"].(map[string]any)["id"].(string)

	status, resp = doJSON(t, ts, bearer, http.MethodPut, placement, map[string]any{"config": `{"by":"first"}`, "expected_config_revision": 0})
	require.Equalf(t, http.StatusOK, status, "the first writer's replacement must land: %v", resp)
	assert.EqualValues(t, 1, resp["data"].(map[string]any)["config_revision"], "a replacement raises the revision")

	status, _ = doJSON(t, ts, bearer, http.MethodPut, placement, map[string]any{"config": `{"by":"second"}`, "expected_config_revision": 0})
	assert.Equal(t, http.StatusConflict, status, "a writer still holding revision 0 must not overwrite the first replacement")

	status, _ = doJSON(t, ts, bearer, http.MethodPut, placement, map[string]any{"x": 3})
	assert.Equal(t, http.StatusOK, status, "a move is not a config replacement and needs no revision")

	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"display": map[string]any{"pixel_shift": 4}, "expected_display_revision": 0})
	require.Equal(t, http.StatusOK, status, "the first display replacement must land")
	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"display": map[string]any{"pixel_shift": 8}, "expected_display_revision": 0})
	assert.Equal(t, http.StatusConflict, status, "a stale display replacement must be refused")
}
