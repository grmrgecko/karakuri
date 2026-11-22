package mcpserver_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/grmrgecko/karakuri/pkg/widgets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMCPGraphPatches pins atomic batches, revision conflicts, and keyed diffs
// through the real MCP transport and persisted draft/deployment boundary.
func TestMCPGraphPatches(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, created := callTool(t, session, "create_flow", map[string]any{"name": "Patch checks"})
	id := created["id"]
	control := models.Control{Name: "Flow dependency control", Kind: "boolean", State: "false", Options: "{}"}
	require.NoError(t, db.DB.Create(&control).Error)
	graph := validGraph()
	graph["nodes"] = append(graph["nodes"].([]map[string]any), map[string]any{"id": "control", "type": "control-get", "position": map[string]any{"x": 0, "y": 400}, "config": map[string]any{"control": control.ID.String()}})
	graph["subgraphs"] = map[string]any{"local": map[string]any{"nodes": []map[string]any{{"id": "nested", "type": "debug", "position": map[string]any{"x": 0, "y": 0}}}, "edges": []any{}}}
	res, saved := callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "deploy_flow", map[string]any{"id": id})
	require.False(t, res.IsError)
	revision := saved["draft_revision"]
	operations := []map[string]any{
		{"op": "update_node", "id": "nested", "subgraph_path": []string{"local"}, "value": map[string]any{"config": map[string]any{"_label": "Nested check"}}},
		{"op": "update_node", "id": "n2", "value": map[string]any{"config": map[string]any{"duration": "1s"}, "position": map[string]any{"y": 80}}},
		{"op": "remove_node", "id": "n3"},
		{"op": "add_node", "id": "replacement", "value": map[string]any{"type": "debug", "position": map[string]any{"x": 400, "y": 80}}},
		{"op": "add_edge", "id": "replacement-edge", "value": map[string]any{"source": "n2", "source_handle": "out", "target": "replacement", "target_handle": "in"}},
	}
	res, _ = callTool(t, session, "patch_flow_graph", map[string]any{"id": id, "expected_revision": revision, "operations": operations})
	require.False(t, res.IsError, "valid batches must persist")
	_, current := callTool(t, session, "get_flow", map[string]any{"id": id})
	assert.NotEqual(t, revision, current["draft_revision"])
	storedGraph := current["graph"].(map[string]any)
	nodes := storedGraph["nodes"].([]any)
	delay := nodes[1].(map[string]any)
	assert.Equal(t, float64(200), delay["position"].(map[string]any)["x"], "merge updates preserve unspecified coordinates")
	assert.Equal(t, "1s", delay["config"].(map[string]any)["duration"])
	edges := storedGraph["edges"].([]any)
	require.Len(t, edges, 2)
	assert.Equal(t, "replacement-edge", edges[1].(map[string]any)["id"], "node deletion removes incident edges")
	_, diff := callTool(t, session, "diff_flow", map[string]any{"id": id})
	changes := diff["changes"].([]any)
	paths := []string{}
	for _, change := range changes {
		paths = append(paths, change.(map[string]any)["path"].(string))
	}
	assert.Contains(t, paths, "/graph/nodes/n2/config/duration")
	assert.Contains(t, paths, "/graph/nodes/n3")
	assert.Contains(t, paths, "/graph/subgraphs/local/nodes/nested/config")
	_, same := callTool(t, session, "diff_flow", map[string]any{"id": id, "from": "1", "to": "deployed"})
	assert.Equal(t, false, same["changed"])
	for _, tc := range []struct {
		name     string
		revision any
		ops      []map[string]any
	}{
		{"stale revision", revision, operations},
		{"invalid final edge", current["draft_revision"], []map[string]any{{"op": "update_node", "id": "n2", "value": map[string]any{"config": map[string]any{"duration": "2s"}}}, {"op": "update_edge", "id": "e1", "value": map[string]any{"target": "absent"}}}},
		{"unknown field", current["draft_revision"], []map[string]any{{"op": "update_node", "id": "n2", "value": map[string]any{"postion": map[string]any{"x": 1}}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := callTool(t, session, "patch_flow_graph", map[string]any{"id": id, "expected_revision": tc.revision, "operations": tc.ops})
			assert.True(t, res.IsError)
			_, stored := callTool(t, session, "get_flow", map[string]any{"id": id})
			assert.Equal(t, current["graph"], stored["graph"], "a rejected batch must leave the complete stored graph unchanged")
		})
	}
	limited := connectMCP(t, srv, gated)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"diff_flow", map[string]any{"id": id}},
		{"patch_flow_graph", map[string]any{"id": id, "expected_revision": current["draft_revision"], "operations": operations}},
		{"get_document_dependencies", map[string]any{"kind": "flow", "id": id}},
	} {
		res, _ := callTool(t, limited, tc.tool, tc.args)
		assert.True(t, res.IsError, "%s must enforce document permissions", tc.tool)
	}
	_, dependencies := callTool(t, session, "get_document_dependencies", map[string]any{"id": id, "kind": "flow"})
	require.Len(t, dependencies["dependencies"], 1)
	dep := dependencies["dependencies"].([]any)[0].(map[string]any)
	assert.Equal(t, "available", dep["status"])
	assert.Equal(t, control.Name, dep["name"])
	var role models.Role
	require.NoError(t, db.DB.Where("name = ?", "mcp-gate-only").First(&role).Error)
	require.NoError(t, db.DB.Create(&models.RolePermission{RoleID: role.ID, Permission: perm.PermFlowsRead}).Error)
	res, _ = callTool(t, limited, "get_document_dependencies", map[string]any{"id": id, "kind": "flow"})
	assert.True(t, res.IsError, "flow read alone must not reach a flow the user was not shared")
	var gatedUser models.User
	require.NoError(t, db.DB.Where("username = ?", "mcp-gated").First(&gatedUser).Error)
	require.NoError(t, db.DB.Create(&models.FlowAccess{FlowID: uuid.MustParse(id.(string)), UserID: &gatedUser.ID, Permission: models.FlowAccessView}).Error)
	res, restricted := callTool(t, limited, "get_document_dependencies", map[string]any{"id": id, "kind": "flow"})
	require.False(t, res.IsError)
	require.Len(t, restricted["dependencies"], 1)
	limitedDep := restricted["dependencies"].([]any)[0].(map[string]any)
	assert.Equal(t, "unchecked", limitedDep["status"], "flow read access must not grant access to referenced control metadata")
	assert.NotContains(t, limitedDep, "name")
}

// TestMCPDashboardInspection exercises schema/default validation, missing
// bindings, version diffs, and host metadata without exposing credentials.
func TestMCPDashboardInspection(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	spec := widgets.Spec{Name: "Inspection", PackageName: "inspection", TagName: "inspection-widget", Scope: widgets.WidgetScope{Bindings: "."},
		ConfigSchema: map[string]any{"type": "object", "properties": map[string]any{"showName": map[string]any{"type": "boolean"}}, "required": []string{"showName"}}, DefaultConfig: map[string]any{"showName": true}}
	require.NoError(t, spec.Validate("inspection", "1", "", ""))
	require.NoError(t, widgets.Register(zap.NewNop(), "inspection", t.TempDir(), []widgets.Spec{spec}))
	t.Cleanup(func() { widgets.Unregister("inspection") })
	var widget models.Widget
	require.NoError(t, db.DB.Where("package_name = ?", "inspection").First(&widget).Error)

	control := models.Control{Name: "Inspection control", Kind: "boolean", State: "false", Options: "{}"}
	require.NoError(t, db.DB.Create(&control).Error)
	_, created := callTool(t, session, "create_dashboard", map[string]any{"name": "Inspection dashboard", "grid_cols": 8, "grid_rows": 6})
	id := created["id"]
	res, placement := callTool(t, session, "add_dashboard_widget", map[string]any{"dashboard_id": id, "package": "inspection", "x": 0, "y": 0, "width": 2, "height": 2, "config": map[string]any{"source": "control", "controlId": control.ID.String()}})
	require.False(t, res.IsError)
	res, valid := callTool(t, session, "validate_dashboard", map[string]any{"id": id})
	require.False(t, res.IsError)
	assert.Equal(t, true, valid["valid"])
	res, _ = callTool(t, session, "deploy_dashboard", map[string]any{"id": id})
	require.False(t, res.IsError)
	res, _ = callTool(t, session, "update_dashboard_widget", map[string]any{"dashboard_id": id, "widget_id": placement["id"], "x": 7, "config": map[string]any{"source": "control", "controlId": uuid.NewString(), "showName": "yes"}})
	require.False(t, res.IsError)
	_, invalid := callTool(t, session, "validate_dashboard", map[string]any{"id": id})
	assert.Equal(t, false, invalid["valid"])
	codes := []string{}
	for _, issue := range invalid["issues"].([]any) {
		codes = append(codes, issue.(map[string]any)["code"].(string))
	}
	assert.Contains(t, codes, "out_of_bounds")
	assert.Contains(t, codes, "invalid_config")
	assert.Contains(t, codes, "dependency_missing")
	_, published := callTool(t, session, "validate_dashboard", map[string]any{"id": id, "view": "deployed"})
	assert.Equal(t, true, published["valid"], "draft errors must not alter deployment inspection")
	_, diff := callTool(t, session, "diff_dashboard", map[string]any{"id": id})
	assert.Equal(t, true, diff["changed"])
	_, dependencies := callTool(t, session, "get_document_dependencies", map[string]any{"id": id, "kind": "dashboard", "view": "deployed"})
	require.Len(t, dependencies["dependencies"], 1)
	dep := dependencies["dependencies"].([]any)[0].(map[string]any)
	assert.Equal(t, control.ID.String(), dep["ref"])
	assert.Equal(t, "available", dep["status"])
	agent := models.Agent{Name: "Inspection host", Hostname: "inspection", OS: "linux", Arch: "amd64", CredentialKey: "private-test-key"}
	require.NoError(t, db.DB.Create(&agent).Error)
	res, status := callTool(t, session, "get_agent_status", map[string]any{"id": agent.ID.String()})
	require.False(t, res.IsError)
	assert.Equal(t, false, status["agent"].(map[string]any)["connected"])
	raw, err := json.Marshal(status)
	require.NoError(t, err)
	assert.False(t, strings.Contains(string(raw), agent.CredentialKey))
	res, listed := callTool(t, session, "list_agents", map[string]any{})
	require.False(t, res.IsError)
	require.Len(t, listed["agents"], 1)
	limited := connectMCP(t, srv, gated)
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"diff_dashboard", map[string]any{"id": id}}, {"validate_dashboard", map[string]any{"id": id}},
		{"get_document_dependencies", map[string]any{"id": id, "kind": "dashboard"}},
		{"list_agents", map[string]any{}}, {"get_agent_status", map[string]any{"id": agent.ID.String()}},
		{"render_dashboard", map[string]any{"id": id}},
		{"render_dashboard", map[string]any{"id": id, "diagnostics": true}},
	} {
		res, _ := callTool(t, limited, tc.tool, tc.args)
		assert.True(t, res.IsError, "%s must enforce inspection permissions", tc.tool)
	}
}

// TestMCPGraphDiagnostics preserves repair instructions for malformed model
// arguments and reports strict problems without preventing incomplete drafts.
func TestMCPGraphDiagnostics(t *testing.T) {
	srv, token, _ := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	_, created := callTool(t, session, "create_flow", map[string]any{"name": "Diagnostics"})
	id := created["id"]
	graph := validGraph()
	graph["nodes"].([]map[string]any)[2]["type"] = "ha-call-service}},{id:"
	res, saved := callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph})
	require.False(t, res.IsError, "unknown plugin nodes remain saveable in drafts")
	validation := saved["validation"].(map[string]any)
	assert.Equal(t, false, validation["valid"])
	assert.Contains(t, validation["details"].(map[string]any)["node:n3"], "unknown node type")
	_, checked := callTool(t, session, "validate_flow_graph", map[string]any{"flow_id": id, "strict": true})
	assert.Equal(t, validation, checked, "stored-draft validation must match post-save diagnostics")
	res, _ = callTool(t, session, "patch_flow_graph", map[string]any{
		"id": id, "expected_revision": saved["draft_revision"],
		"operations": []any{map[string]any{"op": "update_edge", "id": "e1", "value": map[string]any{"target_handle": `in\"}},{id:`}}},
	})
	require.True(t, res.IsError)
	message := res.Content[0].(*mcp.TextContent).Text
	assert.Contains(t, message, "edge:e1")
	assert.Contains(t, message, `target node \"n2\" has no input port`)
	_, unchanged := callTool(t, session, "get_flow", map[string]any{"id": id})
	assert.Equal(t, saved["draft_revision"], unchanged["draft_revision"])
	res, repaired := callTool(t, session, "patch_flow_graph", map[string]any{
		"id": id, "expected_revision": saved["draft_revision"],
		"operations": []any{map[string]any{"op": "update_node", "id": "n3", "value": map[string]any{"type": "debug"}}},
	})
	require.False(t, res.IsError)
	assert.Equal(t, true, repaired["validation"].(map[string]any)["valid"])
}
