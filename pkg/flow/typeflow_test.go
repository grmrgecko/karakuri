package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// carrier is a node that hands its input straight on, the shape a delay, a
// switch, or a filter has.
func carrier() PortSet {
	return PortSet{
		Known:   true,
		Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
		Outputs: []PortSpec{{ID: "out", Type: PortAny, TypeFrom: "in"}},
	}
}

// source is a node that declares what it emits, the shape a parser has.
func source(emits string) PortSet {
	return PortSet{Known: true, Outputs: []PortSpec{{ID: "out", Type: emits}}}
}

// link builds a message edge between two ports.
func link(src, srcPort, tgt, tgtPort string) Edge {
	return Edge{ID: src + "->" + tgt, Source: src, SourceHandle: srcPort, Target: tgt, TargetHandle: tgtPort}
}

// outType reads a resolved output port's type.
func outType(t *testing.T, ports map[string]PortSet, node, port string) string {
	t.Helper()
	spec, ok := ports[node].Output(port)
	require.Truef(t, ok, "node %q has no output %q to read a type from", node, port)
	return spec.Type
}

// TestPropagateTypesCarriesThroughAChain covers the point of the whole thing: a
// node that only passes a message along reports what it was handed, however far
// down the chain it sits.
func TestPropagateTypesCarriesThroughAChain(t *testing.T) {
	g := &Graph{Edges: []Edge{
		link("parse", "out", "delay", "in"),
		link("delay", "out", "gate", "in"),
	}}
	ports := map[string]PortSet{
		"parse": source(PortObject),
		"delay": carrier(),
		"gate":  carrier(),
	}

	PropagateTypes(g, ports)

	assert.Equal(t, PortObject, outType(t, ports, "delay", "out"),
		"a delay behind a parser reports what the parser emits")
	assert.Equal(t, PortObject, outType(t, ports, "gate", "out"),
		"the type carries the whole way down the chain, not just one hop")
}

// TestPropagateTypesStopsAtADeclaredType covers the precedence: a node that
// knows what it emits is not overwritten by what came in.
func TestPropagateTypesStopsAtADeclaredType(t *testing.T) {
	g := &Graph{Edges: []Edge{
		link("parse", "out", "encode", "in"),
		link("encode", "out", "delay", "in"),
	}}
	ports := map[string]PortSet{
		"parse": source(PortObject),
		"encode": {
			Known:   true,
			Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
			Outputs: []PortSpec{{ID: "out", Type: PortString, TypeFrom: "in"}},
		},
		"delay": carrier(),
	}

	PropagateTypes(g, ports)

	assert.Equal(t, PortString, outType(t, ports, "encode", "out"),
		"a node that knows what it emits wins over whatever arrived")
	assert.Equal(t, PortString, outType(t, ports, "delay", "out"),
		"propagation resumes from the declared type, not from the one it replaced")
}

// TestPropagateTypesWithNothingUpstream covers the unwired case, which is what
// an author sees while a graph is half built.
func TestPropagateTypesWithNothingUpstream(t *testing.T) {
	ports := map[string]PortSet{"delay": carrier()}
	PropagateTypes(&Graph{}, ports)

	assert.Equal(t, PortAny, outType(t, ports, "delay", "out"),
		"an unwired passthrough has nothing to report, which is what a half-built graph looks like")
}

// TestPropagateTypesIgnoresValueEdges covers the boundary between the two kinds
// of edge: a value edge supplies a config field, not the message being traced.
func TestPropagateTypesIgnoresValueEdges(t *testing.T) {
	e := link("parse", "out", "delay", "in")
	e.Kind = EdgeValue
	ports := map[string]PortSet{
		"parse": source(PortObject),
		"delay": carrier(),
	}

	PropagateTypes(&Graph{Edges: []Edge{e}}, ports)

	assert.Equal(t, PortAny, outType(t, ports, "delay", "out"),
		"a value edge supplies a config field, so it carries no message to propagate")
}

// TestPropagateTypesSurvivesACycle covers a graph that feeds itself, which must
// resolve to an undeclared type rather than recursing until the depth cap.
func TestPropagateTypesSurvivesACycle(t *testing.T) {
	g := &Graph{Edges: []Edge{
		link("a", "out", "b", "in"),
		link("b", "out", "a", "in"),
	}}
	ports := map[string]PortSet{"a": carrier(), "b": carrier()}

	PropagateTypes(g, ports)

	assert.Equal(t, PortAny, outType(t, ports, "a", "out"),
		"a cycle resolves to undeclared rather than recursing until the depth cap")
}

// TestPropagateTypesLeavesStaticMetadataAlone covers the sharing hazard: the
// port slices belong to the node type, and one placed node's resolution must
// not rewrite them for every other.
func TestPropagateTypesLeavesStaticMetadataAlone(t *testing.T) {
	shared := []PortSpec{{ID: "out", Type: PortAny, TypeFrom: "in"}}
	ports := map[string]PortSet{
		"parse": source(PortObject),
		"delay": {Known: true, Inputs: []PortSpec{{ID: "in", Type: PortAny}}, Outputs: shared},
	}

	PropagateTypes(&Graph{Edges: []Edge{link("parse", "out", "delay", "in")}}, ports)

	assert.Equal(t, PortAny, shared[0].Type,
		"the slice belongs to the node type, so resolving one placed node must not retype every other")
	assert.Equal(t, PortObject, outType(t, ports, "delay", "out"),
		"the resolution still has to land on this node's own port set")
}
