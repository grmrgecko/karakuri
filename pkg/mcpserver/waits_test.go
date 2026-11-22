package mcpserver_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMCPPendingWaits covers US4 over MCP: an armed wait is listed with its
// node, deadline, and missed-deadline setting, cancelling it removes it and
// records who did, and a caller without run permissions is refused both.
func TestMCPPendingWaits(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	clock := engine.NewFakeClock(time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC))
	eng := engine.New(engine.Options{
		Registry: engine.Default(), Clock: clock, Logger: zap.NewNop(),
		History:  engine.NewDBHistory(zap.NewNop()),
		WaitSink: engine.NewDBWaitSink(zap.NewNop()),
	})
	eng.Start()
	flowops.SetEngine(eng)
	t.Cleanup(func() {
		eng.Stop()
		flowops.SetEngine(nil)
	})

	_, created := callTool(t, session, "create_flow", map[string]any{"name": "Pending waits"})
	id := created["id"].(string)
	graph := map[string]any{"nodes": []map[string]any{
		{"id": "start", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
		{"id": "lights_off", "type": "trigger", "position": map[string]any{"x": 200, "y": 0},
			"config": map[string]any{"duration": "2h", "first_source": "nothing", "missed_deadline": "drop"}},
	}, "edges": []map[string]any{
		{"id": "e", "source": "start", "source_handle": "out", "target": "lights_off", "target_handle": "in"},
	}}
	res, _ := callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "deploy_flow", map[string]any{"id": id})
	require.False(t, res.IsError)

	f, err := models.FindFlowByID(uuid.MustParse(id))
	require.NoError(t, err)
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	require.NoError(t, err)
	var g flow.Graph
	require.NoError(t, json.Unmarshal([]byte(version.Graph), &g))
	_, err = eng.RunFlow(engine.RunOptions{FlowID: f.ID, FlowVersionID: version.ID, TriggerNodeID: "start",
		Graph: &g, InitialMsg: &engine.Msg{}, DurableWaits: true})
	require.NoError(t, err)
	require.True(t, clock.BlockUntilAfterCalls(1, 3*time.Second), "the trigger must park")

	res, state := callTool(t, session, "inspect_flow_state", map[string]any{"id": id, "store": "waits"})
	require.False(t, res.IsError)
	waits := state["waits"].([]any)
	require.Len(t, waits, 1)
	w := waits[0].(map[string]any)
	assert.Equal(t, "lights_off", w["node_id"])
	assert.Equal(t, "trigger", w["node_type"])
	assert.Equal(t, "drop", w["missed_deadline"], "the node's setting is reported with the wait")
	assert.Equal(t, "2026-10-30T14:00:00Z", w["deadline"])
	assert.Equal(t, true, w["durable"])

	limited := connectMCP(t, srv, gated)
	res, _ = callTool(t, limited, "inspect_flow_state", map[string]any{"id": id, "store": "waits"})
	assert.True(t, res.IsError, "listing waits needs run read permission")
	res, _ = callTool(t, limited, "cancel_flow_wait", map[string]any{"id": id, "node_id": "lights_off", "key": w["key"]})
	assert.True(t, res.IsError, "cancelling a wait needs run cancel permission")

	res, _ = callTool(t, session, "cancel_flow_wait", map[string]any{"id": id, "node_id": "lights_off", "key": w["key"]})
	require.False(t, res.IsError)
	res, state = callTool(t, session, "inspect_flow_state", map[string]any{"id": id, "store": "waits"})
	require.False(t, res.IsError)
	assert.Empty(t, state["waits"], "a cancelled wait is gone")
	require.Eventually(t, func() bool {
		var n int64
		db.DB.Model(&models.Run{}).Where("flow_id = ? AND error = ?", f.ID, "wait cancelled by mcp-admin").Count(&n)
		return n == 1
	}, 3*time.Second, time.Millisecond, "the cancellation is recorded with who did it")
	res, _ = callTool(t, session, "cancel_flow_wait", map[string]any{"id": id, "node_id": "lights_off", "key": w["key"]})
	assert.True(t, res.IsError, "a wait that is gone cannot be cancelled again")
}
