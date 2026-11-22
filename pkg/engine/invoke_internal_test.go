package engine

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// invocationFixtureNode is a compact executable type for direct invocation
// boundary tests that do not need the scheduler.
type invocationFixtureNode struct {
	meta NodeMeta
	fn   func(context.Context, ExecContext, map[string]*Msg) (map[string][]*Msg, error)
}

func (n *invocationFixtureNode) Meta() NodeMeta { return n.meta }
func (n *invocationFixtureNode) Execute(ctx context.Context, ec ExecContext, inputs map[string]*Msg) (map[string][]*Msg, error) {
	return n.fn(ctx, ec, inputs)
}

// newInvocationFixtureRun builds a real Run and registry without starting its
// scheduler; direct calls still use the ordinary execution and event paths.
func newInvocationFixtureRun(t *testing.T, nodes ...NodeType) (*Run, *execCtx) {
	t.Helper()
	registry := NewRegistry()
	parent := &invocationFixtureNode{meta: NodeMeta{Type: "parent"}, fn: func(context.Context, ExecContext, map[string]*Msg) (map[string][]*Msg, error) {
		return nil, nil
	}}
	registry.Register(parent)
	for _, node := range nodes {
		registry.Register(node)
	}
	e := New(Options{Registry: registry})
	t.Cleanup(e.Stop)
	r, err := newRun(e, RunOptions{
		FlowID: uuid.New(), TriggerNodeID: "parent",
		Graph: &flow.Graph{Nodes: []flow.Node{{ID: "parent", Type: "parent"}}},
	})
	require.NoError(t, err)
	t.Cleanup(r.cancel)
	n := r.nodes["parent"]
	inst := &nodeInstance{node: n, inputs: map[string]*Msg{}, state: NodeStateRunning}
	return r, &execCtx{run: r, node: n, inst: inst, slot: &workSlot{run: r}}
}

// TestPureScriptInvocationRejectsActionsAndNilSlotParking covers inherited
// valueEval: impure calls fail before Execute, while an allowed pure call has
// no worker slot and cannot turn parking or tools into an escape hatch.
func TestPureScriptInvocationRejectsActionsAndNilSlotParking(t *testing.T) {
	var actions int
	impure := &invocationFixtureNode{meta: NodeMeta{Type: "impure"}, fn: func(context.Context, ExecContext, map[string]*Msg) (map[string][]*Msg, error) {
		actions++
		return nil, nil
	}}
	pure := &invocationFixtureNode{meta: NodeMeta{Type: "pure", Pure: true, Outputs: []PortSpec{{ID: "out", Type: flow.PortAny}}}, fn: func(ctx context.Context, ec ExecContext, inputs map[string]*Msg) (map[string][]*Msg, error) {
		parkErr := ec.Park(ctx, time.Second)
		_, toolErr := ec.CallTool(ctx, "tool", nil, time.Second)
		if parkErr == nil || !strings.Contains(parkErr.Error(), "pure") {
			return nil, errors.New("pure evaluation parked instead of rejecting the wait")
		}
		if toolErr == nil {
			return nil, errors.New("pure evaluation admitted a tool call")
		}
		if inputs != nil {
			return nil, errors.New("pure evaluation received message inputs")
		}
		return One("out", &Msg{Payload: "ok"}), nil
	}}
	_, parent := newInvocationFixtureRun(t, impure, pure)
	parent.valueEval = true
	parent.inst = nil
	parent.slot = nil

	_, err := parent.CallScriptNode(context.Background(), nodeapi.NodeCall{Type: "impure", Config: []byte(`{}`)})
	require.Error(t, err)
	var callErr *nodeapi.NodeCallError
	require.ErrorAs(t, err, &callErr)
	assert.Zero(t, callErr.Attempts)
	assert.Zero(t, actions, "pure rejection must happen before the action")

	result, err := parent.CallScriptNode(context.Background(), nodeapi.NodeCall{Type: "pure", Config: []byte(`{}`)})
	require.NoError(t, err)
	assert.Equal(t, 1, result.Attempts)
	assert.Equal(t, "ok", result.Outputs["out"][0].Payload)
}

// TestScriptInvocationStoreIdentityIsStableAndSeparate verifies that explicit
// IDs share state only within the same parent/type tuple and never alias the
// parent graph node's existing store.
func TestScriptInvocationStoreIdentityIsStableAndSeparate(t *testing.T) {
	stateful := &invocationFixtureNode{meta: NodeMeta{
		Type: "stateful", InvocationState: flow.InvocationStateStateful,
		Outputs: []PortSpec{{ID: "out", Type: flow.PortAny}},
	}, fn: func(ctx context.Context, ec ExecContext, inputs map[string]*Msg) (map[string][]*Msg, error) {
		value := ec.NodeStore().Update("count", func(current any, ok bool) any {
			if !ok {
				return 1
			}
			return current.(int) + 1
		})
		return One("out", &Msg{Payload: value}), nil
	}}
	_, parent := newInvocationFixtureRun(t, stateful)
	parent.NodeStore().Set("count", "parent-store")
	call := func(id string) any {
		result, err := parent.CallScriptNode(context.Background(), nodeapi.NodeCall{
			Type: "stateful", Config: []byte(`{}`), Options: nodeapi.NodeCallOptions{ID: id},
		})
		require.NoError(t, err)
		return result.Outputs["out"][0].Payload
	}
	assert.Equal(t, 1, call("a"))
	assert.Equal(t, 2, call("a"))
	assert.Equal(t, 1, call("b"))
	stored, ok := parent.NodeStore().Get("count")
	assert.True(t, ok)
	assert.Equal(t, "parent-store", stored)
	assert.NotEqual(t,
		scriptInvocationStoreID("a|b", "c", "d"),
		scriptInvocationStoreID("a", "b|c", "d"),
		"length-prefix hashing must not introduce delimiter ambiguity")
}

// TestScriptInvocationResultBounds rejects excess messages and JSON before a
// result can cross into the interpreter.
func TestScriptInvocationResultBounds(t *testing.T) {
	n := &runNode{ports: flow.PortSet{Outputs: []flow.PortSpec{{ID: "out", Type: flow.PortAny}}}}
	messages := make([]*Msg, maxScriptCallMessages+1)
	for i := range messages {
		messages[i] = &Msg{Payload: i}
	}
	_, err := boundedScriptCallOutputs(n, map[string][]*Msg{"out": messages})
	assert.ErrorContains(t, err, "message result limit")

	_, err = boundedScriptCallOutputs(n, map[string][]*Msg{"out": {{Payload: strings.Repeat("x", maxScriptCallJSON)}}})
	assert.ErrorContains(t, err, "limit")
}
