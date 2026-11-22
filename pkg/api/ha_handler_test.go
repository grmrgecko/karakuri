package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant/hatest"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const haTestToken = "api-test-token"

// setupHAAPITest boots the shared test server with the HA driver registered
// and a running connection manager installed in the handlers. It returns the
// test server, a bearer token, the fake HA server, and the manager.
func setupHAAPITest(t *testing.T) (*httptest.Server, string, *hatest.Server, *connmgr.Manager) {
	t.Helper()
	ts, bearer := setupTestServer(t)
	config.C.Security.EncryptionKey = testEncryptionKey
	integrations.RegisterKind(homeassistant.KindDef())

	mgr := connmgr.New(nil, nil)
	connops.SetManager(mgr)
	t.Cleanup(func() {
		connops.SetManager(nil)
		mgr.Stop()
	})

	fake := hatest.New(haTestToken)
	t.Cleanup(fake.Close)
	return ts, bearer, fake, mgr
}

// waitForConnected polls the manager until the connection reports connected.
func waitForConnected(t *testing.T, mgr *connmgr.Manager, id string) {
	t.Helper()
	cid, err := uuid.Parse(id)
	require.NoError(t, err, "the connection id must be a UUID")

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if d, ok := mgr.Get(cid); ok && d.Status().State == integrations.StateConnected {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	require.FailNow(t, "the connection never reached the connected state")
}

// TestHAConnectionAndEntities covers the Home Assistant connection from the API
// side, against a fake server speaking the real websocket protocol. Creating
// one brings a driver up under the connection manager, which is what makes the
// entity list possible at all — and that list is what every entity picker in
// the app is populated from, so its sorting and its friendly names are part of
// the interface rather than an implementation detail.
//
// The sentinel edit is here for the same reason as in the MQTT case: the form
// redisplays a masked token, and a save that took the mask literally would
// break the connection at its next reconnect. The disabled case closes the
// loop — with no driver running there is nothing to ask, and the failure has to
// carry a message the operator can act on.
func TestHAConnectionAndEntities(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetState("switch.zigbee_plug", "off", nil)
	fake.SetState("light.kitchen", "on", map[string]any{"friendly_name": "Kitchen Light"})

	// Create the HA connection; the response must mask the token.
	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant",
		"kind": "homeassistant",
		"config": map[string]any{
			"base_url": fake.URL,
			"token":    haTestToken,
		},
	})
	require.Equalf(t, http.StatusCreated, status, "the connection must be created: %v", resp)

	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "the created connection comes back under data")
	connID, _ := data["id"].(string)
	require.NotEmpty(t, connID, "the id addresses every request below")

	cfg, ok := data["config"].(map[string]any)
	require.True(t, ok, "the connection carries its config")
	assert.Equal(t, "••••••", cfg["token"],
		"a long-lived access token is a secret and must come back masked, since the form redisplays this value")

	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodPost, "/api/connections/"+connID+"/test", nil)
	require.Equalf(t, http.StatusOK, status, "the test request must be served: %v", resp)
	assert.Equal(t, true, resp["data"].(map[string]any)["ok"],
		"the test performs a real handshake, so a reachable server with a valid token must pass")

	// Entities are listed sorted with friendly names and states.
	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/ha/entities", nil)
	require.Equalf(t, http.StatusOK, status, "a connected driver must be able to list entities: %v", resp)

	items, ok := resp["data"].([]any)
	require.True(t, ok, "the entity list is an array")
	require.Len(t, items, 2, "both entities the server holds must be listed")

	first, ok := items[0].(map[string]any)
	require.True(t, ok, "an entity is an object")
	second, ok := items[1].(map[string]any)
	require.True(t, ok, "an entity is an object")

	// The fixture sets the switch first, so an unsorted list would return it
	// first as well.
	assert.Equal(t, "light.kitchen", first["entity_id"],
		"entities are sorted by id: a picker in insertion order is unusable once a house has a hundred of them")
	assert.Equal(t, "switch.zigbee_plug", second["entity_id"], "the rest of the list follows in the same order")
	assert.Equal(t, "Kitchen Light", first["friendly_name"],
		"the picker shows the friendly name, since nobody recognises their devices by entity id")
	assert.Equal(t, "on", first["state"],
		"the current state is shown beside each entity, so the author can tell which one they mean")

	// Updating with the sentinel token keeps the stored secret.
	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/connections/"+connID, map[string]any{
		"config": map[string]any{"base_url": fake.URL, "token": "••••••"},
	})
	require.Equal(t, http.StatusOK, status, "an edit that redisplayed the mask must be accepted")

	cid, err := uuid.Parse(connID)
	require.NoError(t, err, "the connection id must be a UUID")
	stored, err := models.FindConnectionByID(cid)
	require.NoError(t, err, "the connection must still be stored")

	var hc homeassistant.Config
	require.NoError(t, stored.GetConfig(&hc), "the stored config must decrypt")
	assert.Equal(t, haTestToken, hc.Token,
		"the sentinel means unchanged: saving the form without retyping the token must not overwrite it with dots")

	// A disabled connection cannot serve entities: 502 with a clear message.
	status, _ = doJSON(t, ts, bearer, http.MethodPut, "/api/connections/"+connID, map[string]any{
		"enabled": false,
	})
	require.Equal(t, http.StatusOK, status, "disabling a connection must be accepted")

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/ha/entities", nil)
	require.Equalf(t, http.StatusBadGateway, status,
		"with no driver running there is nothing to ask, which is an upstream failure rather than a bad request: %v", resp)
	assert.NotEmpty(t, resp["message"],
		"the picker shows this message; without one the author sees an empty list and no reason for it")
}

// TestHAEntitiesWrongKind covers asking an MQTT connection for Home Assistant
// entities. The route is per kind, and a broker has no notion of them — this is
// a 400 rather than a 502 because the request is wrong rather than the upstream
// being unavailable, and the connection is created disabled so no driver could
// answer even by accident.
func TestHAEntitiesWrongKind(t *testing.T) {
	ts, bearer, _, _ := setupHAAPITest(t)
	integrations.RegisterKind(mqtt.KindDef())

	// An MQTT connection cannot serve HA entities.
	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Broker", "kind": "mqtt", "enabled": false,
		"config": map[string]any{"broker_url": "mqtt://127.0.0.1:1883"},
	})
	require.Equalf(t, http.StatusCreated, status, "the MQTT connection must be created: %v", resp)
	connID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, connID, "the id addresses the request below")

	status, _ = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/ha/entities", nil)
	assert.Equal(t, http.StatusBadRequest, status,
		"a broker has no entities, so the request itself is wrong rather than the upstream being unavailable")
}

// TestHAConnectionValidation covers the two fields a Home Assistant connection
// cannot do without, checked by the kind's own validate hook. Both are refused
// at the create rather than at the first connection attempt, so the operator
// finds out while the form is still in front of them.
func TestHAConnectionValidation(t *testing.T) {
	ts, bearer, _, _ := setupHAAPITest(t)

	// Missing base_url and token are rejected.
	status, _ := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "No URL", "kind": "homeassistant",
		"config": map[string]any{"token": "abc"},
	})
	assert.Equal(t, http.StatusBadRequest, status,
		"there is no default Home Assistant address to fall back on, so the connection names nothing")

	status, _ = doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "No Token", "kind": "homeassistant",
		"config": map[string]any{"base_url": "http://ha.local:8123"},
	})
	assert.Equal(t, http.StatusBadRequest, status,
		"the websocket API rejects an unauthenticated client, so a connection with no token could never connect")
}

// TestHAEntitiesByArea covers the picker list once registries are loaded:
// each entity carries its area, floor, and device, the list is ordered by
// floor and area with entities in no area last, hidden entities appear only
// when asked for, and the registry route lists the floors and areas.
func TestHAEntitiesByArea(t *testing.T) {
	ts, bearer, fake, mgr := setupHAAPITest(t)
	fake.SetHouse()
	for _, id := range []string{"sun.sun", "light.bedroom_lamp", "light.kitchen_ceiling",
		"media_player.kitchen_speaker", "binary_sensor.hallway_motion", "light.hidden_strip"} {
		fake.SetState(id, "on", nil)
	}

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "Home Assistant", "kind": "homeassistant",
		"config": map[string]any{"base_url": fake.URL, "token": haTestToken},
	})
	require.Equalf(t, http.StatusCreated, status, "%v", resp)
	connID := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	var items []any
	require.Eventually(t, func() bool {
		_, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/ha/entities", nil)
		items, _ = resp["data"].([]any)
		return len(items) > 0 && items[0].(map[string]any)["area"] != nil
	}, 5*time.Second, 50*time.Millisecond, "the registries never reached the entity list")

	var order []string
	for _, raw := range items {
		order = append(order, raw.(map[string]any)["entity_id"].(string))
	}
	assert.Equal(t, []string{"light.kitchen_ceiling", "media_player.kitchen_speaker", "light.bedroom_lamp",
		"binary_sensor.hallway_motion", "sun.sun"}, order,
		"ground floor, then upstairs, then the hallway on no floor, then no area; hidden left out")
	speaker := items[1].(map[string]any)
	assert.Equal(t, "Kitchen", speaker["area"])
	assert.Equal(t, "Ground floor", speaker["floor"])
	assert.Equal(t, "Kitchen speaker", speaker["device"])

	_, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/ha/entities?hidden=1", nil)
	assert.Len(t, resp["data"].([]any), 6, "hidden entities are listed when asked for")

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/ha/registry", nil)
	require.Equalf(t, http.StatusOK, status, "%v", resp)
	reg := resp["data"].(map[string]any)
	assert.Len(t, reg["areas"], 3)
	assert.Len(t, reg["floors"], 2)
	assert.Equal(t, "loaded", reg["status"].(map[string]any)["area"])

	status, _ = doJSON(t, ts, "", http.MethodGet, "/api/connections/"+connID+"/ha/registry", nil)
	assert.Equal(t, http.StatusUnauthorized, status, "the registry needs connection read")
}
