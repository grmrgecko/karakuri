package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paramPortNode builds a subflow pseudo-port node at a vertical position,
// which is what orders the ports on the placed subflow's card.
func paramPortNode(id, nodeType, portID, portType string, y float64) Node {
	cfg, _ := json.Marshal(map[string]any{"port_id": portID, "port_type": portType})
	return Node{ID: id, Type: nodeType, Position: XY{Y: y}, Config: cfg}
}

// TestSubflowValueInputIsAParameterPort covers a subflow declaring a typed
// parameter. The declared type is what decides the kind of socket: a message
// or any port keeps carrying the message and gates readiness, while a value
// type becomes a parameter that is pulled on demand and never gates. Getting
// this wrong either way deadlocks a subflow — a parameter that gates waits for
// a message nobody sends.
func TestSubflowValueInputIsAParameterPort(t *testing.T) {
	g := &Graph{Nodes: []Node{
		paramPortNode("i1", NodeTypeSubflowInput, "msg", PortAny, 0),
		paramPortNode("i2", NodeTypeSubflowInput, "threshold", PortFloat, 10),
		paramPortNode("o1", NodeTypeSubflowOutput, "out", PortAny, 0),
		paramPortNode("o2", NodeTypeSubflowOutput, "reading", PortInteger, 10),
	}}

	inputs, outputs := SubflowPorts(g)
	require.Len(t, inputs, 2, "both declared inputs must appear on the placed subflow")
	require.Len(t, outputs, 2, "both declared outputs must appear on the placed subflow")

	assert.NotEqual(t, PortKindParam, inputs[0].Kind, "an any-typed input still carries the message")
	assert.False(t, inputs[0].Optional, "a message input gates readiness, or the subflow runs before its input arrives")

	assert.Equal(t, PortKindParam, inputs[1].Kind, "a value-typed input is config, pulled on demand")
	assert.True(t, inputs[1].Optional, "a parameter must not gate readiness, or the subflow waits for a message nobody sends")
	assert.Equal(t, PortFloat, inputs[1].Type, "the declared type is the contract the inside is checked against")

	// A subflow emits values; it never accepts them on an output, so no output
	// is ever a parameter however it is typed.
	for _, o := range outputs {
		assert.NotEqualf(t, PortKindParam, o.Kind, "output %q must not become a parameter", o.ID)
	}
	assert.Equal(t, PortInteger, outputs[1].Type, "a typed output declares what the placed node emits")
}

// TestSubflowPortDefaultDecodes covers the default a value port carries. It is
// read exactly the way the value node reads its own payload — authored text
// plus the format to read it as — so a parameter left unbound at the call site
// still has a value, and a port with no default decodes cleanly rather than
// erroring on the canvas.
func TestSubflowPortDefaultDecodes(t *testing.T) {
	cfg, _ := json.Marshal(map[string]any{
		"port_id": "threshold", "port_type": PortFloat,
		"payload": "21.5", "output_format": "float",
	})

	decoded := DecodePortNode(&Node{Type: NodeTypeSubflowInput, Config: cfg})
	assert.Equal(t, "float", decoded.OutputFormat, "the format is what turns the authored text into a value")
	assert.JSONEq(t, `"21.5"`, string(decoded.Payload), "the default is kept as the text the author typed, not as a parsed number")

	plain := DecodePortNode(&Node{Type: NodeTypeSubflowInput, Config: json.RawMessage(`{"port_id":"x"}`)})
	assert.Empty(t, plain.Payload, "a port with no default has no payload rather than an empty one")
	assert.Equal(t, PortAny, plain.PortType, "an undeclared port type is the message path, which is what a plain subflow input is")
}
