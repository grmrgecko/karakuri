package flow

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSwitchPorts covers the output sockets a switch node draws, which are
// derived from its rule list rather than declared. The id is what an edge is
// stored against, so it has to stay stable across saves; the label is what the
// author reads on the canvas to tell one branch from another.
func TestSwitchPorts(t *testing.T) {
	ports := SwitchPorts(json.RawMessage(`{"property":"payload.temp","rules":[
		{"id":"r1","operator":"gt","value":"30"},
		{"id":"r2","operator":"lt","value":"10"},
		{"id":"r3","operator":"otherwise"}
	]}`))
	require.Len(t, ports, 3, "one output per rule is the whole point of per-rule ports")

	want := []struct{ id, label string }{
		{"r1", "> 30"},
		{"r2", "< 10"},
		{"r3", "otherwise"},
	}
	for i, w := range want {
		assert.Equalf(t, w.id, ports[i].ID, "port %d carries the rule's id, which its edges are stored against", i)
		assert.Equalf(t, w.label, ports[i].Label, "port %d is labelled with the condition the author typed", i)
		assert.Equalf(t, PortAny, ports[i].Type, "a switch routes the message it was given and declares no payload of its own")
	}

	assert.Empty(t, SwitchPorts(nil), "a node with no config yet draws no branches")
}

// TestSwitchPortsFallBackToPosition covers rules saved without an id, which is
// what a hand-written or imported graph looks like. Falling back to position
// gives every branch a stable, addressable handle rather than none.
func TestSwitchPortsFallBackToPosition(t *testing.T) {
	ports := SwitchPorts(json.RawMessage(`{"rules":[{"operator":"eq","value":"on"},{"operator":"otherwise"}]}`))
	require.Len(t, ports, 2, "two rules still draw two branches")

	assert.Equal(t, "r1", ports[0].ID, "the first rule falls back to its position")
	assert.Equal(t, "r2", ports[1].ID, "the second follows, so the ids stay distinct")
	assert.Equal(t, "= on", ports[0].Label, "the label is still built from the condition")
}

// TestSwitchPortsLegacyConfig covers a config written before per-rule ports
// existed. Those graphs have edges wired to the true and false handles, so the
// node has to keep offering them or every switch deployed before the change
// loses its wiring.
func TestSwitchPortsLegacyConfig(t *testing.T) {
	for name, cfg := range map[string]string{
		"rule":       `{"mode":"rules","property":"payload.temp","operator":"gt","value":"21"}`,
		"expression": `{"mode":"expression","condition":"payload.temp > 21"}`,
	} {
		ports := SwitchPorts(json.RawMessage(cfg))
		require.Lenf(t, ports, 2, "the %s form of a legacy switch has two branches", name)
		assert.Equalf(t, SwitchPortTrue, ports[0].ID, "the %s form must keep the handle its edges name", name)
		assert.Equalf(t, SwitchPortFalse, ports[1].ID, "the %s form must keep the handle its edges name", name)
	}
}

// TestSwitchRuleLabel covers the text on a branch. An author-supplied label
// wins outright, and a generated one is truncated: the label is drawn inside
// the node card, so an unbounded expression would push the card across the
// canvas.
func TestSwitchRuleLabel(t *testing.T) {
	assert.Equal(t, "Kitchen",
		SwitchRuleLabel(SwitchRule{Operator: SwitchOpEq, Value: "kitchen", Label: "Kitchen"}),
		"an author who named the branch gets their name, not a rendering of the condition")

	long := SwitchRuleLabel(SwitchRule{
		Operator: SwitchOpExpression,
		Value:    "payload.temperature > 21 and payload.humidity < 40",
	})
	assert.LessOrEqual(t, len([]rune(long)), 16,
		"a generated label is drawn inside the node card and must be truncated to fit: got %q", long)
}

// TestValidateSwitchRules covers what a switch node is allowed to hold in each
// validation mode. Duplicate ids are the one case that is wrong in both: two
// branches sharing a handle makes an edge ambiguous, which no amount of
// further editing fixes.
func TestValidateSwitchRules(t *testing.T) {
	cases := []struct {
		name    string
		config  string
		lenient bool
		wantErr bool
		why     string
	}{
		{name: "valid", config: `{"rules":[{"id":"r1","operator":"gt","value":"5"},{"id":"r2","operator":"otherwise"}]}`},
		{name: "no rules", config: `{"property":"payload"}`, wantErr: true, why: "a switch with no branches routes nowhere"},
		{name: "no rules draft", config: `{"property":"payload"}`, lenient: true},
		{name: "unknown operator", config: `{"rules":[{"id":"r1","operator":"between","value":"5"}]}`, wantErr: true},
		{name: "missing value", config: `{"rules":[{"id":"r1","operator":"gt"}]}`, wantErr: true},
		{name: "missing value draft", config: `{"rules":[{"id":"r1","operator":"gt"}]}`, lenient: true},
		{
			name:   "value not needed",
			config: `{"rules":[{"id":"r1","operator":"exists"}]}`,
			why:    "exists tests for presence, so there is nothing to compare against",
		},
		{
			name:    "duplicate ids",
			config:  `{"rules":[{"id":"r1","operator":"exists"},{"id":"r1","operator":"otherwise"}]}`,
			wantErr: true,
			why:     "two branches sharing a handle makes every edge to it ambiguous",
		},
		{
			name:   "legacy",
			config: `{"mode":"expression","condition":"payload > 1"}`,
			why:    "a switch saved before per-rule ports must still deploy",
		},
	}

	for _, c := range cases {
		p := &problems{details: map[string]string{}}
		node := &Node{ID: "n1", Type: NodeTypeSwitch, Config: json.RawMessage(c.config)}

		validateSwitchRules(p, "node:n1", node, c.lenient)

		if c.wantErr {
			assert.NotEmptyf(t, p.details, "%s must be reported: %s", c.name, c.why)
		} else {
			assert.Emptyf(t, p.details, "%s must be accepted (%s), got %v", c.name, c.why, p.details)
		}
	}
}
