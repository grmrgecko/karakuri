package scriptverify

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckSourceDefaultsToJavaScript pins the empty script-node language and
// rejects unsupported dispatch separately from ordinary authoring diagnostics.
func TestCheckSourceDefaultsToJavaScript(t *testing.T) {
	diagnostics, err := CheckSource(context.Background(), "", `nodes.haGetState();`, false, verificationMetas())
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "node-arguments", diagnostics[0].Code)

	_, err = CheckSource(context.Background(), "ruby", "puts 1", false, verificationMetas())
	assert.True(t, errors.Is(err, ErrUnsupportedLanguage), "unsupported languages need an operational dispatch error")
}

// TestCheckSourceGenericCallsUseExactTypes applies the runtime's generic-call
// contract across every language: lower-camel aliases are named-call only.
func TestCheckSourceGenericCallsUseExactTypes(t *testing.T) {
	tests := []struct {
		language string
		source   string
	}{
		{language: "javascript", source: `nodes.call("base64Encode", {property: "payload"}, {in: msg});`},
		{language: "python", source: `nodes.call("base64Encode", {"property": "payload"}, {"in": msg})`},
		{language: "bash", source: `node call base64Encode '{"property":"payload"}' '{"in":{"payload":"x"}}'`},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			if tc.language == sandbox.LangPython {
				requireVerificationPython(t)
			}
			diagnostics, err := CheckSource(context.Background(), tc.language, tc.source, false, verificationMetas())
			require.NoError(t, err)
			require.Len(t, diagnostics, 1, "an alias must not resolve through the exact-type generic dispatcher")
			assert.Equal(t, "node-function-unknown", diagnostics[0].Code)
			assert.Equal(t, "base64Encode", diagnostics[0].NodeType)
		})
	}
}

// TestCheckGraphUsesValueEdgesAndLocalSubgraphs checks graph-only purity and
// location context for all three languages without running any script body.
func TestCheckGraphUsesValueEdgesAndLocalSubgraphs(t *testing.T) {
	requireVerificationPython(t)
	config := func(language, source string) json.RawMessage {
		raw, err := json.Marshal(map[string]any{"language": language, "script": source})
		require.NoError(t, err)
		return raw
	}
	graph := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "javascript", Type: flow.NodeTypeScript, Config: config("javascript", `nodes.fail({message: "x"}, {in: msg});`)},
			{ID: "python", Type: flow.NodeTypeScript, Config: config("python", `nodes["fail"]({"message": "x"}, {"in": msg})`)},
			{ID: "bash", Type: flow.NodeTypeScript, Config: config("bash", `node fail '{"message":"x"}' '{"in":{"payload":null}}'`)},
			{ID: "ruby", Type: flow.NodeTypeScript, Config: config("ruby", `nodes.fail({});`)},
			{ID: "consumer", Type: "template", Config: json.RawMessage(`{"template":"x"}`)},
		},
		Edges: []flow.Edge{
			{ID: "js-value", Source: "javascript", SourceHandle: "out", Target: "consumer", TargetHandle: "param:template", Kind: flow.EdgeValue},
			{ID: "py-value", Source: "python", SourceHandle: "out", Target: "consumer", TargetHandle: "param:template", Kind: flow.EdgeValue},
			{ID: "sh-value", Source: "bash", SourceHandle: "out", Target: "consumer", TargetHandle: "param:template", Kind: flow.EdgeValue},
		},
		Subgraphs: map[string]flow.Graph{
			"local": {Nodes: []flow.Node{{ID: "nested", Type: flow.NodeTypeScript, Config: config("bash", `node haGetState`)}}},
		},
	}
	findings, err := CheckGraph(context.Background(), graph, verificationMetas())
	require.NoError(t, err)
	for _, id := range []string{"javascript", "python", "bash"} {
		require.Contains(t, findings, "node:"+id)
		require.Len(t, findings["node:"+id], 1)
		assert.Equal(t, "node-pure", findings["node:"+id][0].Code, "a value producer must reject impure calls in %s", id)
	}
	assert.NotContains(t, findings, "node:ruby", "unsupported graph languages retain their existing validation path")
	require.Contains(t, findings, "subgraph:local/node:nested")
	assert.Equal(t, "node-arguments", findings["subgraph:local/node:nested"][0].Code)
}

func requireVerificationPython(t *testing.T) {
	t.Helper()
	if runtime, ok := sandbox.Get(sandbox.LangPython); ok && runtime.Available() == nil {
		return
	}
	path := os.Getenv("KARAKURI_TEST_PYTHON_WASM")
	if path == "" {
		path = sandbox.ResolvePythonWasm("", []string{"../../build/runtimes"})
	}
	if path == "" {
		t.Skip("python.wasm not installed; set KARAKURI_TEST_PYTHON_WASM or run make runtimes")
	}
	sandbox.Register(sandbox.NewPythonRuntime(path, "", 0))
}

func verificationMetas() []flow.NodeMeta {
	return []flow.NodeMeta{
		{
			Type: "base64-encode", Label: "Base64 Encode", Pure: true,
			Inputs:  []flow.PortSpec{{ID: "in", Type: flow.PortString}},
			Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortString}},
			ConfigSpec: []flow.FieldSpec{
				{ID: "property", Type: flow.FieldString, Default: "payload"},
				{ID: "encoding", Type: flow.FieldSelect, Default: "standard", Options: []flow.Option{{Value: "standard"}, {Value: "url"}}},
			},
		},
		{
			Type: "ha-get-state", Label: "HA Get State", Pure: true,
			ConfigSpec: []flow.FieldSpec{
				{ID: "connection", Type: flow.FieldConnection, Required: true},
				{ID: "entity_id", Type: flow.FieldEntity, Required: true},
			},
		},
		{
			Type: "fail", Label: "Fail",
			Inputs:     []flow.PortSpec{{ID: "in", Type: flow.PortAny}},
			ConfigSpec: []flow.FieldSpec{{ID: "message", Type: flow.FieldString, Required: true}},
		},
	}
}

// TestCheckGraphFollowsWiringToValueProducer locks that a script pulled for a
// value through wiring is checked as a pure producer. Flattening splices a
// reroute and a set/get pair out, leaving a value edge from the script, so the
// run evaluates it purely. Only a direct value edge was marked, and deploy
// accepted a script whose side-effecting call then failed at run time. A
// script that sends messages through the same wiring stays unrestricted.
func TestCheckGraphFollowsWiringToValueProducer(t *testing.T) {
	script, err := json.Marshal(map[string]any{"language": "javascript", "script": `nodes.fail({message: "x"}, {in: msg});`})
	require.NoError(t, err)
	js := flow.Node{ID: "js", Type: flow.NodeTypeScript, Config: script}
	consumer := flow.Node{ID: "consumer", Type: "template", Config: json.RawMessage(`{"template":"x"}`), Params: []string{"template"}}
	link := func(id, nodeType string) flow.Node {
		return flow.Node{ID: id, Type: nodeType, Config: json.RawMessage(`{"name":"bus"}`)}
	}
	message := func(src, tgt string) flow.Edge {
		return flow.Edge{ID: src + "-" + tgt, Source: src, SourceHandle: "out", Target: tgt, TargetHandle: "in"}
	}
	value := func(src string) flow.Edge {
		return flow.Edge{ID: src + "-value", Source: src, SourceHandle: "out", Target: "consumer", TargetHandle: "param:template", Kind: flow.EdgeValue}
	}
	cases := []struct {
		name  string
		nodes []flow.Node
		edges []flow.Edge
		pure  bool
	}{
		{
			name:  "pulled through a reroute",
			nodes: []flow.Node{js, {ID: "r", Type: flow.NodeTypeReroute}, consumer},
			edges: []flow.Edge{message("js", "r"), value("r")},
			pure:  true,
		},
		{
			name:  "pulled through a set/get pair",
			nodes: []flow.Node{js, link("set", flow.NodeTypeLinkSet), link("get", flow.NodeTypeLinkGet), consumer},
			edges: []flow.Edge{message("js", "set"), value("get")},
			pure:  true,
		},
		{
			name:  "sending messages through a reroute",
			nodes: []flow.Node{js, {ID: "r", Type: flow.NodeTypeReroute}, consumer},
			edges: []flow.Edge{message("js", "r"), message("r", "consumer")},
		},
	}
	for _, tc := range cases {
		findings, err := CheckGraph(context.Background(), &flow.Graph{Nodes: tc.nodes, Edges: tc.edges}, verificationMetas())
		require.NoError(t, err, "%s: the graph must check", tc.name)
		if !tc.pure {
			assert.NotContains(t, findings, "node:js", "%s: a script on the message path may call nodes with side effects", tc.name)
			continue
		}
		if assert.Contains(t, findings, "node:js", "%s: the script is a value producer", tc.name) {
			assert.Equal(t, "node-pure", findings["node:js"][0].Code, "%s: the side-effecting call is what gets reported", tc.name)
		}
	}
}
