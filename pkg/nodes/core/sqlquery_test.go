package core

import (
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sqlstore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSQLQueryEngineBoundary exercises real engine identity, message bindings,
// JSON output and retry suppression rather than substituting execution contexts.
func TestSQLQueryEngineBoundary(t *testing.T) {
	registry := engine.NewRegistry()
	registry.Register(SQLQueryNode{store: &sqlstore.Store{DataDir: t.TempDir()}})
	eng := engine.New(engine.Options{Registry: registry})
	t.Cleanup(eng.Stop)
	for _, tc := range []struct{ name, config, status string }{
		{"bound message", `{"query":"SELECT ? AS room, ? AS value","parameters":"[payload.room, payload.value]"}`, engine.RunStatusSucceeded},
		{"missing parameters", `{"query":"SELECT ? AS room"}`, engine.RunStatusFailed},
		{"deadline validation", `{"query":"SELECT 1","timeout":0}`, engine.RunStatusFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := &engine.Msg{Payload: map[string]any{"room": "kitchen", "value": 2.5}, Topic: "sensor", Meta: map[string]any{"request_id": "r"}}
			result, err := eng.TestNode(t.Context(), engine.RunOptions{
				Graph:       &flow.Graph{Nodes: []flow.Node{{ID: "sql", Type: "sql-query", Config: json.RawMessage(tc.config), Retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 1}}}},
				StartNodeID: "sql", SeedInputs: map[string]*engine.Msg{"in": msg},
			})
			require.NoError(t, err)
			assert.Equal(t, tc.status, result.Status, "node execution failed: %s", result.Error)
			if tc.status == engine.RunStatusSucceeded {
				require.Len(t, result.Outputs["out"], 1)
				var out engine.Msg
				require.NoError(t, json.Unmarshal(result.Outputs["out"][0], &out))
				assert.Equal(t, msg.Topic, out.Topic)
				assert.Equal(t, msg.Meta, out.Meta)
				assert.Equal(t, map[string]any{"columns": []any{"room", "value"}, "rows": []any{map[string]any{"room": "kitchen", "value": 2.5}}, "changes": float64(0), "last_insert_id": float64(0)}, out.Payload)
			} else {
				assert.NotEmpty(t, result.Error)
				for _, raw := range result.Events {
					var event map[string]any
					require.NoError(t, json.Unmarshal(raw, &event))
					if attempt, ok := event["attempt"]; ok {
						assert.Equal(t, float64(1), attempt, "SQL failures must not repeat despite an authored retry override")
					}
				}
			}
		})
	}
}
