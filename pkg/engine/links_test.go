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

// namedLink builds a set or get node publishing or subscribing to a name.
func namedLink(id, nodeType, name string) flow.Node {
	return flow.Node{ID: id, Type: nodeType, Config: json.RawMessage(fmt.Sprintf(`{"name":%q}`, name))}
}

// TestFlattenSplicesReroute covers the elbow: a reroute disappears and its
// neighbors are wired directly.
func TestFlattenSplicesReroute(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{
			gnode("t", "trigger"),
			{ID: "r", Type: flow.NodeTypeReroute},
			gnode("d", "sink"),
		},
		Edges: []flow.Edge{
			gedge("t", "out", "r", "in"),
			gedge("r", "out", "d", "in"),
		},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a graph of a trigger, a reroute, and a sink must flatten")

	assert.Len(t, flat.Nodes, 2, "the reroute is wiring and must not survive into the graph the engine runs: %v", flat.Nodes)
	assert.Equal(t, map[string]bool{"t.out->d.in": true}, edgeSet(flat),
		"the reroute's two edges must become the one edge it stood in for")
}

// TestFlattenSplicesSetGet covers the named pair: the set is a dead end whose
// source feeds every get subscriber, and the link nodes are gone.
func TestFlattenSplicesSetGet(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{
			gnode("t", "trigger"),
			namedLink("s", flow.NodeTypeLinkSet, "v"),
			namedLink("g1", flow.NodeTypeLinkGet, "v"),
			namedLink("g2", flow.NodeTypeLinkGet, "v"),
			gnode("d1", "sink"),
			gnode("d2", "sink"),
		},
		Edges: []flow.Edge{
			gedge("t", "out", "s", "in"),
			gedge("g1", "out", "d1", "in"),
			gedge("g2", "out", "d2", "in"),
		},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a set with two matching gets must flatten")

	assert.Len(t, flat.Nodes, 3, "the three link nodes are wiring and must not survive: %v", flat.Nodes)
	assert.Equal(t, map[string]bool{"t.out->d1.in": true, "t.out->d2.in": true}, edgeSet(flat),
		"what feeds the set must be wired to every get's target, since that is what the pair stands for")
}

// TestFlattenSetGetKeepsValueKind covers a get feeding a parameter: the
// spliced edge must stay a value edge, or the binding would arrive as a
// message.
func TestFlattenSetGetKeepsValueKind(t *testing.T) {
	valueEdge := gedge("g", "out", "d", "cfg")
	valueEdge.Kind = flow.EdgeValue
	g := &flow.Graph{
		Nodes: []flow.Node{
			gnode("v", "value"),
			namedLink("s", flow.NodeTypeLinkSet, "v"),
			namedLink("g", flow.NodeTypeLinkGet, "v"),
			gnode("d", "sink"),
		},
		Edges: []flow.Edge{
			gedge("v", "out", "s", "in"),
			valueEdge,
		},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a value edge through a link pair must flatten")
	require.Len(t, flat.Edges, 1, "the pair collapses to the one edge it stood for: %v", flat.Edges)

	spliced := flat.Edges[0]
	assert.Equal(t, "v", spliced.Source, "the spliced edge must originate at what fed the set")
	assert.Equal(t, "d", spliced.Target, "the spliced edge must land on what the get fed")
	assert.Equal(t, "cfg", spliced.TargetHandle, "the bound config field must survive the splice")
	assert.True(t, spliced.IsValue(),
		"the splice must stay a value edge, or a config binding arrives as a message on a port that has none")
}

// TestFlattenGetWithoutSetFails covers the defensive error: validation should
// have rejected the graph, and flatten refuses rather than dropping the branch
// silently.
func TestFlattenGetWithoutSetFails(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{namedLink("g", flow.NodeTypeLinkGet, "nope"), gnode("d", "sink")},
		Edges: []flow.Edge{gedge("g", "out", "d", "in")},
	}
	_, err := engine.FlattenGraph(g, nil, nil)
	require.Error(t, err,
		"validation should have caught this, so flatten refuses rather than silently dropping the branch")
}

// TestFlattenLinkNamesAreLevelScoped covers the scoping rule: a set inside a
// subflow does not satisfy a get in the graph placing it.
func TestFlattenLinkNamesAreLevelScoped(t *testing.T) {
	body := flow.Graph{
		Nodes: []flow.Node{
			pNode("pi", flow.NodeTypeSubflowInput, "in"),
			namedLink("s", flow.NodeTypeLinkSet, "v"),
			pNode("po", flow.NodeTypeSubflowOutput, "out"),
		},
		Edges: []flow.Edge{
			gedge("pi", "out", "s", "in"),
			gedge("pi", "out", "po", "in"),
		},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{
			gnode("t", "trigger"),
			sfNode("sub", "sg"),
			namedLink("g", flow.NodeTypeLinkGet, "v"),
			gnode("d", "sink"),
		},
		Edges: []flow.Edge{
			gedge("t", "out", "sub", "in"),
			gedge("g", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": body},
	}
	_, err := engine.FlattenGraph(g, nil, nil)
	require.Error(t, err,
		"link names are scoped to one graph level, so a set inside a subflow must not satisfy a get outside it")
}

// TestExecutionGraphKeepsWhatLinksForward covers cutting a graph at an entry
// before flattening it. Link nodes and bypassed nodes forward what is wired
// into them, so the cut must keep that source or the splice leaves the
// consumer with no input: a resume or run-from-node below a get, a parameter
// bound through a get, and a parameter bound through a reroute.
func TestExecutionGraphKeepsWhatLinksForward(t *testing.T) {
	bind := func(src, tgt string) flow.Edge {
		e := gedge(src, "out", tgt, "param:cfg")
		e.Kind = flow.EdgeValue
		return e
	}
	cases := []struct {
		name  string
		g     *flow.Graph
		entry string
		want  string
	}{
		{
			name: "entry below a get carries the set's source",
			g: graph([]flow.Node{
				gnode("t", "trigger"), gnode("a", "work"),
				namedLink("s", flow.NodeTypeLinkSet, "x"), namedLink("g", flow.NodeTypeLinkGet, "x"),
				gnode("d", "work"), gnode("c", "sink"),
			}, []flow.Edge{
				gedge("t", "out", "a", "in"), gedge("a", "out", "s", "in"),
				gedge("g", "out", "d", "in"), gedge("d", "out", "c", "in"),
			}),
			entry: "d",
			want:  "a.out->d.in",
		},
		{
			name: "parameter pulled through a get keeps the set's source",
			g: graph([]flow.Node{
				gnode("t", "trigger"), gnode("v", "value"),
				namedLink("s", flow.NodeTypeLinkSet, "x"), namedLink("g", flow.NodeTypeLinkGet, "x"),
				gnode("d", "sink"),
			}, []flow.Edge{gedge("t", "out", "d", "in"), gedge("v", "out", "s", "in"), bind("g", "d")}),
			entry: "t",
			want:  "v.out->d.param:cfg",
		},
		{
			name: "parameter pulled through a reroute keeps its source",
			g: graph([]flow.Node{
				gnode("t", "trigger"), gnode("v", "value"), {ID: "r", Type: flow.NodeTypeReroute}, gnode("d", "sink"),
			}, []flow.Edge{gedge("t", "out", "d", "in"), gedge("v", "out", "r", "in"), bind("r", "d")}),
			entry: "t",
			want:  "v.out->d.param:cfg",
		},
	}
	for _, tc := range cases {
		flat, err := engine.FlattenGraph(flow.ExecutionGraph(tc.g, tc.entry), nil, nil)
		if !assert.NoError(t, err, "%s: the cut graph must flatten", tc.name) {
			continue
		}
		assert.True(t, edgeSet(flat)[tc.want], "%s: the splice must produce %s, got %v", tc.name, tc.want, edgeSet(flat))
	}
}

// TestFlattenKeepsMultiPortEdgeOrder covers a multi parameter port, such as an
// agent's tools, fed partly through reroutes. The pulled values arrive in
// edge order, so splicing must leave each source where the consumer's own
// edge stood rather than moving spliced edges to the end.
func TestFlattenKeepsMultiPortEdgeOrder(t *testing.T) {
	bind := func(src, tgt string) flow.Edge {
		e := gedge(src, "out", tgt, "param:tools")
		e.Kind = flow.EdgeValue
		return e
	}
	g := graph([]flow.Node{
		gnode("a", "value"), gnode("b", "value"), gnode("c", "value"), gnode("d", "value"),
		{ID: "rb", Type: flow.NodeTypeReroute}, {ID: "rd", Type: flow.NodeTypeReroute},
		gnode("agent", "sink"),
	}, []flow.Edge{
		gedge("b", "out", "rb", "in"), gedge("d", "out", "rd", "in"),
		bind("a", "agent"), bind("rb", "agent"), bind("c", "agent"), bind("rd", "agent"),
	})
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err)
	var order []string
	for _, e := range flat.Edges {
		order = append(order, e.Source)
	}
	assert.Equal(t, []string{"a", "b", "c", "d"}, order, "the tools must reach the agent in the order its edges list them")
}
