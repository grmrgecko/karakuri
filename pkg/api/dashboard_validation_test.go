package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDashboardReferencesAreValidated locks what a dashboard's group and theme
// references answer. The update and reorder routes read 0 as "none", and the
// create route stored it, which the foreign key refused as a 500. A theme ID
// naming no theme failed the same key on create and update.
func TestDashboardReferencesAreValidated(t *testing.T) {
	ts, admin := setupTestServer(t)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/dashboards",
		map[string]any{"name": "Hall", "group_id": 0, "theme_id": 0})
	require.Equalf(t, http.StatusCreated, status, "0 means no group and no theme, as it does on update: %v", resp)
	var hall models.Dashboard
	require.NoError(t, db.DB.Where("name = ?", "Hall").First(&hall).Error, "the dashboard must be stored")
	assert.Nil(t, hall.GroupID, "an ungrouped dashboard holds NULL, which is what the lists and moves compare")
	assert.Nil(t, hall.ThemeID, "a dashboard without a theme holds NULL")

	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/dashboards",
		map[string]any{"name": "Porch", "theme_id": 4040})
	assert.Equal(t, http.StatusBadRequest, status, "a theme that does not exist is the caller's mistake on create")

	status, _ = doJSON(t, ts, admin, http.MethodPut, "/api/dashboards/"+hall.ID.String(),
		map[string]any{"theme_id": 4040})
	assert.Equal(t, http.StatusBadRequest, status, "a theme that does not exist is the caller's mistake on update")

	theme := &models.Theme{Name: "Night", PackageName: "night", Version: "1.0.0", Variables: "{}"}
	require.NoError(t, db.DB.Create(theme).Error)
	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/dashboards/"+hall.ID.String(),
		map[string]any{"theme_id": theme.ID})
	require.Equalf(t, http.StatusOK, status, "an existing theme is applied: %v", resp)
	require.NoError(t, db.DB.First(&hall, "id = ?", hall.ID).Error)
	require.NotNil(t, hall.ThemeID, "the theme must be stored")
	assert.Equal(t, theme.ID, *hall.ThemeID, "the dashboard names the theme it was given")
}

// TestDashboardRenameKeepsAName locks that a dashboard or dashboard group
// cannot be renamed to nothing. The creates require a name and the updates
// stored an empty one, which left a list row with no label to open it by.
func TestDashboardRenameKeepsAName(t *testing.T) {
	ts, admin := setupTestServer(t)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/dashboards", map[string]any{"name": "Hall"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	id := data(t, resp)["id"].(string)
	status, _ = doJSON(t, ts, admin, http.MethodPut, "/api/dashboards/"+id, map[string]any{"name": ""})
	assert.Equal(t, http.StatusBadRequest, status, "an empty dashboard name is refused on update as on create")
	status, _ = doJSON(t, ts, admin, http.MethodPut, "/api/dashboards/"+id, map[string]any{"name": "Hallway"})
	assert.Equal(t, http.StatusOK, status, "a real name is still a rename")

	group := &models.DashboardGroup{Name: "Upstairs", OwnerID: hallOwner(t)}
	require.NoError(t, db.DB.Create(group).Error)
	status, _ = doJSON(t, ts, admin, http.MethodPut, fmt.Sprintf("/api/dashboard_groups/%d", group.ID), map[string]any{"name": ""})
	assert.Equal(t, http.StatusBadRequest, status, "an empty group name is refused on update as on create")
	require.NoError(t, db.DB.First(group, group.ID).Error)
	assert.Equal(t, "Upstairs", group.Name, "the group keeps its name")
}

// hallOwner returns the fixture admin's ID.
func hallOwner(t *testing.T) uuid.UUID {
	t.Helper()
	var admin models.User
	require.NoError(t, db.DB.Where("username = ?", "admin").First(&admin).Error)
	return admin.ID
}

// TestDashboardShareNamesOneGrantee locks that a share names a user or a
// group, not both. A request carrying both stored one row for the pair, so
// removing the user's share also took the group's away, and deleting the group
// took the user's.
func TestDashboardShareNamesOneGrantee(t *testing.T) {
	ts, admin := setupTestServer(t)
	sessionWith(t, "alice")
	family := &models.UserGroup{Name: "Family"}
	require.NoError(t, family.Create())

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/dashboards", map[string]any{"name": "Hall"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	id := data(t, resp)["id"].(string)
	group := &models.DashboardGroup{Name: "Upstairs", OwnerID: hallOwner(t)}
	require.NoError(t, db.DB.Create(group).Error)

	for _, path := range []string{"/api/dashboards/" + id + "/shares", fmt.Sprintf("/api/dashboard_groups/%d/shares", group.ID)} {
		status, _ = doJSON(t, ts, admin, http.MethodPost, path, map[string]any{"username": "alice", "group_name": "Family"})
		assert.Equalf(t, http.StatusBadRequest, status, "one share is one grantee: %s", path)
		status, _ = doJSON(t, ts, admin, http.MethodPost, path, map[string]any{"username": "alice"})
		assert.Equalf(t, http.StatusCreated, status, "a user alone is shared with: %s", path)
		status, _ = doJSON(t, ts, admin, http.MethodPost, path, map[string]any{"group_name": "Family"})
		assert.Equalf(t, http.StatusCreated, status, "a group alone is shared with: %s", path)
	}
	var both int64
	require.NoError(t, db.DB.Model(&models.DashboardAccess{}).Where("user_id IS NOT NULL AND group_id IS NOT NULL").Count(&both).Error)
	assert.Zero(t, both, "no dashboard share row names both a user and a group")
}
