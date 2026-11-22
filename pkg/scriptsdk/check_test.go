package scriptsdk

import (
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckSourceAcceptsLiteralAndDynamicMessages pins the common authoring
// forms: exact and alias properties, the generic dispatcher, and {in: msg}.
// A dynamic message must not prevent config validation from running.
func TestCheckSourceAcceptsLiteralAndDynamicMessages(t *testing.T) {
	source := `
const a = nodes["base64-encode"]({property: "payload", encoding: "standard"}, {in: msg});
const b = nodes.base64Encode({property: "payload", encoding: "url", padding: false}, {in: msg});
const c = nodes.call("ha-get-state", {connection: "x", entity_id: "light.kitchen"});
return [a, b, c];
`
	assert.Empty(t, CheckSource(source, false, checkerMetas()), "valid canonical, alias, and generic calls must pass without executing a node")

	invalid := `nodes.base64Encode({property: "payload", encoding: "invalid"}, {in: msg});`
	diagnostics := CheckSource(invalid, false, checkerMetas())
	require.Len(t, diagnostics, 1, "a dynamic message must not hide an invalid literal select value")
	assert.Equal(t, "node-config-invalid", diagnostics[0].Code)
}

// TestCheckSourceReportsIndependentDiagnostics checks one source with several
// mistakes so authors receive a useful review rather than only the first error.
func TestCheckSourceReportsIndependentDiagnostics(t *testing.T) {
	source := `
nodes.noSuchNode({});
nodes.haGetState({connection: "x"});
nodes.haGetState({connection: "x", entity_id: "y", bogus: true});
nodes.base64Encode({property: "payload", encoding: 42}, {in: msg});
nodes.base64Encode({property: "payload", encoding: "bad"}, {in: msg});
`
	diagnostics := CheckSource(source, false, checkerMetas())
	require.Len(t, diagnostics, 5, "all five independent call mistakes should be reported")
	codes := make([]string, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		codes = append(codes, diagnostic.Code)
		assert.Equal(t, "error", diagnostic.Severity)
		assert.NotEmpty(t, diagnostic.Message)
		assert.GreaterOrEqual(t, diagnostic.From, 0)
		assert.GreaterOrEqual(t, diagnostic.To, diagnostic.From)
	}
	assert.Equal(t, []string{
		"node-function-unknown",
		"node-config-required",
		"node-config-unknown",
		"node-config-invalid",
		"node-config-invalid",
	}, codes)
}

// TestCheckSourceValidatesInvocationContext covers failures that depend on the
// call shape rather than only the config object.
func TestCheckSourceValidatesInvocationContext(t *testing.T) {
	tests := []struct {
		name   string
		source string
		pure   bool
		code   string
	}{
		{name: "wrong argument count", source: `nodes.haGetState()`, code: "node-arguments"},
		{name: "missing message input", source: `nodes.base64Encode({property: "payload"})`, code: "node-input-required"},
		{name: "unknown message input", source: `nodes.base64Encode({property: "payload"}, {in: msg, side: msg})`, code: "node-input-unknown"},
		{name: "impure value evaluation", source: `nodes.fail({message: "x"}, {in: msg})`, pure: true, code: "node-pure"},
		{name: "missing stable identity", source: `nodes.statefulExample({}, {in: msg})`, code: "node-identity-required"},
		{name: "unknown invocation option", source: `nodes.statefulExample({}, {in: msg}, {id: "x", bogus: true})`, code: "node-options-unknown"},
		{name: "unknown retry option", source: `nodes.base64Encode({property: "payload"}, {in: msg}, {retry: {attempts: 2}})`, code: "node-options-unknown"},
		{name: "ineligible type", source: `nodes.trigger({}, {in: msg})`, code: "node-function-ineligible"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diagnostics := CheckSource(tc.source, tc.pure, checkerMetas())
			require.Len(t, diagnostics, 1, "one invocation-context mistake should produce one diagnostic for %s", tc.name)
			assert.Equal(t, tc.code, diagnostics[0].Code, "unexpected diagnostic for %s", tc.name)
		})
	}
}

// TestCheckSourceLeavesDynamicCodeUnchecked ensures the verifier never executes
// or guesses expressions, and that a user-defined nodes binding is not mistaken
// for the sandbox global.
func TestCheckSourceLeavesDynamicCodeUnchecked(t *testing.T) {
	source := `
const config = buildConfig(payload);
nodes.haGetState(config);
nodes.call(typeName, {connection: "x", entity_id: "light.kitchen"});
`
	assert.Empty(t, CheckSource(source, false, checkerMetas()), "computed config and type names belong to runtime validation")

	shadowed := `function local(nodes) { return nodes.haGetState({}); }`
	assert.Empty(t, CheckSource(shadowed, false, checkerMetas()), "a shadowing function parameter is not the sandbox nodes global")
}

// TestCheckSourceMapsDiagnosticsToOriginalSource pins the wrapper offset at the
// object argument reported by goja: byte 24 and column 25 in this source.
func TestCheckSourceMapsDiagnosticsToOriginalSource(t *testing.T) {
	source := `return nodes.haGetState({ connection: "x" });`
	diagnostics := CheckSource(source, false, checkerMetas())
	require.Len(t, diagnostics, 1)
	assert.Equal(t, 24, diagnostics[0].From, "the diagnostic must point into the original source, not the goja wrapper")
	assert.Equal(t, 1, diagnostics[0].Line)
	assert.Equal(t, 25, diagnostics[0].Column)
}

// TestCheckSourceGenericCallRequiresExactType pins the runtime dispatcher
// contract: aliases belong to named properties, not nodes.call.
func TestCheckSourceGenericCallRequiresExactType(t *testing.T) {
	source := `nodes.call("base64Encode", {property: "payload"}, {in: msg});`
	diagnostics := CheckSource(source, false, checkerMetas())
	require.Len(t, diagnostics, 1, "a lower-camel alias must not resolve as an exact node type")
	assert.Equal(t, "node-function-unknown", diagnostics[0].Code)
	assert.Equal(t, "base64Encode", diagnostics[0].NodeType)
}

func checkerMetas() []flow.NodeMeta {
	return []flow.NodeMeta{
		{
			Type: "base64-encode", Label: "Base64 Encode", Pure: true, Retryable: true,
			Inputs:  []flow.PortSpec{{ID: "in", Type: flow.PortString}},
			Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortString}},
			ConfigSpec: []flow.FieldSpec{
				{ID: "property", Type: flow.FieldString, Default: "payload"},
				{ID: "encoding", Type: flow.FieldSelect, Default: "standard", Options: []flow.Option{{Value: "standard"}, {Value: "url"}}},
				{ID: "padding", Type: flow.FieldBoolean, Default: true},
			},
		},
		{
			Type: "ha-get-state", Label: "HA Get State", Pure: true,
			Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortObject}},
			ConfigSpec: []flow.FieldSpec{
				{ID: "connection", Type: flow.FieldConnection, Required: true},
				{ID: "entity_id", Type: flow.FieldEntity, Required: true},
			},
		},
		{
			Type: "fail", Label: "Fail", Retryable: true,
			Inputs:     []flow.PortSpec{{ID: "in", Type: flow.PortAny}},
			ConfigSpec: []flow.FieldSpec{{ID: "message", Type: flow.FieldString, Required: true}},
		},
		{
			Type: "stateful-example", InvocationState: flow.InvocationStateStateful,
			Inputs: []flow.PortSpec{{ID: "in", Type: flow.PortAny}},
		},
		{Type: "trigger", Category: flow.CategoryTrigger},
	}
}

// TestCheckSourceReadsNullAsOmittedArgument locks the checker to what the
// JavaScript runtime accepts: null in the inputs or options position decodes
// to nothing, exactly as undefined and as None in the Python checker, so a
// script that skips its inputs with null to reach options must still deploy.
// A null config stays an error, since the runtime refuses it too.
func TestCheckSourceReadsNullAsOmittedArgument(t *testing.T) {
	accepted := `
nodes.haGetState({connection: "x", entity_id: "light.kitchen"}, null, null);
nodes.call("ha-get-state", {connection: "x", entity_id: "light.kitchen"}, null, {id: "state"});
nodes.statefulExample({}, {in: msg}, null);
`
	diagnostics := CheckSource(accepted, false, checkerMetas())
	require.Len(t, diagnostics, 1, "null inputs and options are omitted arguments, so only the missing identity is reported")
	assert.Equal(t, "node-identity-required", diagnostics[0].Code,
		"null options still leave a stateful node without the id it needs")

	diagnostics = CheckSource(`nodes.haGetState(null)`, false, checkerMetas())
	require.Len(t, diagnostics, 1, "a null config is not an omitted argument")
	assert.Equal(t, "node-config", diagnostics[0].Code)
}
