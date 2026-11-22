package api_test

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// withEncryptionKey gives the config the key API keys are sealed with.
func withEncryptionKey(t *testing.T) {
	t.Helper()
	previous := config.C.Security.EncryptionKey
	config.C.Security.EncryptionKey = testEncryptionKey
	t.Cleanup(func() { config.C.Security.EncryptionKey = previous })
}

// TestDeletesThatMatchNothingAreNotFound locks that a delete naming a row the
// caller does not hold answers 404. These handlers scoped the delete correctly
// and then reported success for zero rows, and the audit log records every
// 2xx: deleting another user's API key ID wrote "Deleted API key <their key>"
// under the caller's name while the key kept working.
func TestDeletesThatMatchNothingAreNotFound(t *testing.T) {
	ts, admin := setupTestServer(t)
	withEncryptionKey(t)
	alice := sessionWith(t, "alice", perm.PermAPIKeysRead, perm.PermAPIKeysWrite)
	bob := sessionWith(t, "bob", perm.PermAPIKeysRead, perm.PermAPIKeysWrite)

	status, resp := doJSON(t, ts, alice, http.MethodPost, "/api/api_keys", map[string]any{"name": "ci"})
	require.Equalf(t, http.StatusCreated, status, "alice must be able to create a key: %v", resp)
	var key models.APIKey
	require.NoError(t, db.DB.Where("name = ?", "ci").First(&key).Error, "the key must be stored")

	status, _ = doJSON(t, ts, bob, http.MethodDelete, fmt.Sprintf("/api/api_keys/%d", key.ID), nil)
	assert.Equal(t, http.StatusNotFound, status, "another user's key is not one bob can delete")
	assert.NoError(t, db.DB.First(&models.APIKey{}, key.ID).Error, "alice's key must survive")

	status, _ = doJSON(t, ts, alice, http.MethodDelete, fmt.Sprintf("/api/api_keys/%d", key.ID), nil)
	assert.Equal(t, http.StatusOK, status, "the owner deletes the key")

	status, _ = doJSON(t, ts, bob, http.MethodDelete, "/api/auth/sessions/"+uuid.NewString(), nil)
	assert.Equal(t, http.StatusNotFound, status, "a session that is not the caller's is not revoked")

	status, _ = doJSON(t, ts, admin, http.MethodDelete, "/api/users/"+uuid.NewString(), nil)
	assert.Equal(t, http.StatusNotFound, status, "a user that does not exist is not deleted")
}

// TestRolePermissionDeleteStaysInItsRole locks that the role in the path
// scopes the delete. The handler ignored it, so naming role A with a
// permission entry of role B revoked B's grant and logged it against A.
func TestRolePermissionDeleteStaysInItsRole(t *testing.T) {
	ts, admin := setupTestServer(t)
	a := &models.Role{Name: "a"}
	b := &models.Role{Name: "b"}
	require.NoError(t, a.Create())
	require.NoError(t, b.Create())
	grant := &models.RolePermission{RoleID: b.ID, Permission: perm.PermFlowsRead}
	require.NoError(t, db.DB.Create(grant).Error)

	status, _ := doJSON(t, ts, admin, http.MethodDelete, fmt.Sprintf("/api/roles/%d/permissions/%d", a.ID, grant.ID), nil)
	assert.Equal(t, http.StatusNotFound, status, "role a holds no such permission entry")
	assert.NoError(t, db.DB.First(&models.RolePermission{}, grant.ID).Error, "role b keeps its grant")

	status, _ = doJSON(t, ts, admin, http.MethodDelete, fmt.Sprintf("/api/roles/%d/permissions/%d", b.ID, grant.ID), nil)
	assert.Equal(t, http.StatusOK, status, "the entry is removed through its own role")
}

// TestDeletedGroupTakesItsRolesWithIt locks that a group's role assignments
// die with it. Groups are soft-deleted and creating one by the same name
// revives the row with its ID, so an assignment left behind handed the old
// group's role to whoever joined the new one.
func TestDeletedGroupTakesItsRolesWithIt(t *testing.T) {
	setupTestServer(t)
	role := &models.Role{Name: "operators"}
	require.NoError(t, role.Create())
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: perm.PermFlowsWrite}).Error)

	ops := &models.UserGroup{Name: "Ops"}
	require.NoError(t, ops.Create())
	require.NoError(t, db.DB.Create(&models.RoleAssignment{RoleID: role.ID, GroupID: &ops.ID}).Error)
	require.NoError(t, ops.Delete())

	revived := &models.UserGroup{Name: "Ops"}
	require.NoError(t, revived.Create(), "the name is free again after the delete")
	user := &models.User{ID: uuid.New(), Username: "newhire", Email: "newhire@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(user).Error)
	require.NoError(t, db.DB.Create(&models.UserGroupMember{GroupID: revived.ID, UserID: user.ID}).Error)

	assert.False(t, auth.HasPermission(user.ID, perm.PermFlowsWrite),
		"nobody assigned a role to the new group, so its member holds none")
}

// TestAPIKeyRejectsAnUnreadableExpiry locks that an expiry the server cannot
// parse is refused. Lowercase "t" and "z" are legal RFC 3339 that Go's parser
// rejects, and the dropped error produced a key that never expires for a
// caller who asked for one that does.
func TestAPIKeyRejectsAnUnreadableExpiry(t *testing.T) {
	ts, admin := setupTestServer(t)
	withEncryptionKey(t)

	status, _ := doJSON(t, ts, admin, http.MethodPost, "/api/api_keys",
		map[string]any{"name": "lower", "expires_at": "2099-01-01t00:00:00z"})
	assert.Equal(t, http.StatusBadRequest, status, "an unreadable expiry is a validation error")
	assert.Error(t, db.DB.Where("name = ?", "lower").First(&models.APIKey{}).Error, "no key is created without its expiry")

	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/api_keys",
		map[string]any{"name": "upper", "expires_at": "2099-01-01T00:00:00Z"})
	require.Equal(t, http.StatusCreated, status, "a well-formed expiry is accepted")
	var key models.APIKey
	require.NoError(t, db.DB.Where("name = ?", "upper").First(&key).Error)
	require.NotNil(t, key.ExpiresAt, "the expiry is stored")
	assert.Equal(t, 2099, key.ExpiresAt.Year(), "the stored expiry is the one sent")
}

// TestReorderAuthorizesBeforeItWrites covers both reorder routes. A dashboard
// group the caller cannot edit is a 403 and an unknown one a 404; both used to
// surface as a 500, and the access check ran a second query inside the
// transaction, which never returns on a one-connection pool. Ungrouping a
// dashboard through reorder must store NULL, not a group ID of 0 that names
// nothing.
func TestReorderAuthorizesBeforeItWrites(t *testing.T) {
	ts, admin := setupTestServer(t)
	other := sessionWith(t, "other", perm.PermDashboardGroupsRead, perm.PermDashboardGroupsWrite)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/dashboard_groups", map[string]any{"name": "Home"})
	require.Equalf(t, http.StatusCreated, status, "the group must be created: %v", resp)
	groupID := uint(resp["data"].(map[string]any)["id"].(float64))

	reorder := func(token string, id uint) int {
		status, _ := doJSON(t, ts, token, http.MethodPut, "/api/dashboard_groups/reorder",
			map[string]any{"items": []map[string]any{{"id": id, "sort_order": 3}}})
		return status
	}
	assert.Equal(t, http.StatusForbidden, reorder(other, groupID), "a group the caller cannot edit is forbidden")
	assert.Equal(t, http.StatusNotFound, reorder(admin, groupID+1000), "an unknown group is not found")
	assert.Equal(t, http.StatusOK, reorder(admin, groupID), "the owner reorders the group")

	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/dashboards", map[string]any{"name": "Lights"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/dashboards/reorder",
		map[string]any{"items": []map[string]any{{"id": dashID, "group_id": 0, "sort_order": 1}}})
	require.Equalf(t, http.StatusOK, status, "the reorder must apply: %v", resp)
	var dash models.Dashboard
	require.NoError(t, db.DB.Where("id = ?", dashID).First(&dash).Error)
	assert.Nil(t, dash.GroupID, "an ungrouped dashboard has no group ID")
}

// TestDuplicateNamesAreConflicts locks that creating or renaming an account,
// group, role, grant, or assignment onto one that already exists answers 409.
// Each of these surfaced the duplicate as a 500 INTERNAL_ERROR, so the
// settings pages reported a server fault for a name that was merely taken. A
// group rename had no check at all and left two groups with one name, which
// sharing by group name then resolved to whichever row came first.
func TestDuplicateNamesAreConflicts(t *testing.T) {
	ts, admin := setupTestServer(t)

	create := func(path string, body map[string]any) (int, map[string]any) {
		return doJSON(t, ts, admin, http.MethodPost, path, body)
	}
	id := func(resp map[string]any) uint {
		return uint(resp["data"].(map[string]any)["id"].(float64))
	}

	status, resp := create("/api/roles", map[string]any{"name": "operators"})
	require.Equalf(t, http.StatusCreated, status, "the first role must be created: %v", resp)
	roleID := id(resp)
	status, resp = create("/api/roles", map[string]any{"name": "viewers"})
	require.Equalf(t, http.StatusCreated, status, "a second role with its own name must be created: %v", resp)
	viewersID := id(resp)
	status, resp = create("/api/groups", map[string]any{"name": "Ops"})
	require.Equalf(t, http.StatusCreated, status, "the first group must be created: %v", resp)
	groupID := id(resp)
	status, resp = create("/api/groups", map[string]any{"name": "Guests"})
	require.Equalf(t, http.StatusCreated, status, "a second group with its own name must be created: %v", resp)
	guestsID := id(resp)
	user := map[string]any{"username": "carol", "email": "carol@example.com", "password": "password"}
	status, resp = create("/api/users", user)
	require.Equalf(t, http.StatusCreated, status, "the first user must be created: %v", resp)
	carolID := resp["data"].(map[string]any)["id"].(string)
	grant := fmt.Sprintf("/api/roles/%d/permissions", roleID)
	status, resp = create(grant, map[string]any{"permission": perm.PermFlowsRead})
	require.Equalf(t, http.StatusCreated, status, "the first grant must be created: %v", resp)
	assignment := map[string]any{"role_id": roleID, "group_id": groupID}
	status, resp = create("/api/role_assignments", assignment)
	require.Equalf(t, http.StatusCreated, status, "the first assignment must be created: %v", resp)
	owner := fmt.Sprintf("/api/groups/%d/owners", groupID)
	status, resp = create(owner, map[string]any{"owner_group_id": guestsID})
	require.Equalf(t, http.StatusCreated, status, "the first owner must be added: %v", resp)
	theme := map[string]any{"name": "Night", "package_name": "night"}
	status, resp = create("/api/themes", theme)
	require.Equalf(t, http.StatusCreated, status, "the first theme must be created: %v", resp)

	cases := []struct {
		name   string
		method string
		path   string
		body   map[string]any
	}{
		{"a role name already in use", http.MethodPost, "/api/roles", map[string]any{"name": "operators"}},
		{"a role renamed onto another role", http.MethodPut, fmt.Sprintf("/api/roles/%d", viewersID), map[string]any{"name": "operators"}},
		{"a group name already in use", http.MethodPost, "/api/groups", map[string]any{"name": "Ops"}},
		{"a group renamed onto another group", http.MethodPut, fmt.Sprintf("/api/groups/%d", guestsID), map[string]any{"name": "Ops"}},
		{"a username already in use", http.MethodPost, "/api/users", map[string]any{"username": "carol", "email": "other@example.com", "password": "password"}},
		{"an email already in use", http.MethodPost, "/api/users", map[string]any{"username": "dave", "email": "carol@example.com", "password": "password"}},
		{"a user renamed onto another user", http.MethodPut, "/api/users/" + carolID, map[string]any{"username": "admin"}},
		{"a permission the role already holds", http.MethodPost, grant, map[string]any{"permission": perm.PermFlowsRead}},
		{"a role the group already holds", http.MethodPost, "/api/role_assignments", assignment},
		{"an owner the group already has", http.MethodPost, owner, map[string]any{"owner_group_id": guestsID}},
		{"a theme package name already in use", http.MethodPost, "/api/themes", theme},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doJSON(t, ts, admin, tc.method, tc.path, tc.body)
			assert.Equalf(t, http.StatusConflict, status, "%s is a conflict, not a server fault: %v", tc.name, resp)
		})
	}

	var named int64
	require.NoError(t, db.DB.Model(&models.UserGroup{}).Where("name = ?", "Ops").Count(&named).Error)
	assert.Equal(t, int64(1), named, "a refused rename must not leave two groups with one name")

	status, resp = doJSON(t, ts, admin, http.MethodPut, fmt.Sprintf("/api/groups/%d", groupID), map[string]any{"name": "Ops"})
	assert.Equalf(t, http.StatusOK, status, "saving a group under its own name is not a conflict: %v", resp)
	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/users/"+carolID, map[string]any{"username": "carol", "email": "carol@example.com"})
	assert.Equalf(t, http.StatusOK, status, "saving a user under their own name is not a conflict: %v", resp)
}

// TestAdministrativeDeletesThatMatchNothingAreNotFound extends the rule the
// API key, session, and user deletes follow to the remaining administrative
// deletes. Each reported success for a row that did not exist, and since the
// audit log records every 2xx it then wrote entries such as "Removed role
// from" with no role and no target, or "Deleted an agent enrollment token"
// for a token that was never minted.
func TestAdministrativeDeletesThatMatchNothingAreNotFound(t *testing.T) {
	ts, admin := setupTestServer(t)
	entries := recordEvents(t)

	role := &models.Role{Name: "operators"}
	require.NoError(t, role.Create())
	group := &models.UserGroup{Name: "Ops"}
	require.NoError(t, group.Create())
	other := &models.UserGroup{Name: "Guests"}
	require.NoError(t, other.Create())
	member := &models.User{ID: uuid.New(), Username: "carol", Email: "carol@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(member).Error)
	require.NoError(t, db.DB.Create(&models.UserGroupMember{GroupID: group.ID, UserID: member.ID}).Error)
	assignment := &models.RoleAssignment{RoleID: role.ID, GroupID: &group.ID}
	require.NoError(t, db.DB.Create(assignment).Error)
	owner := &models.GroupOwner{GroupID: group.ID, UserID: &member.ID}
	require.NoError(t, db.DB.Create(owner).Error)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/dashboards", map[string]any{"name": "Lights"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard must be created: %v", resp)
	dashID := resp["data"].(map[string]any)["id"].(string)
	share := &models.DashboardAccess{DashboardID: uuid.MustParse(dashID), UserID: &member.ID, Permission: models.DashboardAccessView}
	require.NoError(t, db.DB.Create(share).Error)
	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/dashboard_groups", map[string]any{"name": "Home"})
	require.Equalf(t, http.StatusCreated, status, "the dashboard group must be created: %v", resp)
	dashGroupID := uint(resp["data"].(map[string]any)["id"].(float64))
	groupShare := &models.DashboardGroupAccess{DashboardGroupID: dashGroupID, UserID: &member.ID, Permission: models.DashboardAccessView}
	require.NoError(t, db.DB.Create(groupShare).Error)
	theme := &models.Theme{Name: "Night", PackageName: "night"}
	require.NoError(t, db.DB.Create(theme).Error)
	_, token, err := models.NewEnrollmentToken("garage", "", nil, time.Hour)
	require.NoError(t, err)
	device := &models.MQTTDevice{ID: uuid.New(), Name: "Porch light", ConnectionID: uuid.New()}
	require.NoError(t, db.DB.Create(device).Error)

	cases := []struct {
		name    string
		missing string
		present string
	}{
		{"role assignment", "/api/role_assignments/99999", fmt.Sprintf("/api/role_assignments/%d", assignment.ID)},
		{"group member", fmt.Sprintf("/api/groups/%d/members/%s", other.ID, member.ID), fmt.Sprintf("/api/groups/%d/members/%s", group.ID, member.ID)},
		{"group owner", fmt.Sprintf("/api/groups/%d/owners/%d", other.ID, owner.ID), fmt.Sprintf("/api/groups/%d/owners/%d", group.ID, owner.ID)},
		{"dashboard share", fmt.Sprintf("/api/dashboards/%s/shares/99999", dashID), fmt.Sprintf("/api/dashboards/%s/shares/%d", dashID, share.ID)},
		{"dashboard group share", fmt.Sprintf("/api/dashboard_groups/%d/shares/99999", dashGroupID), fmt.Sprintf("/api/dashboard_groups/%d/shares/%d", dashGroupID, groupShare.ID)},
		{"group", "/api/groups/99999", fmt.Sprintf("/api/groups/%d", group.ID)},
		{"role", "/api/roles/99999", fmt.Sprintf("/api/roles/%d", role.ID)},
		{"theme", "/api/themes/99999", fmt.Sprintf("/api/themes/%d", theme.ID)},
		{"agent enrollment token", "/api/agents/tokens/" + uuid.NewString(), "/api/agents/tokens/" + token.ID.String()},
		{"MQTT device", "/api/mqtt_devices/" + uuid.NewString(), "/api/mqtt_devices/" + device.ID.String()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doJSON(t, ts, admin, http.MethodDelete, tc.missing, nil)
			assert.Equalf(t, http.StatusNotFound, status, "a %s that does not exist is not deleted: %v", tc.name, resp)
			status, resp = doJSON(t, ts, admin, http.MethodDelete, tc.present, nil)
			assert.Equalf(t, http.StatusOK, status, "an existing %s is deleted: %v", tc.name, resp)
		})
	}

	tokenDeletes := 0
	for _, e := range entries() {
		assert.NotEqualf(t, "Removed role  from ", e.Message, "a delete that matched nothing must not reach the security log: %+v", e)
		assert.NotEqual(t, "Deleted group 99999", e.Message, "a group that never existed was not deleted")
		assert.NotEqual(t, "Deleted role 99999", e.Message, "a role that never existed was not deleted")
		if e.Message == "Deleted an agent enrollment token" {
			tokenDeletes++
		}
	}
	assert.Equal(t, 1, tokenDeletes, "only the enrollment token that existed is logged as deleted")
}

// TestGrantsOnMissingGroupsAndRolesAreNotFound locks that a membership, an
// owner, a permission, or an assignment cannot be attached to a group or role
// that does not exist, including the memberships a new user is created with. Each insert answered 201 for any ID, and groups and
// roles are soft-deleted and revived under their old ID by name, so a row
// attached to a deleted group came back as a membership nobody gave the new
// one.
func TestGrantsOnMissingGroupsAndRolesAreNotFound(t *testing.T) {
	ts, admin := setupTestServer(t)
	carol := &models.User{ID: uuid.New(), Username: "carol", Email: "carol@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(carol).Error)
	role := &models.Role{Name: "operators"}
	require.NoError(t, role.Create())
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: perm.PermFlowsWrite}).Error)
	gone := &models.UserGroup{Name: "Guests"}
	require.NoError(t, gone.Create())
	require.NoError(t, gone.Delete())
	goneRole := &models.Role{Name: "retired"}
	require.NoError(t, goneRole.Create())
	require.NoError(t, goneRole.Delete())

	cases := []struct {
		name string
		path string
		body map[string]any
	}{
		{"a member of a deleted group", fmt.Sprintf("/api/groups/%d/members", gone.ID), map[string]any{"user_id": carol.ID}},
		{"an owner of a deleted group", fmt.Sprintf("/api/groups/%d/owners", gone.ID), map[string]any{"user_id": carol.ID}},
		{"a permission on a deleted role", fmt.Sprintf("/api/roles/%d/permissions", goneRole.ID), map[string]any{"permission": perm.PermFlowsWrite}},
		{"an assignment of a deleted role", "/api/role_assignments", map[string]any{"role_id": goneRole.ID, "user_id": carol.ID}},
		{"an assignment to a deleted group", "/api/role_assignments", map[string]any{"role_id": role.ID, "group_id": gone.ID}},
		{"a new user in a deleted group", "/api/users", map[string]any{"username": "dave", "email": "dave@example.com", "password": "password", "group_ids": []uint{gone.ID}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doJSON(t, ts, admin, http.MethodPost, tc.path, tc.body)
			assert.Equalf(t, http.StatusNotFound, status, "%s must be refused: %v", tc.name, resp)
		})
	}

	revived := &models.UserGroup{Name: "Guests"}
	require.NoError(t, revived.Create(), "the name is free again after the delete")
	require.Equal(t, gone.ID, revived.ID, "the revived group keeps the deleted one's ID, which is what makes a stray row matter")
	ids, err := models.GetUserGroupIDs(carol.ID)
	require.NoError(t, err)
	assert.NotContains(t, ids, revived.ID, "nobody added carol to the new group")
	assert.False(t, auth.HasPermission(carol.ID, perm.PermFlowsWrite), "and nobody assigned the new group a role")
	var members int64
	require.NoError(t, db.DB.Model(&models.UserGroupMember{}).Where("group_id = ?", revived.ID).Count(&members).Error)
	assert.Zero(t, members, "the refused user left no membership behind for the new group to inherit")

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/users", map[string]any{
		"username": "dave", "email": "dave@example.com", "password": "password", "group_ids": []uint{revived.ID}})
	require.Equalf(t, http.StatusCreated, status, "the refusal created no account, and a live group is accepted: %v", resp)
	ids, err = models.GetUserGroupIDs(uuid.MustParse(resp["data"].(map[string]any)["id"].(string)))
	require.NoError(t, err)
	assert.Equal(t, []uint{revived.ID}, ids, "the new user is in the group the request named")
}

// TestUpdatesRefuseEmptyNames locks the user, group, and role updates to the
// one-character minimum their creates enforce. An update stored an empty
// name, and a user with an empty username cannot sign in again because login
// requires one character. The fields stay optional, so an update that leaves
// the name out still applies.
func TestUpdatesRefuseEmptyNames(t *testing.T) {
	ts, admin := setupTestServer(t)
	erin := &models.User{ID: uuid.New(), Username: "erin", Email: "erin@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(erin).Error)
	group := &models.UserGroup{Name: "Ops"}
	require.NoError(t, group.Create())
	role := &models.Role{Name: "operators"}
	require.NoError(t, role.Create())

	cases := []struct {
		name    string
		path    string
		empty   map[string]any
		partial map[string]any
	}{
		{"username", "/api/users/" + erin.ID.String(), map[string]any{"username": ""}, map[string]any{"email": "erin@example.org"}},
		{"group name", fmt.Sprintf("/api/groups/%d", group.ID), map[string]any{"name": ""}, map[string]any{}},
		{"role name", fmt.Sprintf("/api/roles/%d", role.ID), map[string]any{"name": ""}, map[string]any{"description": "Runs flows"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, resp := doJSON(t, ts, admin, http.MethodPut, tc.path, tc.empty)
			assert.Equalf(t, http.StatusBadRequest, status, "an empty %s is refused: %v", tc.name, resp)
			status, resp = doJSON(t, ts, admin, http.MethodPut, tc.path, tc.partial)
			assert.Equalf(t, http.StatusOK, status, "an update that omits the %s still applies: %v", tc.name, resp)
		})
	}

	var user models.User
	require.NoError(t, db.DB.Where("id = ?", erin.ID).First(&user).Error)
	assert.Equal(t, "erin", user.Username, "the username survives both updates")
	assert.Equal(t, "erin@example.org", user.Email, "the update that omitted the username changed the email")
	var storedGroup models.UserGroup
	require.NoError(t, db.DB.First(&storedGroup, group.ID).Error)
	assert.Equal(t, "Ops", storedGroup.Name, "the group keeps its name")
	var storedRole models.Role
	require.NoError(t, db.DB.First(&storedRole, role.ID).Error)
	assert.Equal(t, "operators", storedRole.Name, "the role keeps its name")
}

// TestGrantsNameOneExistingPrincipal locks what a role assignment, a group
// membership, and a group owner accept. A user ID naming no account reached
// the foreign key and answered 500. A role assignment or group owner naming
// both a user and a group stored one row for the pair, so deleting the group
// took the user's role with it.
func TestGrantsNameOneExistingPrincipal(t *testing.T) {
	ts, admin := setupTestServer(t)
	sessionWith(t, "alice")
	alice := userIDOf(t, "alice")
	ghost := uuid.NewString()
	role := &models.Role{Name: "operators"}
	require.NoError(t, role.Create())
	ops := &models.UserGroup{Name: "Ops"}
	require.NoError(t, ops.Create())
	leads := &models.UserGroup{Name: "Leads"}
	require.NoError(t, leads.Create())
	members := fmt.Sprintf("/api/groups/%d/members", ops.ID)
	owners := fmt.Sprintf("/api/groups/%d/owners", ops.ID)

	tests := []struct {
		name, path string
		body       map[string]any
		want       int
	}{
		{"a role for a user that does not exist", "/api/role_assignments", map[string]any{"role_id": role.ID, "user_id": ghost}, http.StatusNotFound},
		{"a role for a user and a group at once", "/api/role_assignments", map[string]any{"role_id": role.ID, "user_id": alice, "group_id": ops.ID}, http.StatusBadRequest},
		{"a role for a user", "/api/role_assignments", map[string]any{"role_id": role.ID, "user_id": alice}, http.StatusCreated},
		{"a role for a group", "/api/role_assignments", map[string]any{"role_id": role.ID, "group_id": ops.ID}, http.StatusCreated},
		{"a member that does not exist", members, map[string]any{"user_id": ghost}, http.StatusNotFound},
		{"a member with no user ID", members, map[string]any{}, http.StatusNotFound},
		{"a member", members, map[string]any{"user_id": alice}, http.StatusCreated},
		{"an owner that does not exist", owners, map[string]any{"user_id": ghost}, http.StatusNotFound},
		{"an owner that is a user and a group at once", owners, map[string]any{"user_id": alice, "owner_group_id": leads.ID}, http.StatusBadRequest},
		{"an owner", owners, map[string]any{"user_id": alice}, http.StatusCreated},
		{"an owning group", owners, map[string]any{"owner_group_id": leads.ID}, http.StatusCreated},
	}
	for _, tc := range tests {
		status, resp := doJSON(t, ts, admin, http.MethodPost, tc.path, tc.body)
		assert.Equalf(t, tc.want, status, "%s: %v", tc.name, resp)
	}
}

// TestUpdatesKeepWhatCreatesRequire locks that an update cannot store what
// its create refuses. Each of these creates requires a name, and the user
// create requires an email address; the updates stored an empty name or a
// string that is no address. A flow with no name has no row label to open it
// by, and an empty email is a unique value only one account can hold.
func TestUpdatesKeepWhatCreatesRequire(t *testing.T) {
	ts, admin := setupTestServer(t)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/flows", map[string]any{"name": "Porch light"})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowPath := "/api/flows/" + data(t, resp)["id"].(string)
	theme := &models.Theme{Name: "Night", PackageName: "night", Version: "1.0.0", Variables: "{}"}
	require.NoError(t, db.DB.Create(theme).Error)
	themePath := fmt.Sprintf("/api/themes/%d", theme.ID)
	conn := &models.Connection{ID: uuid.New(), Name: "Broker", Kind: "mqtt"}
	require.NoError(t, db.DB.Create(conn).Error)
	connPath := "/api/connections/" + conn.ID.String()
	userPath := "/api/users/" + userIDOf(t, "admin")

	tests := []struct {
		name, path string
		body       map[string]any
		want       int
	}{
		{"a flow renamed to nothing", flowPath, map[string]any{"name": ""}, http.StatusBadRequest},
		{"a flow renamed", flowPath, map[string]any{"name": "Porch"}, http.StatusOK},
		{"a theme renamed to nothing", themePath, map[string]any{"name": ""}, http.StatusBadRequest},
		{"a theme renamed", themePath, map[string]any{"name": "Dusk"}, http.StatusOK},
		{"a connection renamed to nothing", connPath, map[string]any{"name": ""}, http.StatusBadRequest},
		{"an email that is no address", userPath, map[string]any{"email": "nowhere"}, http.StatusBadRequest},
		{"an empty email", userPath, map[string]any{"email": ""}, http.StatusBadRequest},
		{"an email address", userPath, map[string]any{"email": "admin@home.example"}, http.StatusOK},
	}
	for _, tc := range tests {
		status, resp := doJSON(t, ts, admin, http.MethodPut, tc.path, tc.body)
		assert.Equalf(t, tc.want, status, "%s: %v", tc.name, resp)
	}
	require.NoError(t, db.DB.First(conn, "id = ?", conn.ID).Error)
	assert.Equal(t, "Broker", conn.Name, "the connection keeps its name")
}
