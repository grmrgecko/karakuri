package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// waitTimeout bounds how long tests wait for asynchronous run completion.
// It is a fail-safe only; passing tests never sleep on it.
const waitTimeout = 5 * time.Second

// fakePub records every published message for assertions.
type fakePub struct {
	mu     sync.Mutex
	events []pubEvent
}

type pubEvent struct {
	Topic   string
	Type    string
	Payload map[string]any
}

func (p *fakePub) Publish(topic, msgType string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	p.mu.Lock()
	p.events = append(p.events, pubEvent{Topic: topic, Type: msgType, Payload: m})
	p.mu.Unlock()
}

// nodeStates returns the sequence of node_state values per node ID.
func (p *fakePub) nodeStates() map[string][]string {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make(map[string][]string)
	for _, ev := range p.events {
		if ev.Type != "node_state" {
			continue
		}
		id, _ := ev.Payload["node_id"].(string)
		state, _ := ev.Payload["state"].(string)
		out[id] = append(out[id], state)
	}
	return out
}

// lastState returns each node's final observed state.
func (p *fakePub) lastStates() map[string]string {
	out := make(map[string]string)
	for id, states := range p.nodeStates() {
		out[id] = states[len(states)-1]
	}
	return out
}

// stubNode is a configurable test node type.
type stubNode struct {
	typ      string
	category string
	inputs   []string
	outputs  []flow.PortSpec
	retry    *flow.RetryPolicy
	// fn returns at most one message per port, the shape nearly every node
	// emits; Execute widens it. fanFn is for tests that need fan-out and takes
	// precedence when both are set.
	fn    func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error)
	fanFn func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error)
	// optional lists input ports declared as non-gating.
	optional []string
	// pure marks the stub as evaluable on demand, so value edges may pull it.
	pure bool
	// configSpec declares fields a value edge can bind to.
	configSpec []engine.FieldSpec
}

func (s *stubNode) Meta() engine.NodeMeta {
	meta := engine.NodeMeta{Type: s.typ, Label: s.typ, Category: s.category, Outputs: s.outputs,
		Pure: s.pure, ConfigSpec: s.configSpec}
	for _, in := range s.inputs {
		spec := engine.PortSpec{ID: in, Type: flow.PortAny}
		for _, opt := range s.optional {
			if opt == in {
				spec.Optional = true
			}
		}
		meta.Inputs = append(meta.Inputs, spec)
	}
	meta.DefaultRetry = s.retry
	return meta
}

func (s *stubNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if s.fanFn != nil {
		return s.fanFn(ctx, ec, inputs)
	}
	if s.fn == nil {
		return nil, nil
	}
	out, err := s.fn(ctx, ec, inputs)
	if err != nil {
		return nil, err
	}
	widened := make(map[string][]*engine.Msg, len(out))
	for port, msg := range out {
		if msg == nil {
			continue
		}
		widened[port] = []*engine.Msg{msg}
	}
	return widened, nil
}

// passthrough emits its single input on the given output port.
func passthrough(port string) func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string]*engine.Msg, error) {
	return func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		for _, m := range inputs {
			return map[string]*engine.Msg{port: m}, nil
		}
		return map[string]*engine.Msg{port: {}}, nil
	}
}

// triggerStub builds a trigger node that passes the initial message through.
func triggerStub() *stubNode {
	return &stubNode{
		typ:      "trigger",
		category: flow.CategoryTrigger,
		outputs:  []flow.PortSpec{{ID: "out", Type: flow.PortAny}},
		fn:       passthrough("out"),
	}
}

// outPorts is shorthand for a single normal "out" port.
var outPorts = []flow.PortSpec{{ID: "out", Type: flow.PortAny}}

// errPorts declares "out" plus a wired-capable "error" port.
var errPorts = []flow.PortSpec{
	{ID: "out", Type: flow.PortAny},
	{ID: "error", Type: flow.PortAny, Kind: flow.PortKindError},
}

// node/edge helpers for building tiny in-code graphs.
func gnode(id, typ string) flow.Node { return flow.Node{ID: id, Type: typ} }

func gedge(src, srcPort, tgt, tgtPort string) flow.Edge {
	return flow.Edge{ID: src + "-" + tgt, Source: src, SourceHandle: srcPort, Target: tgt, TargetHandle: tgtPort}
}

func graph(nodes []flow.Node, edges []flow.Edge) *flow.Graph {
	return &flow.Graph{Nodes: nodes, Edges: edges}
}

// testEngine builds an engine over a fresh registry with the given stubs.
func testEngine(t *testing.T, fc *engine.FakeClock, opts engine.Options, stubs ...*stubNode) (*engine.Engine, *fakePub) {
	t.Helper()
	reg := engine.NewRegistry()
	for _, s := range stubs {
		reg.Register(s)
	}
	pub := &fakePub{}
	opts.Registry = reg
	opts.Clock = fc
	opts.Publisher = pub
	e := engine.New(opts)
	e.Start()
	t.Cleanup(e.Stop)
	return e, pub
}

// startRun starts a run of the given graph seeded at node "t".
func startRun(t *testing.T, e *engine.Engine, g *flow.Graph, opts ...func(*engine.RunOptions)) *engine.RunHandle {
	t.Helper()
	ro := engine.RunOptions{
		FlowID:        uuid.New(),
		FlowName:      "test-flow",
		FlowVersionID: 1,
		TriggerNodeID: "t",
		Graph:         g,
		InitialMsg:    &engine.Msg{Payload: map[string]any{"hello": "world"}},
	}
	for _, o := range opts {
		o(&ro)
	}
	h, err := e.RunFlow(ro)
	require.NoError(t, err, "the run must start before anything about it can be asserted")
	return h
}

// waitDone blocks until the run settles or the fail-safe timeout expires.
func waitDone(t *testing.T, h *engine.RunHandle) engine.RunSnapshot {
	t.Helper()
	select {
	case <-h.Done():
	case <-time.After(waitTimeout):
		require.FailNowf(t, "the run never settled",
			"a run that hangs is the failure, not the timeout: %+v", h.Snapshot())
	}
	return h.Snapshot()
}

// TestLinearSuccessChain is the baseline the rest of the engine tests are read
// against: a chain runs in dependency order, every node reports succeeded, and
// the run does too. Order matters because a node must not start before its
// input exists, which is the whole scheduling contract.
func TestLinearSuccessChain(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var order []string
	var mu sync.Mutex
	record := func(id string) func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		return func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			mu.Lock()
			order = append(order, id)
			mu.Unlock()
			return passthrough("out")(ctx, ec, in)
		}
	}
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts, fn: record("a")},
		&stubNode{typ: "b", inputs: []string{"in"}, outputs: outPorts, fn: record("b")},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a"), gnode("b", "b")},
		[]flow.Edge{gedge("t", "out", "a", "in"), gedge("a", "out", "b", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "the run must succeed: %s", snap.Error)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"a", "b"}, order,
		"b consumes a's output, so it must not start before a has produced one")

	last := pub.lastStates()
	for _, id := range []string{"t", "a", "b"} {
		assert.Equalf(t, "succeeded", last[id], "node %s must end succeeded: the editor draws its card from this state", id)
	}
}

// TestParallelBranchesExecuteConcurrently covers the scheduler actually being
// parallel. Both branches block until the other has started, so the test can
// only pass if they run at once — a scheduler that serialized them would
// deadlock here rather than merely be slow.
func TestParallelBranchesExecuteConcurrently(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	started := make(chan string, 2)
	bothStarted := make(chan struct{})
	branch := func(id string) func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		return func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			started <- id
			select {
			case <-bothStarted:
				return map[string]*engine.Msg{"out": {}}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(waitTimeout):
				return nil, errors.New("branches did not run in parallel")
			}
		}
	}
	e, _ := testEngine(t, fc, engine.Options{MaxParallelNodes: 2},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts, fn: branch("a")},
		&stubNode{typ: "b", inputs: []string{"in"}, outputs: outPorts, fn: branch("b")},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a"), gnode("b", "b")},
		[]flow.Edge{gedge("t", "out", "a", "in"), gedge("t", "out", "b", "in")},
	)
	h := startRun(t, e, g)

	// Both branches must be running before either is released.
	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-started:
			seen[id] = true
		case <-time.After(waitTimeout):
			require.FailNowf(t, "the branches did not both start",
				"each waits for the other, so a scheduler that serialized them deadlocks here; saw %v", seen)
		}
	}
	close(bothStarted)

	snap := waitDone(t, h)
	require.Equalf(t, "succeeded", snap.Status, "both branches were released and must finish: %s", snap.Error)
}

// TestRetrySucceedsAfterFailures covers the retry policy end to end against a
// fake clock, which is the only way to assert the delays without waiting them
// out. The backoff is the arithmetic worth pinning: 100ms, then 100ms * 2.0 =
// 200ms. The published state sequence is what the editor animates, so a node
// that retried twice has to say so rather than silently succeeding.
func TestRetrySucceedsAfterFailures(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var attempts atomic.Int32
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "flaky", inputs: []string{"in"}, outputs: outPorts,
			retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 100, Backoff: 2.0},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				if attempts.Add(1) <= 2 {
					return nil, errors.New("flaky failure")
				}
				return map[string]*engine.Msg{"out": {}}, nil
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("f", "flaky")},
		[]flow.Edge{gedge("t", "out", "f", "in")},
	)
	h := startRun(t, e, g)

	// First retry wait: 100ms.
	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the node must wait on the clock before retrying")
	fc.Advance(100 * time.Millisecond)
	// Second retry wait: 100ms * 2.0 backoff = 200ms.
	require.True(t, fc.BlockUntilAfterCalls(2, waitTimeout), "the second failure must wait again")
	fc.Advance(200 * time.Millisecond)

	snap := waitDone(t, h)
	require.Equalf(t, "succeeded", snap.Status, "the third attempt succeeds, so the run must: %s", snap.Error)

	assert.Equal(t, int32(3), attempts.Load(), "MaxAttempts 3 means two retries after the first failure, not three retries")
	assert.Equal(t, []time.Duration{100 * time.Millisecond, 200 * time.Millisecond}, fc.AfterCalls(),
		"the waits are the delay and the delay times the backoff, which is what makes a flapping device recoverable")
	assert.Equal(t,
		[]string{"running", "retrying", "running", "retrying", "running", "succeeded"},
		pub.nodeStates()["f"],
		"the editor animates this sequence, so a node that retried twice must say so rather than just succeed")
}

// TestFailureBranchIsDecidedByPortKind covers who owns a port called "error".
// The failure branch is the port a node declared as one, not any port that
// happens to carry the name: a script author who declares an output named
// "error" owns it outright, and the node then has no failure branch, so a
// failure ends the run instead of being routed onto the author's port.
func TestFailureBranchIsDecidedByPortKind(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var reached int32
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "doomed", inputs: []string{"in"},
			// Named "error" but not declared as a failure branch.
			outputs: []flow.PortSpec{{ID: "error", Type: flow.PortAny}},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return nil, errors.New("always fails")
			},
		},
		&stubNode{
			typ: "catcher", inputs: []string{"in"},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				atomic.AddInt32(&reached, 1)
				return nil, nil
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("d", "doomed"), gnode("c", "catcher")},
		[]flow.Edge{gedge("t", "out", "d", "in"), gedge("d", "error", "c", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))
	assert.Equal(t, "failed", snap.Status,
		"the node declared no failure branch, so a failure ends the run rather than being routed")
	assert.Zero(t, atomic.LoadInt32(&reached),
		"the author's own port must not receive the failure: only a port of kind error is a failure branch")
}

// TestRetryExhaustedWithErrorBranch covers the point of the whole failure
// design: a node that exhausts its retries and has a failure branch routes onto
// it, and the run succeeds because the failure was handled. The branch carries
// the metadata a handler needs to say what broke, the normal branch is skipped
// rather than run with no message, and the run is still flagged as having had
// failures so the history does not read as clean.
func TestRetryExhaustedWithErrorBranch(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var errMsg *engine.Msg
	var mu sync.Mutex
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "doomed", inputs: []string{"in"}, outputs: errPorts,
			retry: &flow.RetryPolicy{MaxAttempts: 2, DelayMs: 10},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return nil, errors.New("always fails")
			},
		},
		&stubNode{typ: "normal", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
		&stubNode{
			typ: "catcher", inputs: []string{"in"},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				mu.Lock()
				errMsg = in["in"]
				mu.Unlock()
				return nil, nil
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("d", "doomed"), gnode("n", "normal"), gnode("c", "catcher")},
		[]flow.Edge{
			gedge("t", "out", "d", "in"),
			gedge("d", "out", "n", "in"),
			gedge("d", "error", "c", "in"),
		},
	)
	h := startRun(t, e, g)
	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the node must wait on the clock before its one retry")
	fc.Advance(10 * time.Millisecond)

	snap := waitDone(t, h)
	require.Equalf(t, "succeeded", snap.Status, "a failure routed onto a branch is handled, so the run succeeds: %s", snap.Error)
	assert.True(t, snap.HadFailures, "a handled failure must still mark the run, or its history reads as clean")

	mu.Lock()
	defer mu.Unlock()
	require.NotNil(t, errMsg, "the failure branch is wired and must receive the message")
	assert.Equal(t, "always fails", errMsg.Meta["error"], "the handler has nothing to report without the message")
	assert.Equal(t, 2, errMsg.Meta["attempts"], "MaxAttempts 2 means the branch is reached after two tries")
	assert.Equal(t, "d", errMsg.Meta["node_id"], "the handler must be able to name which node failed")

	last := pub.lastStates()
	assert.Equal(t, "failed", last["d"], "the node failed even though the run recovered")
	assert.Equal(t, "succeeded", last["c"], "the handler ran to completion")
	assert.Equal(t, "skipped", last["n"], "the success branch got no message, so it is skipped rather than run empty")
}

// TestRetryExhaustedWithoutErrorBranch is the other half: with nowhere to route
// the failure, the run fails and carries the node's error as its own, so the
// history says what actually went wrong rather than "a node failed".
func TestRetryExhaustedWithoutErrorBranch(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "doomed", inputs: []string{"in"}, outputs: outPorts,
			retry: &flow.RetryPolicy{MaxAttempts: 2, DelayMs: 10},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return nil, errors.New("always fails")
			},
		},
		&stubNode{typ: "normal", inputs: []string{"in"}, fn: passthrough("out")},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("d", "doomed"), gnode("n", "normal")},
		[]flow.Edge{gedge("t", "out", "d", "in"), gedge("d", "out", "n", "in")},
	)
	h := startRun(t, e, g)
	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the node must wait on the clock before its one retry")
	fc.Advance(10 * time.Millisecond)

	snap := waitDone(t, h)
	require.Equal(t, "failed", snap.Status, "with no branch to route onto, an exhausted retry fails the run")
	assert.Equal(t, "always fails", snap.Error, "the run must carry the node's own error, not a generic one")

	last := pub.lastStates()
	assert.Equal(t, "failed", last["d"], "the failing node is the one marked failed")
	assert.Equal(t, "skipped", last["n"], "downstream got no message and is skipped rather than left pending")
}

// TestBranchNotTakenSkipsDownstream covers what happens to a branch a router
// did not emit on. Skipping has to propagate the whole way down, or a node two
// hops behind an untaken branch sits pending forever and the run never settles.
func TestBranchNotTakenSkipsDownstream(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "splitter", inputs: []string{"in"},
			outputs: []flow.PortSpec{{ID: "left", Type: flow.PortAny}, {ID: "right", Type: flow.PortAny}},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return map[string]*engine.Msg{"left": in["in"]}, nil
			},
		},
		&stubNode{typ: "sink", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("s", "splitter"), gnode("l", "sink"), gnode("r", "sink"), gnode("r2", "sink")},
		[]flow.Edge{
			gedge("t", "out", "s", "in"),
			gedge("s", "left", "l", "in"),
			gedge("s", "right", "r", "in"),
			gedge("r", "out", "r2", "in"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "not taking a branch is normal routing, not a failure: %s", snap.Error)

	last := pub.lastStates()
	assert.Equal(t, "succeeded", last["l"], "the taken branch runs")
	assert.Equal(t, "skipped", last["r"], "the untaken branch is skipped")
	assert.Equal(t, "skipped", last["r2"], "skipping must reach the whole way down, or this node stays pending and the run never settles")
}

// TestJoinWithMissingInputIsSkipped covers a node whose inputs can no longer
// all arrive. The join gates on both ports and one branch was never taken, so
// waiting is waiting forever; it is skipped and the run settles.
func TestJoinWithMissingInputIsSkipped(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "splitter", inputs: []string{"in"},
			outputs: []flow.PortSpec{{ID: "left", Type: flow.PortAny}, {ID: "right", Type: flow.PortAny}},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return map[string]*engine.Msg{"left": in["in"]}, nil
			},
		},
		&stubNode{typ: "join", inputs: []string{"a", "b"}, outputs: outPorts, fn: passthrough("out")},
	)
	// Join needs both inputs but only "a" (from the taken left branch) arrives.
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("s", "splitter"), gnode("j", "join")},
		[]flow.Edge{
			gedge("t", "out", "s", "in"),
			gedge("s", "left", "j", "a"),
			gedge("s", "right", "j", "b"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "an unreachable join is not a failure: %s", snap.Error)
	assert.Equal(t, "skipped", pub.lastStates()["j"],
		"one gating input can never arrive, so waiting on it would hang the run")
}

// TestCancelDuringRetryWait covers cancelling a run parked on a retry delay.
// The delay is a minute of fake time that is never advanced, so the run can
// only settle if cancellation interrupts the wait rather than queueing behind
// it — which is what an operator pressing cancel on a stuck run needs.
func TestCancelDuringRetryWait(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "doomed", inputs: []string{"in"}, outputs: outPorts,
			retry: &flow.RetryPolicy{MaxAttempts: 5, DelayMs: 60000},
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return nil, errors.New("fails")
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("d", "doomed")},
		[]flow.Edge{gedge("t", "out", "d", "in")},
	)
	h := startRun(t, e, g)
	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the node must be parked on the retry delay to cancel out of it")
	require.True(t, e.CancelRun(h.ID), "an active run must be findable by id, or the cancel button does nothing")

	// The clock is never advanced: settling proves cancellation interrupted the
	// wait rather than queueing behind it.
	snap := waitDone(t, h)
	require.Equal(t, "canceled", snap.Status, "cancelling must interrupt a retry wait, not wait it out")
}

// TestRunTimeout covers the run-level deadline. A node that blocks forever has
// to be cut off by the watcher rather than holding a worker slot for good, and
// the run reports the timeout as its reason so the history distinguishes it
// from an operator cancelling.
func TestRunTimeout(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{DefaultRunTimeout: 10 * time.Second},
		triggerStub(),
		&stubNode{
			typ: "hang", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("h", "hang")},
		[]flow.Edge{gedge("t", "out", "h", "in")},
	)
	h := startRun(t, e, g)
	// The hanging node never touches the clock, so the only waiter is the
	// timeout watcher.
	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the run must arm its timeout watcher on the injectable clock")
	fc.Advance(10 * time.Second)

	snap := waitDone(t, h)
	require.Equal(t, "canceled", snap.Status, "a node that blocks forever must not hold a worker slot forever")
	assert.Equal(t, "run timeout", snap.Error, "the reason distinguishes a timeout from an operator cancelling")
}

// TestPerFlowConcurrencyCap covers the per-flow run limit, which is what keeps
// a chatty trigger from starting a run per message until the process dies. The
// cap is per flow rather than global, so a second flow has to be unaffected —
// otherwise one busy automation would stall every other.
func TestPerFlowConcurrencyCap(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	release := make(chan struct{})
	running := make(chan struct{}, 1)
	e, _ := testEngine(t, fc, engine.Options{MaxRunsPerFlow: 1},
		triggerStub(),
		&stubNode{
			typ: "block", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				running <- struct{}{}
				select {
				case <-release:
					return map[string]*engine.Msg{"out": {}}, nil
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("b", "block")},
		[]flow.Edge{gedge("t", "out", "b", "in")},
	)
	flowID := uuid.New()
	h := startRun(t, e, g, func(ro *engine.RunOptions) { ro.FlowID = flowID })
	<-running

	// A second run of the same flow must be dropped.
	_, err := e.RunFlow(engine.RunOptions{
		FlowID:        flowID,
		TriggerNodeID: "t",
		Graph:         g,
		InitialMsg:    &engine.Msg{},
	})
	require.ErrorIs(t, err, engine.ErrRunLimit,
		"a second run of a capped flow must be refused by a named error the trigger can log")

	// A different flow is unaffected: the cap is per flow, not global.
	h2 := startRun(t, e, g)
	<-running

	close(release)
	assert.Equal(t, "succeeded", waitDone(t, h).Status, "the run holding the slot must finish normally")
	assert.Equal(t, "succeeded", waitDone(t, h2).Status, "another flow must not be blocked by this flow's cap")
}

// TestPanicTreatedAsError covers a node panicking, which is what a plugin bug
// or a nil dereference in a node looks like. It has to be recovered into an
// ordinary node failure: a panic that escaped would take the whole server down,
// and one broken node type would stop every automation on the host.
func TestPanicTreatedAsError(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "bomb", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				panic("kaboom")
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("b", "bomb")},
		[]flow.Edge{gedge("t", "out", "b", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equal(t, "failed", snap.Status, "a panic must become a node failure, not take the server down with it")
	assert.Equal(t, "panic in node execution: kaboom", snap.Error,
		"the recovered value must reach the history, or the operator has nothing to debug from")
	assert.Equal(t, "failed", pub.lastStates()["b"], "the panicking node is the one marked failed")
}

// TestErrorFlowTriggered covers the flow-level error handler: a failed run
// hands a description of the failure to another flow, which is how an operator
// gets notified about an automation that broke. The second half is the guard
// that keeps that from recursing — an error handler that fails must not trigger
// another error handler, or one bad handler loops forever.
func TestErrorFlowTriggered(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	errorFlowID := uuid.New()
	type call struct {
		flowID uuid.UUID
		msg    *engine.Msg
	}
	calls := make(chan call, 2)
	e, _ := testEngine(t, fc, engine.Options{
		ErrorFlowRunner: func(id uuid.UUID, msg *engine.Msg) { calls <- call{id, msg} },
	},
		triggerStub(),
		&stubNode{
			typ: "doomed", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				return nil, errors.New("boom")
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("d", "doomed")},
		[]flow.Edge{gedge("t", "out", "d", "in")},
	)
	flowID := uuid.New()
	h := startRun(t, e, g, func(ro *engine.RunOptions) {
		ro.FlowID = flowID
		ro.ErrorFlowID = &errorFlowID
	})
	snap := waitDone(t, h)
	require.Equal(t, "failed", snap.Status, "the handler only fires for a failed run")

	select {
	case c := <-calls:
		assert.Equal(t, errorFlowID, c.flowID, "the configured handler flow is the one that must be started")

		payload, ok := c.msg.Payload.(map[string]any)
		require.True(t, ok, "the handler receives an object it can read fields off")
		assert.Equal(t, flowID.String(), payload["flow_id"], "the handler must be able to name which flow broke")
		assert.Equal(t, "d", payload["failed_node_id"], "the handler must be able to name which node broke")
		assert.Equal(t, "boom", payload["error"], "the handler must be able to report what the failure said")
		assert.Equal(t, "error_handler", c.msg.Meta["source"],
			"the handler needs to tell an error-triggered run from a normal one")
	case <-time.After(waitTimeout):
		require.FailNow(t, "the error flow runner was never invoked for a failed run")
	}

	// A failing run marked as an error handler must not trigger the runner.
	h2 := startRun(t, e, g, func(ro *engine.RunOptions) {
		ro.ErrorFlowID = &errorFlowID
		ro.IsErrorHandler = true
	})
	require.Equal(t, "failed", waitDone(t, h2).Status, "the handler run itself fails, which is what arms the recursion")

	select {
	case <-calls:
		assert.Fail(t, "an error handler that fails must not trigger another error flow, or a broken handler loops forever")
	default:
	}
}

// TestRunFinishedEventsPublished covers the websocket lifecycle events two
// different screens subscribe to: the flow's run list watches
// flow:{id}:runs, and an open run view watches run:{id}. Both have to be
// published or one of those screens silently stops updating.
func TestRunFinishedEventsPublished(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{}, triggerStub())
	g := graph([]flow.Node{gnode("t", "trigger")}, nil)
	flowID := uuid.New()
	h := startRun(t, e, g, func(ro *engine.RunOptions) { ro.FlowID = flowID })
	waitDone(t, h)

	pub.mu.Lock()
	defer pub.mu.Unlock()
	flowTopic := "flow:" + flowID.String() + ":runs"
	runTopic := "run:" + h.ID.String()

	var sawStarted, sawFlowFinished, sawRunFinished bool
	for _, ev := range pub.events {
		switch {
		case ev.Topic == flowTopic && ev.Type == "run_started":
			sawStarted = true
			run, ok := ev.Payload["run"].(map[string]any)
			require.True(t, ok, "run_started must carry the run row the list renders")
			assert.Equal(t, h.ID.String(), run["id"], "the list needs the id to address the run it just added")
			assert.Equal(t, "running", run["status"], "a run announced as started must be announced as running")
		case ev.Topic == flowTopic && ev.Type == "run_finished":
			sawFlowFinished = true
		case ev.Topic == runTopic && ev.Type == "run_finished":
			sawRunFinished = true
			assert.Equal(t, "succeeded", ev.Payload["status"], "the open run view reads the outcome off this event")
		}
	}
	assert.True(t, sawStarted, "the flow's run list adds the row on run_started")
	assert.True(t, sawFlowFinished, "the flow's run list updates the row on run_finished")
	assert.True(t, sawRunFinished, "an open run view watches its own topic and never sees the flow's")
}

// TestStopCancelsActiveRuns covers shutdown. A stopping engine cancels what is
// in flight rather than waiting on a node that may never return, and refuses
// new runs afterwards by a named error so a trigger firing during shutdown logs
// something meaningful instead of hanging.
func TestStopCancelsActiveRuns(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{
			typ: "hang", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("h", "hang")},
		[]flow.Edge{gedge("t", "out", "h", "in")},
	)
	h := startRun(t, e, g)
	require.Len(t, e.ActiveRuns(), 1, "the hanging run must be in flight before Stop means anything")

	e.Stop()

	require.Equal(t, "canceled", waitDone(t, h).Status,
		"shutdown must cancel what is in flight rather than wait on a node that may never return")

	_, err := e.RunFlow(engine.RunOptions{TriggerNodeID: "t", Graph: g})
	assert.ErrorIs(t, err, engine.ErrStopped,
		"a trigger firing during shutdown must get a named error to log, not a hang")
}

// TestNoteNodesNeverScheduled covers annotation nodes. A note is documentation
// drawn on the canvas; scheduling it would put a card with a running state on
// something that does nothing, and gate the run on it besides.
func TestNoteNodesNeverScheduled(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "note", category: flow.CategoryAnnotation},
	)
	g := graph([]flow.Node{gnode("t", "trigger"), gnode("n1", "note")}, nil)
	snap := waitDone(t, startRun(t, e, g))
	require.Equal(t, "succeeded", snap.Status, "a graph of a trigger and a note has nothing that can fail")
	assert.Empty(t, pub.nodeStates()["n1"],
		"an annotation must never be scheduled, or the canvas shows a running state on a comment")
}

func TestPermanentFailurePreservesErrorEnvelope(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var received *engine.Msg
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(),
		&stubNode{typ: "failed", inputs: []string{"in"}, outputs: errPorts, retry: &flow.RetryPolicy{MaxAttempts: 3, DelayMs: 100}, fn: func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			return nil, &engine.ExecutionError{Message: "permanent response", Details: map[string]any{"status": 400}}
		}},
		&stubNode{typ: "catcher", inputs: []string{"in"}, fn: func(_ context.Context, _ engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			received = in["in"]
			return nil, nil
		}},
	)
	g := graph([]flow.Node{gnode("t", "trigger"), gnode("f", "failed"), gnode("c", "catcher")}, []flow.Edge{gedge("t", "out", "f", "in"), gedge("f", "error", "c", "in")})
	h := startRun(t, e, g, func(opts *engine.RunOptions) {
		opts.InitialMsg = &engine.Msg{Payload: "original", Topic: "device", Meta: map[string]any{"trace": "kept", "retry_after_ms": 60000}}
	})
	snap := waitDone(t, h)
	require.Equal(t, engine.RunStatusSucceeded, snap.Status)
	require.NotNil(t, received)
	assert.Equal(t, "original", received.Payload)
	assert.Equal(t, "device", received.Topic)
	assert.Equal(t, "kept", received.Meta["trace"])
	assert.Equal(t, 1, received.Meta["attempts"])
	assert.Equal(t, false, received.Meta["retryable"])
	assert.Equal(t, map[string]any{"status": 400}, received.Meta["error_details"])
	assert.NotContains(t, received.Meta, "retry_after_ms", "an earlier failure must not supply the current retry delay")
	assert.Empty(t, fc.AfterCalls(), "permanent errors must not schedule a retry")
}
