package flow

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

// linkNode builds a set or get node publishing or subscribing to a name.
func linkNode(id, nodeType, name string) Node {
	return Node{ID: id, Type: nodeType, Config: json.RawMessage(fmt.Sprintf(`{"name":%q}`, name))}
}

// TestPropagateTypesAcrossSetGet covers the virtual source: a get's output
// reports what the set publishing its name receives, with no drawn edge
// between them. The set is a dead end with no output port, so the get resolves
// through the set's own feeder.
func TestPropagateTypesAcrossSetGet(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			linkNode("s", NodeTypeLinkSet, "v"),
			linkNode("g", NodeTypeLinkGet, "v"),
		},
		Edges: []Edge{link("parse", "out", "s", "in")},
	}
	ports := map[string]PortSet{
		"parse": source(PortObject),
		"s":     {Known: true, Inputs: []PortSpec{{ID: "in", Type: PortAny}}},
		"g":     {Known: true, Outputs: []PortSpec{{ID: "out", Type: PortAny, TypeFrom: "in"}}},
	}

	PropagateTypes(g, ports)

	assert.Equal(t, PortObject, outType(t, ports, "g", "out"),
		"a get reports what feeds the set publishing its name, with no drawn edge between them")
}

// TestValidateLinks covers the wiring holes strict validation reports: a name
// published twice, a get with no set, and a set nothing feeds.
func TestValidateLinks(t *testing.T) {
	g := &Graph{
		Nodes: []Node{
			linkNode("s1", NodeTypeLinkSet, "v"),
			linkNode("s2", NodeTypeLinkSet, "v"),
			linkNode("g1", NodeTypeLinkGet, "v"),
			linkNode("g2", NodeTypeLinkGet, "missing"),
		},
		Edges: []Edge{link("src", "out", "s1", "in")},
	}
	p := &problems{details: make(map[string]string)}
	msgSources := map[string]portRef{"s1/in": {node: "src", port: "out"}}

	validateLinks(p, "", g, LinkSetIndex(g), msgSources)

	assert.Contains(t, p.details, "node:s2", "a name published twice makes every get of it ambiguous")
	assert.Contains(t, p.details, "node:g2", "a get whose name nothing publishes reads a message that never arrives")
	// s2 is also unwired, so its entry may carry both problems; s1 is wired and
	// named once, and must be clean.
	assert.NotContains(t, p.details, "node:s1", "a wired, uniquely named set is correct wiring and must not be reported")
	assert.NotContains(t, p.details, "node:g1", "a get matched to a set is correct wiring and must not be reported")
}

// TestEffectiveValueSource covers the purity chase: a value edge whose source
// is link wiring is judged on the node the splice will actually pull from.
func TestEffectiveValueSource(t *testing.T) {
	nodes := map[string]*Node{
		"http": {ID: "http", Type: "http-request"},
		"r":    {ID: "r", Type: NodeTypeReroute},
		"s":    {ID: "s", Type: NodeTypeLinkSet},
		"g":    {ID: "g", Type: NodeTypeLinkGet},
	}
	nodes["s"].Config = json.RawMessage(`{"name":"v"}`)
	nodes["g"].Config = json.RawMessage(`{"name":"v"}`)
	msgSources := map[string]portRef{
		"r/in": {node: "http", port: "out"},
		"s/in": {node: "r", port: "out"},
	}
	linkSets := map[string]string{"v": "s"}

	assert.Equal(t, "http",
		effectiveValueSource(nodes, nil, msgSources, linkSets, portRef{node: "g", port: "out"}),
		"purity is judged on the node the splice pulls from, not on the wiring in between")
	assert.Equal(t, "http",
		effectiveValueSource(nodes, nil, msgSources, linkSets, portRef{node: "http", port: "out"}),
		"a source that is not wiring is already the node to judge")
	assert.Empty(t,
		effectiveValueSource(nodes, nil, map[string]portRef{}, linkSets, portRef{node: "r", port: "out"}),
		"an unwired chain reaches no node, so there is nothing to judge rather than something to guess")
}

// TestEffectiveValueSourceThroughBypass covers the bypass step: a value edge
// leaving a bypassed node is judged on whatever feeds the input its output
// forwards.
func TestEffectiveValueSourceThroughBypass(t *testing.T) {
	nodes := map[string]*Node{
		"http": {ID: "http", Type: "http-request"},
		"b":    {ID: "b", Type: "delay", Bypass: true},
	}
	ports := map[string]*nodePorts{
		"b": {PortSet: PortSet{
			Known:   true,
			Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
			Outputs: []PortSpec{{ID: "out", Type: PortAny, TypeFrom: "in"}},
		}},
	}
	msgSources := map[string]portRef{"b/in": {node: "http", port: "out"}}

	assert.Equal(t, "http",
		effectiveValueSource(nodes, ports, msgSources, nil, portRef{node: "b", port: "out"}),
		"a bypassed node forwards its input, so the node feeding that input is the one to judge")
}
