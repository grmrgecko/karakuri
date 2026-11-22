package api_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/config"
	"github.com/grmrgecko/karakuri/pkg/access"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// connectionUserPerms are the roles a household account would need to add and
// use connections and to build flows with them.
var connectionUserPerms = append([]string{
	permissions.PermConnectionsRead, permissions.PermConnectionsWrite, permissions.PermConnectionsInvoke,
}, flowUserPerms...)

// publishFixture is a node with one connection field. It reads its config
// and reaches nothing else, so a run of it shows whether the engine let its
// run user name the connection.
type publishFixture struct{}

func (publishFixture) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "test-publish", Label: "Publish fixture", Category: "action",
		Inputs:     []flow.PortSpec{{ID: "in", Label: "In"}},
		ConfigSpec: []flow.FieldSpec{{ID: "connection", Label: "Connection", Type: flow.FieldConnection, ConnectionKind: "mqtt"}},
	}
}

func (publishFixture) Execute(_ context.Context, ec engine.ExecContext, _ map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		Connection string `json:"connection"`
	}
	return nil, ec.Config(&cfg)
}

// mqttOutGraph is a deployable draft publishing to the given connection.
func mqttOutGraph(connectionID string) map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "out", "type": "test-publish", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{"connection": connectionID}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "out", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// listedIDs returns the IDs a list route shows a user, with the access level
// each item reports.
func listedIDs(t *testing.T, status int, resp map[string]any) map[string]string {
	t.Helper()
	require.Equalf(t, http.StatusOK, status, "the list must load: %v", resp)
	out := map[string]string{}
	for _, item := range resp["data"].([]any) {
		m := item.(map[string]any)
		level, _ := m["access"].(string)
		out[m["id"].(string)] = level
	}
	return out
}

// TestConnectionAccess pins feature 047 against the real API. Before it, a
// connection was global: anyone holding connection read listed every one, and
// anyone who could deploy a flow could publish through any broker by naming
// it in a node. Each step depends on the share state the previous one left.
func TestConnectionAccess(t *testing.T) {
	ts, _ := setupRunServer(t)
	config.C.Security.EncryptionKey = testEncryptionKey
	integrations.RegisterKind(mqtt.KindDef())
	engine.Default().Register(publishFixture{})
	t.Cleanup(func() { engine.Default().Unregister("test-publish") })
	alice := sessionWith(t, "alice", connectionUserPerms...)
	bob := sessionWith(t, "bob", connectionUserPerms...)
	boss := sessionWith(t, "boss", permissions.PermConnectionsRead, permissions.PermConnectionsManage)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	// A disabled connection starts no driver, so nothing here dials a broker.
	status, resp := call(alice, http.MethodPost, "/api/connections", map[string]any{
		"name": "Workshop broker", "kind": "mqtt", "enabled": false,
		"config": map[string]any{"broker_url": "mqtt://127.0.0.1:1"},
	})
	require.Equalf(t, http.StatusCreated, status, "alice must create her connection: %v", resp)
	connID := resp["data"].(map[string]any)["id"].(string)
	base := "/api/connections/" + connID
	assert.Equal(t, "admin", resp["data"].(map[string]any)["access"], "the creator owns the connection")

	status, resp = call(bob, http.MethodPost, "/api/flows", map[string]any{"name": "Garage door"})
	require.Equalf(t, http.StatusCreated, status, "bob must create his flow: %v", resp)
	flowID := resp["data"].(map[string]any)["id"].(string)
	status, resp = call(bob, http.MethodPut, "/api/flows/"+flowID+"/graph", map[string]any{"graph": mqttOutGraph(connID)})
	require.Equalf(t, http.StatusOK, status, "a draft may name any connection; saving is lenient: %v", resp)

	t.Run("an unshared connection is invisible and unusable", func(t *testing.T) {
		status, resp := call(bob, http.MethodGet, "/api/connections", nil)
		assert.NotContains(t, listedIDs(t, status, resp), connID, "bob must not see alice's connection listed")
		status, _ = call(bob, http.MethodGet, base, nil)
		assert.Equal(t, http.StatusNotFound, status, "reading it answers as if it does not exist")
		status, _ = call(bob, http.MethodPost, base+"/mqtt/publish", map[string]any{"topic": "garage/door", "payload": "open"})
		assert.Equal(t, http.StatusNotFound, status, "publishing through it answers as if it does not exist")
		status, resp = call(bob, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{})
		assert.Equalf(t, http.StatusBadRequest, status, "deploying a flow that names it must be refused: %v", resp)
		assert.Contains(t, resp["message"], "not shared with you", "the refusal names the connection rather than failing validation")
	})

	t.Run("a run acting as a user without access fails at the node", func(t *testing.T) {
		previous := access.SetRunChecks(true)
		t.Cleanup(func() { access.SetRunChecks(previous) })
		status, resp := call(bob, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{})
		require.Equalf(t, http.StatusAccepted, status, "the test run starts; the node is what fails: %v", resp)
		detail := waitRunStatus(t, ts, bob, flowID, "failed", "")
		assert.Equal(t, "failed", nodeState(t, detail, "out"), "the publish node must fail before reaching the connection")
	})

	t.Run("a manager reaches every connection", func(t *testing.T) {
		status, resp := call(boss, http.MethodGet, "/api/connections", nil)
		assert.Equal(t, "admin", listedIDs(t, status, resp)[connID], "the manage permission holds admin on every connection")
	})

	t.Run("a use share admits using it but not changing it", func(t *testing.T) {
		status, resp := call(alice, http.MethodPost, base+"/shares", map[string]any{"username": "bob", "permission": "use"})
		require.Equalf(t, http.StatusCreated, status, "alice must share her connection: %v", resp)

		status, resp = call(bob, http.MethodGet, "/api/connections", nil)
		assert.Equal(t, "use", listedIDs(t, status, resp)[connID], "bob now sees the connection at use")
		status, resp = call(bob, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{})
		assert.Equalf(t, http.StatusOK, status, "deploying against a shared connection succeeds: %v", resp)
		status, _ = call(bob, http.MethodPut, base, map[string]any{"name": "Renamed by bob"})
		assert.Equal(t, http.StatusForbidden, status, "use does not admit changing the connection")
		status, _ = call(bob, http.MethodGet, base+"/shares", nil)
		assert.Equal(t, http.StatusForbidden, status, "use does not admit reading who it is shared with")
		status, _ = call(bob, http.MethodDelete, base, nil)
		assert.Equal(t, http.StatusForbidden, status, "use does not admit deleting the connection")
	})

	t.Run("an edit share admits changing it but not sharing it", func(t *testing.T) {
		status, resp := call(alice, http.MethodPost, base+"/shares", map[string]any{"username": "bob", "permission": "edit"})
		require.Equalf(t, http.StatusCreated, status, "alice must raise bob's share: %v", resp)
		status, resp = call(bob, http.MethodPut, base, map[string]any{"name": "Workshop broker (bob)"})
		assert.Equalf(t, http.StatusOK, status, "edit admits changing the connection: %v", resp)
		status, _ = call(bob, http.MethodPost, base+"/shares", map[string]any{"username": "boss", "permission": "use"})
		assert.Equal(t, http.StatusForbidden, status, "edit does not admit sharing the connection")
	})
}

// TestAgentAccess pins the agent half of feature 047: an agent belongs to
// whoever minted its token, other users neither list nor change it, and a
// share at use lists it without letting the user disable or delete it.
func TestAgentAccess(t *testing.T) {
	ts, _ := setupTestServer(t)
	agentPerms := []string{permissions.PermAgentsRead, permissions.PermAgentsWrite, permissions.PermAgentsRevoke}
	alice := sessionWith(t, "alice", agentPerms...)
	bob := sessionWith(t, "bob", agentPerms...)
	call := func(token, method, path string, body any) (int, map[string]any) {
		t.Helper()
		return doJSON(t, ts, token, method, path, body)
	}

	owner, err := uuid.Parse(userIDOf(t, "alice"))
	require.NoError(t, err)
	agent := &models.Agent{ID: uuid.New(), Name: "alice-laptop", Enabled: true, OwnerID: &owner}
	require.NoError(t, db.DB.Create(agent).Error)
	base := "/api/agents/" + agent.ID.String()

	status, resp := call(bob, http.MethodGet, "/api/agents", nil)
	assert.NotContains(t, listedIDs(t, status, resp), agent.ID.String(), "bob must not see alice's agent listed")
	status, _ = call(bob, http.MethodPut, base, map[string]any{"name": "bobs-now", "enabled": false})
	assert.Equal(t, http.StatusNotFound, status, "changing an unshared agent answers as if it does not exist")

	status, resp = call(alice, http.MethodGet, "/api/agents", nil)
	assert.Equal(t, "admin", listedIDs(t, status, resp)[agent.ID.String()], "the token's minter owns the agent")

	status, resp = call(alice, http.MethodPost, base+"/shares", map[string]any{"username": "bob", "permission": "use"})
	require.Equalf(t, http.StatusCreated, status, "alice must share her agent: %v", resp)
	status, resp = call(bob, http.MethodGet, "/api/agents", nil)
	assert.Equal(t, "use", listedIDs(t, status, resp)[agent.ID.String()], "bob now sees the agent at use")
	status, _ = call(bob, http.MethodPut, base, map[string]any{"name": "alice-laptop", "enabled": false})
	assert.Equal(t, http.StatusForbidden, status, "use does not admit disabling the agent")
	status, _ = call(bob, http.MethodDelete, base, nil)
	assert.Equal(t, http.StatusForbidden, status, "use does not admit deleting the agent")
}
