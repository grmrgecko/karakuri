package mcpserver_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMCPSharedDraftLifecycle pins the boundary between live drafts and frozen
// deployments, including stale graph writes and whole-draft restoration.
func TestMCPSharedDraftLifecycle(t *testing.T) {
	srv, token, _ := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, created := callTool(t, session, "create_flow", map[string]any{"name": "Shared draft"})
	id := created["id"].(string)
	changes := make(chan *mq.Message, 20)
	sub := mq.B.Subscribe("editor:flow:"+id, func(message *mq.Message) { changes <- message })
	t.Cleanup(func() { mq.B.Unsubscribe(sub) })
	res, saved := callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": validGraph()})
	require.False(t, res.IsError)
	oldRevision := saved["draft_revision"]
	res, _ = callTool(t, session, "deploy_flow", map[string]any{"id": id})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "update_flow", map[string]any{"id": id, "name": "Draft title", "error_flow_id": id})
	require.False(t, res.IsError)
	graph := validGraph()
	graph["nodes"].([]map[string]any)[0]["position"] = map[string]any{"x": 400, "y": 80}
	res, _ = callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph, "expected_revision": oldRevision})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": validGraph(), "expected_revision": oldRevision})
	assert.True(t, res.IsError, "a stale browser or MCP graph must not replace another author's work")
	f, err := models.FindFlowByID(uuid.MustParse(id))
	require.NoError(t, err)
	deployed, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	require.NoError(t, err)
	runtime := *f
	deployed.ApplySettings(&runtime)
	assert.Equal(t, "Shared draft", runtime.Name)
	assert.Nil(t, runtime.ErrorFlowID, "draft error routing must not enter the deployed engine settings")
	_, current := callTool(t, session, "get_flow", map[string]any{"id": id})
	assert.True(t, current["has_draft_changes"].(bool))
	res, restored := callTool(t, session, "restore_flow_version", map[string]any{"id": id})
	require.False(t, res.IsError)
	assert.Equal(t, "Shared draft", restored["name"])
	assert.Equal(t, oldRevision, restored["draft_revision"])
	assert.False(t, restored["has_draft_changes"].(bool))
	require.Len(t, changes, 5, "save, deploy, metadata, graph, and restore must notify open editors")
	for len(changes) > 0 {
		assert.JSONEq(t, "null", string((<-changes).Payload), "notifications must not expose draft contents")
	}

	_, dash := callTool(t, session, "create_dashboard", map[string]any{"name": "Shared dashboard", "layout_mode": "scroll"})
	did := dash["id"].(string)
	theme := models.Theme{Name: "Draft theme", PackageName: "draft-theme", Variables: "{}"}
	require.NoError(t, db.DB.Create(&theme).Error)
	res, _ = callTool(t, session, "deploy_dashboard", map[string]any{"id": did})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "update_dashboard", map[string]any{"id": did, "name": "New title", "theme_id": theme.ID})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "add_dashboard_section", map[string]any{"dashboard_id": did, "title": "Draft section"})
	require.False(t, res.IsError)
	_, published := callTool(t, session, "get_dashboard", map[string]any{"id": did, "view": "deployed"})
	assert.Equal(t, "Shared dashboard", published["name"])
	assert.Nil(t, published["theme_id"])
	assert.Empty(t, published["sections"])
	res, restored = callTool(t, session, "restore_dashboard_version", map[string]any{"id": did})
	require.False(t, res.IsError)
	assert.Equal(t, "Shared dashboard", restored["name"])
	assert.Nil(t, restored["theme_id"])
	assert.Empty(t, restored["sections"])
	assert.False(t, restored["has_draft_changes"].(bool))
}

// TestMCPRunHistory pins exact-run lookup, flow scoping, event pagination, and
// retention so a newer trigger cannot hide the test an agent is diagnosing.
func TestMCPRunHistory(t *testing.T) {
	srv, token, _ := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, created := callTool(t, session, "create_flow", map[string]any{"name": "Run history"})
	id := created["id"].(string)
	flowID := uuid.MustParse(id)
	var first, previous, last uuid.UUID
	active := &models.Run{ID: uuid.New(), FlowID: flowID, Status: "running", StartedAt: time.Now().Add(-time.Hour)}
	require.NoError(t, models.ReplaceLastRun(active))
	for i := 0; i < 102; i++ {
		run := &models.Run{ID: uuid.New(), FlowID: flowID, Status: "running", StartedAt: time.Now().Add(time.Duration(i) * time.Second)}
		if i == 0 {
			first = run.ID
		}
		previous, last = last, run.ID
		require.NoError(t, models.ReplaceLastRun(run))
		require.NoError(t, models.CreateRunEvent(&models.RunEvent{RunID: run.ID, NodeID: "n1", Event: "node_state", State: "succeeded"}))
		require.NoError(t, models.CreateRunEvent(&models.RunEvent{RunID: run.ID, NodeID: "n1", Event: "debug", Payload: "42"}))
		require.NoError(t, models.FinishRun(run.ID, "succeeded", time.Now(), "", false))
	}
	res, history := callTool(t, session, "list_flow_runs", map[string]any{"id": id, "status": "succeeded", "limit": 2})
	require.False(t, res.IsError)
	assert.Equal(t, float64(100), history["total"])
	require.Len(t, history["runs"], 2)
	_, err := models.FindRunByID(active.ID)
	require.NoError(t, err, "active runs must outlive the completed-history limit")
	_, err = models.FindRunByID(first)
	require.Error(t, err)
	events, err := models.ListRunEvents(first)
	require.NoError(t, err)
	assert.Empty(t, events)
	res, detail := callTool(t, session, "get_flow_run", map[string]any{"id": id, "run_id": previous, "include_events": true, "node_id": "n1", "limit": 1})
	require.False(t, res.IsError)
	assert.Equal(t, previous.String(), detail["run"].(map[string]any)["id"])
	assert.True(t, detail["has_more_events"].(bool))
	res, detail = callTool(t, session, "get_flow_run", map[string]any{"id": id, "run_id": previous, "include_events": true, "after_event_id": detail["next_event_id"]})
	require.False(t, res.IsError)
	require.Len(t, detail["events"], 1)
	_, other := callTool(t, session, "create_flow", map[string]any{"name": "Other history"})
	res, _ = callTool(t, session, "get_flow_run", map[string]any{"id": other["id"], "run_id": previous})
	assert.True(t, res.IsError)
}
