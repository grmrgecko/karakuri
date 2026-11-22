package engine_test

import (
	"context"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fanStub builds a node that fans its input out into n messages stamped with a
// group frame, the shape the split node emits.
func fanStub(typ string, n int) *stubNode {
	return &stubNode{
		typ:     typ,
		inputs:  []string{"in"},
		outputs: outPorts,
		fanFn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
			src := in["in"]
			if src == nil {
				src = &engine.Msg{}
			}
			msgs := make([]*engine.Msg, 0, n)
			for i := 0; i < n; i++ {
				msgs = append(msgs, &engine.Msg{
					Payload: i,
					Group:   src.PushGroup("g", i, n),
				})
			}
			return map[string][]*engine.Msg{"out": msgs}, nil
		},
	}
}

// collector records the payloads a node saw, along with each message's group.
type collector struct {
	mu     sync.Mutex
	seen   []any
	groups [][]engine.GroupFrame
}

func (c *collector) node(typ string) *stubNode {
	return &stubNode{
		typ:     typ,
		inputs:  []string{"in"},
		outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			msg := in["in"]
			c.mu.Lock()
			c.seen = append(c.seen, msg.Payload)
			c.groups = append(c.groups, msg.Group)
			c.mu.Unlock()
			return map[string]*engine.Msg{"out": msg}, nil
		},
	}
}

// payloads returns the collected payloads sorted, since instances of a fanned
// out node run concurrently and finish in no particular order.
func (c *collector) payloads() []int {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]int, 0, len(c.seen))
	for _, v := range c.seen {
		n, _ := v.(int)
		out = append(out, n)
	}
	sort.Ints(out)
	return out
}

// TestFanOutRunsDownstreamOncePerMessage covers the core of fan-out: a node
// emitting several messages on one port runs everything downstream once per
// message, as a separate instance each time. Each part carries the group frame
// that produced it, which is what a fan-in node later pops to know the set is
// complete, and each instance reports its own states so the editor can show a
// node that ran four times rather than one that ran once.
func TestFanOutRunsDownstreamOncePerMessage(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	col := &collector{}
	e, pub := testEngine(t, fc, engine.Options{},
		triggerStub(),
		fanStub("split", 4),
		col.node("sink"),
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("s", "split"), gnode("k", "sink")},
		[]flow.Edge{gedge("t", "out", "s", "in"), gedge("s", "out", "k", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "a fanned-out run must complete: %s", snap.Error)
	assert.Equal(t, []int{0, 1, 2, 3}, col.payloads(),
		"the sink must run once per emitted message, seeing each part exactly once")

	col.mu.Lock()
	defer col.mu.Unlock()
	for _, group := range col.groups {
		require.Lenf(t, group, 1, "each part carries exactly the one frame the split pushed: %+v", group)
		assert.Equal(t, "g", group[0].ID, "the frame names the fan-out a later fan-in must pop")
		assert.Equal(t, 4, group[0].Count, "the frame carries the set size, which is how a join knows it is complete")
	}

	// One running/succeeded pair per instance, so the editor shows a node that
	// ran four times rather than one that ran once.
	succeeded := 0
	for _, state := range pub.nodeStates()["k"] {
		if state == "succeeded" {
			succeeded++
		}
	}
	assert.Equal(t, 4, succeeded, "each instance must report its own outcome, or the canvas under-reports the work done")
}

// TestFanInCollapsesGroupToOneInstance covers the other end: a node
// accumulating a group's parts in its node store and emitting once the frame's
// count is reached. Popping the frame is what takes the message back out of the
// group, so everything after the join runs once rather than once per part —
// without that, a fan-in would fan back out again.
func TestFanInCollapsesGroupToOneInstance(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var mu sync.Mutex
	arrivals := 0
	emitted := 0

	// join accumulates the parts of a group and emits once the group is full,
	// the way the join node uses its node store.
	join := &stubNode{
		typ:     "join",
		inputs:  []string{"in"},
		outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			msg := in["in"]
			mu.Lock()
			arrivals++
			mu.Unlock()
			frame, rest, ok := msg.PopGroup()
			if !ok {
				return map[string]*engine.Msg{"out": msg}, nil
			}
			count := ec.NodeStore().Update("g:"+frame.ID, func(cur any, ok bool) any {
				n, _ := cur.(int)
				return n + 1
			})
			if n, _ := count.(int); n < frame.Count {
				return nil, nil
			}
			mu.Lock()
			emitted++
			mu.Unlock()
			return map[string]*engine.Msg{"out": {Payload: "joined", Group: rest}}, nil
		},
	}
	col := &collector{}
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(), fanStub("split", 3), join, col.node("sink"),
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("s", "split"), gnode("j", "join"), gnode("k", "sink")},
		[]flow.Edge{gedge("t", "out", "s", "in"), gedge("s", "out", "j", "in"), gedge("j", "out", "k", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "a fan-out into a fan-in must complete: %s", snap.Error)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 3, arrivals, "every part of the group must reach the join, or it can never be complete")
	assert.Equal(t, 1, emitted, "the join emits once the frame's count is reached, not once per part")

	col.mu.Lock()
	defer col.mu.Unlock()
	assert.Equal(t, []any{"joined"}, col.seen, "everything after the join runs once, on the one joined message")
	assert.Empty(t, col.groups[0],
		"the join popped the frame, so the message is out of the group; leaving it on would fan out again downstream")
}

// TestUngroupedSecondMessageDoesNotRerunNode covers the diamond: two branches
// feeding one input port with ungrouped messages. Without a group frame there
// is nothing to key an instance on, so the engine keeps its first-wins
// behaviour — the alternative would silently double every diamond in every
// existing flow.
func TestUngroupedSecondMessageDoesNotRerunNode(t *testing.T) {
	// A diamond where both branches feed one input port: the node keeps the
	// engine's original first-wins behavior because neither message is grouped.
	fc := engine.NewFakeClock(time.Now())
	col := &collector{}
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "a", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
		&stubNode{typ: "b", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
		col.node("sink"),
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "a"), gnode("b", "b"), gnode("k", "sink")},
		[]flow.Edge{
			gedge("t", "out", "a", "in"), gedge("t", "out", "b", "in"),
			gedge("a", "out", "k", "in"), gedge("b", "out", "k", "in"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "a diamond must complete: %s", snap.Error)

	col.mu.Lock()
	defer col.mu.Unlock()
	assert.Len(t, col.seen, 1,
		"ungrouped messages keep first-wins, or every diamond in every existing flow would silently double")
}

// TestOptionalPortDispatchesWithoutGatingSibling covers an optional input.
// A node with a gating "in" and an optional "reset" must run when only the
// reset is wired: an optional port is exactly one that does not have to arrive,
// and gating on it would make a reset-only wiring dead.
func TestOptionalPortDispatchesWithoutGatingSibling(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var mu sync.Mutex
	var ports []string

	// The node has a gating "in" and an optional "reset"; only reset is wired,
	// so it must still run.
	target := &stubNode{
		typ:      "target",
		inputs:   []string{"in", "reset"},
		optional: []string{"reset"},
		outputs:  outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			mu.Lock()
			for port := range in {
				ports = append(ports, port)
			}
			mu.Unlock()
			return nil, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), target)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("x", "target")},
		[]flow.Edge{gedge("t", "out", "x", "reset")},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "a node reached only through its optional port must still run: %s", snap.Error)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"reset"}, ports,
		"the node must run with just the port that arrived, not wait on the gating one nothing feeds")
}

// TestGatingPortStillWaitsForEveryInput is the counterweight: ports that do
// gate must all arrive before the node runs, and it runs once with both rather
// than twice with one each. A node that fired on the first arrival would see
// half a message.
func TestGatingPortStillWaitsForEveryInput(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var mu sync.Mutex
	runs := 0
	target := &stubNode{
		typ:     "target",
		inputs:  []string{"a", "b"},
		outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			mu.Lock()
			runs++
			assert.Len(t, in, 2, "a gating node must not run until every gating port has a message")
			mu.Unlock()
			return nil, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(),
		&stubNode{typ: "p", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")},
		target,
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("p", "p"), gnode("x", "target")},
		[]flow.Edge{
			gedge("t", "out", "x", "a"),
			gedge("t", "out", "p", "in"), gedge("p", "out", "x", "b"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "the run must complete: %s", snap.Error)

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, 1, runs, "both inputs arriving must produce one run with both, not one run per arrival")
}

// TestEmitDeliversBeforeExecuteReturns covers ExecContext.Emit against the
// return value. Emit dispatches immediately rather than being buffered until
// the node finishes, which is what lets a long-running node stream results out
// as it produces them; the return value is delivered as well, so a node can do
// both.
func TestEmitDeliversBeforeExecuteReturns(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	downstreamRan := make(chan struct{})
	release := make(chan struct{})

	// The node emits, waits for proof that the downstream node already ran,
	// then returns a second message on another port.
	emitter := &stubNode{
		typ:    "emitter",
		inputs: []string{"in"},
		outputs: []flow.PortSpec{
			{ID: "out", Type: flow.PortAny},
			{ID: "late", Type: flow.PortAny},
		},
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			ec.Emit("out", &engine.Msg{Payload: "early"})
			select {
			case <-release:
			case <-time.After(waitTimeout):
				assert.Fail(t, "the downstream node never ran while the emitter was still executing",
					"Emit must dispatch immediately, or a streaming node buffers everything until it finishes")
			}
			return map[string]*engine.Msg{"late": {Payload: "late"}}, nil
		},
	}
	col := &collector{}
	e, _ := testEngine(t, fc, engine.Options{},
		triggerStub(), emitter,
		&stubNode{
			typ: "early", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				close(downstreamRan)
				close(release)
				return nil, nil
			},
		},
		col.node("sink"),
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("m", "emitter"), gnode("e", "early"), gnode("k", "sink")},
		[]flow.Edge{
			gedge("t", "out", "m", "in"),
			gedge("m", "out", "e", "in"),
			gedge("m", "late", "k", "in"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "the run must complete: %s", snap.Error)

	select {
	case <-downstreamRan:
	default:
		require.FailNow(t, "the emitted message never reached the downstream node")
	}

	col.mu.Lock()
	defer col.mu.Unlock()
	assert.Equal(t, []any{"late"}, col.seen,
		"the return value is delivered as well as the emit, so a node can stream and still return")
}

// TestParkOutlivesTheRunTimeout covers what Park does to the run deadline.
// A delay node parking for ten minutes inside a one-minute run must not be
// killed by the run timeout: parked time is waiting, not work, and counting it
// would make any delay longer than the timeout impossible to build.
func TestParkOutlivesTheRunTimeout(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	parked := &stubNode{
		typ:     "parked",
		inputs:  []string{"in"},
		outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			// Park well past the run timeout; parked time must not trip it.
			if err := ec.Park(ctx, 10*time.Minute); err != nil {
				return nil, err
			}
			return map[string]*engine.Msg{"out": {Payload: "woke"}}, nil
		},
	}
	col := &collector{}
	e, _ := testEngine(t, fc, engine.Options{DefaultRunTimeout: time.Minute},
		triggerStub(), parked, col.node("sink"),
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("p", "parked"), gnode("k", "sink")},
		[]flow.Edge{gedge("t", "out", "p", "in"), gedge("p", "out", "k", "in")},
	)
	h := startRun(t, e, g)

	// Both the timeout watchdog and the park are waiting on the injectable
	// clock; advancing past the park releases it and the timeout together.
	require.True(t, fc.BlockUntilAfterCalls(2, waitTimeout), "the run must arm both the timeout and the park on the clock")
	fc.Advance(10 * time.Minute)

	snap := waitDone(t, h)
	require.Equalf(t, "succeeded", snap.Status,
		"parked time is waiting rather than work, so a ten-minute park inside a one-minute run must survive: %s", snap.Error)

	col.mu.Lock()
	defer col.mu.Unlock()
	assert.Equal(t, []any{"woke"}, col.seen, "the node must resume and emit after the park, not be cut off during it")
}

// TestParkReleasesItsWorkerSlot covers why Park exists at all. The engine is
// given a single worker and the parked node hands a message to the other node
// before parking, so the other node can only run if parking gave the slot back.
// Holding it would mean a flow full of delays stalls every other flow on the
// host.
func TestParkReleasesItsWorkerSlot(t *testing.T) {
	// The engine is given a single worker. The parked node emits a message to
	// the other node before parking, so that node can only run if parking gave
	// the slot back.
	fc := engine.NewFakeClock(time.Now())
	other := make(chan struct{})
	parked := &stubNode{
		typ:    "parked",
		inputs: []string{"in"},
		outputs: []flow.PortSpec{
			{ID: "out", Type: flow.PortAny},
			{ID: "late", Type: flow.PortAny},
		},
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			ec.Emit("out", &engine.Msg{Payload: "go"})
			if err := ec.Park(ctx, time.Minute); err != nil {
				return nil, err
			}
			select {
			case <-other:
			default:
				assert.Fail(t, "the other node never ran while this one was parked",
					"Park must give the worker slot back, or a flow full of delays stalls every other flow")
			}
			return map[string]*engine.Msg{"late": {Payload: "woke"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{MaxParallelNodes: 1},
		triggerStub(), parked,
		&stubNode{
			typ: "other", inputs: []string{"in"}, outputs: outPorts,
			fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				close(other)
				return nil, nil
			},
		},
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("p", "parked"), gnode("o", "other")},
		[]flow.Edge{gedge("t", "out", "p", "in"), gedge("p", "out", "o", "in")},
	)
	h := startRun(t, e, g)

	select {
	case <-other:
	case <-time.After(waitTimeout):
		require.FailNow(t, "the parked node held the only worker slot, so the other node never ran")
	}
	fc.Advance(time.Minute)

	assert.Equal(t, "succeeded", waitDone(t, h).Status, "the parked node must wake and finish after the clock moves")
}

// TestNodeStoreOutlivesRunsAndPurges covers the node store's lifetime, which is
// what makes a rate limiter or a counter node possible: it is scoped to the
// flow and survives between runs, unlike run variables. Purging is the reset an
// operator gets, and it has to actually clear the state rather than only look
// like it.
func TestNodeStoreOutlivesRunsAndPurges(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	counter := &stubNode{
		typ:     "counter",
		inputs:  []string{"in"},
		outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			n := ec.NodeStore().Update("seen", func(cur any, ok bool) any {
				c, _ := cur.(int)
				return c + 1
			})
			return map[string]*engine.Msg{"out": {Payload: n}}, nil
		},
	}
	col := &collector{}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), counter, col.node("sink"))
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("c", "counter"), gnode("k", "sink")},
		[]flow.Edge{gedge("t", "out", "c", "in"), gedge("c", "out", "k", "in")},
	)

	flowID := uuid.New()
	sameFlow := func(ro *engine.RunOptions) { ro.FlowID = flowID }
	for i := 0; i < 3; i++ {
		snap := waitDone(t, startRun(t, e, g, sameFlow))
		require.Equalf(t, "succeeded", snap.Status, "run %d must complete: %s", i, snap.Error)
	}
	require.Equal(t, []int{1, 2, 3}, col.payloads(),
		"the counter must keep climbing across runs: node state is flow-scoped and outlives a run")

	// Purging resets the store, so the next run starts counting again.
	e.PurgeNodeState(flowID)
	snap := waitDone(t, startRun(t, e, g, sameFlow))
	require.Equalf(t, "succeeded", snap.Status, "the run after a purge must complete: %s", snap.Error)

	// Sorted, so the fourth run's fresh 1 lands beside the first run's.
	assert.Equal(t, []int{1, 1, 2, 3}, col.payloads(),
		"purging must actually clear the store, or the operator's reset does nothing")
}

// TestFanOutBeyondWorkerCountCompletes covers dispatch under back pressure: 64
// parts through 2 workers. A dispatcher that blocked handing off a part while
// holding the slot the part needs would deadlock here, which is the failure a
// real split over a large array would hit in production.
func TestFanOutBeyondWorkerCountCompletes(t *testing.T) {
	// Many more parts than workers: the run must not deadlock on dispatch.
	fc := engine.NewFakeClock(time.Now())
	col := &collector{}
	e, _ := testEngine(t, fc, engine.Options{MaxParallelNodes: 2},
		triggerStub(), fanStub("split", 64), col.node("sink"),
	)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("s", "split"), gnode("k", "sink")},
		[]flow.Edge{gedge("t", "out", "s", "in"), gedge("s", "out", "k", "in")},
	)
	snap := waitDone(t, startRun(t, e, g))

	require.Equalf(t, "succeeded", snap.Status, "64 parts through 2 workers must not deadlock: %s", snap.Error)
	assert.Len(t, col.payloads(), 64, "every part must be delivered, however few workers there are")
}
