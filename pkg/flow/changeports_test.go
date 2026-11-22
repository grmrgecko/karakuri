package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChangePorts covers the change node's derived output type: what its rules
// leave in the payload is what the next node is told to expect. The rule is
// mirrored in the editor (lib/utils/changeRules.ts), so the cases here are
// the shared specification of it — and the path check is what keeps the type
// honest, since a rule writing to topic or meta leaves the payload alone and
// must not claim otherwise. A rule that rewrites the payload from an
// expression must not inherit the input's type either: an object source
// rewritten to payload.temp used to reach a number input still typed as an
// object, and strict validation rejected the edge.
func TestChangePorts(t *testing.T) {
	cases := []struct {
		name   string
		config string
		want   string
		// carried is the output type once an object source feeds the node.
		// Only a node whose rules leave the payload alone may inherit it.
		carried string
	}{
		{
			name:    "no rules leaves the payload as it arrived",
			config:  `{"rules":[]}`,
			want:    PortAny,
			carried: PortObject,
		},
		{
			name:    "setting the payload to a string",
			config:  `{"rules":[{"op":"set","target":"payload","from":"string","value":"on"}]}`,
			want:    PortString,
			carried: PortString,
		},
		{
			name:    "a rule saved without an op is a set",
			config:  `{"rules":[{"target":"payload","from":"number","value":1}]}`,
			want:    PortNumber,
			carried: PortNumber,
		},
		{
			name:    "a rule saved without a value source writes a string",
			config:  `{"rules":[{"op":"set","target":"payload"}]}`,
			want:    PortString,
			carried: PortString,
		},
		{
			name:    "an empty target is the payload",
			config:  `{"rules":[{"op":"set","target":"","from":"boolean","value":true}]}`,
			want:    PortBool,
			carried: PortBool,
		},
		{
			name:    "setting the payload from JSON",
			config:  `{"rules":[{"op":"set","target":"payload","from":"json","value":{}}]}`,
			want:    PortObject,
			carried: PortObject,
		},
		{
			// The value comes from somewhere the config cannot name a type for.
			name:    "an expression undeclares the payload",
			config:  `{"rules":[{"op":"set","target":"payload","from":"expression","value":"payload.x"}]}`,
			want:    PortAny,
			carried: PortAny,
		},
		{
			name:    "deleting the payload undeclares it",
			config:  `{"rules":[{"op":"set","target":"payload","from":"string"},{"op":"delete","target":"payload"}]}`,
			want:    PortAny,
			carried: PortAny,
		},
		{
			name:    "writing a property makes the payload an object",
			config:  `{"rules":[{"op":"set","target":"payload.state","from":"string","value":"on"}]}`,
			want:    PortObject,
			carried: PortObject,
		},
		{
			name:    "a bare target path is read from the payload",
			config:  `{"rules":[{"op":"set","target":"state","from":"string","value":"on"}]}`,
			want:    PortObject,
			carried: PortObject,
		},
		{
			name:    "writing the topic leaves the payload alone",
			config:  `{"rules":[{"op":"set","target":"topic","from":"string","value":"kitchen"}]}`,
			want:    PortAny,
			carried: PortObject,
		},
		{
			name:    "writing metadata leaves the payload alone",
			config:  `{"rules":[{"op":"set","target":"meta.seen","from":"boolean","value":true}]}`,
			want:    PortAny,
			carried: PortObject,
		},
		{
			// Rules apply in order, so the last one to touch the payload is the
			// one that says what comes out.
			name: "the last rule to touch the payload wins",
			config: `{"rules":[
				{"op":"set","target":"payload","from":"json","value":{}},
				{"op":"set","target":"topic","from":"string","value":"kitchen"},
				{"op":"set","target":"payload","from":"string","value":"on"}
			]}`,
			want:    PortString,
			carried: PortString,
		},
		{
			name:    "an unreadable config declares nothing",
			config:  `not json`,
			want:    PortAny,
			carried: PortObject,
		},
	}

	for _, c := range cases {
		ports := ChangePorts(json.RawMessage(c.config))
		require.Lenf(t, ports, 1, "the change node has one output, whatever its rules say (%s)", c.name)
		require.Equalf(t, "out", ports[0].ID, "the output keeps its id across every rule shape (%s)", c.name)
		assert.Equalf(t, c.want, ports[0].Type, "output type for %q", c.name)

		g := &Graph{Edges: []Edge{link("src", "out", "chg", "in")}}
		set := map[string]PortSet{
			"src": source(PortObject),
			"chg": {Known: true, Inputs: []PortSpec{{ID: "in", Type: PortAny}}, Outputs: ports},
		}
		PropagateTypes(g, set)
		assert.Equalf(t, c.carried, outType(t, set, "chg", "out"), "output type behind an object source for %q", c.name)
	}
}
