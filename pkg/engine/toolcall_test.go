package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolStub builds a stub with the tool node's shape. The engine recognizes it
// by flow.NodeTypeTool: call traffic is injected on its behalf and returns
// are intercepted. Its Execute only ever runs as a pull, answering the
// definition a caller reads over the tools value edge.
func toolStub() *stubNode {
	return &stubNode{
		typ: flow.NodeTypeTool, pure: true,
		inputs: []string{flow.ToolReturnPort}, optional: []string{flow.ToolReturnPort},
		outputs: []flow.PortSpec{
			{ID: "out", Type: "ai-tool"},
			{ID: flow.ToolCallPort, Type: flow.PortObject},
		},
		fn: func(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			return map[string]*engine.Msg{"out": {Payload: "definition"}}, nil
		},
	}
}

// toolGraphNode places a tool node declaring one "city" argument.
func toolGraphNode() flow.Node {
	return flow.Node{ID: "tool", Type: flow.NodeTypeTool,
		Config: json.RawMessage(`{"name":"lookup","tool_args":[{"id":"city"}]}`)}
}

// toolPulledBy wires the tool's definition into a caller's tools input the way
// an agent reads it. That value edge is what places the tool and its call
// branch in the run: a tool nothing pulls is a draft left on the canvas, and
// calling it by id fails as not in the graph.
func toolPulledBy(caller string) flow.Edge {
	return flow.Edge{ID: "tools", Source: "tool", SourceHandle: "out",
		Target: caller, TargetHandle: flow.ParamPortID("tools"), Kind: flow.EdgeValue}
}

// TestToolCallResolvesPerCallArguments is the call/return loop end to end: a
// node calls the same graph-backed tool twice with different arguments, the
// branch reads the argument through a value edge on the tool's arg port, and
// each call must see its own value — the run-scoped pull memo must not apply,
// or the second call would observe the first call's city.
func TestToolCallResolvesPerCallArguments(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	var echoRuns int32
	results := make(chan string, 2)

	caller := &stubNode{
		typ: "caller", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			for _, city := range []string{"Oslo", "Bergen"} {
				msg, err := ec.CallTool(ctx, "tool", map[string]any{"city": city}, time.Minute)
				if err != nil {
					return nil, err
				}
				results <- msg.Payload.(string)
			}
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	echo := &stubNode{
		typ: "echo", inputs: []string{"in"}, outputs: outPorts,
		configSpec: []engine.FieldSpec{{ID: "city", Label: "City", Type: flow.FieldString}},
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			atomic.AddInt32(&echoRuns, 1)
			var cfg struct {
				City string `json:"city"`
			}
			if err := ec.Config(&cfg); err != nil {
				return nil, err
			}
			// The group stack rides along the way every real node propagates
			// it; the call frame in it is what routes the return to its call.
			return map[string]*engine.Msg{"out": {Payload: "weather in " + cfg.City, Group: in["in"].Group}}, nil
		},
	}
	e, pub := testEngine(t, fc, engine.Options{}, triggerStub(), caller, echo, toolStub())

	g := graph(
		[]flow.Node{
			gnode("t", "trigger"),
			gnode("caller", "caller"),
			toolGraphNode(),
			{ID: "echo", Type: "echo", Params: []string{"city"}},
		},
		[]flow.Edge{
			gedge("t", "out", "caller", "in"),
			toolPulledBy("caller"),
			gedge("tool", flow.ToolCallPort, "echo", "in"),
			gedge("echo", "out", "tool", flow.ToolReturnPort),
			{ID: "arg", Source: "tool", SourceHandle: flow.ToolArgPortID("city"),
				Target: "echo", TargetHandle: flow.ParamPortID("city"), Kind: flow.EdgeValue},
		},
	)

	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the call/return loop must complete: %s", snap.Error)
	assert.Equal(t, "weather in Oslo", <-results, "the observation is the message wired into the return input")
	assert.Equal(t, "weather in Bergen", <-results,
		"the second call must see its own arguments, not the first call's memoized value")
	assert.Equal(t, int32(2), atomic.LoadInt32(&echoRuns), "each call runs the branch on an instance of its own")
	assert.Equal(t, "succeeded", pub.lastStates()["tool"],
		"a tool node that served calls reports its per-call events and is never settled as skipped")
}

// TestToolArgumentThroughPureNodeIsPerCall locks that the per-call rule holds
// when a pure node sits between the argument port and its consumer. The pure
// node's value depends on the call's arguments, so the run-wide pull memo
// must not answer the second call with what the first one computed.
func TestToolArgumentThroughPureNodeIsPerCall(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	results := make(chan string, 2)
	textSpec := []engine.FieldSpec{{ID: "text", Label: "Text", Type: flow.FieldString}}

	caller := &stubNode{
		typ: "caller", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			for _, city := range []string{"Oslo", "Bergen"} {
				msg, err := ec.CallTool(ctx, "tool", map[string]any{"city": city}, time.Minute)
				if err != nil {
					return nil, err
				}
				results <- msg.Payload.(string)
			}
			return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
		},
	}
	readText := func(ec engine.ExecContext) (string, error) {
		var cfg struct {
			Text string `json:"text"`
		}
		err := ec.Config(&cfg)
		return cfg.Text, err
	}
	upper := &stubNode{
		typ: "upper", pure: true, outputs: outPorts, configSpec: textSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			text, err := readText(ec)
			return map[string]*engine.Msg{"out": {Payload: strings.ToUpper(text)}}, err
		},
	}
	echo := &stubNode{
		typ: "echo", inputs: []string{"in"}, outputs: outPorts, configSpec: textSpec,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			text, err := readText(ec)
			return map[string]*engine.Msg{"out": {Payload: "weather in " + text, Group: in["in"].Group}}, err
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), caller, upper, echo, toolStub())

	g := graph(
		[]flow.Node{
			gnode("t", "trigger"),
			gnode("caller", "caller"),
			toolGraphNode(),
			{ID: "upper", Type: "upper", Params: []string{"text"}},
			{ID: "echo", Type: "echo", Params: []string{"text"}},
		},
		[]flow.Edge{
			gedge("t", "out", "caller", "in"),
			toolPulledBy("caller"),
			gedge("tool", flow.ToolCallPort, "echo", "in"),
			gedge("echo", "out", "tool", flow.ToolReturnPort),
			{ID: "arg", Source: "tool", SourceHandle: flow.ToolArgPortID("city"),
				Target: "upper", TargetHandle: flow.ParamPortID("text"), Kind: flow.EdgeValue},
			{ID: "val", Source: "upper", SourceHandle: "out",
				Target: "echo", TargetHandle: flow.ParamPortID("text"), Kind: flow.EdgeValue},
		},
	)

	snap := waitDone(t, startRun(t, e, g))
	require.Equalf(t, "succeeded", snap.Status, "the call/return loop must complete: %s", snap.Error)
	assert.Equal(t, "weather in OSLO", <-results, "the first call reads its own argument through the pure node")
	assert.Equal(t, "weather in BERGEN", <-results,
		"the second call must re-evaluate the pure node with its own argument")
}

// TestToolBranchFailureBecomesCallError covers failure containment: a branch
// node failing with no error branch wired must resolve the waiting call with
// the error — the agent turns that into an observation — rather than halting
// the run, which would leave the parked caller waiting forever.
func TestToolBranchFailureBecomesCallError(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	observed := make(chan string, 1)

	caller := &stubNode{
		typ: "caller", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			_, err := ec.CallTool(ctx, "tool", map[string]any{}, time.Minute)
			if err == nil {
				return nil, errors.New("the branch failure must surface as the call's error")
			}
			observed <- err.Error()
			return map[string]*engine.Msg{"out": {Payload: "recovered"}}, nil
		},
	}
	failer := &stubNode{
		typ: "failer", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			return nil, errors.New("boom")
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), caller, failer, toolStub())

	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("caller", "caller"), toolGraphNode(), gnode("f", "failer")},
		[]flow.Edge{
			gedge("t", "out", "caller", "in"),
			toolPulledBy("caller"),
			gedge("tool", flow.ToolCallPort, "f", "in"),
		},
	)

	snap := waitDone(t, startRun(t, e, g))
	assert.Equalf(t, "succeeded", snap.Status,
		"a contained branch failure is handled, so the run itself succeeds: %s", snap.Error)
	assert.True(t, snap.HadFailures, "the contained failure still counts as a node failure")
	assert.Contains(t, <-observed, "boom", "the caller reads the branch's own error text")
}

// TestToolCallTimesOutWhenBranchNeverReturns covers the bound on a branch
// that completes without ever reaching the return input: the call's timer —
// on the injectable clock — fails the call, and the run still settles instead
// of hanging on a parked caller nothing will wake.
func TestToolCallTimesOutWhenBranchNeverReturns(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	observed := make(chan string, 1)

	caller := &stubNode{
		typ: "caller", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			_, err := ec.CallTool(ctx, "tool", map[string]any{}, 30*time.Second)
			if err == nil {
				return nil, errors.New("a branch that never returns must time the call out")
			}
			observed <- err.Error()
			return map[string]*engine.Msg{"out": {Payload: "recovered"}}, nil
		},
	}
	silent := &stubNode{
		typ: "silent", inputs: []string{"in"},
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			return nil, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), caller, silent, toolStub())

	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("caller", "caller"), toolGraphNode(), gnode("s", "silent")},
		[]flow.Edge{
			gedge("t", "out", "caller", "in"),
			toolPulledBy("caller"),
			gedge("tool", flow.ToolCallPort, "s", "in"),
		},
	)

	h := startRun(t, e, g)
	// The call's timer is the only clock.After in this run; fire it once it
	// is registered.
	require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the call must register its timeout timer")
	fc.Advance(30 * time.Second)

	snap := waitDone(t, h)
	assert.Equalf(t, "succeeded", snap.Status, "a timed-out call is the caller's to handle: %s", snap.Error)
	assert.Contains(t, <-observed, "timed out", "the error names what happened in words a model can act on")
}

// TestToolBranchOutlivingItsCallStaysContained pins the containment of a
// branch that keeps running after its call timed out. The call leaves the
// pending set at the timeout, but the branch's later failure or argument read
// still belongs to that call: it must not become the unhandled failure that
// fails the run.
func TestToolBranchOutlivingItsCallStaysContained(t *testing.T) {
	cases := []struct {
		name string
		// fails makes the slow node fail after the timeout; otherwise it
		// passes the call message on to a node that reads the call's argument.
		fails bool
	}{
		{name: "late branch failure", fails: true},
		{name: "late argument read", fails: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fc := engine.NewFakeClock(time.Now())
			observed := make(chan string, 1)
			release := make(chan struct{})
			read := make(chan string, 1)

			caller := &stubNode{
				typ: "caller", inputs: []string{"in"}, outputs: outPorts,
				fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
					_, err := ec.CallTool(ctx, "tool", map[string]any{"city": "Oslo"}, 30*time.Second)
					if err == nil {
						return nil, errors.New("a branch held past its timeout must time the call out")
					}
					observed <- err.Error()
					return map[string]*engine.Msg{"out": {Payload: "recovered"}}, nil
				},
			}
			slow := &stubNode{
				typ: "slow", inputs: []string{"in"}, outputs: outPorts,
				fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
					<-release
					if tc.fails {
						return nil, errors.New("branch broke after the call ended")
					}
					return map[string]*engine.Msg{"out": {Payload: "late", Group: in["in"].Group}}, nil
				},
			}
			reader := &stubNode{
				typ: "reader", inputs: []string{"in"},
				configSpec: []engine.FieldSpec{{ID: "city", Label: "City", Type: flow.FieldString}},
				fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
					var cfg struct {
						City string `json:"city"`
					}
					if err := ec.Config(&cfg); err != nil {
						return nil, err
					}
					read <- cfg.City
					return nil, nil
				},
			}
			e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), caller, slow, reader, toolStub())

			g := graph(
				[]flow.Node{gnode("t", "trigger"), gnode("caller", "caller"), toolGraphNode(),
					gnode("slow", "slow"), {ID: "reader", Type: "reader", Params: []string{"city"}}},
				[]flow.Edge{
					gedge("t", "out", "caller", "in"),
					toolPulledBy("caller"),
					gedge("tool", flow.ToolCallPort, "slow", "in"),
					gedge("slow", "out", "reader", "in"),
					{ID: "arg", Source: "tool", SourceHandle: flow.ToolArgPortID("city"),
						Target: "reader", TargetHandle: flow.ParamPortID("city"), Kind: flow.EdgeValue},
				},
			)

			h := startRun(t, e, g)
			require.True(t, fc.BlockUntilAfterCalls(1, waitTimeout), "the call must register its timeout timer")
			fc.Advance(30 * time.Second)
			assert.Contains(t, <-observed, "timed out", "the caller sees the timeout before the branch finishes")
			close(release)

			snap := waitDone(t, h)
			assert.Equalf(t, "succeeded", snap.Status, "a branch that outlives its call is still the call's problem: %s", snap.Error)
			if tc.fails {
				assert.True(t, snap.HadFailures, "the contained failure still counts as a node failure")
				return
			}
			select {
			case city := <-read:
				assert.Equal(t, "Oslo", city, "the branch keeps reading the arguments of the call it serves")
			default:
				assert.Fail(t, "the node after the slow one never read the call's argument")
			}
		})
	}
}

// TestToolArgumentOutsideBranchFails pins the per-call scope of argument
// ports: a node on the main message path binding one has no pending call to
// read from, and the failure must say so rather than yield a stale or empty
// value.
func TestToolArgumentOutsideBranchFails(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	consumer := &stubNode{
		typ: "consumer", inputs: []string{"in"}, outputs: outPorts,
		configSpec: []engine.FieldSpec{{ID: "city", Label: "City", Type: flow.FieldString}},
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			return map[string]*engine.Msg{"out": {Payload: "ran"}}, nil
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), consumer, toolStub())

	g := graph(
		[]flow.Node{gnode("t", "trigger"), toolGraphNode(),
			{ID: "c", Type: "consumer", Params: []string{"city"}}},
		[]flow.Edge{
			gedge("t", "out", "c", "in"),
			{ID: "arg", Source: "tool", SourceHandle: flow.ToolArgPortID("city"),
				Target: "c", TargetHandle: flow.ParamPortID("city"), Kind: flow.EdgeValue},
		},
	)

	snap := waitDone(t, startRun(t, e, g))
	assert.Equal(t, "failed", snap.Status, "an argument read with no call in flight cannot be answered")
	assert.Contains(t, snap.Error, "call branch", "the error names where the argument would have been readable")
}

// TestHaltedRunFailsPendingToolCalls covers a run that stops scheduling while a
// tool call is in flight. An unhandled failure elsewhere halts the run, so the
// branch's next node is never dispatched and nothing can reach the return
// input. The caller used to stay parked until its timeout, holding a failed
// run open for as long as the tool allowed; the call must fail at the halt so
// the run settles. The fake clock is never advanced, so a call that waits on
// its timer never returns.
func TestHaltedRunFailsPendingToolCalls(t *testing.T) {
	fc := engine.NewFakeClock(time.Now())
	observed := make(chan string, 1)
	branchStarted := make(chan struct{})
	releaseBranch := make(chan struct{})

	caller := &stubNode{
		typ: "caller", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			_, err := ec.CallTool(ctx, "tool", map[string]any{}, time.Hour)
			if err == nil {
				return nil, errors.New("a call whose branch can no longer run must fail")
			}
			observed <- err.Error()
			return nil, err
		},
	}
	first := &stubNode{
		typ: "first", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			close(branchStarted)
			<-releaseBranch
			return map[string]*engine.Msg{"out": in["in"]}, nil
		},
	}
	second := &stubNode{typ: "second", inputs: []string{"in"}, outputs: outPorts, fn: passthrough("out")}
	bomb := &stubNode{
		typ: "bomb", inputs: []string{"in"}, outputs: outPorts,
		fn: func(ctx context.Context, ec engine.ExecContext, in map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			<-branchStarted
			return nil, errors.New("boom")
		},
	}
	e, _ := testEngine(t, fc, engine.Options{}, triggerStub(), caller, first, second, bomb, toolStub())

	g := graph(
		[]flow.Node{gnode("t", "trigger"), gnode("caller", "caller"), toolGraphNode(),
			gnode("first", "first"), gnode("second", "second"), gnode("bomb", "bomb")},
		[]flow.Edge{
			gedge("t", "out", "caller", "in"),
			gedge("t", "out", "bomb", "in"),
			toolPulledBy("caller"),
			gedge("tool", flow.ToolCallPort, "first", "in"),
			gedge("first", "out", "second", "in"),
			gedge("second", "out", "tool", flow.ToolReturnPort),
		},
	)

	h := startRun(t, e, g)
	// The run records its error when the loop applies the bomb's failure, which
	// is the halt. Only then may the branch move on.
	require.Eventually(t, func() bool { return h.Snapshot().Error != "" }, waitTimeout, 5*time.Millisecond,
		"the unhandled failure must halt the run while the call is in flight")
	close(releaseBranch)

	snap := waitDone(t, h)
	assert.Equal(t, "failed", snap.Status, "the unhandled failure is what ends the run")
	assert.Contains(t, snap.Error, "boom", "the run reports the failure that halted it, not the call it abandoned")
	select {
	case msg := <-observed:
		assert.Contains(t, msg, "run stopped", "the caller is told why its call ended")
	default:
		assert.Fail(t, "the caller never saw its call fail")
	}
}
