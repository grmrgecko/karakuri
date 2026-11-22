package sandbox

import (
	"context"
	"strings"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckPythonSourceValidatesWithoutExecutingUserCode runs the real CPython
// parser while a trailing exception proves the submitted body stays unexecuted.
func TestCheckPythonSourceValidatesWithoutExecutingUserCode(t *testing.T) {
	Register(sharedPython(t))
	source := `
canonical = nodes["base64-encode"]({"property": "payload"}, {"in": msg})
generic = nodes.call("base64-encode", {"property": "payload"}, {"in": msg})
invalid = nodes.base64Encode({"property": "payload", "encoding": "bad"}, {"in": msg})
raise RuntimeError("the verifier executed user source")
`
	diagnostics, err := CheckPythonSource(context.Background(), source, false, pythonCheckerMetas())
	require.NoError(t, err, "CPython must parse rather than execute the submitted source")
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "node-config-invalid", diagnostics[0].Code)
	assert.Equal(t, "base64-encode", diagnostics[0].NodeType)
}

// TestCheckPythonSourceMapsOriginalBytePositions pins CPython's UTF-8 AST
// columns after a multibyte character and keeps syntax errors as diagnostics.
func TestCheckPythonSourceMapsOriginalBytePositions(t *testing.T) {
	Register(sharedPython(t))
	config := `{"connection": "x"}`
	source := `snowman = "☃"; nodes.haGetState(` + config + `)`
	diagnostics, err := CheckPythonSource(context.Background(), source, false, pythonCheckerMetas())
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	from := strings.Index(source, config)
	assert.Equal(t, from, diagnostics[0].From)
	assert.Equal(t, from+1, diagnostics[0].Column, "AST columns count UTF-8 bytes, not runes")
	assert.Equal(t, "node-config-required", diagnostics[0].Code)

	syntax, err := CheckPythonSource(context.Background(), "nodes.haGetState(", false, pythonCheckerMetas())
	require.NoError(t, err, "CPython syntax errors are authoring findings, not runtime failures")
	require.Len(t, syntax, 1)
	assert.Equal(t, "script-syntax", syntax[0].Code)
	assert.Equal(t, 1, syntax[0].Line)
}

// TestCheckPythonSourceRecognizesModulePrefix keeps karakuri.nodes under the
// same strict checks as the script-level global.
func TestCheckPythonSourceRecognizesModulePrefix(t *testing.T) {
	Register(sharedPython(t))
	diagnostics, err := CheckPythonSource(context.Background(), `karakuri.nodes.haGetState({})`, false, pythonCheckerMetas())
	require.NoError(t, err)
	require.Len(t, diagnostics, 1)
	assert.Equal(t, "node-config-required", diagnostics[0].Code)
}

// TestCheckPythonSourceLeavesComputedAndShadowedCallsForRuntime checks that
// keywords, starred arguments, and local nodes bindings never become guessed
// static invocations.
func TestCheckPythonSourceLeavesComputedAndShadowedCallsForRuntime(t *testing.T) {
	Register(sharedPython(t))
	source := `
nodes.haGetState(build_config())
nodes.call(NODE_TYPE, {})
nodes.haGetState(*args)
nodes.haGetState(config={})
`
	diagnostics, err := CheckPythonSource(context.Background(), source, false, pythonCheckerMetas())
	require.NoError(t, err)
	assert.Empty(t, diagnostics)

	shadowed := `def local(nodes):
    return nodes.haGetState()
`
	diagnostics, err = CheckPythonSource(context.Background(), shadowed, false, pythonCheckerMetas())
	require.NoError(t, err)
	assert.Empty(t, diagnostics, "a function argument shadows the sandbox nodes global")
}

func pythonCheckerMetas() []flow.NodeMeta {
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
	}
}
