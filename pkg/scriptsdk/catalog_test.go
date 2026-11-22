package scriptsdk_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewCatalogDeterministicWireContract locks the public function names,
// ordering, identity flags, and revision encoding. The digest is calculated
// independently here so registry iteration order cannot silently change the
// editor's cache key or make aliases point at a different node type.
func TestNewCatalogDeterministicWireContract(t *testing.T) {
	metas := []flow.NodeMeta{
		{Type: "ha-get-state", InvocationState: flow.InvocationStateUnknown},
		{Type: "foo_bar"},
		{Type: "dynamic", DynamicPorts: true, Inputs: []flow.PortSpec{{ID: "in", Type: flow.PortAny}}},
		{Type: "base64-encode", Pure: true, ConfigSpec: []flow.FieldSpec{{ID: "property", Type: flow.FieldString, Required: true}}},
		{Type: "foo-bar"},
	}
	catalog := scriptsdk.NewCatalog(metas)
	require.Len(t, catalog.Functions, 5, "all ordinary types, including dynamic-port types, belong in the derived catalog")
	assert.Equal(t, []string{"base64-encode", "dynamic", "foo-bar", "foo_bar", "ha-get-state"}, []string{
		catalog.Functions[0].Name, catalog.Functions[1].Name, catalog.Functions[2].Name,
		catalog.Functions[3].Name, catalog.Functions[4].Name,
	}, "functions must be sorted by their exact node type")
	assert.Equal(t, []string{"base64Encode"}, catalog.Functions[0].Aliases,
		"the ergonomic alias is lower camel case while the canonical name remains base64-encode")
	assert.Empty(t, catalog.Functions[2].Aliases, "foo-bar and foo_bar both derive fooBar, so neither may claim it")
	assert.Empty(t, catalog.Functions[3].Aliases, "an ambiguous alias is omitted rather than reassigned")
	assert.Equal(t, []string{"haGetState"}, catalog.Functions[4].Aliases)
	assert.False(t, catalog.Functions[0].IdentityRequired, "the built-in zero value is the documented audited-stateless default")
	assert.Equal(t, flow.InvocationStateStateless, catalog.Functions[0].Meta.InvocationState,
		"the function catalog makes the zero-value state explicit on the wire")
	assert.True(t, catalog.Functions[4].IdentityRequired, "unknown store use requires a stable invocation ID")

	ordered := struct {
		SchemaVersion int                  `json:"schema_version"`
		Functions     []scriptsdk.Function `json:"functions"`
	}{SchemaVersion: scriptsdk.SchemaVersion, Functions: catalog.Functions}
	raw, err := json.Marshal(ordered)
	require.NoError(t, err, "public catalog metadata must remain JSON serializable")
	digest := sha256.Sum256(raw)
	assert.Equal(t, hex.EncodeToString(digest[:]), catalog.Revision,
		"revision is the SHA-256 of schema-versioned ordered contract JSON")

	reversed := make([]flow.NodeMeta, len(metas))
	for i := range metas {
		reversed[len(metas)-1-i] = metas[i]
	}
	assert.Equal(t, catalog, scriptsdk.NewCatalog(reversed),
		"registry input order must not change either the catalog or its revision")
	metas[3].Description = "changed metadata"
	assert.NotEqual(t, catalog.Revision, scriptsdk.NewCatalog(metas).Revision,
		"an editor-visible metadata change must invalidate a cached declaration revision")
}

// TestFunctionsEligibility pins the graph constructs and opaque capability
// boundary that cannot be represented by one synchronous JSON/base64 call.
// Dynamic ports alone are not a reason to reject an otherwise ordinary node.
func TestFunctionsEligibility(t *testing.T) {
	base := func() flow.NodeMeta {
		return flow.NodeMeta{
			Type:    "ordinary",
			Inputs:  []flow.PortSpec{{ID: "in", Type: flow.PortAny}},
			Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortAny}},
		}
	}
	tests := []struct {
		name string
		edit func(*flow.NodeMeta)
		want bool
	}{
		{name: "ordinary node", want: true},
		{name: "dynamic ports remain eligible", edit: func(m *flow.NodeMeta) { m.DynamicPorts = true }, want: true},
		{name: "trigger category", edit: func(m *flow.NodeMeta) { m.Category = flow.CategoryTrigger }},
		{name: "trigger flag outside category", edit: func(m *flow.NodeMeta) { m.Trigger = true }},
		{name: "annotation", edit: func(m *flow.NodeMeta) { m.Category = flow.CategoryAnnotation }},
		{name: "subflow boundary", edit: func(m *flow.NodeMeta) { m.Category = flow.CategoryPort }},
		{name: "link routing", edit: func(m *flow.NodeMeta) { m.Type = flow.NodeTypeLinkSet }},
		{name: "subflow routing", edit: func(m *flow.NodeMeta) { m.Type = flow.NodeTypeSubflow }},
		{name: "tool routing", edit: func(m *flow.NodeMeta) { m.Type = flow.NodeTypeTool }},
		{name: "nested script", edit: func(m *flow.NodeMeta) { m.Type = flow.NodeTypeScript }},
		{name: "join fan in", edit: func(m *flow.NodeMeta) { m.Type = "join" }},
		{name: "batch fan in", edit: func(m *flow.NodeMeta) { m.Type = "batch" }},
		{name: "aggregate fan in", edit: func(m *flow.NodeMeta) { m.Type = "aggregate" }},
		{name: "opaque input", edit: func(m *flow.NodeMeta) { m.Inputs[0].Type = "model-handle" }},
		{name: "opaque output", edit: func(m *flow.NodeMeta) { m.Outputs[0].Type = "model-handle" }},
		{name: "opaque parameter", edit: func(m *flow.NodeMeta) {
			m.ConfigSpec = []flow.FieldSpec{{ID: "model", Type: flow.FieldJSON, Port: "model-handle"}}
		}},
		{name: "config-derived opaque output", edit: func(m *flow.NodeMeta) {
			m.Outputs[0].TypeRule = &flow.PortTypeRule{Cases: []flow.PortTypeCase{{Type: "model-handle"}}}
		}},
	}
	for _, tc := range tests {
		meta := base()
		if tc.edit != nil {
			tc.edit(&meta)
		}
		assert.Equalf(t, tc.want, scriptsdk.Eligible(meta), "eligibility mismatch for %s", tc.name)
	}
}
