package migrate

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRewriteAssistResponsePorts pins the saved-flow upgrade from three HA
// Assist return sockets to one. Stream wins when present; otherwise the full
// response envelope wins over the answer-only string.
func TestRewriteAssistResponsePorts(t *testing.T) {
	graph := flow.Graph{
		Nodes: []flow.Node{
			{ID: "assist", Type: "ha-assist"},
			{ID: "other", Type: "debug"},
		},
		Edges: []flow.Edge{
			{ID: "answer", Source: "agent", Target: "assist", TargetHandle: "response"},
			{ID: "envelope", Source: "agent", SourceHandle: "transcript", Target: "assist", TargetHandle: "transcript"},
			{ID: "stream", Source: "agent", SourceHandle: "transcript", Target: "assist", TargetHandle: "stream"},
			{ID: "unrelated", Source: "agent", Target: "other", TargetHandle: "in"},
		},
	}

	require.True(t, rewriteAssistResponsePorts(&graph))
	require.Len(t, graph.Edges, 2)
	assert.Equal(t, "stream", graph.Edges[0].ID, "the stream carries the final envelope and replaces the two completed-response wires")
	assert.Equal(t, "response", graph.Edges[0].TargetHandle)
	assert.Equal(t, "unrelated", graph.Edges[1].ID)
	assert.False(t, rewriteAssistResponsePorts(&graph), "rewriting an upgraded graph must be a no-op")
}
