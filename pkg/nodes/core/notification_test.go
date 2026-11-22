package core

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNotificationEngineBoundary pins expression rendering and retry suppression
// against a real webhook, including an authored retry override on rejection.
func TestNotificationEngineBoundary(t *testing.T) {
	require.NoError(t, config.Load())
	previousKey := config.C.Security.EncryptionKey
	config.C.Security.EncryptionKey = "6368616e676520746869732070617373776f726420746f206120736563726574"
	t.Cleanup(func() { config.C.Security.EncryptionKey = previousKey })
	setupControls(t)
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
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) == nil {
			received <- payload
		}
		if reject.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
		} else {
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(receiver.Close)
	connection := &models.Connection{Name: "Node webhook", Kind: notification.Kind, Enabled: true}
	require.NoError(t, connection.SetConfig(notification.Config{Service: "http", Credentials: map[string]string{}, Endpoints: []notification.Endpoint{{ID: "room", Label: "Room", Target: receiver.URL}}}))
	require.NoError(t, db.DB.Create(connection).Error)
	require.NoError(t, manager.StartConnection(connection.ID))
	registry := engine.NewRegistry()
	registry.Register(NotificationNode{})
	eng := engine.New(engine.Options{Registry: registry})
	t.Cleanup(eng.Stop)
	raw, err := json.Marshal(map[string]any{"connection": connection.ID.String(), "endpoint": "room", "subject": "Room {{ payload.room }}", "message": "Value {{ payload.value }}"})
	require.NoError(t, err)
	msg := &engine.Msg{Payload: map[string]any{"room": "kitchen", "value": 21.0}, Topic: "sensor", Meta: map[string]any{"request_id": "r"}}
	for _, tc := range []struct {
		name   string
		reject bool
		status string
	}{{"provider acceptance", false, engine.RunStatusSucceeded}, {"provider rejection", true, engine.RunStatusFailed}} {
		t.Run(tc.name, func(t *testing.T) {
			reject.Store(tc.reject)
			result, err := eng.TestNode(t.Context(), engine.RunOptions{Graph: &flow.Graph{Nodes: []flow.Node{{ID: "notify", Type: "notification", Config: raw, Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 1}}}}, StartNodeID: "notify", SeedInputs: map[string]*engine.Msg{"in": msg}})
			require.NoError(t, err)
			assert.Equal(t, tc.status, result.Status, result.Error)
			assert.Equal(t, map[string]string{"subject": "Room kitchen", "message": "Value 21"}, <-received)
			if !tc.reject {
				require.Len(t, result.Outputs["out"], 1)
				var output engine.Msg
				require.NoError(t, json.Unmarshal(result.Outputs["out"][0], &output))
				assert.Equal(t, msg.Payload, output.Payload)
				assert.Equal(t, msg.Topic, output.Topic)
				assert.Equal(t, msg.Meta, output.Meta)
			}
		})
	}
	assert.Equal(t, int32(2), calls.Load(), "One accepted and one refused delivery must produce two requests despite the retry override.")
}
