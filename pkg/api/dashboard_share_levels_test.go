package api_test

import (
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDashboardShareLevels pins the read share level. A read share shows a
// private dashboard without operating it, a view share operates it, and an
// unknown level is refused rather than stored. Before the check, a typo such
// as "admn" was saved, granted no access, and still counted as having been
// shared, which let the user operate a public dashboard.
func TestDashboardShareLevels(t *testing.T) {
	ts, _ := setupTestServer(t)
	owner := sessionWith(t, "owner", permissions.PermDashboardsRead, permissions.PermDashboardsWrite)
	reader := sessionWith(t, "reader")
	operator := sessionWith(t, "operator")

	status, resp := doJSON(t, ts, owner, http.MethodPost, "/api/dashboards", map[string]any{"name": "Hallway"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	shares := "/api/dashboards/" + dashID + "/shares"

	status, _ = doJSON(t, ts, owner, http.MethodPost, shares, map[string]any{"username": "reader", "permission": "admn"})
	assert.Equal(t, http.StatusBadRequest, status, "an unknown share level must be refused")
	status, resp = doJSON(t, ts, owner, http.MethodPost, shares, map[string]any{"username": "reader", "permission": "read"})
	require.Equalf(t, http.StatusCreated, status, "the read share must be created: %v", resp)
	status, resp = doJSON(t, ts, owner, http.MethodPost, shares, map[string]any{"username": "operator"})
	require.Equalf(t, http.StatusCreated, status, "a share with no level must default to view: %v", resp)
	assert.Equal(t, "view", resp["data"].(map[string]any)["permission"], "the default level must operate, as shares did before read existed")

	for _, tc := range []struct {
		name    string
		token   string
		operate bool
	}{
		{"owner", owner, true},
		{"reader", reader, false},
		{"operator", operator, true},
	} {
		status, resp := doJSON(t, ts, tc.token, http.MethodGet, "/api/dashboards/"+dashID, nil)
		require.Equalf(t, http.StatusOK, status, "%s must be able to open the shared dashboard: %v", tc.name, resp)
		assert.Equalf(t, tc.operate, resp["data"].(map[string]any)["can_operate"], "can_operate for %s", tc.name)
	}

	status, _ = doJSON(t, ts, reader, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"name": "Renamed"})
	assert.Equal(t, http.StatusForbidden, status, "a read share must not edit the dashboard")
}

// TestDashboardWriteResponsesRedactTheOwner locks the responses of the write
// routes to what a read shows of a dashboard's owner. Update, deploy, and
// version restore answer with the dashboard, and only the read routes stripped
// the owner, so an editor without api:dashboards:read was handed the owner's
// email address and UI preferences by saving a change. The owner still sees
// their own account.
func TestDashboardWriteResponsesRedactTheOwner(t *testing.T) {
	ts, _ := setupTestServer(t)
	owner := sessionWith(t, "owner", permissions.PermDashboardsRead, permissions.PermDashboardsWrite, permissions.PermDashboardsDeploy)
	editor := sessionWith(t, "editor", permissions.PermDashboardsWrite, permissions.PermDashboardsDeploy)

	status, resp := doJSON(t, ts, owner, http.MethodPost, "/api/dashboards", map[string]any{"name": "Hallway"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	base := "/api/dashboards/" + resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, owner, http.MethodPost, base+"/shares", map[string]any{"username": "editor", "permission": "edit"})
	require.Equalf(t, http.StatusCreated, status, "the edit share must be created: %v", resp)

	for _, tc := range []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"update", http.MethodPut, base, map[string]any{"description": "by the door"}},
		{"deploy", http.MethodPost, base + "/deploy", map[string]any{}},
		{"version restore", http.MethodPost, base + "/versions/1/restore", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doJSON(t, ts, editor, tc.method, tc.path, tc.body)
			require.Equalf(t, http.StatusOK, status, "an edit share must reach %s: %v", tc.name, resp)
			shown, _ := resp["data"].(map[string]any)["owner"].(map[string]any)
			require.NotNil(t, shown, "the response names the owner")
			assert.Equal(t, "owner", shown["username"], "the owner is still named")
			assert.Empty(t, shown["email"], "an editor without dashboard read is not shown the owner's email address")
		})
	}

	status, resp = doJSON(t, ts, owner, http.MethodPut, base, map[string]any{"description": "hall"})
	require.Equalf(t, http.StatusOK, status, "the owner must be able to update: %v", resp)
	assert.Equal(t, "owner@example.com", resp["data"].(map[string]any)["owner"].(map[string]any)["email"],
		"the owner sees their own account")
}
