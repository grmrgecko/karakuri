package api_test

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestScriptNodeFunctionCatalogAPI exercises the real router and permission
// middleware around the editor's catalog. It pins the versioned response and
// proves that graph-only types are not advertised while an authenticated user
// without flow read permission receives no catalog.
func TestScriptNodeFunctionCatalogAPI(t *testing.T) {
	srv, token := setupTestServer(t)

	status, body := doJSON(t, srv, token, http.MethodGet, "/api/scripts/node_functions", nil)
	require.Equalf(t, http.StatusOK, status, "a flow reader must receive the function catalog: %v", body)
	data, ok := body["data"].(map[string]any)
	require.Truef(t, ok, "the catalog uses the standard data envelope: %v", body)
	assert.Equal(t, float64(1), data["schema_version"], "the response format is explicitly versioned")
	revision, ok := data["revision"].(string)
	require.True(t, ok, "the editor cache needs a revision string")
	assert.Len(t, revision, 64, "a SHA-256 revision is 64 lowercase hexadecimal characters")

	entries, ok := data["functions"].([]any)
	require.Truef(t, ok, "functions must be an array: %v", data["functions"])
	byName := make(map[string]map[string]any, len(entries))
	for _, entry := range entries {
		function, ok := entry.(map[string]any)
		require.Truef(t, ok, "a function entry is an object: %v", entry)
		name, _ := function["name"].(string)
		byName[name] = function
	}
	encode, ok := byName["base64-encode"]
	require.True(t, ok, "an ordinary executable node must be exposed by its exact type")
	assert.Equal(t, "base64-encode", encode["node_type"], "the advertised function cannot fall back to another implementation")
	assert.Equal(t, []any{"base64Encode"}, encode["aliases"], "the unambiguous lower-camel alias reaches the editor")
	assert.NotContains(t, byName, "script", "nested scripts have independent call budgets and are not ordinary functions")
	assert.NotContains(t, byName, "join", "fan-in nodes need graph message scheduling")
	assert.NotContains(t, byName, "manual", "triggers are not synchronous functions")

	status, second := doJSON(t, srv, token, http.MethodGet, "/api/scripts/node_functions", nil)
	require.Equal(t, http.StatusOK, status)
	secondData, _ := second["data"].(map[string]any)
	assert.Equal(t, revision, secondData["revision"], "an unchanged registry must return a stable revision")

	reader := &models.User{ID: uuid.New(), Username: "catalog-reader", Email: "catalog-reader@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(reader).Error, "the permission fixture needs a real user")
	role := &models.Role{Name: "Function catalog reader"}
	require.NoError(t, role.Create(), "the permission fixture needs a real role")
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: permissions.PermFlowsRead}).Error,
		"the role must grant flow read and no management permissions")
	require.NoError(t, db.DB.Create(&models.RoleAssignment{RoleID: role.ID, UserID: &reader.ID}).Error,
		"the reader needs the flow-read role assignment")
	tokens, err := auth.CreateSession(reader, "127.0.0.1", "test")
	require.NoError(t, err, "the permission fixture needs a real session")
	status, _ = doJSON(t, srv, tokens.AccessToken, http.MethodGet, "/api/scripts/node_functions", nil)
	assert.Equal(t, http.StatusOK, status, "flow read alone is sufficient; plugin management permission is not required")

	outsider := &models.User{ID: uuid.New(), Username: "catalog-outsider", Email: "catalog-outsider@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(outsider).Error, "the denied-permission fixture needs a real user")
	tokens, err = auth.CreateSession(outsider, "127.0.0.1", "test")
	require.NoError(t, err, "the denied-permission fixture needs a real session")
	status, _ = doJSON(t, srv, tokens.AccessToken, http.MethodGet, "/api/scripts/node_functions", nil)
	assert.Equal(t, http.StatusForbidden, status, "authentication without flow read permission is not enough")

	resp, err := srv.Client().Get(srv.URL + "/api/scripts/node_functions")
	require.NoError(t, err, "the anonymous request must reach the server")
	defer func() { _ = resp.Body.Close() }()
	assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "the function catalog is not public")
}
