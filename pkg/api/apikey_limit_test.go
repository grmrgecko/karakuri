package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createKey creates an API key and returns its raw value, failing the test
// unless the server answers with wantStatus.
func createKey(t *testing.T, ts *httptest.Server, token string, body map[string]any, wantStatus int) string {
	t.Helper()
	status, resp := doJSON(t, ts, token, http.MethodPost, "/api/api_keys", body)
	require.Equalf(t, wantStatus, status, "creating key %v: %v", body, resp)
	if status != http.StatusCreated {
		return ""
	}
	return resp["data"].(map[string]any)["key"].(string)
}

// getStatus requests a path with an optional query token and returns the
// status, for the render URLs, which answer with files rather than JSON.
func getStatus(t *testing.T, ts *httptest.Server, path string) int {
	t.Helper()
	resp, err := http.Get(ts.URL + path)
	require.NoError(t, err)
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode
}

// TestLimitedKeyHoldsOnlyItsPermissions covers 001-FR-017 and FR-019: an
// administrator's key limited to flow reads lists flows but cannot write one,
// and cannot reach another user's private flow through the flow manage
// permission the administrator's own session uses.
func TestLimitedKeyHoldsOnlyItsPermissions(t *testing.T) {
	ts, admin := setupTestServer(t)
	withEncryptionKey(t)
	carol := sessionWith(t, "carol", perm.PermFlowsRead, perm.PermFlowsWrite)

	status, resp := doJSON(t, ts, carol, http.MethodPost, "/api/flows", map[string]any{"name": "Carol's private flow"})
	require.Equalf(t, http.StatusCreated, status, "carol creates a flow: %v", resp)
	carolFlow := resp["data"].(map[string]any)["id"].(string)

	key := createKey(t, ts, admin, map[string]any{"name": "read only", "permissions": []string{perm.PermFlowsRead}}, http.StatusCreated)

	status, _ = doJSON(t, ts, key, http.MethodGet, "/api/flows", nil)
	assert.Equal(t, http.StatusOK, status, "the key holds flow reads")

	status, _ = doJSON(t, ts, key, http.MethodPost, "/api/flows", map[string]any{"name": "Written by a read key"})
	assert.Equal(t, http.StatusForbidden, status, "the key does not hold flow writes, though its user does")

	status, _ = doJSON(t, ts, key, http.MethodGet, "/api/connections", nil)
	assert.Equal(t, http.StatusForbidden, status, "the key does not hold connection reads")

	status, _ = doJSON(t, ts, admin, http.MethodGet, "/api/flows/"+carolFlow, nil)
	assert.Equal(t, http.StatusOK, status, "the administrator's session reaches carol's flow through flow manage")
	status, _ = doJSON(t, ts, key, http.MethodGet, "/api/flows/"+carolFlow, nil)
	assert.Equal(t, http.StatusNotFound, status, "the key's limit leaves out flow manage, so carol's flow is not there for it")
}

// TestKeyLimitCannotExceedItsCreator covers 001-FR-018: a key may not list a
// permission its creator's roles do not hold, and a key may not create a key
// broader than itself.
func TestKeyLimitCannotExceedItsCreator(t *testing.T) {
	ts, _ := setupTestServer(t)
	withEncryptionKey(t)
	dave := sessionWith(t, "dave", perm.PermFlowsRead, perm.PermAPIKeysRead, perm.PermAPIKeysWrite)

	createKey(t, ts, dave, map[string]any{"name": "too broad", "permissions": []string{perm.PermFlowsWrite}}, http.StatusForbidden)
	createKey(t, ts, dave, map[string]any{"name": "wildcard", "permissions": []string{"api:flows:*"}}, http.StatusForbidden)
	var count int64
	require.NoError(t, db.DB.Model(&models.APIKey{}).Where("name IN ?", []string{"too broad", "wildcard"}).Count(&count).Error)
	assert.Zero(t, count, "a refused key is not stored")

	createKey(t, ts, dave, map[string]any{"name": "bad shape", "permissions": []string{"api::read"}}, http.StatusBadRequest)

	minter := createKey(t, ts, dave, map[string]any{"name": "minter",
		"permissions": []string{perm.PermFlowsRead, perm.PermAPIKeysWrite}}, http.StatusCreated)
	createKey(t, ts, minter, map[string]any{"name": "unlimited child"}, http.StatusForbidden)
	createKey(t, ts, minter, map[string]any{"name": "broader child", "permissions": []string{perm.PermAPIKeysRead}}, http.StatusForbidden)
	createKey(t, ts, minter, map[string]any{"name": "narrow child", "permissions": []string{perm.PermFlowsRead}}, http.StatusCreated)
}

// TestKeyLimitFollowsRoleChanges covers the per-request half of 001-FR-017: a
// key limited to every flow permission loses flow writes when its user's role
// does.
func TestKeyLimitFollowsRoleChanges(t *testing.T) {
	ts, _ := setupTestServer(t)
	withEncryptionKey(t)
	erin := sessionWith(t, "erin", perm.PermFlowsRead, perm.PermFlowsWrite, perm.PermAPIKeysWrite)
	key := createKey(t, ts, erin, map[string]any{"name": "flows", "permissions": []string{perm.PermFlowsRead, perm.PermFlowsWrite}}, http.StatusCreated)

	status, _ := doJSON(t, ts, key, http.MethodPost, "/api/flows", map[string]any{"name": "Before"})
	assert.Equal(t, http.StatusCreated, status, "the key writes flows while erin's role grants it")

	require.NoError(t, db.DB.Where("permission = ?", perm.PermFlowsWrite).
		Where("role_id IN (?)", db.DB.Model(&models.Role{}).Select("id").Where("name = ?", "erin role")).
		Delete(&models.RolePermission{}).Error)

	status, _ = doJSON(t, ts, key, http.MethodPost, "/api/flows", map[string]any{"name": "After"})
	assert.Equal(t, http.StatusForbidden, status, "the key never holds more than erin's roles do")
}

// TestRenderKeyOpensOnlyItsDashboardRender covers 007-FR-019 and SC-005: a
// render key opens its private dashboard's render URL and nothing else, its
// value is shown again only to its owner's session, and deleting the
// dashboard deletes the key.
func TestRenderKeyOpensOnlyItsDashboardRender(t *testing.T) {
	ts, admin := setupTestServer(t)
	withEncryptionKey(t)

	makeDashboard := func(name string) string {
		status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/dashboards", map[string]any{"name": name})
		require.Equalf(t, http.StatusCreated, status, "creating %s: %v", name, resp)
		return resp["data"].(map[string]any)["id"].(string)
	}
	hall := makeDashboard("Hall panel")
	other := makeDashboard("Other panel")

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/api_keys", map[string]any{"name": "Hall frame", "dashboard_id": hall})
	require.Equalf(t, http.StatusCreated, status, "creating the render key: %v", resp)
	data := resp["data"].(map[string]any)
	raw := data["key"].(string)
	keyID := fmt.Sprint(data["id"])
	assert.Equal(t, true, data["limited"], "a render key is limited")

	// Rendering is not running in the test server, so a URL the key opens
	// answers 503 and one it does not answers 404.
	assert.Equal(t, http.StatusNotFound, getStatus(t, ts, "/dashboard/"+hall+"/render/100x100.png"),
		"a private dashboard's render URL needs a credential")
	assert.Equal(t, http.StatusServiceUnavailable, getStatus(t, ts, "/dashboard/"+hall+"/render/100x100.png?token="+raw),
		"the render key opens its dashboard's render URL")
	assert.Equal(t, http.StatusNotFound, getStatus(t, ts, "/dashboard/"+other+"/render/100x100.png?token="+raw),
		"the render key opens no other dashboard's render")

	status, _ = doJSON(t, ts, raw, http.MethodGet, "/api/dashboards/"+hall, nil)
	assert.Equal(t, http.StatusNotFound, status, "an API route treats a render key as no credential, so the private dashboard is not found")
	status, _ = doJSON(t, ts, raw, http.MethodGet, "/api/api_keys", nil)
	assert.Equal(t, http.StatusUnauthorized, status, "an authenticated route refuses a render key")

	status, resp = doJSON(t, ts, admin, http.MethodGet, "/api/api_keys/"+keyID+"/value", nil)
	require.Equalf(t, http.StatusOK, status, "the owner's session sees the render key again: %v", resp)
	assert.Equal(t, raw, resp["data"].(map[string]any)["key"])

	plain := createKey(t, ts, admin, map[string]any{"name": "plain"}, http.StatusCreated)
	status, _ = doJSON(t, ts, plain, http.MethodGet, "/api/api_keys/"+keyID+"/value", nil)
	assert.Equal(t, http.StatusForbidden, status, "another key cannot read a key's value")

	status, resp = doJSON(t, ts, admin, http.MethodGet, "/api/api_keys?dashboard_id="+hall, nil)
	require.Equal(t, http.StatusOK, status)
	listed := resp["data"].([]any)
	require.Len(t, listed, 1, "the dashboard filter lists only that dashboard's render keys")
	assert.Equal(t, "Hall panel", listed[0].(map[string]any)["dashboard_name"])

	status, _ = doJSON(t, ts, admin, http.MethodDelete, "/api/dashboards/"+hall, nil)
	require.Equal(t, http.StatusOK, status)
	var count int64
	require.NoError(t, db.DB.Model(&models.APIKey{}).Where("name = ?", "Hall frame").Count(&count).Error)
	assert.Zero(t, count, "deleting the dashboard deletes its render key")
}
