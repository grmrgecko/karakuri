package core

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConnectionStatusRealLifecycle uses HA's real WebSocket handshake to verify
// initial status, filtered transitions, reconnect, disablement and disarming.
func TestConnectionStatusRealLifecycle(t *testing.T) {
	oldConfig := config.C
	t.Cleanup(func() { config.C = oldConfig })
	require.NoError(t, config.Load())
	config.C.Security.EncryptionKey = "6368616e676520746869732070617373776f726420746f206120736563726574"
	setupControls(t)
	oldKind, hadKind := integrations.KindFor(homeassistant.Kind)
	integrations.RegisterKind(homeassistant.KindDef())
	t.Cleanup(func() {
		if hadKind {
			integrations.RegisterKind(oldKind)
		} else {
			integrations.UnregisterKind(homeassistant.Kind)
		}
	})
	server := hatest.New("status-test-token")
	t.Cleanup(server.Close)
	conn := &models.Connection{Name: "Status test HA", Kind: homeassistant.Kind, Enabled: true}
	require.NoError(t, conn.SetConfig(homeassistant.Config{BaseURL: server.URL, Token: server.Token}))
	require.NoError(t, db.DB.Create(conn).Error)
	mgr := connmgr.New(nil, nil)
	oldManager := connops.Manager()
	connops.SetManager(mgr)
	t.Cleanup(func() { connops.SetManager(oldManager) })
	t.Cleanup(mgr.Stop)
	require.NoError(t, mgr.StartConnection(conn.ID))
	require.Eventually(t, func() bool {
		status, found := mgr.Status(conn.ID)
		return found && status.State == integrations.StateConnected
	}, 5*time.Second, 10*time.Millisecond, "the protocol peer must authenticate before the trigger arms")
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	emitted := make(chan *engine.Msg, 32)
	done := make(chan error, 1)
	cfg := `{"connection":"` + conn.ID.String() + `","emit_initial":true,"states":"connected,disabled"}`
	go func() {
		done <- (ConnectionStatusNode{}).Start(ctx, newTestCtx(cfg, nil), func(msg *engine.Msg) { emitted <- msg })
	}()
	read := func(state string) map[string]any {
		t.Helper()
		select {
		case msg := <-emitted:
			payload, ok := msg.Payload.(map[string]any)
			require.True(t, ok)
			assert.Equal(t, state, payload["state"])
			assert.Equal(t, conn.ID.String(), msg.Topic)
			assert.Equal(t, conn.Name, payload["connection_name"])
			_, err := time.Parse(time.RFC3339Nano, payload["since"].(string))
			assert.NoError(t, err, "status timestamps must be consumable by time nodes")
			return payload
		case <-time.After(8 * time.Second):
			require.FailNow(t, "connection status did not arrive", state)
			return nil
		}
	}
	initial := read(integrations.StateConnected)
	assert.Equal(t, true, initial["initial"])
	assert.Equal(t, "", initial["previous_state"])
	server.CloseClients()
	recovered := read(integrations.StateConnected)
	assert.Equal(t, false, recovered["initial"])
	assert.Contains(t, []string{integrations.StateError, integrations.StateConnecting}, recovered["previous_state"], "filtered failures still update the previous-state tracker")
	mgr.StopConnection(conn.ID)
	disabled := read(integrations.StateDisabled)
	assert.Equal(t, integrations.StateConnected, disabled["previous_state"])
	require.NoError(t, mgr.StartConnection(conn.ID))
	read(integrations.StateConnected)
	// The real manager replaces the driver while the watcher keeps its stable ID.
	// A config restart is not a disable; the filtered connecting report sits
	// between the two connected events.
	require.NoError(t, mgr.RestartConnection(conn.ID))
	restarted := read(integrations.StateConnected)
	assert.Equal(t, integrations.StateConnecting, restarted["previous_state"])
	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.FailNow(t, "disarming did not release the status watcher")
	}
	mgr.StopConnection(conn.ID)
	assert.Empty(t, emitted, "disarmed triggers must not emit later reports")
	// A failed factory has no running driver, but its retained error remains readable.
	conn.Kind = "unavailable-status-test-kind"
	require.NoError(t, models.SaveConnection(conn))
	require.Error(t, mgr.StartConnection(conn.ID))
	out, err := (ConnectionStatusNode{}).Execute(t.Context(), newTestCtx(cfg, nil), nil)
	require.NoError(t, err)
	payload := only(out, "out").Payload.(map[string]any)
	assert.Equal(t, integrations.StateError, payload["state"])
	assert.Contains(t, payload["error"], "no driver registered")
	encoded, err := json.Marshal(payload)
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), server.Token, "status events must never expose the connection configuration")
}
