package flowops

import (
	"encoding/json"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatchKeepsLargeIntegersInUntouchedConfig locks the rule that a patch
// changes only the fields it names. An update re-encodes the whole item, and
// decoding stored numbers as floats rounded any integer past 2^53, so moving a
// node rewrote a 64-bit ID elsewhere in its config to a different ID.
func TestPatchKeepsLargeIntegersInUntouchedConfig(t *testing.T) {
	g := &flow.Graph{Nodes: []flow.Node{{
		ID:     "n1",
		Type:   "work",
		Config: json.RawMessage(`{"channel_id":9007199254740993,"label":"before"}`),
	}}}

	err := patchGraphAt(g, nil, GraphOperation{
		Op:    "update_node",
		ID:    "n1",
		Value: map[string]any{"config": map[string]any{"label": "after"}},
	})
	require.NoError(t, err, "a merge update of one config field must apply")

	assert.Equal(t, `{"channel_id":9007199254740993,"label":"after"}`, string(g.Nodes[0].Config),
		"a config number the patch did not name must come back digit for digit, not rounded through a float")
}
