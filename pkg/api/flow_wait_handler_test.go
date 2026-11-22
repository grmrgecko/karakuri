package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlowWaitRoutes covers the REST shape of pending waits: a flow lists its
// waits under data.waits, and cancelling one that does not exist is a 404
// rather than a silent success.
func TestFlowWaitRoutes(t *testing.T) {
	srv, token := setupRunServer(t)
	status, created := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "Wait routes"})
	require.Equal(t, http.StatusCreated, status)
	id := created["data"].(map[string]any)["id"].(string)

	status, listed := doJSON(t, srv, token, http.MethodGet, "/api/flows/"+id+"/waits", nil)
	require.Equal(t, http.StatusOK, status, "listing waits: %v", listed)
	assert.Equal(t, []any{}, listed["data"].(map[string]any)["waits"], "a flow with nothing pending lists an empty set")

	status, _ = doJSON(t, srv, token, http.MethodDelete, "/api/flows/"+id+"/waits/n1?key=armed", nil)
	assert.Equal(t, http.StatusNotFound, status, "a wait that does not exist cannot be cancelled")
}
