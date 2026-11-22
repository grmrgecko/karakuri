package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paramMeta is a node type with one bindable config field, so a placed node can
// expose it as a value input port.
func paramMeta(nodeType string, dynamic bool) NodeMeta {
	return NodeMeta{
		Type:         nodeType,
		DynamicPorts: dynamic,
		Inputs:       []PortSpec{{ID: "in", Label: "In", Type: PortAny}},
		Outputs:      []PortSpec{{ID: "out", Label: "Out", Type: PortAny}},
		ConfigSpec:   []FieldSpec{{ID: "property", Label: "Property", Type: FieldString}},
	}
}

// TestNodePortsAppendsParamsForEveryType is the reason resolution lives in one
// function. Parameter ports used to be appended before the per-type derivation
// ran, so the types that replace their ports silently lost them: a switch node
// with a wired config field validated on the server but drew no socket.
func TestNodePortsAppendsParamsForEveryType(t *testing.T) {
	cases := []struct {
		name    string
		typ     string
		dynamic bool
		config  string
	}{
		{name: "static", typ: "debug"},
		{name: "manual", typ: NodeTypeManual, dynamic: true, config: `{"output_format":"string"}`},
		{name: "switch", typ: NodeTypeSwitch, dynamic: true,
			config: `{"rules":[{"id":"r1","operator":"eq"}]}`},
		{name: "script", typ: NodeTypeScript, dynamic: true,
			config: `{"inputs":[{"id":"celsius","type":"float"}]}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n := &Node{
				ID: "n1", Type: c.typ, Params: []string{"property"},
				Config: json.RawMessage(c.config),
			}
			set := NodePorts(paramMeta(c.typ, c.dynamic), n, nil)
			require.True(t, set.Known, "a placed node of a known type must report its ports")

			_, ok := set.Input(ParamPortID("property"))
			assert.Truef(t, ok, "the bound config field must draw a socket, whatever else the type derives: %+v", set.Inputs)
		})
	}
}

// TestNodePortsDoesNotMutateTypeMetadata guards the shared slice: static ports
// belong to the node type and are handed to every placed node of that type, so
// appending a parameter port in place would leak it onto unrelated nodes.
func TestNodePortsDoesNotMutateTypeMetadata(t *testing.T) {
	meta := paramMeta("debug", false)
	before := len(meta.Inputs)

	NodePorts(meta, &Node{ID: "a", Type: "debug", Params: []string{"property"}}, nil)
	plain := NodePorts(meta, &Node{ID: "b", Type: "debug"}, nil)

	assert.Len(t, meta.Inputs, before, "the type's own port list is shared by every placed node and must not grow")
	assert.Len(t, plain.Inputs, before, "a node that bound nothing must not inherit another node's parameter socket")
}

// TestNodePortsHonorsNoParam keeps configuration-only editors from growing a
// socket when the node receives the same data on its message input.
func TestNodePortsHonorsNoParam(t *testing.T) {
	meta := paramMeta("configured-history", false)
	meta.ConfigSpec[0].NoParam = true

	set := NodePorts(meta, &Node{ID: "n1", Type: meta.Type, Params: []string{"property"}}, nil)

	_, ok := set.Input(ParamPortID("property"))
	assert.False(t, ok, "a no-param field must stay editable without drawing a value socket")
}

// TestPrimaryInputSkipsControlPorts covers the port a node reads when it wants
// "the message": reset, return, and parameter sockets have other roles.
func TestPrimaryInputSkipsControlPorts(t *testing.T) {
	meta := NodeMeta{
		Type: "trigger-node",
		Inputs: []PortSpec{
			{ID: "reset", Label: "Reset", Type: PortAny, Optional: true},
			{ID: "response", Label: "Response", Type: PortAny, Kind: PortKindReturn},
			{ID: "in", Label: "In", Type: PortAny},
		},
		ConfigSpec: []FieldSpec{{ID: "property", Label: "Property", Type: FieldString}},
	}
	set := NodePorts(meta, &Node{ID: "n1", Type: "trigger-node", Params: []string{"property"}}, nil)

	port, ok := set.PrimaryInput()
	require.True(t, ok, "a node with a message input must report one")
	assert.Equal(t, "in", port.ID, "the reset input is optional and the parameter is config, so neither carries the message")
}

// TestWebhookReturnPortOnlyExistsInSynchronousMode keeps the editor and engine
// from accepting a reply edge for a request that was already answered.
func TestWebhookReturnPortOnlyExistsInSynchronousMode(t *testing.T) {
	meta := NodeMeta{Type: NodeTypeWebhook, Inputs: []PortSpec{{ID: "response", Kind: PortKindReturn}}}

	async := NodePorts(meta, &Node{ID: "w1", Type: NodeTypeWebhook, Config: json.RawMessage(`{"response_mode":"async"}`)}, nil)
	_, ok := async.Input("response")
	assert.False(t, ok, "an asynchronous webhook has no waiting request to return to")

	sync := NodePorts(meta, &Node{ID: "w2", Type: NodeTypeWebhook, Config: json.RawMessage(`{"response_mode":"sync"}`)}, nil)
	port, ok := sync.Input("response")
	require.True(t, ok, "a synchronous webhook must expose its return socket")
	assert.Equal(t, PortKindReturn, port.Kind)
}

// TestErrorOutputRequiresPortKind states who owns a port named "error": the
// failure branch is the port declared as one, so a node that named a normal
// output "error" keeps it and has no failure branch at all.
func TestErrorOutputRequiresPortKind(t *testing.T) {
	declared := NodeMeta{Type: "a", Outputs: []PortSpec{
		{ID: "out", Type: PortAny},
		{ID: "error", Type: PortAny, Kind: PortKindError},
	}}
	port, ok := NodePorts(declared, &Node{ID: "n", Type: "a"}, nil).ErrorOutput()
	require.True(t, ok, "a port declared with PortKindError is the failure branch")
	assert.Equal(t, "error", port.ID, "the declared failure branch is the one the engine routes a failure to")

	named := NodeMeta{Type: "b", Outputs: []PortSpec{{ID: "error", Type: PortAny}}}
	_, ok = NodePorts(named, &Node{ID: "n", Type: "b"}, nil).ErrorOutput()
	assert.False(t, ok, "a port merely named \"error\" is an ordinary output, so this node has no failure branch at all")
}

// TestNodePortsUnresolvedSubflow covers the one type this package cannot finish
// on its own: without the referenced graph there are no ports to report, and
// the caller suppresses per-port checks rather than inventing them.
func TestNodePortsUnresolvedSubflow(t *testing.T) {
	set := NodePorts(NodeMeta{Type: NodeTypeSubflow, DynamicPorts: true},
		&Node{ID: "n1", Type: NodeTypeSubflow}, nil)

	assert.False(t, set.Known, "without the referenced graph there is no way to know the ports")
	assert.Empty(t, set.Inputs, "unknown must mean empty, so a caller cannot mistake invented ports for real ones")
	assert.Empty(t, set.Outputs, "unknown must mean empty, so a caller cannot mistake invented ports for real ones")
}

// TestParamPortShowIf pins the conditional-socket contract the editor mirrors
// (lib/utils/payloadPorts.ts): a port field with a ShowIf produces its port
// only while the condition holds, "*" means any non-empty value, and against
// a tokens-control value one matching token is enough. The guardrail's model
// input depends on all three rules.
func TestParamPortShowIf(t *testing.T) {
	meta := NodeMeta{
		Type: "gated",
		ConfigSpec: []FieldSpec{
			{ID: "checks", Type: FieldString},
			{ID: "model", Type: FieldJSON, Port: "ai-model",
				ShowIf: &ShowIf{Field: "checks", Equals: "*"}},
			{ID: "topic", Type: FieldText,
				ShowIf: &ShowIf{Field: "checks", Equals: "topic"}},
		},
	}
	cases := []struct {
		name      string
		config    string
		wantModel bool
		wantTopic bool
	}{
		{name: "unset hides the port", config: `{}`},
		{name: "any token satisfies the wildcard", config: `{"checks":"jailbreak"}`, wantModel: true},
		{name: "one token of a list matches its own field", config: `{"checks":"jailbreak, topic"}`,
			wantModel: true, wantTopic: true},
	}
	for _, tc := range cases {
		n := &Node{ID: "n", Type: "gated", Config: json.RawMessage(tc.config)}
		set := NodePorts(meta, n, nil)
		_, hasModel := set.Input(ParamPortID("model"))
		assert.Equalf(t, tc.wantModel, hasModel, "model port presence: %s", tc.name)
		values := map[string]any{}
		require.NoError(t, json.Unmarshal([]byte(tc.config), &values))
		topicField := meta.ConfigSpec[2]
		assert.Equalf(t, tc.wantTopic, FieldShown(meta.ConfigSpec, topicField, values),
			"topic field visibility: %s", tc.name)
	}
}

// TestClassifyPortsDerivation pins the classifier's category-port contract
// the editor mirrors (payloadPorts.ts classifyPorts): one port per token in
// written order, IDs lowercased under the cat: prefix so a display-case
// rename keeps its wiring, case-insensitive duplicates collapsed, and the
// static outputs (other, error) kept after the derived routes.
func TestClassifyPortsDerivation(t *testing.T) {
	meta := NodeMeta{
		Type:         NodeTypeAIClassify,
		DynamicPorts: true,
		Inputs:       []PortSpec{{ID: "in", Type: PortAny}},
		Outputs:      []PortSpec{{ID: "other", Label: "Other", Type: PortAny, TypeFrom: "in"}, ErrorPort()},
	}
	n := &Node{ID: "c", Type: NodeTypeAIClassify,
		Config: json.RawMessage(`{"categories": "Urgent, Newsletter, urgent"}`)}
	set := NodePorts(meta, n, nil)

	require.Len(t, set.Outputs, 4, "two categories plus other plus error")
	assert.Equal(t, "cat:urgent", set.Outputs[0].ID, "the port ID is the lowercased category")
	assert.Equal(t, "Urgent", set.Outputs[0].Label, "the label keeps the written case")
	assert.Equal(t, "in", set.Outputs[0].TypeFrom, "routing carries whatever arrived")
	assert.Equal(t, "cat:newsletter", set.Outputs[1].ID)
	assert.Equal(t, "other", set.Outputs[2].ID, "the fallback follows the derived routes")
	assert.Equal(t, PortKindError, set.Outputs[3].Kind)
}
