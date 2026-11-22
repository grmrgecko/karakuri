package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDashboardGroupMoveIsASharingChange pins that a dashboard inherits its
// group's shares, so moving it between groups needs admin on the dashboard
// and edit on the target group. Without that, a user holding an edit share
// moved the dashboard into a group they own and became its admin through the
// group. Listing a group's shares follows the same edit check.
func TestDashboardGroupMoveIsASharingChange(t *testing.T) {
	ts, _ := setupTestServer(t)
	perms := []string{
		permissions.PermDashboardsRead, permissions.PermDashboardsWrite,
		permissions.PermDashboardGroupsRead, permissions.PermDashboardGroupsWrite,
	}
	owner := sessionWith(t, "owner", perms...)
	editor := sessionWith(t, "editor", perms...)

	status, resp := doJSON(t, ts, owner, http.MethodPost, "/api/dashboards", map[string]any{"name": "Owner's"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, owner, http.MethodPost, "/api/dashboards/"+dashID+"/shares", map[string]any{"username": "editor", "permission": "edit"})
	require.Equalf(t, http.StatusCreated, status, "the edit share must be created: %v", resp)

	newGroup := func(token, name string) uint {
		status, resp := doJSON(t, ts, token, http.MethodPost, "/api/dashboard_groups", map[string]any{"name": name})
		require.Equalf(t, http.StatusCreated, status, "the %s group must be created: %v", name, resp)
		return uint(resp["data"].(map[string]any)["id"].(float64))
	}
	ownerGroup := newGroup(owner, "Owner group")
	editorGroup := newGroup(editor, "Editor group")

	status, _ = doJSON(t, ts, editor, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"group_id": editorGroup})
	assert.Equal(t, http.StatusForbidden, status, "an editor must not move the dashboard into a group they own")
	status, _ = doJSON(t, ts, editor, http.MethodPut, "/api/dashboards/reorder", map[string]any{
		"items": []map[string]any{{"id": dashID, "group_id": editorGroup, "sort_order": 0}},
	})
	assert.Equal(t, http.StatusForbidden, status, "reorder must not move the dashboard into another group either")
	status, _ = doJSON(t, ts, editor, http.MethodDelete, "/api/dashboards/"+dashID, nil)
	assert.Equal(t, http.StatusForbidden, status, "the editor must still lack admin on the dashboard")
	status, resp = doJSON(t, ts, editor, http.MethodPut, "/api/dashboards/reorder", map[string]any{
		"items": []map[string]any{{"id": dashID, "group_id": nil, "sort_order": 3}},
	})
	assert.Equalf(t, http.StatusOK, status, "reordering within the same group stays open to an editor: %v", resp)

	status, _ = doJSON(t, ts, owner, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"group_id": editorGroup})
	assert.Equal(t, http.StatusForbidden, status, "the owner must not place the dashboard in a group they cannot edit")
	status, resp = doJSON(t, ts, owner, http.MethodPut, "/api/dashboards/"+dashID, map[string]any{"group_id": ownerGroup})
	require.Equalf(t, http.StatusOK, status, "the owner moves the dashboard into their own group: %v", resp)
	assert.EqualValues(t, ownerGroup, resp["data"].(map[string]any)["group_id"], "the move must persist")

	status, _ = doJSON(t, ts, editor, http.MethodPost, "/api/dashboards", map[string]any{"name": "Planted", "group_id": ownerGroup})
	assert.Equal(t, http.StatusForbidden, status, "creating a dashboard in someone else's group needs edit on the group")

	shares := fmt.Sprintf("/api/dashboard_groups/%d/shares", ownerGroup)
	status, _ = doJSON(t, ts, editor, http.MethodGet, shares, nil)
	assert.Equal(t, http.StatusForbidden, status, "a private group's shares must not be listed to a user without edit on it")
	status, _ = doJSON(t, ts, owner, http.MethodGet, shares, nil)
	assert.Equal(t, http.StatusOK, status, "the group owner lists its shares")
}
