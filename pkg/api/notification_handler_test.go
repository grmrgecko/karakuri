package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/auth"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNotificationSettingsAndDelivery pins encrypted nested-secret round trips
// and HTTP webhook requests through the real permission and connection lifecycle.
func TestNotificationSettingsAndDelivery(t *testing.T) {
	srv, token := setupConnectionTest(t)
	previousKind, hadKind := integrations.KindFor(notification.Kind)
	integrations.RegisterKind(notification.KindDef())
	previousManager := connops.Manager()
	manager := connmgr.New(nil, nil)
	connops.SetManager(manager)
	t.Cleanup(func() {
		manager.Stop()
		connops.SetManager(previousManager)
		if hadKind {
			integrations.RegisterKind(previousKind)
		} else {
			integrations.UnregisterKind(notification.Kind)
		}
	})
	var calls atomic.Int32
	var reject atomic.Bool
	received := make(chan map[string]string, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, "Bearer private-token", r.Header.Get("Authorization"))
		assert.Equal(t, http.MethodPut, r.Method)
		assert.Equal(t, "/second", r.URL.Path)
		var payload map[string]string
		if assert.NoError(t, json.NewDecoder(r.Body).Decode(&payload)) {
			received <- payload
		}
		if reject.Load() {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("private-token " + receiverSecret))
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(receiver.Close)
	cfg := map[string]any{"service": "http", "credentials": map[string]string{"method": "PUT", "headers_json": `{"Authorization":"Bearer private-token"}`}, "endpoints": []notification.Endpoint{{ID: "first", Label: "First", Target: receiver.URL + "/first"}, {ID: "second", Label: "Second", Target: receiver.URL + "/second"}}}
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{"name": "Notifications", "kind": notification.Kind, "enabled": true, "config": cfg})
	require.Equal(t, http.StatusCreated, status, "%v", resp)
	item := resp["data"].(map[string]any)
	id := item["id"].(string)
	configView := item["config"].(map[string]any)
	assert.Equal(t, notification.Mask, configView["credentials"].(map[string]any)["headers_json"])
	endpoints := configView["endpoints"].([]any)
	for _, endpoint := range endpoints {
		assert.Equal(t, notification.Mask, endpoint.(map[string]any)["target"])
	}
	row, err := models.FindConnectionByID(uuid.MustParse(id))
	require.NoError(t, err)
	stored, err := json.Marshal(row)
	require.NoError(t, err)
	assert.NotContains(t, string(stored), "private-token")
	assert.NotContains(t, row.Config, "private-token")
	assert.NotContains(t, row.Config, receiver.URL)
	assert.NotContains(t, string(stored), receiver.URL)
	// Reorder masked rows and reread the encrypted source of truth.
	configView["endpoints"] = []any{endpoints[1], endpoints[0]}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/connections/"+id, map[string]any{"name": "Notifications edited", "enabled": true, "config": configView})
	require.Equal(t, http.StatusOK, status, "%v", resp)
	row, err = models.FindConnectionByID(uuid.MustParse(id))
	require.NoError(t, err)
	raw, err := row.ConfigJSON()
	require.NoError(t, err)
	var persisted notification.Config
	require.NoError(t, json.Unmarshal(raw, &persisted))
	assert.Equal(t, "second", persisted.Endpoints[0].ID)
	assert.Equal(t, receiver.URL+"/second", persisted.Endpoints[0].Target)
	assert.Equal(t, receiver.URL+"/first", persisted.Endpoints[1].Target)
	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/connections/"+id+"/notifications/endpoints", nil)
	require.Equal(t, http.StatusOK, status)
	public, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(public), receiver.URL)
	assert.NotContains(t, string(public), "target")
	sendPath := "/api/connections/" + id + "/notifications/send"
	payload := map[string]any{"endpoint": "second", "subject": "Room", "message": "Temperature 21"}
	status, resp = doJSON(t, srv, token, http.MethodPost, sendPath, payload)
	require.Equal(t, http.StatusOK, status, "%v", resp)
	assert.Equal(t, map[string]string{"subject": "Room", "message": "Temperature 21"}, <-received)
	for _, endpoint := range []string{"missing", receiver.URL + "/second"} {
		payload["endpoint"] = endpoint
		status, _ = doJSON(t, srv, token, http.MethodPost, sendPath, payload)
		assert.Equal(t, http.StatusBadGateway, status)
	}
	assert.Equal(t, int32(1), calls.Load(), "Unapproved IDs must not contact the provider.")
	// A connections reader can discover endpoints but cannot send a message.
	user := &models.User{ID: uuid.New(), Username: "reader", Email: "reader@example.com", PasswordHash: "unused"}
	require.NoError(t, db.DB.Create(user).Error)
	role := &models.Role{Name: "Notification reader"}
	require.NoError(t, role.Create())
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: permissions.PermConnectionsRead}).Error)
	require.NoError(t, db.DB.Create(&models.RoleAssignment{RoleID: role.ID, UserID: &user.ID}).Error)
	tokens, err := auth.CreateSession(user, "127.0.0.1", "test")
	require.NoError(t, err)
	status, _ = doJSON(t, srv, tokens.AccessToken, http.MethodGet, "/api/notification_services", nil)
	assert.Equal(t, http.StatusOK, status)
	payload["endpoint"] = "second"
	status, _ = doJSON(t, srv, tokens.AccessToken, http.MethodPost, sendPath, payload)
	assert.Equal(t, http.StatusForbidden, status)
	assert.Equal(t, int32(1), calls.Load())
	reject.Store(true)
	status, resp = doJSON(t, srv, token, http.MethodPost, sendPath, payload)
	assert.Equal(t, http.StatusBadGateway, status)
	errorBody, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.NotContains(t, string(errorBody), "private-token")
	assert.NotContains(t, string(errorBody), receiverSecret)
	assert.NotContains(t, string(errorBody), receiver.URL)
	assert.Equal(t, int32(2), calls.Load(), "Provider rejection must not cause an automatic retry.")
	// Removal revokes an old ID even when a caller still has its saved graph.
	configView["endpoints"] = []any{endpoints[0]}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/connections/"+id, map[string]any{"name": "Notifications edited", "enabled": true, "config": configView})
	require.Equal(t, http.StatusOK, status, "%v", resp)
	status, _ = doJSON(t, srv, token, http.MethodPost, sendPath, payload)
	assert.Equal(t, http.StatusBadGateway, status)
	assert.Equal(t, int32(2), calls.Load())
}

const receiverSecret = "sensitive-provider-response"
