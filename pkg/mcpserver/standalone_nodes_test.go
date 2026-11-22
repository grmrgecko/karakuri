package mcpserver_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMCPStandaloneNodes exercises node types without creating any flow. Real
// execution must retain fan-out outputs and debug events, respect deadlines,
// and leave flow history and durable state untouched.
func TestMCPStandaloneNodes(t *testing.T) {
	srv, token, gated := setupMCPServer(t)
	session := connectMCP(t, srv, token)
	sandbox.Register(sandbox.JSRuntime{})
	eng := engine.New(engine.Options{Registry: engine.Default(), Logger: zap.NewNop(), History: engine.NewDBHistory(zap.NewNop())})
	eng.Start()
	flowops.SetEngine(eng)
	t.Cleanup(func() { flowops.SetEngine(nil); eng.Stop() })
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":17}`))
	}))
	t.Cleanup(peer.Close)
	scriptConfig := map[string]any{"language": "javascript", "script": "return input.left + input.right;", "inputs": []any{map[string]any{"id": "left", "type": "integer"}, map[string]any{"id": "right", "type": "integer"}}}
	res, info := callTool(t, session, "get_node_type", map[string]any{"type": "script", "config": scriptConfig})
	require.False(t, res.IsError)
	require.Len(t, info["resolved_inputs"], 2)
	assert.Equal(t, "left", info["resolved_inputs"].([]any)[0].(map[string]any)["id"])
	assert.NotEmpty(t, info["config_spec"])
	before := map[string]int64{}
	tables := []string{"flows", "flow_versions", "runs", "run_events", "variables", "node_store_entries"}
	for _, table := range tables {
		var count int64
		require.NoError(t, db.DB.Table(table).Count(&count).Error)
		before[table] = count
	}
	for _, tc := range []struct {
		name, typ      string
		config, inputs map[string]any
		timeout        int
		status         string
		outputs        []any
		errorContains  string
	}{
		{"literal binary port field", "image", map[string]any{"source": "ABH/", "mime": "image/png"}, map[string]any{}, 0, "succeeded", []any{map[string]any{"mime": "image/png", "data": "ABH/"}}, ""},
		{"custom inputs", "script", scriptConfig, map[string]any{"left": map[string]any{"payload": 11}, "right": map[string]any{"payload": 22}}, 0, "succeeded", []any{float64(33)}, ""},
		{"all split messages", "split", map[string]any{"mode": "array"}, map[string]any{"in": map[string]any{"payload": []any{1, 2, 3}}}, 0, "succeeded", []any{float64(1), float64(2), float64(3)}, ""},
		{"real HTTP request", "http-request", map[string]any{"url": peer.URL, "response_format": "json"}, map[string]any{"in": map[string]any{"payload": nil}}, 0, "succeeded", []any{map[string]any{"value": float64(17)}}, ""},
		{"error result", "script", map[string]any{"language": "javascript", "script": "throw new Error('node check failed');"}, map[string]any{"in": map[string]any{"payload": 1}}, 0, "failed", nil, "node check failed"},
		{"parked deadline", "delay", map[string]any{"duration": "10s"}, map[string]any{"in": map[string]any{"payload": 1}}, 1, "canceled", nil, "deadline exceeded"},
		{"first fresh state", "filter", map[string]any{}, map[string]any{"in": map[string]any{"payload": 7}}, 0, "succeeded", []any{float64(7)}, ""},
		{"second fresh state", "filter", map[string]any{}, map[string]any{"in": map[string]any{"payload": 7}}, 0, "succeeded", []any{float64(7)}, ""},
		{"debug event", "debug", map[string]any{"note": "standalone debug"}, map[string]any{"in": map[string]any{"payload": 9}}, 0, "succeeded", nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			started := time.Now()
			res, result := callTool(t, session, "test_node", map[string]any{"type": tc.typ, "config": tc.config, "inputs": tc.inputs, "timeout_seconds": tc.timeout})
			raw, _ := json.Marshal(res)
			require.False(t, res.IsError, "%s", raw)
			assert.Equal(t, tc.status, result["status"])
			assert.NotContains(t, result, "run_id", "standalone tests have no saved run to poll")
			if tc.errorContains != "" {
				assert.Contains(t, result["error"], tc.errorContains)
			}
			if tc.timeout > 0 {
				assert.Less(t, time.Since(started), 5*time.Second, "wall deadlines must include parked time")
			}
			messages, _ := result["outputs"].(map[string]any)["out"].([]any)
			payloads := []any{}
			for _, msg := range messages {
				payloads = append(payloads, msg.(map[string]any)["payload"])
			}
			assert.Equal(t, append([]any{}, tc.outputs...), payloads)
			require.NotEmpty(t, result["events"])
			if tc.typ == "debug" {
				found := false
				for _, event := range result["events"].([]any) {
					value := event.(map[string]any)
					if value["event"] == "debug" {
						found = true
						assert.Equal(t, float64(9), value["payload"])
						assert.Equal(t, "standalone debug", value["note"])
					}
				}
				assert.True(t, found, "debug nodes must return their logged payload")
			}
		})
	}
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"invalid configuration", map[string]any{"type": "http-request", "config": map[string]any{}, "inputs": map[string]any{"in": map[string]any{"payload": nil}}}},
		{"routing needs a flow", map[string]any{"type": "subflow"}},
		{"missing binary port field", map[string]any{"type": "image"}},
		{"missing input", map[string]any{"type": "script", "config": scriptConfig}},
	} {
		t.Run(tc.name, func(t *testing.T) { res, _ := callTool(t, session, "test_node", tc.args); assert.True(t, res.IsError) })
	}
	res, result := callTool(t, session, "test_node", map[string]any{"type": "manual", "config": map[string]any{"output_format": "string", "payload": strings.Repeat("x", (1<<20)+1)}})
	require.False(t, res.IsError)
	assert.Equal(t, true, result["truncated"], "oversized outputs must not make the tool response unbounded")
	limited := connectMCP(t, srv, gated)
	res, _ = callTool(t, limited, "test_node", map[string]any{"type": "manual"})
	assert.True(t, res.IsError)
	for _, table := range tables {
		var count int64
		require.NoError(t, db.DB.Table(table).Count(&count).Error)
		assert.Equal(t, before[table], count, "standalone tests must not write %s", table)
	}
	var persisted []models.Flow
	require.NoError(t, db.DB.Find(&persisted).Error)
	assert.Empty(t, persisted, "no temporary flow may appear in the editor")
}
