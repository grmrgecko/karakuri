package flow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBypassPairsTypeFromWins covers the declared passthrough: an output that
// names its input with TypeFrom forwards that input, whatever the types say.
// Bypassing a node has to leave the graph's message path intact, so the pairing
// is what the engine splices across when a node is switched off.
func TestBypassPairsTypeFromWins(t *testing.T) {
	set := PortSet{
		Known: true,
		Inputs: []PortSpec{
			{ID: "reset", Type: PortAny, Optional: true},
			{ID: "in", Type: PortAny},
		},
		Outputs: []PortSpec{{ID: "out", Type: PortAny, TypeFrom: "in"}},
	}
	assert.Equal(t, "in", BypassPairs(set)["out"],
		"a declared TypeFrom names the input to splice from, so nothing has to be inferred")
}

// TestBypassPairsByType covers the fallback matching for outputs that declare
// no TypeFrom: an output takes the first message input whose declared payload
// satisfies it, and an output no input can honestly feed is left unpaired
// rather than wired to something that would arrive as the wrong type.
func TestBypassPairsByType(t *testing.T) {
	set := PortSet{
		Known: true,
		Inputs: []PortSpec{
			{ID: "text", Type: PortString},
			{ID: "count", Type: PortInteger},
		},
		Outputs: []PortSpec{
			{ID: "num", Type: PortNumber},
			{ID: "doc", Type: PortObject},
			{ID: "error", Type: PortAny, Kind: PortKindError, TypeFrom: "text"},
		},
	}
	pairs := BypassPairs(set)

	assert.Equal(t, "count", pairs["num"], "an integer input widens to a number output, so it can supply it")
	assert.NotContains(t, pairs, "doc", "no input supplies an object, so the output must stay unpaired")
	assert.NotContains(t, pairs, "error", "a bypassed node cannot fail, so its failure branch has nothing to carry")
}

// TestBypassPairsSkipsParams covers the parameter boundary: a value socket is
// config pulled on demand, not a message, and forwarding one would put a
// config value on the message path.
func TestBypassPairsSkipsParams(t *testing.T) {
	set := PortSet{
		Known: true,
		Inputs: []PortSpec{
			{ID: "param:x", Type: PortAny, Kind: PortKindParam},
			{ID: "in", Type: PortAny},
		},
		Outputs: []PortSpec{{ID: "out", Type: PortAny}},
	}
	assert.Equal(t, "in", BypassPairs(set)["out"],
		"the message input must be chosen over the parameter socket, which carries config rather than a message")
}

// TestBypassable covers which nodes the flag means anything on. Bypass splices
// a message across a node, so it needs something to splice: a node with no
// message input has nothing to forward, and wiring nodes are already the
// splice.
func TestBypassable(t *testing.T) {
	passthrough := PortSet{
		Known:   true,
		Inputs:  []PortSpec{{ID: "in", Type: PortAny}},
		Outputs: []PortSpec{{ID: "out", Type: PortAny}},
	}
	assert.True(t, Bypassable("delay", passthrough),
		"a node with a message input and an output is exactly what bypass splices across")

	assert.False(t, Bypassable(NodeTypeReroute, passthrough),
		"a link node is already wiring, so bypassing it would mean bypassing an edge")
	assert.False(t, Bypassable("manual", PortSet{Known: true, Outputs: passthrough.Outputs}),
		"a trigger has no input, so there is nothing to forward from")
	assert.False(t, Bypassable("template", PortSet{
		Known:   true,
		Inputs:  []PortSpec{{ID: "param:x", Type: PortAny, Kind: PortKindParam}},
		Outputs: passthrough.Outputs,
	}), "parameter sockets are config, not a message input to forward")
}
