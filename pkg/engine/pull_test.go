package engine_test

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// paramSpec is a config field a value edge can bind to.
var paramSpec = []engine.FieldSpec{{ID: "threshold", Label: "Threshold", Type: flow.FieldNumber}}

// valueEdge builds an edge that binds a parameter rather than delivering a message.
func valueEdge(src, srcPort, tgt, field string) flow.Edge {
	return flow.Edge{
		ID: src + "-param-" + tgt, Source: src, SourceHandle: srcPort,
		Target: tgt, TargetHandle: flow.ParamPortID(field), Kind: flow.EdgeValue,
	}
}

// constNode is a pure stub emitting a fixed payload, counting its executions so
// tests can prove memoization.
func constNode(typ string, value any, runs *int32) *stubNode {
	return &stubNode{
		typ: typ, pure: true, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			atomic.AddInt32(runs, 1)
			return map[string]*engine.Msg{"out": {Payload: value}}, nil
		},
	}
}

// TestValueEdgeSuppliesParameter covers the core of parameter binding: a pure
// node's output arrives as a config field on the consumer, overriding the
// literal stored on the node. The pulled node ran, so it must settle as
// succeeded rather than skipped — the run view is the record of what executed.
func TestValueEdgeSuppliesParameter(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	got := make(chan float64, 1)

	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Threshold float64 `json:"threshold"`
			}
			if err := ec.Config(&cfg); err != nil {
				return nil, err
			}
			got <- cfg.Threshold
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}

	e, pub := testEngine(t, fc, engine.Options{}, triggerStub(), constNode("konst", 21.5, &runs), consumer)

	g := graph(
		[]flow.Node{
			gnode("t", "trigger"),
			gnode("konst", "konst"),
			// The literal is what the field would be without a wire.
			{ID: "c", Type: "consumer", Config: json.RawMessage(`{"threshold":1}`),
				Params: []string{"threshold"}},
		},
		[]flow.Edge{gedge("t", "out", "c", "in"), valueEdge("konst", "out", "c", "threshold")},
	)

	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the run must complete for the consumer to have read anything: %s", snap.Error)

	select {
	case v := <-got:
		assert.Equal(t, 21.5, v, "the pulled value must override the literal stored on the node, which is what wiring the field means")
	default:
		require.FailNow(t, "the consumer never ran, so nothing was pulled")
	}
	assert.Equal(t, int32(1), runs, "a pure source is evaluated once per run, not once per message")
	assert.Equal(t, "succeeded", pub.lastStates()["konst"],
		"a node pulled for a parameter executed, so reporting it skipped would say it sat the run out")
}

// TestUnwiredParameterKeepsLiteral covers the engine's backstop for a graph
// that bypassed strict validation (which rejects an exposed field with no
// wire): the configured literal stays in force rather than failing the node.
func TestUnwiredParameterKeepsLiteral(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	got := make(chan float64, 1)
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Threshold float64 `json:"threshold"`
			}
			_ = ec.Config(&cfg)
			got <- cfg.Threshold
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"),
			{ID: "c", Type: "consumer", Config: json.RawMessage(`{"threshold":7}`), Params: []string{"threshold"}}},
		[]flow.Edge{gedge("t", "out", "c", "in")},
	)
	waitDone(t, startRun(t, e, g))
	assert.Equal(t, 7.0, <-got,
		"with nothing wired there is nothing to pull, so the literal stands rather than the node failing")
}

// TestMultiPortFieldCollectsArray covers a multi port field, the AI agent's
// tool fan-in: every wired source is pulled and the config field holds their
// values as an array in graph edge order, and a single wire still arrives as a
// one-element array so the consumer decodes one shape.
func TestMultiPortFieldCollectsArray(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	got := make(chan []any, 1)

	spec := []engine.FieldSpec{
		{ID: "tools", Label: "Tools", Type: flow.FieldJSON, Port: "toolset", Multi: true},
	}
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: spec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Tools []any `json:"tools"`
			}
			if err := ec.Config(&cfg); err != nil {
				return nil, err
			}
			got <- cfg.Tools
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(),
		constNode("ka", "first", &runs), constNode("kb", "second", &runs), consumer)

	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "ka"), gnode("b", "kb"), gnode("c", "consumer")},
		[]flow.Edge{
			gedge("t", "out", "c", "in"),
			valueEdge("a", "out", "c", "tools"),
			valueEdge("b", "out", "c", "tools"),
		},
	)

	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the run must complete for the consumer to have read anything: %s", snap.Error)
	assert.Equal(t, []any{"first", "second"}, <-got,
		"every wired source must arrive, in graph edge order, so the agent's tool list is deterministic")
}

// TestMultiPortFieldSingleWireStillArray pins the shape contract: multi means
// array however many wires are attached, so a node never branches on count.
func TestMultiPortFieldSingleWireStillArray(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	got := make(chan []any, 1)
	spec := []engine.FieldSpec{
		{ID: "tools", Label: "Tools", Type: flow.FieldJSON, Port: "toolset", Multi: true},
	}
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: spec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Tools []any `json:"tools"`
			}
			_ = ec.Config(&cfg)
			got <- cfg.Tools
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), constNode("ka", "only", &runs), consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("a", "ka"), gnode("c", "consumer")},
		[]flow.Edge{gedge("t", "out", "c", "in"), valueEdge("a", "out", "c", "tools")},
	)
	waitDone(t, startRun(t, e, g))
	assert.Equal(t, []any{"only"}, <-got, "one wire is a one-element array, not a bare value")
}

// TestValueEdgeMemoizedAcrossParameters covers one value node feeding two
// parameters: it is evaluated once for the run, not once per reader.
func TestValueEdgeMemoizedAcrossParameters(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	spec := []engine.FieldSpec{
		{ID: "a", Type: flow.FieldNumber},
		{ID: "b", Type: flow.FieldNumber},
	}
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: spec,
		fn: passthrough("out"),
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), constNode("konst", 3.0, &runs), consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("konst", "konst"),
			{ID: "c", Type: "consumer", Params: []string{"a", "b"}}},
		[]flow.Edge{
			gedge("t", "out", "c", "in"),
			valueEdge("konst", "out", "c", "a"),
			{ID: "k-b", Source: "konst", SourceHandle: "out", Target: "c",
				TargetHandle: flow.ParamPortID("b"), Kind: flow.EdgeValue},
		},
	)
	waitDone(t, startRun(t, e, g))
	assert.Equal(t, int32(1), runs,
		"the value is memoized per run, so two readers must not mean two evaluations of a source that may hit the network")
}

// TestValueEdgeDoesNotDeliverMessages covers the separation of the two edge
// kinds: a value edge must not schedule or feed the consumer's message path.
func TestValueEdgeDoesNotDeliverMessages(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	inputs := make(chan int, 1)
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			inputs <- len(in)
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), constNode("konst", 1.0, &runs), consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("konst", "konst"),
			{ID: "c", Type: "consumer", Params: []string{"threshold"}}},
		[]flow.Edge{gedge("t", "out", "c", "in"), valueEdge("konst", "out", "c", "threshold")},
	)
	waitDone(t, startRun(t, e, g))
	assert.Equal(t, 1, <-inputs,
		"only the trigger's message may arrive: a value edge supplies config and must never appear on the message path")
}

// TestValueEdgeRejectsCycle covers a parameter chain that loops: the run fails
// naming the cycle rather than spinning.
func TestValueEdgeRejectsCycle(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	spec := []engine.FieldSpec{{ID: "v", Type: flow.FieldNumber}}
	pureA := &stubNode{typ: "pa", pure: true, outputs: outPorts, configSpec: spec, fn: passthrough("out")}
	pureB := &stubNode{typ: "pb", pure: true, outputs: outPorts, configSpec: spec, fn: passthrough("out")}
	consumer := &stubNode{typ: "consumer", inputs: []string{"in"}, outputs: outPorts,
		configSpec: spec, fn: passthrough("out")}

	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), pureA, pureB, consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"),
			{ID: "a", Type: "pa", Params: []string{"v"}},
			{ID: "b", Type: "pb", Params: []string{"v"}},
			{ID: "c", Type: "consumer", Params: []string{"v"}}},
		[]flow.Edge{
			gedge("t", "out", "c", "in"),
			valueEdge("a", "out", "c", "v"),
			valueEdge("b", "out", "a", "v"),
			valueEdge("a", "out", "b", "v"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equal(t, "failed", snap.Status, "a parameter chain that loops must fail the run rather than spin pulling forever")
	assert.Contains(t, snap.Error, "cycle",
		"the error must name the cycle: an author looking at three wired fields has no other way to see it")
}

// TestValueEdgeCycleAcrossParallelBranches covers a loop between two pure
// nodes read by consumers on parallel branches. Each branch can start
// evaluating its own half before reaching the other, and the in-flight
// sharing that lets parallel branches reuse one evaluation must not leave
// them waiting on each other: the run fails naming the cycle, as it does with
// one consumer. A gate pulled first by each half holds both halves in flight
// until the other has started, which is the interleaving that deadlocked.
func TestValueEdgeCycleAcrossParallelBranches(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	spec := []engine.FieldSpec{{ID: "v", Type: flow.FieldJSON, Port: "any", Multi: true}}
	var started sync.WaitGroup
	started.Add(2)
	gate := &stubNode{typ: "gate", pure: true, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			started.Done()
			both := make(chan struct{})
			go func() { started.Wait(); close(both) }()
			select {
			case <-both:
			case <-time.After(time.Second):
			}
			return map[string]*engine.Msg{"out": {Payload: 1}}, nil
		}}
	half := &stubNode{typ: "half", pure: true, outputs: outPorts, configSpec: spec, fn: passthrough("out")}
	consumer := &stubNode{typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: spec, fn: passthrough("out")}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), gate, half, consumer)

	bind := func(id, src, tgt string) flow.Edge {
		e := valueEdge(src, "out", tgt, "v")
		e.ID = id
		return e
	}
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("gx", "gate"), gnode("gy", "gate"),
			gnode("x", "half"), gnode("y", "half"), gnode("c1", "consumer"), gnode("c2", "consumer")},
		[]flow.Edge{
			gedge("t", "out", "c1", "in"), gedge("t", "out", "c2", "in"),
			bind("x-c1", "x", "c1"), bind("y-c2", "y", "c2"),
			// The gate comes first in edge order, so each half pulls it before
			// pulling the other half.
			bind("gx-x", "gx", "x"), bind("y-x", "y", "x"),
			bind("gy-y", "gy", "y"), bind("x-y", "x", "y"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equal(t, "failed", snap.Status, "a parameter loop split across parallel branches must fail the run, not hang it")
	assert.Contains(t, snap.Error, "cycle", "the error must name the cycle, as it does for a single consumer")
}

// TestValueEdgeReusesOutputOfNodeWithErrorBranch covers the reuse rule for the
// shape a script node has: one result port plus a failure branch. A failure
// branch carries no result a parameter could read, so it must not count toward
// "this node has more than one output" — otherwise a script on the message path
// would be executed a second time to supply a value it already produced.
func TestValueEdgeReusesOutputOfNodeWithErrorBranch(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	producer := &stubNode{
		typ: "producer", inputs: []string{"in"}, outputs: errPorts, pure: true,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			atomic.AddInt32(&runs, 1)
			return map[string]*engine.Msg{"out": {Payload: 7.0}}, nil
		},
	}
	got := make(chan float64, 1)
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Threshold float64 `json:"threshold"`
			}
			_ = ec.Config(&cfg)
			got <- cfg.Threshold
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), producer, consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("p", "producer"),
			{ID: "c", Type: "consumer", Params: []string{"threshold"}}},
		[]flow.Edge{
			gedge("t", "out", "p", "in"),
			gedge("p", "out", "c", "in"),
			valueEdge("p", "out", "c", "threshold"),
		},
	)
	waitDone(t, startRun(t, e, g))
	assert.Equal(t, 7.0, <-got, "the parameter must read the value the producer already emitted")
	assert.Equal(t, int32(1), runs,
		"a failure branch carries no result, so it must not count as a second output port and force a re-run")
}

// TestValueEdgeRejectsFanOutSource covers a wire that cannot mean anything: a
// node emitting several messages on a port has no single value to bind, and
// quietly taking the first would look like a working parameter that drops the
// rest.
func TestValueEdgeRejectsFanOutSource(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	splitter := &stubNode{
		typ: "splitter", outputs: outPorts, pure: true,
		fanFn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
			return map[string][]*engine.Msg{"out": {{Payload: 1.0}, {Payload: 2.0}}}, nil
		},
	}
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: passthrough("out"),
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), splitter, consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("s", "splitter"),
			{ID: "c", Type: "consumer", Params: []string{"threshold"}}},
		[]flow.Edge{
			gedge("t", "out", "c", "in"),
			valueEdge("s", "out", "c", "threshold"),
		},
	)
	snap := waitDone(t, startRun(t, e, g))
	require.Equal(t, "failed", snap.Status,
		"a fan-out source has no single value to bind, and silently taking the first would look like a working parameter")
	assert.Contains(t, snap.Error, "single value",
		"the error must explain that the source emits several, or the wire looks merely broken")
}

// TestValueEdgeReusesMessagePathOutput covers a node that already ran on the
// message path and is also bound to a parameter. Its recorded output is reused
// rather than the node being run a second time — which matters because a pure
// source may still be an HTTP request or a file read.
func TestValueEdgeReusesMessagePathOutput(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var runs int32
	producer := &stubNode{
		typ: "producer", inputs: []string{"in"}, outputs: outPorts, pure: true,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			atomic.AddInt32(&runs, 1)
			return map[string]*engine.Msg{"out": {Payload: 5.0}}, nil
		},
	}
	got := make(chan float64, 1)
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Threshold float64 `json:"threshold"`
			}
			_ = ec.Config(&cfg)
			got <- cfg.Threshold
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), producer, consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("p", "producer"),
			{ID: "c", Type: "consumer", Params: []string{"threshold"}}},
		[]flow.Edge{
			gedge("t", "out", "p", "in"),
			gedge("p", "out", "c", "in"),
			valueEdge("p", "out", "c", "threshold"),
		},
	)
	waitDone(t, startRun(t, e, g))
	assert.Equal(t, 5.0, <-got, "the parameter must read the value the producer already emitted")
	assert.Equal(t, int32(1), runs,
		"the message-path output must be reused: a pure source may still be an HTTP request or a file read")
}

// TestValueEdgeIgnoresFailureBranchOutput covers a node that failed on the
// message path with its error branch wired. What it emitted then is the error
// message, which carries the payload the node was handed, not a result. A
// parameter bound to its result port used to read that payload as though the
// node had produced it, so a consumer on the error branch got the unparsed
// input in place of the value it asked for.
func TestValueEdgeIgnoresFailureBranchOutput(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	parser := &stubNode{
		typ: "parser", inputs: []string{"in"}, outputs: errPorts, pure: true,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			return nil, assert.AnError
		},
	}
	got := make(chan float64, 1)
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			var cfg struct {
				Threshold float64 `json:"threshold"`
			}
			if err := ec.Config(&cfg); err != nil {
				return nil, err
			}
			got <- cfg.Threshold
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), parser, consumer)
	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("p", "parser"),
			{ID: "c", Type: "consumer", Params: []string{"threshold"}}},
		[]flow.Edge{
			gedge("t", "out", "p", "in"),
			gedge("p", "error", "c", "in"),
			valueEdge("p", "out", "c", "threshold"),
		},
	)
	snap := waitDone(t, startRun(t, e, g, func(o *engine.RunOptions) {
		o.InitialMsg = &engine.Msg{Payload: 99.0}
	}))

	select {
	case v := <-got:
		require.FailNowf(t, "the consumer read a parameter the source never produced",
			"threshold arrived as %v, the payload the failed node was handed", v)
	default:
	}
	assert.Equal(t, "failed", snap.Status,
		"a parameter whose source produced no result cannot be supplied, so the consumer must fail rather than run on something else")
	assert.Contains(t, snap.Error, "threshold",
		"the failure must name the parameter that could not be supplied")
}

// TestFreshParameterKeepsOrdinarySnapshot proves that refreshing a dependency
// chain does not replace the snapshot other consumers share within the run.
func TestFreshParameterKeepsOrdinarySnapshot(t *testing.T) {
	var runs atomic.Int32
	source := &stubNode{typ: "changing", pure: true, outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		return map[string]*engine.Msg{"out": {Payload: float64(runs.Add(1))}}, nil
	}}
	chain := &stubNode{typ: "chain", pure: true, outputs: outPorts, configSpec: paramSpec, fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		var cfg struct {
			Threshold float64 `json:"threshold"`
		}
		if err := ec.Config(&cfg); err != nil {
			return nil, err
		}
		return map[string]*engine.Msg{"out": {Payload: cfg.Threshold * 10}}, nil
	}}
	observed := make(chan []any, 1)
	consumer := &stubNode{typ: "observer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec, fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		var before, after struct {
			Threshold float64 `json:"threshold"`
		}
		if err := ec.Config(&before); err != nil {
			return nil, err
		}
		refresher := ec.(engine.ParameterRefresher)
		fresh, bound, err := refresher.RefreshParameter(ctx, "threshold")
		if err != nil {
			return nil, err
		}
		if err := ec.Config(&after); err != nil {
			return nil, err
		}
		observed <- []any{before.Threshold, fresh, after.Threshold, bound}
		return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
	}}
	e, _ := testEngine(t, engine.NewFakeClock(time.Now()), engine.Options{}, triggerStub(), source, chain, consumer)
	g := graph([]flow.Node{gnode("t", "trigger"), gnode("s", "changing"), {ID: "chain", Type: "chain", Params: []string{"threshold"}}, {ID: "c", Type: "observer", Params: []string{"threshold"}}}, []flow.Edge{gedge("t", "out", "c", "in"), valueEdge("s", "out", "chain", "threshold"), valueEdge("chain", "out", "c", "threshold")})
	snap := waitDone(t, startRun(t, e, g))
	require.Equal(t, "succeeded", snap.Status, snap.Error)
	assert.Equal(t, []any{float64(10), float64(20), float64(10), true}, <-observed, "a fresh chain must bypass every prior pull while preserving the ordinary snapshot")
	assert.Equal(t, int32(2), runs.Load(), "each independent observation evaluates its source once")
}

// TestValueEdgeOverlaysNullConfig locks that a wired parameter reaches a node
// whose stored config is JSON null, the form an API client writes for a node
// it left unconfigured. The overlay decoded null into a nil map and wrote the
// pulled value into it, so the node failed with a panic instead of running.
func TestValueEdgeOverlaysNullConfig(t *testing.T) {
	cases := []struct {
		name   string
		config json.RawMessage
	}{
		{name: "null config", config: json.RawMessage(`null`)},
		{name: "absent config", config: nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := engine.NewFakeClock(time.Now())
			var runs int32
			got := make(chan float64, 1)
			consumer := &stubNode{
				typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
				fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
					var cfg struct {
						Threshold float64 `json:"threshold"`
					}
					if err := ec.Config(&cfg); err != nil {
						return nil, err
					}
					got <- cfg.Threshold
					return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
				},
			}
			e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), constNode("konst", 21.5, &runs), consumer)
			g := graph(
				[]flow.Node{
					gnode("t", "trigger"),
					gnode("konst", "konst"),
					{ID: "c", Type: "consumer", Config: tc.config, Params: []string{"threshold"}},
				},
				[]flow.Edge{gedge("t", "out", "c", "in"), valueEdge("konst", "out", "c", "threshold")},
			)

			snap := waitDone(t, startRun(t, e, g))
			require.Equalf(t, "succeeded", snap.Status, "a node without stored config must still take its wired parameter: %s", snap.Error)
			select {
			case v := <-got:
				assert.Equal(t, 21.5, v, "the pulled value is the only config the node has, so it must arrive")
			default:
				require.FailNow(t, "the consumer never read its config")
			}
		})
	}
}

// TestCancelDuringParameterPullIsCanceled locks the rule that cancellation
// never takes the failure path. A run canceled while a consumer was still
// pulling its parameter settled as failed with "context canceled" and started
// the flow's error handler, where a cancel during the consumer's own
// execution settles as canceled.
func TestCancelDuringParameterPullIsCanceled(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	pulling := make(chan struct{})
	slow := &stubNode{
		typ: "slow", pure: true, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			close(pulling)
			<-ctx.Done()
			return nil, ctx.Err()
		},
	}
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts, configSpec: paramSpec,
		fn: passthrough("out"),
	}
	e, pub := testEngine(t, fc, engine.Options{}, triggerStub(), slow, consumer)
	g := graph(
		[]flow.Node{
			gnode("t", "trigger"),
			gnode("slow", "slow"),
			{ID: "c", Type: "consumer", Params: []string{"threshold"}},
		},
		[]flow.Edge{gedge("t", "out", "c", "in"), valueEdge("slow", "out", "c", "threshold")},
	)

	h := startRun(t, e, g)
	select {
	case <-pulling:
	case <-time.After(waitTimeout):
		require.FailNow(t, "the consumer never pulled its parameter, so there is no pull to cancel")
	}
	require.True(t, e.CancelRun(h.ID), "the run must be active to cancel it")

	snap := waitDone(t, h)
	assert.Equal(t, "canceled", snap.Status,
		"a cancel that lands during a parameter pull is still a cancel, not a node failure")
	assert.NotEqual(t, "failed", pub.lastStates()["c"],
		"the consumer was abandoned, not failed, so its last state must not say failed")
}
