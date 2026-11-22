package agent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/grmrgecko/karakuri/agent/agentnode"
)

// TestDisableWithholdsNodes covers the agent's one safety control. The disable
// list is what stands between a deployed flow and a node that shuts the machine
// down, so it has to withhold exactly what it names and refuse a name it does
// not recognize: a typo that quietly did nothing would leave power registered
// on a host whose operator believes it is off.
func TestDisableWithholdsNodes(t *testing.T) {
	plug, err := New(agentnode.Options{}, []string{"power", " Clipboard-Write "})
	require.NoError(t, err, "both names are real node types, so the list must be accepted")

	enabled := plug.EnabledNames()
	assert.NotContains(t, enabled, "power", "a disabled node type is never registered")
	assert.NotContains(t, enabled, "clipboard-write",
		"the list is trimmed and case-folded, since an operator writes it by hand in agent.yaml")
	assert.Contains(t, enabled, "script", "disabling one node type must not touch the others")
	assert.Len(t, enabled, len(plug.nodeTypes())-2, "exactly the two named types are withheld")

	_, err = New(agentnode.Options{}, []string{"powr"})
	require.Error(t, err, "a name matching no node type is an operator mistake and must stop the agent")
	assert.Contains(t, err.Error(), "power", "the error lists the real names so the typo is obvious")
}
