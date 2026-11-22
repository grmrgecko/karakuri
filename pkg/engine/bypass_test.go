package engine_test

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bypassRegistry builds a registry with the stub types the flatten tests
// bypass: a passthrough worker and a parser that declares a typed output.
func bypassRegistry() *engine.Registry {
	reg := engine.NewRegistry()
	reg.Register(&stubNode{typ: "work", inputs: []string{"in"},
		outputs: []flow.PortSpec{{ID: "out", Type: flow.PortAny, TypeFrom: "in"}}})
	reg.Register(&stubNode{typ: "parse", inputs: []string{"in"},
		outputs: []flow.PortSpec{
			{ID: "out", Type: flow.PortObject},
			{ID: "error", Type: flow.PortAny, Kind: flow.PortKindError, TypeFrom: "in"},
		}})
	return reg
}

// TestFlattenBypassSplicesPassthrough covers the ordinary case: a bypassed
// node with a passthrough output disappears and its neighbors connect.
func TestFlattenBypassSplicesPassthrough(t *testing.T) {
	b := gnode("b", "work")
	b.Bypass = true
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), b, gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "b", "in"),
			gedge("b", "out", "d", "in"),
		},
	}
	flat, err := engine.FlattenGraph(g, nil, bypassRegistry())
	require.NoError(t, err, "a bypassed passthrough must flatten")

	assert.Len(t, flat.Nodes, 2, "a bypassed node must not survive into the graph the engine runs: %v", flat.Nodes)
	assert.Equal(t, map[string]bool{"t.out->d.in": true}, edgeSet(flat),
		"switching a node off must leave the message path intact, not break the chain")
}

// TestFlattenBypassDropsUnmatchedOutputs covers the honesty rule: a bypassed
// parser's object output has no input that supplies an object, so the branch
// goes dark instead of receiving mislabelled bytes — and the error branch
// gets nothing, because a bypassed node cannot fail.
func TestFlattenBypassDropsUnmatchedOutputs(t *testing.T) {
	b := gnode("b", "parse")
	b.Bypass = true
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), b, gnode("d", "sink"), gnode("e", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "b", "in"),
			gedge("b", "out", "d", "in"),
			gedge("b", "error", "e", "in"),
		},
	}
	flat, err := engine.FlattenGraph(g, nil, bypassRegistry())
	require.NoError(t, err, "a bypassed parser must still flatten")
	assert.Empty(t, flat.Edges,
		"no input supplies an object and a bypassed node cannot fail, so both branches go dark rather than carry the wrong thing: %v",
		flat.Edges)
}

// TestFlattenBypassIgnoredOnTriggers covers the guard: a flag on a node with
// no message input means nothing, and the node stays in the graph.
func TestFlattenBypassIgnoredOnTriggers(t *testing.T) {
	tr := gnode("t", "trigger")
	tr.Bypass = true
	g := &flow.Graph{
		Nodes: []flow.Node{tr, gnode("d", "sink")},
		Edges: []flow.Edge{gedge("t", "out", "d", "in")},
	}
	reg := bypassRegistry()
	reg.Register(&stubNode{typ: "trigger", category: flow.CategoryTrigger, outputs: outPorts})
	flat, err := engine.FlattenGraph(g, nil, reg)
	require.NoError(t, err, "a trigger carrying a meaningless bypass flag must still flatten")

	assert.Len(t, flat.Nodes, 2, "a trigger has no input to splice from, so the flag means nothing and it stays: %v", flat.Nodes)
	assert.Len(t, flat.Edges, 1, "the trigger's edge must survive: %v", flat.Edges)
}

// TestFlattenBypassedSubflowSkipsBody covers bypassing a subflow node: the
// body is never expanded, and the parent's edges connect straight through the
// subflow's matching ports.
func TestFlattenBypassedSubflowSkipsBody(t *testing.T) {
	sf := sfNode("s1", "sg")
	sf.Bypass = true
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sf, gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": simpleBody()},
	}
	flat, err := engine.FlattenGraph(g, nil, bypassRegistry())
	require.NoError(t, err, "a bypassed subflow must flatten")

	assert.Len(t, flat.Nodes, 2, "bypassing a subflow must skip expanding its body, not inline it and then skip it: %v", flat.Nodes)
	assert.Equal(t, map[string]bool{"t.out->d.in": true}, edgeSet(flat),
		"the parent's edges must connect straight through the subflow's matching ports")
}
