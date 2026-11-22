package engine_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	engineScriptPythonOnce     sync.Once
	engineScriptPythonRuntime  *sandbox.PythonRuntime
	engineScriptPythonCacheDir string
)

// TestMain releases the CPython runtime shared by this package's real-engine
// script cases, matching the runtime lifetime used by the sandbox suite.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "karakuri-engine-python-cache-")
	if err == nil {
		engineScriptPythonCacheDir = dir
	}
	code := m.Run()
	if engineScriptPythonRuntime != nil {
		_ = engineScriptPythonRuntime.Close(context.Background())
	}
	if engineScriptPythonCacheDir != "" {
		_ = os.RemoveAll(engineScriptPythonCacheDir)
	}
	os.Exit(code)
}

// registerEngineScriptRuntime installs the selected real interpreter and skips
// only Python cases when the external wasm asset is unavailable.
func registerEngineScriptRuntime(t *testing.T, language string) {
	t.Helper()
	sandbox.Register(sandbox.JSRuntime{})
	sandbox.Register(sandbox.ShellRuntime{})
	sandbox.SetDefaults(sandbox.Limits{}, sandbox.NetworkNone)
	if language != sandbox.LangPython {
		return
	}
	engineScriptPythonOnce.Do(func() {
		path := os.Getenv("KARAKURI_TEST_PYTHON_WASM")
		if path == "" {
			path = sandbox.ResolvePythonWasm("", []string{"../../build/runtimes"})
		}
		if path != "" {
			engineScriptPythonRuntime = sandbox.NewPythonRuntime(path, engineScriptPythonCacheDir, 0)
		}
	})
	if engineScriptPythonRuntime == nil {
		t.Skip("python.wasm not installed; set KARAKURI_TEST_PYTHON_WASM or run make runtimes")
	}
	sandbox.Register(engineScriptPythonRuntime)
}

// scriptTestConfig builds a script node's stored configuration without quoting
// each language's source by hand.
func scriptTestConfig(t *testing.T, language, source string) json.RawMessage {
	t.Helper()
	config, err := json.Marshal(map[string]any{"language": language, "script": source})
	require.NoError(t, err)
	return config
}

// testRealScriptEngine registers the actual ScriptNode beside compact fixtures
// so language bindings cross the same scheduler and invocation path as a flow.
func testRealScriptEngine(t *testing.T, stubs ...*stubNode) (*engine.Engine, *fakePub) {
	t.Helper()
	registry := engine.NewRegistry()
	registry.Register(core.ScriptNode{})
	for _, stub := range stubs {
		registry.Register(stub)
	}
	pub := &fakePub{}
	e := engine.New(engine.Options{
		Registry: registry, Clock: engine.NewFakeClock(time.Now()), Publisher: pub, MaxParallelNodes: 1,
	})
	e.Start()
	t.Cleanup(e.Stop)
	return e, pub
}

// TestScriptInvocationObservesOutputsWithoutGraphDelivery exercises a real run:
// direct params reach Config, mid-execution and returned messages merge, empty
// ports stay arrays, and invocation events carry their own identity.
func TestScriptInvocationObservesOutputsWithoutGraphDelivery(t *testing.T) {
	var executions atomic.Int32
	callee := &stubNode{
		typ: "callee",
		outputs: []flow.PortSpec{
			{ID: "out", Type: flow.PortAny},
			{ID: "empty", Type: flow.PortAny},
			{ID: "error", Type: flow.PortAny, Kind: flow.PortKindError},
		},
		configSpec: []engine.FieldSpec{{ID: "value", Type: flow.FieldString, Port: flow.PortString, Required: true}},
		fanFn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
			executions.Add(1)
			var cfg struct {
				Value string `json:"value"`
			}
			if err := ec.Config(&cfg); err != nil {
				return nil, err
			}
			ec.Emit("out", &engine.Msg{Payload: "early", Topic: "first"})
			return map[string][]*engine.Msg{"out": {{Payload: cfg.Value, Topic: "second"}}}, nil
		},
	}
	var invocation *nodeapi.NodeCallResult
	caller := &stubNode{typ: "caller", outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		invoker, ok := ec.(nodeapi.ScriptNodeInvoker)
		if !ok {
			return nil, errors.New("execution context has no script invoker")
		}
		result, err := invoker.CallScriptNode(ctx, nodeapi.NodeCall{
			Type: "callee", Config: json.RawMessage(`{}`),
			Options: nodeapi.NodeCallOptions{ID: "stable", Params: map[string]any{"value": "from-param"}},
		})
		if err != nil {
			return nil, err
		}
		invocation = result
		return map[string]*engine.Msg{"out": {Payload: "caller-done"}}, nil
	}}

	e, pub := testEngine(t, engine.NewFakeClock(time.Now()), engine.Options{}, triggerStub(), caller, callee)
	handle := startRun(t, e, graph(
		[]flow.Node{gnode("t", "trigger"), gnode("caller", "caller")},
		[]flow.Edge{gedge("t", "out", "caller", "in")},
	))
	snapshot := waitDone(t, handle)
	require.Equal(t, engine.RunStatusSucceeded, snapshot.Status, snapshot.Error)
	require.NotNil(t, invocation)
	assert.Equal(t, 1, invocation.Sequence)
	assert.Equal(t, 1, invocation.Attempts)
	assert.Equal(t, "stable", invocation.InvocationID)
	require.Len(t, invocation.Outputs["out"], 2)
	assert.Equal(t, "early", invocation.Outputs["out"][0].Payload)
	assert.Equal(t, "from-param", invocation.Outputs["out"][1].Payload)
	assert.NotNil(t, invocation.Outputs["empty"], "an empty ordinary output must be an array, not null")
	assert.Empty(t, invocation.Outputs["empty"])
	assert.NotContains(t, invocation.Outputs, "error")
	assert.Equal(t, int32(1), executions.Load(), "the synthetic node must not also be scheduled as a graph node")

	pub.mu.Lock()
	defer pub.mu.Unlock()
	var terminal map[string]any
	for _, event := range pub.events {
		if event.Type == "node_state" && strings.HasPrefix(event.Payload["node_id"].(string), "script-call:") && event.Payload["state"] == engine.NodeStateSucceeded {
			terminal = event.Payload
		}
	}
	require.NotNil(t, terminal, "the synthetic terminal event must be published")
	assert.Equal(t, "caller", terminal["parent_node_id"])
	assert.Equal(t, "callee", terminal["node_type"])
	assert.Equal(t, "stable", terminal["invocation_id"])
	assert.EqualValues(t, 1, terminal["sequence"])
	assert.NotEqual(t, "callee", terminal["node_id"], "history identity must be distinct from graph and store identities")
}

// TestScriptInvocationContainsExhaustedRetry verifies per-call retries and a
// caught failure: attempts remain visible and HadFailures is set without
// stopping the caller or scheduling an unhandled graph failure.
func TestScriptInvocationContainsExhaustedRetry(t *testing.T) {
	var attempts atomic.Int32
	callee := &stubNode{
		typ: "flaky", outputs: outPorts, retry: &flow.RetryPolicy{MaxAttempts: 3},
		fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
			attempts.Add(1)
			return nil, &engine.ExecutionError{
				Message: "still unavailable", Retryable: true,
				Details: map[string]any{"kind": "temporary"},
			}
		},
	}
	var caught *nodeapi.NodeCallError
	caller := &stubNode{typ: "caller", outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
		invoker := ec.(nodeapi.ScriptNodeInvoker)
		_, err := invoker.CallScriptNode(ctx, nodeapi.NodeCall{Type: "flaky", Config: json.RawMessage(`{}`)})
		if !errors.As(err, &caught) {
			return nil, fmt.Errorf("expected a classified call error, got %w", err)
		}
		return map[string]*engine.Msg{"out": {Payload: "fallback"}}, nil
	}}

	e, _ := testEngine(t, engine.NewFakeClock(time.Now()), engine.Options{}, triggerStub(), caller, callee)
	handle := startRun(t, e, graph(
		[]flow.Node{gnode("t", "trigger"), gnode("caller", "caller")},
		[]flow.Edge{gedge("t", "out", "caller", "in")},
	))
	snapshot := waitDone(t, handle)
	assert.Equal(t, engine.RunStatusSucceeded, snapshot.Status, snapshot.Error)
	assert.True(t, snapshot.HadFailures, "a caught node failure remains visible on the run")
	assert.Equal(t, int32(3), attempts.Load())
	require.NotNil(t, caught)
	assert.Equal(t, 3, caught.Attempts)
	assert.True(t, caught.Retryable)
	assert.Equal(t, "temporary", caught.Details["kind"])
}

// TestNodeTestScriptUsesFullCatalog proves isolated node tests retain the
// callable registry and forward the invocation capability through capture in
// every language, using only the isolated engine's temporary stores.
func TestNodeTestScriptUsesFullCatalog(t *testing.T) {
	tests := []struct {
		language string
		source   string
	}{
		{language: sandbox.LangJavaScript, source: `return nodes["callee"]({}).out[0].payload;`},
		{language: sandbox.LangPython, source: `result = nodes["callee"]({})["out"][0]["payload"]`},
		{language: sandbox.LangBash, source: "reply=$(node callee '{}') || exit 1\njsonget out.0.payload \"$reply\"\n"},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			registerEngineScriptRuntime(t, tc.language)
			var executions atomic.Int32
			callee := &stubNode{typ: "callee", outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				executions.Add(1)
				return map[string]*engine.Msg{"out": {Payload: "from-call"}}, nil
			}}
			registry := engine.NewRegistry()
			registry.Register(core.ScriptNode{})
			registry.Register(callee)
			e := engine.New(engine.Options{Registry: registry, MaxParallelNodes: 1})
			t.Cleanup(e.Stop)
			result, err := e.TestNode(context.Background(), engine.RunOptions{
				Graph: &flow.Graph{Nodes: []flow.Node{{
					ID: "script", Type: "script", Config: scriptTestConfig(t, tc.language, tc.source),
				}}},
				StartNodeID: "script",
				SeedInputs:  map[string]*engine.Msg{"in": {}},
			})
			require.NoError(t, err)
			assert.Equal(t, engine.RunStatusSucceeded, result.Status, result.Error)
			assert.Equal(t, int32(1), executions.Load())
			require.Len(t, result.Outputs["out"], 1)
			var output struct {
				Payload string `json:"payload"`
			}
			require.NoError(t, json.Unmarshal(result.Outputs["out"][0], &output))
			assert.Equal(t, "from-call", output.Payload)
		})
	}
}

// TestPythonAndBashScriptInvocationRetriesAndHistory drives caught retry
// exhaustion and a fallback through real interpreters. The run must retain each
// synthetic call's attempts, sequence, and parent identity without stopping the
// successfully handled script.
func TestPythonAndBashScriptInvocationRetriesAndHistory(t *testing.T) {
	tests := []struct {
		language string
		source   string
	}{
		{
			language: sandbox.LangPython,
			source: `
import karakuri
try:
    nodes["flaky"]({})
except karakuri.NodeCallError as error:
    attempts = error.attempts
fallback = nodes["callee"]({})
result = {"attempts": attempts, "payload": fallback["out"][0]["payload"]}
`,
		},
		{
			language: sandbox.LangBash,
			source: `
				if failure=$(node flaky '{}'); then
					log "expected the flaky call to fail"
					exit 1
				fi
				attempts=$(jsonget attempts "$failure")
				fallback=$(node callee '{}') || exit 1
				payload=$(jsonget out.0.payload "$fallback")
				printf '{"attempts":%s,"payload":"%s"}\n' "$attempts" "$payload"
			`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			registerEngineScriptRuntime(t, tc.language)
			var attempts, fallbacks atomic.Int32
			flaky := &stubNode{
				typ: "flaky", outputs: outPorts, retry: &flow.RetryPolicy{MaxAttempts: 3},
				fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
					attempts.Add(1)
					return nil, &engine.ExecutionError{Message: "still unavailable", Retryable: true}
				},
			}
			callee := &stubNode{typ: "callee", outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				fallbacks.Add(1)
				return map[string]*engine.Msg{"out": {Payload: "fallback"}}, nil
			}}
			observed := make(chan map[string]any, 1)
			observer := &stubNode{typ: "observer", inputs: []string{"in"}, outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				payload, ok := inputs["in"].Payload.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("script result is %T, want an object", inputs["in"].Payload)
				}
				observed <- payload
				return map[string]*engine.Msg{"out": {Payload: "observed"}}, nil
			}}
			e, pub := testRealScriptEngine(t, triggerStub(), flaky, callee, observer)
			handle := startRun(t, e, graph(
				[]flow.Node{
					gnode("t", "trigger"),
					{ID: "script", Type: "script", Config: scriptTestConfig(t, tc.language, tc.source)},
					gnode("observer", "observer"),
				},
				[]flow.Edge{
					gedge("t", "out", "script", "in"),
					gedge("script", "out", "observer", "in"),
				},
			))
			snapshot := waitDone(t, handle)
			require.Equal(t, engine.RunStatusSucceeded, snapshot.Status, snapshot.Error)
			assert.True(t, snapshot.HadFailures, "the caught call failure must remain visible on the run")
			assert.Equal(t, int32(3), attempts.Load(), "the called node's own policy controls its attempts")
			assert.Equal(t, int32(1), fallbacks.Load())
			result := <-observed
			assert.Equal(t, float64(3), result["attempts"], "the binding must expose the exhausted attempt count")
			assert.Equal(t, "fallback", result["payload"])

			terminals := make(map[string]map[string]any)
			pub.mu.Lock()
			for _, event := range pub.events {
				if event.Type != "node_state" {
					continue
				}
				id, _ := event.Payload["node_id"].(string)
				state, _ := event.Payload["state"].(string)
				if !strings.HasPrefix(id, "script-call:") || (state != engine.NodeStateFailed && state != engine.NodeStateSucceeded) {
					continue
				}
				nodeType, _ := event.Payload["node_type"].(string)
				terminals[nodeType] = event.Payload
			}
			pub.mu.Unlock()
			require.Len(t, terminals, 2, "the failed and fallback calls need distinct terminal history")
			failed := terminals["flaky"]
			require.NotNil(t, failed)
			assert.Equal(t, "script", failed["parent_node_id"])
			assert.EqualValues(t, 1, failed["sequence"])
			assert.Equal(t, "call-1", failed["invocation_id"])
			assert.EqualValues(t, 3, failed["attempt"])
			assert.Equal(t, true, failed["handled"])
			succeeded := terminals["callee"]
			require.NotNil(t, succeeded)
			assert.Equal(t, "script", succeeded["parent_node_id"])
			assert.EqualValues(t, 2, succeeded["sequence"])
			assert.Equal(t, "call-2", succeeded["invocation_id"])
			assert.EqualValues(t, 1, succeeded["attempt"])
		})
	}
}

// TestPythonAndBashScriptCallsSuppressOuterReplay proves the real retry loop
// honors the ScriptNode's nonretryable failure after a successful call, rather
// than merely checking the error classification outside the engine.
func TestPythonAndBashScriptCallsSuppressOuterReplay(t *testing.T) {
	tests := []struct {
		language string
		source   string
	}{
		{language: sandbox.LangPython, source: "nodes[\"callee\"]({})\nraise RuntimeError(\"after action\")\n"},
		{language: sandbox.LangBash, source: "node callee '{}' >/dev/null || exit 1\nexit 1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			registerEngineScriptRuntime(t, tc.language)
			var executions atomic.Int32
			callee := &stubNode{typ: "callee", outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				executions.Add(1)
				return map[string]*engine.Msg{"out": {Payload: "done"}}, nil
			}}
			e, pub := testRealScriptEngine(t, triggerStub(), callee)
			handle := startRun(t, e, graph(
				[]flow.Node{
					gnode("t", "trigger"),
					{
						ID: "script", Type: "script", Config: scriptTestConfig(t, tc.language, tc.source),
						Retry: &flow.RetryPolicy{MaxAttempts: 3},
					},
				},
				[]flow.Edge{gedge("t", "out", "script", "in")},
			))
			snapshot := waitDone(t, handle)
			require.Equal(t, engine.RunStatusFailed, snapshot.Status)
			assert.Equal(t, int32(1), executions.Load(), "an outer script retry must not replay the successful action")
			assert.Equal(t, []string{engine.NodeStateRunning, engine.NodeStateFailed}, pub.nodeStates()["script"],
				"the script must fail on its first outer attempt without a retrying transition")
		})
	}
}

// TestPythonAndBashPureScriptsRejectActions exercises a pulled ScriptNode in
// both new languages. Each binding catches a zero-attempt rejection and the
// impure target never executes, while the value edge still receives a result.
func TestPythonAndBashPureScriptsRejectActions(t *testing.T) {
	type pureObservation struct {
		attempts int
		nodeType string
	}
	tests := []struct {
		language string
		source   string
	}{
		{
			language: sandbox.LangPython,
			source: `
import karakuri
try:
    nodes["callee"]({})
except karakuri.NodeCallError as error:
    result = {"attempts": error.attempts, "node_type": error.node_type}
`,
		},
		{
			language: sandbox.LangBash,
			source: `
				if failure=$(node callee '{}'); then
					log "expected the pure call to be rejected"
					exit 1
				fi
				attempts=$(jsonget attempts "$failure")
				node_type=$(jsonget node_type "$failure")
				printf '{"attempts":%s,"node_type":"%s"}\n' "$attempts" "$node_type"
			`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			registerEngineScriptRuntime(t, tc.language)
			var actions atomic.Int32
			callee := &stubNode{typ: "callee", outputs: outPorts, fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
				actions.Add(1)
				return map[string]*engine.Msg{"out": {Payload: "must not run"}}, nil
			}}
			observed := make(chan pureObservation, 1)
			consumer := &stubNode{
				typ: "consumer", inputs: []string{"in"}, outputs: outPorts,
				configSpec: []engine.FieldSpec{{ID: "observation", Type: flow.FieldJSON}},
				fn: func(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string]*engine.Msg, error) {
					var config struct {
						Observation struct {
							Attempts int    `json:"attempts"`
							NodeType string `json:"node_type"`
						} `json:"observation"`
					}
					if err := ec.Config(&config); err != nil {
						return nil, err
					}
					observed <- pureObservation{attempts: config.Observation.Attempts, nodeType: config.Observation.NodeType}
					return map[string]*engine.Msg{"out": {Payload: "observed"}}, nil
				},
			}
			e, _ := testRealScriptEngine(t, triggerStub(), callee, consumer)
			handle := startRun(t, e, graph(
				[]flow.Node{
					gnode("t", "trigger"),
					{ID: "script", Type: "script", Config: scriptTestConfig(t, tc.language, tc.source)},
					{ID: "consumer", Type: "consumer", Params: []string{"observation"}},
				},
				[]flow.Edge{
					gedge("t", "out", "consumer", "in"),
					valueEdge("script", "out", "consumer", "observation"),
				},
			))
			snapshot := waitDone(t, handle)
			require.Equal(t, engine.RunStatusSucceeded, snapshot.Status, snapshot.Error)
			assert.True(t, snapshot.HadFailures, "the caught pure-call rejection remains visible on the run")
			assert.Zero(t, actions.Load(), "pure rejection must happen before the impure node executes")
			observation := <-observed
			assert.Zero(t, observation.attempts, "a validation rejection has no execution attempts")
			assert.Equal(t, "callee", observation.nodeType, "the script must return the caught call identity through the value edge")
		})
	}
}
