package scriptsdk

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckBashSourceDecodesQuotedJSONInCommandSubstitutions pins the shell
// shapes authors actually use: single-quoted JSON and captured node output.
func TestCheckBashSourceDecodesQuotedJSONInCommandSubstitutions(t *testing.T) {
	source := `
valid=$(node base64Encode '{"property":"payload","encoding":"standard"}' '{"in":{"payload":"ok"}}')
invalid=$(node call base64-encode '{"property":"payload","encoding":"bad"}' '{"in":{"payload":"ok"}}')
`
	diagnostics := CheckBashSource(source, false, checkerMetas())
	require.Len(t, diagnostics, 1, "the valid command must not hide the invalid generic call")
	assert.Equal(t, "node-config-invalid", diagnostics[0].Code)
	assert.Equal(t, "base64-encode", diagnostics[0].NodeType)
	assert.Equal(t, strings.Index(source, `'{"property":"payload","encoding":"bad"}'`), diagnostics[0].From)
}

// TestCheckBashSourceLeavesDynamicShellBehaviorForRuntime covers expansions,
// argument splitting, wrapper builtins, and function shadowing without treating
// any of them as proof that a call is valid.
func TestCheckBashSourceLeavesDynamicShellBehaviorForRuntime(t *testing.T) {
	tests := []struct {
		name   string
		source string
	}{
		{name: "computed generic type", source: `node call "$NODE_TYPE" '{}'`},
		{name: "unquoted expansion can change argument count", source: `node base64Encode '{}' $ARGUMENTS`},
		{name: "quoted dynamic inputs retain config checks", source: `node base64Encode '{}' "$INPUTS"`},
		{name: "shell function shadows builtin", source: `node() { node noSuch '{}'; }`},
		{name: "command wrapper remains dynamic", source: `command node noSuch '{}'`},
		{name: "exec wrapper remains dynamic", source: `exec node noSuch '{}'`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Empty(t, CheckBashSource(tc.source, false, checkerMetas()), "dynamic shell behavior must not produce fabricated diagnostics for %s", tc.name)
		})
	}
}

// TestCheckBashSourceUsesBytePositions checks mvdan's byte columns against a
// UTF-8 prefix and keeps parser failures in the ordinary diagnostic channel.
func TestCheckBashSourceUsesBytePositions(t *testing.T) {
	config := `'{"property":"payload","encoding":"bad"}'`
	source := `log "☃"; node base64Encode ` + config + ` '{"in":{"payload":"ok"}}'`
	diagnostics := CheckBashSource(source, false, checkerMetas())
	require.Len(t, diagnostics, 1)
	from := strings.Index(source, config)
	assert.Equal(t, from, diagnostics[0].From, "offsets must address the original UTF-8 bytes")
	assert.Equal(t, from+1, diagnostics[0].Column, "mvdan columns and API columns are both one-based byte positions")
	assert.Equal(t, from+len(config), diagnostics[0].To)

	syntax := CheckBashSource(`node base64Encode '`, false, checkerMetas())
	require.Len(t, syntax, 1, "an unterminated shell quote is an authoring diagnostic")
	assert.Equal(t, "script-syntax", syntax[0].Code)
	assert.Equal(t, 1, syntax[0].Line)
	assert.Greater(t, syntax[0].Column, 1)
}
