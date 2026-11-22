package flow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// payloadFormatRule mirrors the manual node's output rule, which lives with
// the node in pkg/nodes/core: the port takes the type of the configured
// payload, and an unset format stays undeclared because the text is read as
// JSON when it parses and kept as text when it does not.
func payloadFormatRule() *PortTypeRule {
	return &PortTypeRule{
		Cases: []PortTypeCase{
			{Match: map[string][]string{"output_format": {PayloadString}}, Type: PortString},
			{Match: map[string][]string{"output_format": {PayloadInteger}}, Type: PortInteger},
			{Match: map[string][]string{"output_format": {PayloadFloat}}, Type: PortFloat},
			{Match: map[string][]string{"output_format": {PayloadBoolean}}, Type: PortBool},
			{Match: map[string][]string{"output_format": {PayloadJSON, PayloadYAML}}, Type: PortObject},
		},
		Default: PortAny,
	}
}

// TestManualPorts covers the derived output port: the ID is stable so typing a
// node never re-points its edges, and the type follows the configured payload
// through the ordinary rule resolution, with no manual special case left. The
// rows are every payload format the editor offers plus the two ways a config
// can say nothing, which have to stay undeclared rather than guess.
func TestManualPorts(t *testing.T) {
	meta := NodeMeta{Type: "manual", Outputs: []PortSpec{{ID: "out", Label: "Out", Type: PortAny, TypeRule: payloadFormatRule()}}}
	cases := []struct {
		name string
		cfg  string
		want string
		why  string
	}{
		{name: "unconfigured", cfg: ``, want: PortAny, why: "a node just dropped on the canvas declares nothing"},
		{name: "no type chosen", cfg: `{"payload":"{}"}`, want: PortAny, why: "text alone does not say how to read it"},
		{name: "string", cfg: `{"payload":"hi","output_format":"string"}`, want: PortString},
		{name: "integer", cfg: `{"payload":"1","output_format":"integer"}`, want: PortInteger},
		{name: "float", cfg: `{"payload":"1.5","output_format":"float"}`, want: PortFloat},
		{name: "boolean", cfg: `{"payload":"true","output_format":"boolean"}`, want: PortBool},
		{name: "json", cfg: `{"payload":"{}","output_format":"json"}`, want: PortObject},
		{name: "yaml", cfg: `{"payload":"a: 1","output_format":"yaml"}`, want: PortObject, why: "YAML parses to the same shape as JSON"},
		{name: "unreadable config", cfg: `not json`, want: PortAny, why: "a hand-edited graph must not take the canvas down"},
	}
	for _, c := range cases {
		set := NodePorts(meta, &Node{ID: "m", Type: "manual", Config: json.RawMessage(c.cfg)}, nil)

		require.Lenf(t, set.Outputs, 1, "a manual trigger has one output whatever its config says (%s)", c.name)
		assert.Equalf(t, "out", set.Outputs[0].ID, "the port id must stay stable, or typing a node re-points its edges (%s)", c.name)
		assert.Equalf(t, c.want, set.Outputs[0].Type, "output type for %q: %s", c.name, c.why)
	}
}

// TestCompatible covers the assignability rules, which are directional: a
// declared type flows into a port accepting less specific values, not back.
func TestCompatible(t *testing.T) {
	cases := []struct {
		src, tgt string
		want     bool
		why      string
	}{
		{PortString, PortString, true, "exact match"},

		// The rule that keeps existing graphs working: an undeclared input
		// takes anything, so typing an output must not break its edges.
		{PortString, PortAny, true, "a declared payload satisfies an undeclared input"},
		{PortObject, PortAny, true, "a declared payload satisfies an undeclared input"},
		{PortAny, PortAny, true, "undeclared to undeclared"},

		// Numeric widening.
		{PortInteger, PortFloat, true, "an integer is a float"},
		{PortInteger, PortNumber, true, "an integer is a number"},
		{PortFloat, PortNumber, true, "a float is a number"},

		// The point of typed inputs: what they reject.
		{PortAny, PortString, false, "an unknown payload cannot satisfy a typed input"},
		{PortFloat, PortInteger, false, "a float is not an integer"},
		{PortString, PortInteger, false, "unrelated types"},
		{PortObject, PortString, false, "unrelated types"},
		{PortAny, PortBool, false, "an unknown payload cannot satisfy a typed input"},
		{PortBool, PortNumber, false, "a boolean is not numeric"},
		{PortNumber, PortAny, true, "anything satisfies an undeclared input"},
		{PortAny, PortInteger, false, "an undeclared payload cannot satisfy a typed input"},

		// Binary is base64 text, so it widens to string but not back.
		{PortBinary, PortString, true, "base64 text is a string"},
		{PortString, PortBinary, false, "arbitrary text is not encoded bytes"},
		{PortBinary, PortAny, true, "a declared payload satisfies an undeclared input"},
		{PortBinary, PortObject, false, "unrelated types"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, Compatible(c.src, c.tgt), "Compatible(%q, %q): %s", c.src, c.tgt, c.why)
	}
}

// TestMessageCompatible covers what the message path allows on top of plain
// assignability: an undeclared payload reaching a typed input. That is not a
// hole in the checks but what "any" means on a message edge — a webhook body or
// an MQTT payload is not knowable until the run happens.
func TestMessageCompatible(t *testing.T) {
	cases := []struct {
		src, tgt string
		want     bool
		why      string
	}{
		{PortAny, PortString, true, "a runtime-decided payload may feed a typed input"},
		{PortAny, PortObject, true, "a runtime-decided payload may feed a typed input"},
		{PortString, PortAny, true, "a declared payload satisfies an undeclared input"},
		{PortInteger, PortNumber, true, "widening still applies"},

		// A declared mismatch is still a mistake the author can see.
		{PortObject, PortString, false, "two declared types that disagree"},
		{PortString, PortBinary, false, "two declared types that disagree"},
	}
	for _, c := range cases {
		assert.Equalf(t, c.want, MessageCompatible(c.src, c.tgt), "MessageCompatible(%q, %q): %s", c.src, c.tgt, c.why)
	}
}

// TestValidateManualDerivedPortType covers the end of the chain: a manual
// trigger's configured payload type becomes the port type validation checks
// edges against.
func TestValidateManualDerivedPortType(t *testing.T) {
	reg := testRegistry()

	// A typed trigger still feeds a plain message input, which is what every
	// node accepts today. Typing a node must not invalidate its graph.
	g := &Graph{
		Nodes: []Node{
			node("m", "manual", `{"payload":"hi","output_format":"string"}`),
			node("d", "debug", ""),
		},
		Edges: []Edge{edge("e1", "m", "out", "d", "in")},
	}
	require.NoError(t, ValidateFull(g, reg, nil, false),
		"typing a trigger must not invalidate the graph it is already wired into")

	// A typed input rejects a payload type it does not accept. The gate node's
	// cond port takes a bool, and a string trigger cannot satisfy it.
	g = &Graph{
		Nodes: []Node{
			node("m", "manual", `{"payload":"hi","output_format":"string"}`),
			node("g", "gate", ""),
		},
		Edges: []Edge{edge("e1", "m", "out", "g", "cond")},
	}
	err := ValidateFull(g, reg, nil, false)
	require.Error(t, err, "a string output feeding a bool input is exactly what typed ports are for")

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr, "validation problems reach the editor as an AppError with per-node details")

	joined := strings.Join(valuesOf(appErr.Details), " ")
	assert.Contains(t, joined, "not compatible",
		"the problem must name the type mismatch, since that is what the editor highlights: %v", appErr.Details)

	// The same graph is fine through the any-typed port.
	g.Edges = []Edge{edge("e1", "m", "out", "g", "any_in")}
	assert.NoError(t, ValidateFull(g, reg, nil, false),
		"the same trigger through an undeclared input is fine: any accepts a declared payload")
}

// valuesOf flattens a problem detail map into its messages, which is what the
// assertions above search: the keys are node ids and vary with the fixture.
func valuesOf(details map[string]string) []string {
	out := make([]string, 0, len(details))
	for _, v := range details {
		out = append(out, v)
	}
	return out
}
