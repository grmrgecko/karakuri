package flowops

import (
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestApplyFaceplateValuesKeepsRestOfConfig locks that applying a value
// rewrites only the faceplate's stored values. The config was decoded with
// numbers as floats and written back, so an Apply rounded an integer past
// 2^53 held anywhere else in the node, such as a control's default.
func TestApplyFaceplateValuesKeepsRestOfConfig(t *testing.T) {
	g := &flow.Graph{Nodes: []flow.Node{{
		ID: "fp", Type: flow.NodeTypeFaceplate,
		Config: json.RawMessage(`{"name":"Settings","controls":[` +
			`{"id":"level","label":"Level","type":"number","default":50},` +
			`{"id":"chat","label":"Chat","type":"object","default":{"id":9007199254740993}}]}`),
	}}}

	require.NoError(t, applyFaceplateValues(g, FaceplateValues{"fp": {"level": 70.0}}, false),
		"a number inside the control's range must apply")

	var cfg map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(g.Nodes[0].Config, &cfg), "the rewritten config must still be a JSON object")
	assert.JSONEq(t, `{"level":70}`, string(cfg["values"]), "the applied value is stored under values")
	assert.Contains(t, string(cfg["controls"]), "9007199254740993",
		"a control default the Apply never named must keep its digits")
}

// TestSetConfigKeyOnNullConfig locks that a key can be written into a node
// whose stored config is JSON null, the form an API client writes for an
// unconfigured subflow node. Decoding null left a nil map, and storing the
// node's faceplate values in it panicked.
func TestSetConfigKeyOnNullConfig(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  json.RawMessage
		want string
	}{
		{"null config", json.RawMessage(`null`), `{"order":2}`},
		{"absent config", nil, `{"order":2}`},
		{"existing config", json.RawMessage(`{"name":"Settings","order":0}`), `{"name":"Settings","order":2}`},
	} {
		var out json.RawMessage
		var err error
		require.NotPanicsf(t, func() { out, err = setConfigKey(tc.raw, "order", 2) },
			"%s: writing one key must not panic", tc.name)
		require.NoErrorf(t, err, "%s: the key must be written", tc.name)
		assert.JSONEqf(t, tc.want, string(out), "%s: the key is set and every other key is kept", tc.name)
	}
}
