package api_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	mochi "github.com/mochi-mqtt/server/v2"
	"github.com/mochi-mqtt/server/v2/hooks/auth"
	"github.com/mochi-mqtt/server/v2/listeners"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testEncryptionKey is a fixed 32-byte hex key for connection config crypto.
const testEncryptionKey = "6368616e676520746869732070617373776f726420746f206120736563726574"

// setupConnectionTest boots the shared test server with connection support:
// an encryption key, the mqtt driver, and a running connection manager.
func setupConnectionTest(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, token := setupTestServer(t)
	config.C.Security.EncryptionKey = testEncryptionKey
	integrations.RegisterKind(mqtt.KindDef())
	return srv, token
}

// startTestBroker runs an embedded MQTT broker on a random port.
func startTestBroker(t *testing.T) string {
	t.Helper()
	server := mochi.New(&mochi.Options{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	require.NoError(t, server.AddHook(new(auth.AllowHook), nil), "the test broker accepts any client")
	tcp := listeners.NewTCP(listeners.Config{ID: "api-test", Address: "127.0.0.1:0"})
	require.NoError(t, server.AddListener(tcp), "the test broker needs a loopback listener")
	go func() { _ = server.Serve() }()
	t.Cleanup(func() { _ = server.Close() })
	return tcp.Address()
}

// TestConnectionCRUDAndMasking covers how a connection's credentials are
// handled from the moment they arrive to the moment the row is deleted. Three
// separate things have to hold at once and none is visible from the others: the
// password must be encrypted at rest, it must never come back out of the API,
// and it must survive an edit that only saw the mask.
//
// That last one is the reason for the sentinel: the form shows dots, so a save
// that took them literally would overwrite a working password with the string
// "••••••" and the connection would fail on the next reconnect, long after the
// author had moved on. The stored row is decrypted directly here because the
// API deliberately will not show it.
func TestConnectionCRUDAndMasking(t *testing.T) {
	srv, token := setupConnectionTest(t)

	// Create an MQTT connection with a password.
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Broker",
		"kind": "mqtt",
		"config": map[string]any{
			"broker_url": "mqtt://127.0.0.1:1883",
			"username":   "karakuri",
			"password":   "super-secret",
		},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)

	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "the created connection comes back under data")
	connID, _ := data["id"].(string)
	require.NotEmpty(t, connID, "the id addresses every request below")

	cfg, ok := data["config"].(map[string]any)
	require.True(t, ok, "the connection carries its config")
	assert.Equal(t, "••••••", cfg["password"],
		"the secret must come back as the sentinel: the form redisplays this value, so the real one would be in the page")
	assert.Equal(t, "mqtt://127.0.0.1:1883", cfg["broker_url"],
		"only secrets are masked; the rest of the config must be readable or the form cannot show it")

	cid, err := uuid.Parse(connID)
	require.NoError(t, err, "the returned id must be a UUID")
	stored, err := models.FindConnectionByID(cid)
	require.NoError(t, err, "the connection must be stored")
	require.NotEmpty(t, stored.Config, "the stored config must not be blank")
	assert.NotEqual(t, byte('{'), stored.Config[0],
		"the config is stored encrypted, so anyone with database access does not also have every broker password")

	// List must include the connection with masked config and a status.
	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/connections", nil)
	require.Equalf(t, http.StatusOK, status, "the connection list must be served: %v", resp)

	items, ok := resp["data"].([]any)
	require.True(t, ok, "the list is an array")
	require.Len(t, items, 1, "the one connection created above must be listed")

	item, ok := items[0].(map[string]any)
	require.True(t, ok, "a list entry is an object")
	assert.Equal(t, "••••••", item["config"].(map[string]any)["password"],
		"the list masks secrets too: a single unmasked endpoint is enough to leak them")
	assert.Contains(t, item, "status",
		"the connections page draws its health indicator from this, so a listing without it renders nothing")

	// Updating with the sentinel must preserve the stored password.
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/connections/"+connID, map[string]any{
		"config": map[string]any{
			"broker_url": "mqtt://127.0.0.1:2883",
			"username":   "karakuri",
			"password":   "••••••",
		},
	})
	require.Equalf(t, http.StatusOK, status, "an edit that redisplayed the mask must be accepted: %v", resp)

	stored, err = models.FindConnectionByID(cid)
	require.NoError(t, err, "the connection must still be stored")
	var mc mqtt.Config
	require.NoError(t, stored.GetConfig(&mc), "the stored config must decrypt")

	assert.Equal(t, "super-secret", mc.Password,
		"the sentinel means unchanged: saving the form without retyping the password must not overwrite it with dots")
	assert.Equal(t, "mqtt://127.0.0.1:2883", mc.BrokerURL,
		"the rest of the edit must still apply, or preserving the secret would mean ignoring the form")

	// Updating with a new password must replace the stored one.
	status, _ = doJSON(t, srv, token, http.MethodPut, "/api/connections/"+connID, map[string]any{
		"config": map[string]any{
			"broker_url": "mqtt://127.0.0.1:2883",
			"password":   "new-secret",
		},
	})
	require.Equal(t, http.StatusOK, status, "changing the password must be accepted")

	stored, err = models.FindConnectionByID(cid)
	require.NoError(t, err, "the connection must still be stored")
	require.NoError(t, stored.GetConfig(&mc), "the stored config must decrypt")
	assert.Equal(t, "new-secret", mc.Password,
		"a real value must replace the stored one, or the password could never be changed at all")

	// Deletion is blocked while a flow depends on the connection.
	dep := models.FlowDependency{FlowID: uuid.New(), Kind: "connection", RefID: connID}
	require.NoError(t, db.DB.Create(&dep).Error, "the test needs a flow depending on the connection")

	status, resp = doJSON(t, srv, token, http.MethodDelete, "/api/connections/"+connID, nil)
	require.Equalf(t, http.StatusConflict, status,
		"deleting a connection a flow uses would break that flow silently at its next run: %v", resp)

	require.NoError(t, db.DB.Delete(&models.FlowDependency{}, "id = ?", dep.ID).Error,
		"the dependency must be removable for the delete to be allowed")

	status, _ = doJSON(t, srv, token, http.MethodDelete, "/api/connections/"+connID, nil)
	require.Equal(t, http.StatusOK, status, "with nothing depending on it the connection must be deletable")

	status, _ = doJSON(t, srv, token, http.MethodGet, "/api/connections/"+connID, nil)
	assert.Equal(t, http.StatusNotFound, status, "a deleted connection must be gone, not merely hidden from the list")
}

// TestConnectionValidation covers what the connection form refuses. The kind
// catalog is data a plugin registers into, so an unknown kind has no schema and
// no factory — accepting one would store a connection nothing can ever build.
// The missing broker URL comes from the kind's own validate hook, which is what
// makes per-kind rules possible without a switch anywhere in the handler.
func TestConnectionValidation(t *testing.T) {
	srv, token := setupConnectionTest(t)

	// Unknown kinds are rejected.
	status, _ := doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{
		"name": "Bad", "kind": "carrier-pigeon", "config": map[string]any{},
	})
	assert.Equal(t, http.StatusBadRequest, status,
		"a kind no plugin registered has no factory, so accepting it would store a connection nothing can build")

	status, _ = doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{
		"name": "No URL", "kind": "mqtt", "config": map[string]any{},
	})
	assert.Equal(t, http.StatusBadRequest, status,
		"the kind's own validate hook rejects this, which is what makes per-kind rules possible with no switch in the handler")

	body := map[string]any{"name": "Dup", "kind": "mqtt",
		"config": map[string]any{"broker_url": "mqtt://localhost:1883"}}
	status, _ = doJSON(t, srv, token, http.MethodPost, "/api/connections", body)
	require.Equal(t, http.StatusCreated, status, "the first connection of that name must be created")

	status, _ = doJSON(t, srv, token, http.MethodPost, "/api/connections", body)
	assert.Equal(t, http.StatusConflict, status,
		"a flow node names its connection in the picker, so two with one name would be indistinguishable there")
}

// TestConnectionTestEndpoint covers the Test button on the connection form
// against a live embedded broker and a dead port. The failure is reported as a
// successful request carrying a failed result rather than as an HTTP error,
// because the request itself worked — the form needs to render the broker's
// message beside the field, and a 500 would surface as a generic toast with
// nothing useful in it.
func TestConnectionTestEndpoint(t *testing.T) {
	srv, token := setupConnectionTest(t)
	addr := startTestBroker(t)

	// A connection against the live embedded broker tests ok.
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{
		"name": "Live", "kind": "mqtt",
		"config": map[string]any{"broker_url": "mqtt://" + addr},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	liveID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, liveID, "the id addresses the test request below")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/connections/"+liveID+"/test", nil)
	require.Equalf(t, http.StatusOK, status, "the test request must be served: %v", resp)
	assert.Equal(t, true, resp["data"].(map[string]any)["ok"],
		"a reachable broker must test clean, or the button is useless")

	// A connection against a dead port reports the failure.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{
		"name": "Dead", "kind": "mqtt",
		"config": map[string]any{"broker_url": "mqtt://127.0.0.1:1"},
	})
	require.Equalf(t, http.StatusCreated, status, "an unreachable broker is still a storable connection: %v", resp)
	deadID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, deadID, "the id addresses the test request below")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/connections/"+deadID+"/test", nil)
	require.Equalf(t, http.StatusOK, status,
		"a failed test is a successful request carrying a failed result, not an HTTP error: %v", resp)

	result, ok := resp["data"].(map[string]any)
	require.True(t, ok, "the test result comes back under data")
	assert.Equal(t, false, result["ok"], "port 1 is never listening, so the test must fail")
	assert.NotEmpty(t, result["error"],
		"the form renders this beside the field; without a message the operator has nothing to act on")
}

// TestConnectionCreatedDisabledStaysDisabled locks the enabled flag a create
// request carries. The column defaults to enabled, and an insert treats a
// false as unset and takes the default, so a connection created disabled was
// stored enabled and its driver was started against a system the author had
// not finished pointing it at.
func TestConnectionCreatedDisabledStaysDisabled(t *testing.T) {
	srv, token := setupConnectionTest(t)

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/connections", map[string]any{
		"name": "Spare broker", "kind": "mqtt", "enabled": false,
		"config": map[string]any{"broker_url": "mqtt://127.0.0.1:1883"},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)
	data, _ := resp["data"].(map[string]any)
	assert.Equal(t, false, data["enabled"], "the response must report the connection as disabled, as it was asked for")

	id, err := uuid.Parse(data["id"].(string))
	require.NoError(t, err, "the response must carry the new connection's ID")
	stored, err := models.FindConnectionByID(id)
	require.NoError(t, err, "the connection row must exist")
	assert.False(t, stored.Enabled, "the stored row must be disabled, or the next start runs a connection nobody enabled")
}
