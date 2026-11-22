package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sfNode builds a subflow instance node referencing a local subgraph.
func sfNode(id, subgraphID string) flow.Node {
	return flow.Node{ID: id, Type: flow.NodeTypeSubflow, SubgraphID: subgraphID}
}

// sharedNode builds a subflow instance node referencing a shared subflow.
func sharedNode(id string, flowID uuid.UUID) flow.Node {
	return flow.Node{ID: id, Type: flow.NodeTypeSubflow, SubflowID: &flowID}
}

// pNode builds a pseudo-port node declaring the given port ID.
func pNode(id, nodeType, portID string) flow.Node {
	return flow.Node{
		ID: id, Type: nodeType,
		Config: json.RawMessage(fmt.Sprintf(`{"port_id":%q}`, portID)),
	}
}

// simpleBody is an in -> work -> out subflow body around the "work" stub type.
func simpleBody() flow.Graph {
	return flow.Graph{
		Nodes: []flow.Node{
			pNode("pi", flow.NodeTypeSubflowInput, "in"),
			gnode("w", "work"),
			pNode("po", flow.NodeTypeSubflowOutput, "out"),
		},
		Edges: []flow.Edge{
			gedge("pi", "out", "w", "in"),
			gedge("w", "out", "po", "in"),
		},
	}
}

// edgeSet renders flattened edges as "src.h->tgt.h" strings for assertions.
func edgeSet(g *flow.Graph) map[string]bool {
	out := make(map[string]bool, len(g.Edges))
	for _, e := range g.Edges {
		out[fmt.Sprintf("%s.%s->%s.%s", e.Source, e.SourceHandle, e.Target, e.TargetHandle)] = true
	}
	return out
}

// TestFlattenLocalSubflowBindsPorts covers inlining a subflow: the body's nodes
// are namespaced under the placing node's id, and the parent's edges bind
// straight to whatever the pseudo-ports stood in for. The namespacing is what
// keeps two placements of one subflow from colliding, and it is also the id the
// editor addresses a node by when you drill into a running subflow.
func TestFlattenLocalSubflowBindsPorts(t *testing.T) {
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sfNode("s1", "sg"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": simpleBody()},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a flow placing a local subflow must flatten")

	ids := make(map[string]bool)
	for _, n := range flat.Nodes {
		ids[n.ID] = true
	}
	assert.Equal(t, map[string]bool{"t": true, "s1/w": true, "d": true}, ids,
		"the body's worker is namespaced under the placing node and the pseudo-ports are gone")
	assert.Equal(t, map[string]bool{"t.out->s1/w.in": true, "s1/w.out->d.in": true}, edgeSet(flat),
		"the parent's edges must bind to whatever the pseudo-ports stood in for")
}

// TestFlattenNestedTwoDeep covers a subflow inside a subflow. The namespace
// accumulates one segment per level, so the path names the whole placement
// chain rather than just the innermost one — two different outer placements of
// the same inner subflow have to stay distinct.
func TestFlattenNestedTwoDeep(t *testing.T) {
	inner := simpleBody()
	outer := flow.Graph{
		Nodes: []flow.Node{
			pNode("pi", flow.NodeTypeSubflowInput, "in"),
			sfNode("mid", "inner"),
			pNode("po", flow.NodeTypeSubflowOutput, "out"),
		},
		Edges: []flow.Edge{
			gedge("pi", "out", "mid", "in"),
			gedge("mid", "out", "po", "in"),
		},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sfNode("s1", "outer"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"outer": outer, "inner": inner},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a two-level nesting must flatten")

	assert.Equal(t, map[string]bool{"t.out->s1/mid/w.in": true, "s1/mid/w.out->d.in": true}, edgeSet(flat),
		"the namespace accumulates one segment per level, so the path names the whole placement chain")
}

// TestFlattenPassthroughSubflow covers a subflow whose body is nothing but a
// wire from its input to its output. Both pseudo-ports splice out and the
// parent's neighbours connect directly, leaving no node at all — the same
// result as a reroute, reached by a different path.
func TestFlattenPassthroughSubflow(t *testing.T) {
	// The subflow body wires its input pseudo-node straight to its output.
	body := flow.Graph{
		Nodes: []flow.Node{
			pNode("pi", flow.NodeTypeSubflowInput, "in"),
			pNode("po", flow.NodeTypeSubflowOutput, "out"),
		},
		Edges: []flow.Edge{gedge("pi", "out", "po", "in")},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sfNode("s1", "sg"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": body},
	}
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a passthrough subflow must flatten")

	assert.Equal(t, map[string]bool{"t.out->d.in": true}, edgeSet(flat),
		"a body that is only a wire leaves no node behind, so the parent's neighbours connect directly")
}

// TestFlattenDepthGuard covers the backstop under the cycle check. Validation
// rejects a self-including subgraph, but flatten also runs on graphs that never
// went through it — a stored version from before the check existed — and
// unbounded recursion there would take the process down rather than fail one
// run.
func TestFlattenDepthGuard(t *testing.T) {
	// A self-including subgraph; validation rejects this, flatten must still
	// stop at the depth cap instead of recursing forever.
	body := flow.Graph{Nodes: []flow.Node{sfNode("again", "sg")}}
	g := &flow.Graph{
		Nodes:     []flow.Node{sfNode("s1", "sg")},
		Subgraphs: map[string]flow.Graph{"sg": body},
	}
	_, err := engine.FlattenGraph(g, nil, nil)
	require.Error(t, err, "a self-including subgraph must not be expanded forever")
	assert.Contains(t, err.Error(), "depth",
		"the error must name the cap so an operator can tell it from an ordinary failure")
}

// TestFlattenSharedWithoutResolverFails covers flattening a shared subflow
// with no way to fetch it. Refusing is the point: dropping the reference would
// produce a runnable graph that silently does less than the author drew.
func TestFlattenSharedWithoutResolverFails(t *testing.T) {
	id := uuid.New()
	g := &flow.Graph{Nodes: []flow.Node{sharedNode("s1", id)}}
	_, err := engine.FlattenGraph(g, nil, nil)
	require.Error(t, err, "a shared reference with no resolver must fail rather than be dropped from the graph")
	assert.Contains(t, err.Error(), "no resolver", "the error must say what was missing, not just that it failed")
}

// TestFlattenSharedResolverError covers the resolver refusing, which is what an
// undeployed shared subflow looks like. The resolver's own message has to reach
// the caller, since it is the only thing that names which subflow is missing.
func TestFlattenSharedResolverError(t *testing.T) {
	id := uuid.New()
	g := &flow.Graph{Nodes: []flow.Node{sharedNode("s1", id)}}
	resolver := func(uuid.UUID) (*flow.Graph, error) {
		return nil, errors.New("shared subflow is not deployed")
	}
	_, err := engine.FlattenGraph(g, resolver, nil)
	require.Error(t, err, "a shared subflow that cannot be resolved must fail the flatten")
	assert.Contains(t, err.Error(), "not deployed",
		"the resolver's own message must survive: it is what names the missing subflow")
}

// TestFlattenNoSubflowsReturnsGraphUnchanged covers the common case. Most
// graphs have no subflows and no bypassed nodes, and flattening runs on every
// run, so it hands the graph straight back rather than copying it.
func TestFlattenNoSubflowsReturnsGraphUnchanged(t *testing.T) {
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("d", "sink")},
		[]flow.Edge{gedge("t", "out", "d", "in")},
	)
	flat, err := engine.FlattenGraph(g, nil, nil)
	require.NoError(t, err, "a plain graph must flatten")
	assert.Same(t, g, flat, "a graph with nothing to flatten must be handed back, not copied on every run")
}

// TestRunThroughLocalSubflow covers a run reaching the inside of a subflow. The
// namespaced node reports its states so the editor can draw them in the drilled
// view, and the subflow node itself reports nothing — after flattening it does
// not exist, and a card for it would show a state nothing produced.
func TestRunThroughLocalSubflow(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "work", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
		&stubNode{typ: "sink", inputs: []string{"in"}, fn: nil},
	)
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sfNode("s1", "sg"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": simpleBody()},
	}
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "a run through a subflow must complete: %s", snap.Error)

	last := pub.lastStates()
	for _, id := range []string{"t", "s1/w", "d"} {
		assert.Equalf(t, "succeeded", last[id], "node %s must report its state under its namespaced id", id)
	}
	assert.NotContains(t, last, "s1",
		"the subflow node does not exist after flattening, so a state for it would be one nothing produced")
}

// TestRunSharedSubflow covers the same path through the library rather than a
// local subgraph: the resolver supplies the body and the run reaches inside it
// exactly as it would a local one.
func TestRunSharedSubflow(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	sharedID := uuid.New()
	body := simpleBody()
	e, pub := testEngine(t, fc, engine.Options{
		SubflowResolver: func(id uuid.UUID) (*flow.Graph, error) {
			if id != sharedID {
				return nil, errors.New("unknown subflow")
			}
			return &body, nil
		},
	},
		triggerStub(),
		&stubNode{typ: "work", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
		&stubNode{typ: "sink", inputs: []string{"in"}, fn: nil},
	)
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sharedNode("s1", sharedID), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
	}
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "a run through a shared subflow must complete: %s", snap.Error)

	last := pub.lastStates()
	assert.Equal(t, "succeeded", last["s1/w"], "the resolved body runs under the placing node's namespace")
	assert.Equal(t, "succeeded", last["d"], "what follows the subflow must receive its output")
}

// TestRunUndeployedSharedSubflowFailsAtCreation covers where an undeployed
// shared subflow is caught. It fails when the run is created rather than
// partway through, so the trigger gets an error it can log instead of a run
// that starts, does some of the work, and then dies with side effects behind
// it.
func TestRunUndeployedSharedSubflowFailsAtCreation(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	sharedID := uuid.New()
	e, _ := testEngine(t, fc, engine.Options{
		SubflowResolver: func(uuid.UUID) (*flow.Graph, error) {
			return nil, errors.New(`shared subflow "Helper" is not deployed`)
		},
	}, triggerStub())
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sharedNode("s1", sharedID)},
		Edges: []flow.Edge{gedge("t", "out", "s1", "in")},
	}
	_, err := e.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), TriggerNodeID: "t", Graph: g, InitialMsg: &engine.Msg{},
	})
	require.Error(t, err, "the run must be refused rather than started and abandoned partway")
	assert.Contains(t, err.Error(), "not deployed",
		"the resolver's message names which subflow to deploy, which is what the operator needs")
}

// TestRetryInsideSubflow covers a retry policy on a node inside a subflow.
// Flattening rewrites the node's id, so the retry has to survive that rewrite
// and the states have to be published under the namespaced id — otherwise the
// drilled view shows a node that never retried.
func TestRetryInsideSubflow(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var attempts atomic.Int32
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "flaky", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				if attempts.Add(1) == 1 {
					return nil, errors.New("first attempt fails")
				}
				return map[string]*engine.Msg{"out": {}}, nil
			},
		},
		&stubNode{typ: "sink", inputs: []string{"in"}, fn: nil},
	)
	body := flow.Graph{
		Nodes: []flow.Node{
			pNode("pi", flow.NodeTypeSubflowInput, "in"),
			{ID: "w", Type: "flaky", Retry: &flow.RetryPolicy{MaxAttempts: 2, DelayMs: 100}},
			pNode("po", flow.NodeTypeSubflowOutput, "out"),
		},
		Edges: []flow.Edge{
			gedge("pi", "out", "w", "in"),
			gedge("w", "out", "po", "in"),
		},
	}
	g := &flow.Graph{
		Nodes: []flow.Node{gnode("t", "trigger"), sfNode("s1", "sg"), gnode("d", "sink")},
		Edges: []flow.Edge{
			gedge("t", "out", "s1", "in"),
			gedge("s1", "out", "d", "in"),
		},
		Subgraphs: map[string]flow.Graph{"sg": body},
	}
	h := startRun(t, e, g)

	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout),
		"the node's retry policy must survive being namespaced by the flatten")
	fc.Advance(100 * time.Millisecond)

	snap := waitDone(t, h)
	require.Equalf(t, "succeeded", snap.Status, "the second attempt succeeds, so the run must: %s", snap.Error)
	assert.Equal(t, int32(2), attempts.Load(), "MaxAttempts 2 means one retry after the first failure")

	states := pub.nodeStates()["s1/w"]
	require.NotEmpty(t, states, "the node must publish under its namespaced id or the drilled view stays blank")
	assert.Contains(t, states, "retrying", "the drilled view has to show the retry, not just the outcome")
	assert.Equal(t, "succeeded", states[len(states)-1], "the node ends succeeded after the retry")
}
