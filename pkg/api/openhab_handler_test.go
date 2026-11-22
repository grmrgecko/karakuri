package api_test

import (
	"net/http"
	"testing"

	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/connmgr"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab/openhabtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestOpenHABItems covers the item list behind every openHAB picker: a
// connection to a fake house lists its items by floor and location with items
// in no location last, and the list needs connection read.
func TestOpenHABItems(t *testing.T) {
	ts, bearer := setupTestServer(t)
	config.C.Security.EncryptionKey = testEncryptionKey
	integrations.RegisterKind(openhab.KindDef())
	mgr := connmgr.New(nil, nil)
	connops.SetManager(mgr)
	t.Cleanup(func() { connops.SetManager(nil); mgr.Stop() })

	fake := openhabtest.New()
	t.Cleanup(fake.Close)
	fake.House()

	status, resp := doJSON(t, ts, bearer, http.MethodPost, "/api/connections", map[string]any{
		"name": "openHAB test", "kind": "openhab", "config": map[string]any{"base_url": fake.URL},
	})
	require.Equalf(t, http.StatusCreated, status, "%v", resp)
	connID := resp["data"].(map[string]any)["id"].(string)
	waitForConnected(t, mgr, connID)

	status, resp = doJSON(t, ts, bearer, http.MethodGet, "/api/connections/"+connID+"/openhab/items", nil)
	require.Equalf(t, http.StatusOK, status, "%v", resp)
	items := resp["data"].([]any)
	require.Len(t, items, 18)
	first := items[0].(map[string]any)
	assert.Equal(t, "Ground floor", first["floor_label"], "items on the ground floor come first")
	last := items[len(items)-1].(map[string]any)
	assert.Empty(t, last["location"], "items in no location come last")
	byName := map[string]map[string]any{}
	for _, raw := range items {
		it := raw.(map[string]any)
		byName[it["name"].(string)] = it
	}
	assert.Equal(t, "Kitchen", byName["KitchenTemp"]["location_label"])
	assert.Equal(t, "Number:Temperature", byName["KitchenTemp"]["type"])
	assert.Equal(t, "Group:Switch", byName["gLights"]["type"])

	status, _ = doJSON(t, ts, "", http.MethodGet, "/api/connections/"+connID+"/openhab/items", nil)
	assert.Equal(t, http.StatusUnauthorized, status, "the item list needs connection read")
}
