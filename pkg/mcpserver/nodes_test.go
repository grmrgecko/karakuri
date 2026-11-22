package mcpserver_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMCPNodeInspectionAndExecution exercises placed-node tools over MCP with
// real persistence and scheduling. Isolated tests must retain output history
// without running neighbors or changing the saved configuration.
func TestMCPNodeInspectionAndExecution(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	sandbox.Register(sandbox.JSRuntime{})
	eng := engine.New(engine.Options{Registry: engine.Default(), Clock: engine.RealClock{}, Logger: zap.NewNop(), History: engine.NewDBHistory(zap.NewNop())})
	eng.Start()
	flowops.SetEngine(eng)
	t.Cleanup(func() { flowops.SetEngine(nil); eng.Stop() })
	res, created := callTool(t, session, "create_flow", map[string]any{"name": "Node inspection"})
	require.False(t, res.IsError)
	id := created["id"]
	graph := validGraph()
	nodes := graph["nodes"].([]map[string]any)
	nodes[0]["config"] = map[string]any{"output_format": "integer", "payload": 21}
	nodes[2]["type"] = "delay"
	nodes[2]["config"] = map[string]any{"duration": "1s"}
	nodes[1]["type"] = "script"
	nodes[1]["config"] = map[string]any{"language": "javascript", "script": "return payload * 2;", "_label": "Double the input", "inputs": []any{map[string]any{"id": "number", "type": "integer"}}, "outputs": []any{map[string]any{"id": "result", "type": "integer"}}}
	edges := graph["edges"].([]map[string]any)
	edges[0]["target_handle"] = "number"
	edges[1]["source_handle"] = "result"
	graph["subgraphs"] = map[string]any{"local": map[string]any{"nodes": []any{map[string]any{"id": "local-node", "type": "debug", "position": map[string]any{"x": 0, "y": 0}}}, "edges": []any{}}}
	res, saved := callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph})
	rawResult, _ := json.Marshal(res)
	require.False(t, res.IsError, "%s", rawResult)
	res, _ = callTool(t, session, "deploy_flow", map[string]any{"id": id})
	require.False(t, res.IsError)
	res, listed := callTool(t, session, "list_flow_nodes", map[string]any{"id": id})
	require.False(t, res.IsError)
	assert.Len(t, listed["nodes"], 3)
	summary := listed["nodes"].([]any)[1].(map[string]any)
	assert.Equal(t, map[string]any{"id": "n2", "type": "script", "label": "Double the input", "available": true}, summary,
		"list responses must not duplicate configurations, ports or wiring")
	assert.Equal(t, saved["draft_revision"], listed["draft_revision"])
	assert.Equal(t, []any{"local"}, listed["subgraph_ids"])
	res, detail := callTool(t, session, "get_flow_node", map[string]any{"id": id, "node_id": "n2"})
	require.False(t, res.IsError)
	info := detail["node"].(map[string]any)
	assert.Equal(t, "number", info["inputs"].([]any)[0].(map[string]any)["id"])
	assert.Equal(t, "result", info["outputs"].([]any)[0].(map[string]any)["id"])
	assert.NotEmpty(t, info["metadata"].(map[string]any)["config_spec"])
	assert.Equal(t, "return payload * 2;", info["node"].(map[string]any)["config"].(map[string]any)["script"], "focused inspection must retain saved configuration")
	assert.Len(t, info["incoming_edges"], 1)
	assert.Len(t, info["outgoing_edges"], 1)
	res, detail = callTool(t, session, "get_flow_node", map[string]any{"id": id, "node_id": "n3"})
	require.False(t, res.IsError)
	info = detail["node"].(map[string]any)
	assert.Equal(t, "integer", info["outputs"].([]any)[0].(map[string]any)["type"], "pass-through ports must report their upstream payload type")
	res, listed = callTool(t, session, "list_flow_edges", map[string]any{"id": id, "node_id": "n2"})
	require.False(t, res.IsError)
	assert.Len(t, listed["edges"], 2)
	res, listed = callTool(t, session, "list_flow_nodes", map[string]any{"id": id, "subgraph_id": "local"})
	require.False(t, res.IsError)
	assert.Len(t, listed["nodes"], 1)

	for _, tc := range []struct {
		name, node, port, script, status string
		payload, want                    any
	}{
		{"custom ports", "n2", "number", "return payload * 3;", "succeeded", 7, float64(21)},
		{"execution failure", "n2", "number", "throw new Error('isolated failure');", "failed", 7, nil},
		{"trigger seed", "n1", "__trigger__", "", "succeeded", 9, float64(9)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := map[string]any{"id": id, "node_id": tc.node, "inputs": map[string]any{tc.port: map[string]any{"payload": tc.payload, "topic": "node-check"}}}
			if tc.script != "" {
				args["config_overrides"] = map[string]any{"script": tc.script}
			}
			res, started := callTool(t, session, "test_flow_node", args)
			rawResult, _ := json.Marshal(res)
			require.False(t, res.IsError, "%s", rawResult)
			var result map[string]any
			require.Eventually(t, func() bool {
				res, result = callTool(t, session, "get_flow_run", map[string]any{"id": id, "run_id": started["run_id"], "include_events": true})
				if res.IsError {
					return false
				}
				run, _ := result["run"].(map[string]any)
				return run["status"] == tc.status
			}, 5*time.Second, 20*time.Millisecond)
			states := result["node_states"].(map[string]any)
			assert.Len(t, states, 1, "neither upstream nor downstream nodes may execute")
			assert.Equal(t, tc.status, states[tc.node].(map[string]any)["state"])
			if tc.want != nil {
				var output any
				for _, raw := range result["events"].([]any) {
					event := raw.(map[string]any)
					var payload struct {
						Outputs map[string]struct {
							Payload any    `json:"payload"`
							Topic   string `json:"topic"`
						} `json:"outputs"`
					}
					if text, ok := event["payload"].(string); ok {
						require.NoError(t, json.Unmarshal([]byte(text), &payload))
					}
					for _, msg := range payload.Outputs {
						output = msg.Payload
						assert.Equal(t, "node-check", msg.Topic)
					}
				}
				assert.Equal(t, tc.want, output)
			}
		})
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"missing input", map[string]any{"id": id, "node_id": "n2"}},
		{"unknown input", map[string]any{"id": id, "node_id": "n2", "inputs": map[string]any{"wrong": map[string]any{"payload": 1}}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, _ := callTool(t, session, "test_flow_node", tc.args)
			assert.True(t, res.IsError)
		})
	}
	_, current := callTool(t, session, "get_flow", map[string]any{"id": id})
	assert.Equal(t, saved["draft_revision"], current["draft_revision"], "temporary overrides must not edit the shared draft")

	// Add a wired config value after deployment to distinguish the two views.
	graph["nodes"] = append(nodes, map[string]any{"id": "duration-value", "type": "manual", "position": map[string]any{"x": 0, "y": 200}, "config": map[string]any{"output_format": "string", "payload": "0s"}})
	nodes[2]["params"] = []string{"duration"}
	graph["edges"] = append(edges, map[string]any{"id": "duration-edge", "source": "duration-value", "source_handle": "out", "target": "n3", "target_handle": "param:duration", "kind": "value"})
	res, saved = callTool(t, session, "save_flow_graph", map[string]any{"id": id, "graph": graph})
	rawResult, _ = json.Marshal(res)
	require.False(t, res.IsError, "%s", rawResult)
	for _, tc := range []struct {
		view  string
		count int
	}{{"draft", 3}, {"deployed", 2}} {
		res, listed = callTool(t, session, "list_flow_edges", map[string]any{"id": id, "view": tc.view})
		require.False(t, res.IsError)
		assert.Len(t, listed["edges"], tc.count)
	}
	args := map[string]any{"id": id, "node_id": "n3", "inputs": map[string]any{"in": map[string]any{"payload": 11}}}
	res, _ = callTool(t, session, "test_flow_node", args)
	assert.True(t, res.IsError, "wired value inputs must not silently fall back to stored config")
	args["config_overrides"] = map[string]any{"duration": "1ms"}
	res, started := callTool(t, session, "test_flow_node", args)
	rawResult, _ = json.Marshal(res)
	require.False(t, res.IsError, "%s", rawResult)
	require.Eventually(t, func() bool {
		res, result := callTool(t, session, "get_flow_run", map[string]any{"id": id, "run_id": started["run_id"]})
		if res.IsError {
			return false
		}
		if result["run"].(map[string]any)["status"] != "succeeded" {
			return false
		}
		assert.Len(t, result["node_states"], 1, "value sources must not execute during an isolated test")
		return true
	}, 5*time.Second, 20*time.Millisecond)
	_, current = callTool(t, session, "get_flow", map[string]any{"id": id})
	assert.Equal(t, saved["draft_revision"], current["draft_revision"])
	res, started = callTool(t, session, "test_flow_node", map[string]any{"id": id, "subgraph_id": "local", "node_id": "local-node", "inputs": map[string]any{"in": map[string]any{"payload": 1}}})
	require.False(t, res.IsError)
	assert.Equal(t, "@subgraph/local/local-node", started["node_id"], "local tests must not share root-node durable keys")
	limited := connectMCP(t, srv, gated)
	for _, name := range []string{"list_flow_nodes", "get_flow_node", "list_flow_edges", "test_flow_node"} {
		res, _ := callTool(t, limited, name, map[string]any{"id": id, "node_id": "n2"})
		assert.True(t, res.IsError, "%s must enforce flow permissions", name)
	}
}
