package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSecurityEntries covers US2 and SC-006: sign-ins, a failed sign-in,
// administrative changes, and connection credentials are recorded with their
// actor, and no entry anywhere holds a password, token, or key.
func TestSecurityEntries(t *testing.T) {
	ts, admin := setupTestServer(t)
	read := recordEvents(t)
	previous := config.C.Security.EncryptionKey
	config.C.Security.EncryptionKey = testEncryptionKey
	t.Cleanup(func() { config.C.Security.EncryptionKey = previous })

	const wrongPassword = "wrong-horse-battery-staple"
	const newPassword = "brand-new-secret-password"
	const brokerPassword = "broker-secret-value"

	status, _ := doJSON(t, ts, "", http.MethodPost, "/api/auth/login", map[string]any{"username": "admin", "password": wrongPassword})
	assert.Equal(t, http.StatusUnauthorized, status)
	status, _ = doJSON(t, ts, "", http.MethodPost, "/api/auth/login", map[string]any{"username": "admin", "password": "password"})
	require.Equal(t, http.StatusOK, status)

	status, resp := doJSON(t, ts, admin, http.MethodPost, "/api/api_keys", map[string]any{"name": "Event test key"})
	require.Equalf(t, http.StatusCreated, status, "create key: %v", resp)
	data := resp["data"].(map[string]any)
	rawKey := data["key"].(string)
	keyID := fmt.Sprint(data["id"])
	status, _ = doJSON(t, ts, admin, http.MethodDelete, "/api/api_keys/"+keyID, nil)
	require.Equal(t, http.StatusOK, status)

	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/users", map[string]any{"username": "eventtest", "email": "eventtest@example.com", "password": newPassword})
	require.Equalf(t, http.StatusCreated, status, "create user: %v", resp)
	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/roles", map[string]any{"name": "Event test role"})
	require.Equalf(t, http.StatusCreated, status, "create role: %v", resp)
	roleID := fmt.Sprint(resp["data"].(map[string]any)["id"])
	status, _ = doJSON(t, ts, admin, http.MethodPost, "/api/roles/"+roleID+"/permissions", map[string]any{"permission": "api:events:read"})
	require.Equal(t, http.StatusCreated, status)

	status, resp = doJSON(t, ts, admin, http.MethodPost, "/api/connections", map[string]any{
		"name": "Event test broker", "kind": "mqtt", "enabled": false,
		"config": map[string]any{"broker_url": "mqtt://127.0.0.1:1", "username": "u", "password": brokerPassword}})
	require.Equalf(t, http.StatusCreated, status, "create connection: %v", resp)
	connID := resp["data"].(map[string]any)["id"].(string)
	status, resp = doJSON(t, ts, admin, http.MethodPut, "/api/connections/"+connID, map[string]any{
		"config": map[string]any{"broker_url": "mqtt://127.0.0.1:2", "username": "u", "password": brokerPassword + "-2"}})
	require.Equalf(t, http.StatusOK, status, "update connection: %v", resp)

	rows := read()
	var messages []string
	for _, e := range rows {
		if e.Category == models.EventSecurity {
			messages = append(messages, e.Message)
		}
		raw, _ := json.Marshal(e)
		for _, secret := range []string{wrongPassword, newPassword, brokerPassword, rawKey, "\"password\""} {
			assert.NotContains(t, string(raw), secret, "an entry holds a secret: %s", raw)
		}
	}
	for _, want := range []string{
		"Failed sign-in for admin",
		"admin signed in",
		"Created API key Event test key",
		"Deleted API key Event test key",
		"Created user eventtest",
		"Created role Event test role",
		"Granted api:events:read to role Event test role",
		"Created connection Event test broker (mqtt)",
		"Changed connection Event test broker: broker_url, credentials",
	} {
		assert.Contains(t, messages, want)
	}

	for _, e := range rows {
		if e.Message == "Created user eventtest" {
			assert.Equal(t, events.ActorUser, e.ActorKind)
			assert.Equal(t, "admin", e.ActorName)
		}
		if e.Message == "Failed sign-in for admin" {
			assert.Equal(t, models.EventWarning, e.Level)
			assert.Equal(t, events.ActorAnonymous, e.ActorKind)
		}
	}
	assert.False(t, strings.Contains(strings.Join(messages, "\n"), "password"), "no message mentions a password field")
}
