package flowops

import (
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodes/mqttnodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTestParamsPayloadTypedByPort covers the seed a test run builds from a
// trigger's stored test parameters. The config holds text an author typed, and
// the trigger's output port declares what it emits, so the seed has to be read
// as that type or a test run exercises the graph with a payload no real message
// would ever carry.
//
// Binary is the case that matters: a real mqtt-in in binary mode emits bytes,
// while the config can only hold base64 text, so the seed is the one place the
// two have to be reconciled. It is driven against the real node's metadata
// rather than a stub, since the port types are exactly what is being tested.
func TestTestParamsPayloadTypedByPort(t *testing.T) {
	meta := mqttnodes.InNode{}.Meta()

	// A binary trigger holds base64 text in its config and must seed real bytes.
	binary := &flow.Node{Type: "mqtt-in", Config: json.RawMessage(`{"format":"binary"}`)}
	value, err := testParamsPayload(meta, binary, "aGVsbG8=")
	require.NoError(t, err, "base64 text is the only way a config can hold bytes, so it must be accepted")
	assert.Equal(t, []byte("hello"), value,
		"the seed must be decoded to real bytes, since that is what a real message on this port carries")

	_, err = testParamsPayload(meta, binary, "not base64!")
	assert.Error(t, err, "text that is not base64 cannot become bytes, and seeding it as text would break the port's type")

	_, err = testParamsPayload(meta, binary, map[string]any{"a": 1})
	assert.Error(t, err, "an object has no byte form a flow author would recognise")

	// Every other port type already holds what it promised.
	text := &flow.Node{Type: "mqtt-in", Config: json.RawMessage(`{"format":"text"}`)}
	value, err = testParamsPayload(meta, text, "ON")
	require.NoError(t, err, "a text trigger's stored seed is already text")
	assert.Equal(t, "ON", value, "nothing needs converting, so the stored value is the seed")

	object := &flow.Node{Type: "mqtt-in", Config: json.RawMessage(`{"format":"json"}`)}
	value, err = testParamsPayload(meta, object, map[string]any{"state": "ON"})
	require.NoError(t, err, "a JSON trigger's stored seed is already a document")
	assert.Equal(t, map[string]any{"state": "ON"}, value, "the stored document is the seed, unchanged")
}

// TestSeedInputsFollowWiring locks that a run started partway down a graph is
// seeded with what the node would have received. Reroutes, set/get pairs, and
// bypassed nodes never run, so they store no output; reading the seed from the
// drawn edge refused every start below one with "no stored output". The seed
// comes from the node the wiring forwards. The direct row pins the plain case.
func TestSeedInputsFollowWiring(t *testing.T) {
	node := func(id, typ, config string) flow.Node {
		n := flow.Node{ID: id, Type: typ}
		if config != "" {
			n.Config = json.RawMessage(config)
		}
		return n
	}
	edge := func(id, src, tgt string) flow.Edge {
		return flow.Edge{ID: id, Source: src, SourceHandle: "out", Target: tgt, TargetHandle: "in"}
	}
	stored := &engine.Msg{Payload: "from the last run"}
	outputs := map[string]map[string]*engine.Msg{"src": {"out": stored}}

	for _, tc := range []struct {
		name  string
		nodes []flow.Node
		edges []flow.Edge
	}{
		{
			name:  "a direct edge",
			nodes: []flow.Node{node("src", "manual", ""), node("b", "debug", "")},
			edges: []flow.Edge{edge("e1", "src", "b")},
		},
		{
			name:  "through a reroute",
			nodes: []flow.Node{node("src", "manual", ""), node("r", flow.NodeTypeReroute, ""), node("b", "debug", "")},
			edges: []flow.Edge{edge("e1", "src", "r"), edge("e2", "r", "b")},
		},
		{
			name: "through a set and get pair",
			nodes: []flow.Node{
				node("src", "manual", ""), node("set", flow.NodeTypeLinkSet, `{"name":"bus"}`),
				node("get", flow.NodeTypeLinkGet, `{"name":"bus"}`), node("b", "debug", ""),
			},
			edges: []flow.Edge{edge("e1", "src", "set"), edge("e2", "get", "b")},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &flow.Graph{Nodes: tc.nodes, Edges: tc.edges}
			seed, err := buildSeedInputs(g, "b", outputs)
			require.NoError(t, err, "the upstream node ran last time, so its output is there to seed from")
			assert.Same(t, stored, seed["in"], "the start node's input is seeded with the message the wiring forwards")
		})
	}

	g := &flow.Graph{
		Nodes: []flow.Node{node("src", "manual", ""), node("r", flow.NodeTypeReroute, ""), node("b", "debug", "")},
		Edges: []flow.Edge{edge("e1", "src", "r"), edge("e2", "r", "b")},
	}
	_, err := buildSeedInputs(g, "b", map[string]map[string]*engine.Msg{})
	require.Error(t, err, "a start with nothing stored upstream is still refused")
	assert.Contains(t, err.Error(), "src.out", "the report names the node that has to run first, not the wiring in between")
}
