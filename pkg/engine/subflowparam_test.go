package engine_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// valuePortNode builds a value-typed subflow input carrying a default.
func valuePortNode(id, portID, portType, defaultText string) flow.Node {
	cfg := fmt.Sprintf(`{"port_id":%q,"port_type":%q,"payload":%q,"output_format":"float"}`,
		portID, portType, defaultText)
	return flow.Node{ID: id, Type: flow.NodeTypeSubflowInput, Config: json.RawMessage(cfg)}
}

// paramBody is a subflow whose worker takes its "threshold" parameter from a
// value-typed input port.
func paramBody() flow.Graph {
	return flow.Graph{
		Nodes: []flow.Node{
			pNode("pi", flow.NodeTypeSubflowInput, "in"),
			valuePortNode("pv", "threshold", flow.PortFloat, "9.5"),
			{ID: "w", Type: "work", Params: []string{"threshold"}},
			pNode("po", flow.NodeTypeSubflowOutput, "out"),
		},
		Edges: []flow.Edge{
			gedge("pi", "out", "w", "in"),
			gedge("w", "out", "po", "in"),
			{ID: "pv-w", Source: "pv", SourceHandle: "out",
				Target: "w", TargetHandle: flow.ParamPortID("threshold"), Kind: flow.EdgeValue},
		},
	}
}

// TestSubflowParameterKeepsDefaultWhenUnwired covers a parent that does not
// supply the parameter: the input node stays in the flattened graph as the
// value node holding the port's default.
func TestSubflowParameterKeepsDefaultWhenUnwired(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sfNode("s1", "sg"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": paramBody()},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a subflow with an unwired parameter must flatten")

	ids := map[string]bool{}
	for _, n := range flat.Nodes {
		ids[n.ID] = true
	}
	assert.Containsf(t, ids, "s1/pv",
		"an unwired value port is the node holding the default, so splicing it out would leave the parameter unsupplied: %v", ids)

	edge := paramEdge(t, flat, "s1/w", "threshold")
	assert.Equal(t, "s1/pv", edge.Source, "the parameter must be pulled from the port node holding the default")
	assert.True(t, edge.IsValue(), "the binding must stay a value edge, or it arrives as a message on a port with none")
}

// TestSubflowParameterWiredFromParent covers the parent supplying the value:
// the pseudo-node is spliced out and the consumer points straight at the
// parent's source, still over a value edge.
func TestSubflowParameterWiredFromParent(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{
			gnode("t", "trigger"),
			gnode("k", "konst"),
			sfNode("s1", "sg"),
			gnode("d", "sink"),
		},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
			// The parent binds the subflow's parameter port.
			{ID: "k-s1", Source: "k", SourceHandle: "out",
				Target: "s1", TargetHandle: "threshold", Kind: flow.EdgeValue},
		},
		Subgraphs: map[string]flow.Graph{"sg": paramBody()},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a subflow with a parameter wired from the parent must flatten")

	ids := map[string]bool{}
	for _, n := range flat.Nodes {
		ids[n.ID] = true
	}
	assert.NotContainsf(t, ids, "s1/pv",
		"the parent supplied the value, so the default node has nothing left to supply and must be spliced out: %v", ids)

	edge := paramEdge(t, flat, "s1/w", "threshold")
	assert.Equal(t, "k", edge.Source, "the consumer must pull straight from the parent's source, not through a hop")
	assert.True(t, edge.IsValue(), "the binding must stay a value edge across the subflow boundary")
}

// paramEdge finds the edge binding a node's named parameter in a flattened
// graph, which is what a value pull follows at run time.
func paramEdge(t *testing.T, g *flow.Graph, target, param string) flow.Edge {
	t.Helper()
	handle := flow.ParamPortID(param)
	for _, e := range g.Edges {
		if e.Target == target && e.TargetHandle == handle {
			return e
		}
	}
	require.FailNowf(t, "the parameter binding was lost by the flatten",
		"no edge binds %s on %s; a lost binding means the node runs with its config default instead: %+v", param, target, g.Edges)
	return flow.Edge{}
}

// TestSubflowParameterKeepsDefaultBehindDarkBypass locks the rule that a
// subflow parameter falls back to its default whenever nothing ends up
// supplying it. A bypassed node whose output pairs with no input goes dark, so
// a parameter wired from it has no source after the splice. The default node
// was dropped whenever the splice reached it before the bypassed node, which
// left the consumer with no binding at all on a random share of flattens.
func TestSubflowParameterKeepsDefaultBehindDarkBypass(t *testing.T) {
	b := gnode("b", "parse")
	b.Bypass = true
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), b, sfNode("s1", "sg"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
			{ID: "b-s1", Source: "b", SourceHandle: "out",
				Target: "s1", TargetHandle: "threshold", Kind: flow.EdgeValue},
		},
		Subgraphs: map[string]flow.Graph{"sg": paramBody()},
	}

	// The splice order follows map iteration, so one flatten proves nothing.
	for i := 0; i < 64; i++ {
		flat, err := engine.FlattenGraph(g, nil, bypassRegistry())
		require.NoError(t, err, "a parameter wired from a bypassed node must flatten")

		ids := map[string]bool{}
		for _, n := range flat.Nodes {
			ids[n.ID] = true
		}
		require.Containsf(t, ids, "s1/pv",
			"the bypassed source went dark, so the default node is the only thing left to supply the parameter: %v", ids)
		edge := paramEdge(t, flat, "s1/w", "threshold")
		assert.Equal(t, "s1/pv", edge.Source, "the parameter must be pulled from the port node holding the default")
	}
}
