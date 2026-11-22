package core

import (
	"context"
	"sort"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// switchPorts runs one message through a switch node and reports which output
// ports it was routed to, sorted so assertions do not depend on map order.
func switchPorts(t *testing.T, cfg string, msg *engine.Msg) []string {
	t.Helper()
	out, err := SwitchNode{}.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "the switch must route this message before its branches can be read")
	ports := make([]string, 0, len(out))
	for port, msgs := range out {
		if len(msgs) > 0 && msgs[0] != nil {
			ports = append(ports, port)
		}
	}
	sort.Strings(ports)
	return ports
}

// assertPorts states which branches a message left on. Naming every expected
// branch is what makes the check meaningful: a switch that fired an extra one
// has fanned out rather than routed.
func assertPorts(t *testing.T, got []string, want ...string) {
	t.Helper()
	assert.Equal(t, want, got, "the message must leave on exactly these branches")
}

// TestSwitchRoutesPerRule covers the per-rule branches, which is what a switch
// is for: each rule owns an output, so a value picks its own path instead of
// being forced through a chain of true/false splits.
func TestSwitchRoutesPerRule(t *testing.T) {
	cfg := `{"property":"payload.temp","rules":[
		{"id":"hot","operator":"gt","value":"30"},
		{"id":"cold","operator":"lt","value":"10"},
		{"id":"mild","operator":"otherwise"}
	]}`

	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"temp": 35.0}}), "hot")
	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"temp": 5.0}}), "cold")
	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"temp": 20.0}}), "mild")
}

// TestSwitchOtherwiseIgnoresPosition covers the otherwise rule's precedence. It
// fires only when nothing else matched, wherever it sits in the list — an
// author who dragged it to the top must not find it swallowing every message.
func TestSwitchOtherwiseIgnoresPosition(t *testing.T) {
	cfg := `{"property":"payload.state","rules":[
		{"id":"none","operator":"otherwise"},
		{"id":"on","operator":"eq","value":"on"}
	]}`

	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"state": "on"}}), "on")
	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"state": "off"}}), "none")
}

// TestSwitchMatchModes covers the two match modes against overlapping rules.
// 35 satisfies both "> 20" and "> 30", so the default stops at the first and
// "all" takes both — an author with overlapping thresholds gets one branch or
// every matching branch, and nothing in between.
func TestSwitchMatchModes(t *testing.T) {
	rules := `"rules":[
		{"id":"warm","operator":"gt","value":"20"},
		{"id":"hot","operator":"gt","value":"30"},
		{"id":"other","operator":"otherwise"}
	]`
	msg := &engine.Msg{Payload: map[string]any{"temp": 35.0}}

	// The default stops at the first match.
	assertPorts(t, switchPorts(t, `{"property":"payload.temp",`+rules+`}`, msg), "warm")
	assertPorts(t, switchPorts(t, `{"property":"payload.temp","match":"all",`+rules+`}`, msg), "hot", "warm")
}

// TestSwitchCopiesMessagePerBranch covers the aliasing hazard in match-all
// mode: two branches run in parallel, and handing them one message would let a
// change node on one branch rewrite what the other is still reading. Metadata
// is the map that matters, since it is mutable and shared by default.
func TestSwitchCopiesMessagePerBranch(t *testing.T) {
	cfg := `{"property":"payload","match":"all","rules":[
		{"id":"a","operator":"exists"},
		{"id":"b","operator":"eq","value":"on"}
	]}`
	msg := &engine.Msg{Payload: "on", Meta: map[string]any{"source": "test"}}
	out, err := SwitchNode{}.Execute(context.Background(), newTestCtx(cfg, nil), map[string]*engine.Msg{"in": msg})
	require.NoError(t, err, "both rules match, so both branches must fire")

	a, b := out["a"][0], out["b"][0]
	require.NotSame(t, a, b, "parallel branches must not receive the same message")

	b.Meta["source"] = "changed"
	assert.Equal(t, "test", a.Meta["source"],
		"metadata must be copied, not shared: a change on one branch would otherwise rewrite what the other reads")
}

// TestSwitchOperators covers every operator a rule can use, each against a
// value that exercises it. The at-bound rows are the ones worth having: lte and
// gte have to include the boundary, since a threshold rule written as "at or
// above 10" is wrong by one degree if it does not.
func TestSwitchOperators(t *testing.T) {
	tests := []struct {
		name    string
		rule    string
		payload any
		want    string
	}{
		{"eq", `{"id":"m","operator":"eq","value":"on"}`, "on", "m"},
		{"neq", `{"id":"m","operator":"neq","value":"on"}`, "off", "m"},
		{"lte at bound", `{"id":"m","operator":"lte","value":"10"}`, 10.0, "m"},
		{"gte at bound", `{"id":"m","operator":"gte","value":"10"}`, 10.0, "m"},
		{"gte below", `{"id":"m","operator":"gte","value":"10"}`, 9.0, "else"},
		{"contains", `{"id":"m","operator":"contains","value":"kitchen"}`, "light.kitchen", "m"},
		{"regex", `{"id":"m","operator":"regex","value":"^light\\."}`, "light.kitchen", "m"},
		{"regex no match", `{"id":"m","operator":"regex","value":"^switch\\."}`, "light.kitchen", "else"},
		{"exists", `{"id":"m","operator":"exists"}`, "anything", "m"},
		{"expression", `{"id":"m","operator":"expression","value":"payload == 'on'"}`, "on", "m"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := `{"property":"payload","rules":[` + tt.rule + `,{"id":"else","operator":"otherwise"}]}`
			assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: tt.payload}), tt.want)
		})
	}
}

// TestSwitchInterpolatesRuleValues covers a rule comparing two parts of one
// message rather than against a constant, which is how a flow checks a reading
// against a setpoint that arrived with it.
func TestSwitchInterpolatesRuleValues(t *testing.T) {
	cfg := `{"property":"payload.state","rules":[
		{"id":"same","operator":"eq","value":"{{ payload.expected }}"},
		{"id":"other","operator":"otherwise"}
	]}`
	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"state": "on", "expected": "on"}}), "same")
	assertPorts(t, switchPorts(t, cfg, &engine.Msg{Payload: map[string]any{"state": "on", "expected": "off"}}), "other")
}

// TestSwitchLegacyConfig covers a switch saved before per-rule ports existed.
// Those graphs have edges wired to the true and false handles, so the node has
// to keep routing to them — both the rule form and the expression form, since
// both were stored.
func TestSwitchLegacyConfig(t *testing.T) {
	rule := `{"mode":"rules","property":"payload.temp","operator":"gt","value":"21"}`
	assertPorts(t, switchPorts(t, rule, &engine.Msg{Payload: map[string]any{"temp": 25.0}}), "true")
	assertPorts(t, switchPorts(t, rule, &engine.Msg{Payload: map[string]any{"temp": 15.0}}), "false")

	expression := `{"mode":"expression","condition":"payload.temp > 21"}`
	assertPorts(t, switchPorts(t, expression, &engine.Msg{Payload: map[string]any{"temp": 25.0}}), "true")
	assertPorts(t, switchPorts(t, expression, &engine.Msg{Payload: map[string]any{"temp": 15.0}}), "false")
}

// TestSwitchWithoutRulesFails covers a switch with nothing to route by. It
// fails rather than passing the message somewhere: with no rules there is no
// branch to take, and dropping the message silently would look like a working
// filter.
func TestSwitchWithoutRulesFails(t *testing.T) {
	_, err := SwitchNode{}.Execute(context.Background(), newTestCtx(`{"property":"payload"}`, nil),
		map[string]*engine.Msg{"in": {Payload: "x"}})
	assert.Error(t, err,
		"a switch with no rules has no branch to take, and dropping the message silently would look like a filter")
}
