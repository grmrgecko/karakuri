package core

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	scriptCallPythonOnce     sync.Once
	scriptCallPythonRuntime  *sandbox.PythonRuntime
	scriptCallPythonCacheDir string
)

// TestMain releases the CPython runtime shared by this package's script tests.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "karakuri-core-python-cache-")
	if err == nil {
		scriptCallPythonCacheDir = dir
	}
	code := m.Run()
	if scriptCallPythonRuntime != nil {
		_ = scriptCallPythonRuntime.Close(context.Background())
	}
	if scriptCallPythonCacheDir != "" {
		_ = os.RemoveAll(scriptCallPythonCacheDir)
	}
	os.Exit(code)
}

// registerScriptCallRuntime installs the selected real interpreter, sharing the
// expensive CPython wasm compilation across all Python cases in this package.
func registerScriptCallRuntime(t *testing.T, language string) {
	t.Helper()
	registerScriptRuntimes(t)
	if language != sandbox.LangPython {
		return
	}
	scriptCallPythonOnce.Do(func() {
		path := os.Getenv("KARAKURI_TEST_PYTHON_WASM")
		if path == "" {
			path = sandbox.ResolvePythonWasm("", []string{"../../../build/runtimes"})
		}
		if path != "" {
			scriptCallPythonRuntime = sandbox.NewPythonRuntime(path, scriptCallPythonCacheDir, 0)
		}
	})
	if scriptCallPythonRuntime == nil {
		t.Skip("python.wasm not installed; set KARAKURI_TEST_PYTHON_WASM or run make runtimes")
	}
	sandbox.Register(scriptCallPythonRuntime)
}

// scriptInvokerTestCtx adds the optional engine capabilities to the ordinary
// in-memory node context and tracks admissions across Execute calls.
type scriptInvokerTestCtx struct {
	*testCtx
	metas []nodeapi.NodeMeta
	call  func(context.Context, nodeapi.NodeCall) (*nodeapi.NodeCallResult, error)
	count int
	began bool
}

func newScriptInvokerTestCtx(t *testing.T, language, source string) *scriptInvokerTestCtx {
	t.Helper()
	registerScriptCallRuntime(t, language)
	config, err := json.Marshal(map[string]any{"language": language, "script": source})
	require.NoError(t, err)
	return &scriptInvokerTestCtx{
		testCtx: newTestCtx(string(config), nil),
		metas:   []nodeapi.NodeMeta{{Type: "test-node", Outputs: []nodeapi.PortSpec{{ID: "out", Type: "any"}}}},
	}
}

func (c *scriptInvokerTestCtx) ScriptNodeFunctions() []nodeapi.NodeMeta { return c.metas }
func (c *scriptInvokerTestCtx) CallScriptNode(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
	c.count++
	if c.call == nil {
		c.began = true
		return &nodeapi.NodeCallResult{NodeType: call.Type, Sequence: c.count, Attempts: 1, Outputs: map[string][]*nodeapi.Msg{}}, nil
	}
	result, err := c.call(ctx, call)
	if result != nil {
		c.began = true
	}
	var callErr *nodeapi.NodeCallError
	if errors.As(err, &callErr) && callErr.Attempts > 0 {
		c.began = true
	}
	return result, err
}
func (c *scriptInvokerTestCtx) ScriptNodeCallCount() int    { return c.count }
func (c *scriptInvokerTestCtx) ScriptNodeCallStarted() bool { return c.began }

// TestScriptCallTrackerCapability limits the mediated invoker to the three flow
// script languages and to contexts that actually received an engine invoker.
func TestScriptCallTrackerCapability(t *testing.T) {
	tests := []struct {
		name     string
		language string
		invoker  bool
		want     bool
	}{
		{name: "JavaScript", language: sandbox.LangJavaScript, invoker: true, want: true},
		{name: "Python", language: sandbox.LangPython, invoker: true, want: true},
		{name: "Bash", language: sandbox.LangBash, invoker: true, want: true},
		{name: "without invoker", language: sandbox.LangJavaScript},
		{name: "unsupported language", language: "ruby", invoker: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := newTestCtx("", nil)
			var ec engine.ExecContext = base
			if tc.invoker {
				ec = &scriptInvokerTestCtx{testCtx: base}
			}
			capability := newScriptCallTracker(ec).capability(tc.language)
			if tc.want {
				assert.NotNil(t, capability)
			} else {
				assert.Nil(t, capability)
			}
		})
	}
}

// TestScriptNodeCallLimitIsFatalEvenWhenCaught proves the 101st call performs
// no action and neither variables nor output commit after the fatal boundary,
// including when the real Python or Bash binding handles the reported failure.
func TestScriptNodeCallLimitIsFatalEvenWhenCaught(t *testing.T) {
	tests := []struct {
		language string
		source   string
	}{
		{
			language: sandbox.LangJavaScript,
			source: `
				for (let i = 0; i < 101; i++) {
					try { nodes.testNode({}); } catch (error) {}
				}
				vars.committed = true;
				return "must not emit";
			`,
		},
		{
			language: sandbox.LangPython,
			source: `
import karakuri
for _ in range(101):
    try:
        nodes.testNode({})
    except karakuri.NodeCallError:
        pass
vars["committed"] = True
result = "must not emit"
`,
		},
		{
			language: sandbox.LangBash,
			source: `
				for ((i=0; i<101; i++)); do
					if node test-node '{}' >/dev/null; then :; else :; fi
				done
				setvar -s run committed true
				printf 'must not emit'
			`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			ec := newScriptInvokerTestCtx(t, tc.language, tc.source)
			outputs, err := ScriptNode{}.Execute(context.Background(), ec, nil)
			require.Error(t, err)
			assert.Nil(t, outputs, "a caught budget failure must not emit a script result")
			assert.Equal(t, 100, ec.count, "exactly 100 calls may reach the invoker")
			var executionErr *engine.ExecutionError
			require.ErrorAs(t, err, &executionErr)
			assert.False(t, executionErr.Retryable)
			var callErr *nodeapi.NodeCallError
			require.ErrorAs(t, err, &callErr)
			assert.True(t, callErr.Fatal)
			_, committed := ec.GetVar("committed")
			assert.False(t, committed, "staged variable writes must not survive the fatal boundary")
		})
	}
}

// TestScriptNodeCallSuppressesOuterRetry covers a later interpreter failure:
// once an action ran, the script error is intentionally nonretryable in every
// language so an outer attempt cannot replay that action.
func TestScriptNodeCallSuppressesOuterRetry(t *testing.T) {
	tests := []struct {
		language string
		source   string
	}{
		{language: sandbox.LangJavaScript, source: `nodes.testNode({}); throw new Error("after action");`},
		{language: sandbox.LangPython, source: "nodes.testNode({})\nraise RuntimeError(\"after action\")\n"},
		{language: sandbox.LangBash, source: "node test-node '{}' >/dev/null || exit 1\nexit 1\n"},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			ec := newScriptInvokerTestCtx(t, tc.language, tc.source)
			_, err := ScriptNode{}.Execute(context.Background(), ec, nil)
			require.Error(t, err)
			var executionErr *engine.ExecutionError
			require.ErrorAs(t, err, &executionErr)
			assert.False(t, executionErr.Retryable)
			assert.Equal(t, 1, ec.count)
		})
	}
}

// TestScriptNodeCallBudgetSpansOuterAttempts starts a fresh ScriptNode.Execute
// with the same context, as the engine retry loop does. Validation-only calls
// did not suppress that retry, but their admissions still consume the budget.
func TestScriptNodeCallBudgetSpansOuterAttempts(t *testing.T) {
	ec := newScriptInvokerTestCtx(t, sandbox.LangJavaScript, `
		for (let i = 0; i < 100; i++) {
			try { nodes.testNode({}); } catch (error) {}
		}
		throw new Error("ordinary retry");
	`)
	ec.call = func(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
		return nil, &nodeapi.NodeCallError{NodeType: call.Type, Cause: errors.New("invalid call")}
	}
	_, firstErr := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.Error(t, firstErr)
	var executionErr *engine.ExecutionError
	assert.False(t, errors.As(firstErr, &executionErr), "no execution began, so the ordinary script retry remains available")
	assert.Equal(t, 100, ec.count)

	_, secondErr := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.Error(t, secondErr)
	var callErr *nodeapi.NodeCallError
	require.ErrorAs(t, secondErr, &callErr)
	assert.True(t, callErr.Fatal)
	assert.Equal(t, 100, ec.count, "the next outer attempt cannot admit another action")
}

// TestScriptNodeWritebackPreservesReentrantWrites verifies canonical diffs in
// every scope while retaining run-null and persistent deletion semantics.
func TestScriptNodeWritebackPreservesReentrantWrites(t *testing.T) {
	ec := newScriptInvokerTestCtx(t, sandbox.LangJavaScript, `
		vars.mine = "run";
		flow.mine = "flow";
		global.mine = "global";
		vars.remove = null;
		delete flow.remove;
		delete global.remove;
		nodes.testNode({});
		return "done";
	`)
	for _, scope := range []string{engine.ScopeRun, engine.ScopeFlow, engine.ScopeGlobal} {
		require.NoError(t, ec.SetScoped(scope, "shared", "before"))
		require.NoError(t, ec.SetScoped(scope, "remove", true))
	}
	ec.call = func(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
		for _, scope := range []string{engine.ScopeRun, engine.ScopeFlow, engine.ScopeGlobal} {
			_ = ec.SetScoped(scope, "shared", "from-called-node")
			_ = ec.SetScoped(scope, "called", true)
		}
		return &nodeapi.NodeCallResult{NodeType: call.Type, Attempts: 1, Outputs: map[string][]*nodeapi.Msg{}}, nil
	}
	_, err := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.NoError(t, err)
	for _, scope := range []string{engine.ScopeRun, engine.ScopeFlow, engine.ScopeGlobal} {
		shared, _ := ec.GetScoped(scope, "shared")
		assert.Equal(t, "from-called-node", shared, scope)
		called, _ := ec.GetScoped(scope, "called")
		assert.Equal(t, true, called, scope)
		mine, _ := ec.GetScoped(scope, "mine")
		assert.Equal(t, scope, mine)
		_, removed := ec.GetScoped(scope, "remove")
		assert.False(t, removed, scope)
	}
}

// TestScriptNodeInterpreterFailureRollsBackOnlyScriptWrites ensures called
// node effects remain while staged script writes are discarded on failure.
func TestScriptNodeInterpreterFailureRollsBackOnlyScriptWrites(t *testing.T) {
	ec := newScriptInvokerTestCtx(t, sandbox.LangJavaScript, `
		vars.mine = true;
		flow.mine = true;
		nodes.testNode({});
		throw new Error("interpretation failed");
	`)
	ec.call = func(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
		ec.SetVar("called", true)
		_ = ec.SetScoped(engine.ScopeFlow, "called", true)
		return &nodeapi.NodeCallResult{NodeType: call.Type, Attempts: 1, Outputs: map[string][]*nodeapi.Msg{}}, nil
	}
	_, err := ScriptNode{}.Execute(context.Background(), ec, nil)
	require.Error(t, err)
	_, runMine := ec.GetVar("mine")
	_, flowMine := ec.GetScoped(engine.ScopeFlow, "mine")
	assert.False(t, runMine)
	assert.False(t, flowMine)
	_, runCalled := ec.GetVar("called")
	_, flowCalled := ec.GetScoped(engine.ScopeFlow, "called")
	assert.True(t, runCalled)
	assert.True(t, flowCalled)
}

// TestScriptNodeFlowWritebackAcrossLanguages checks the same reentrant boundary
// through real Python and Bash: a successful script keeps its own flow write and
// the called node's write, while interpreter failure discards only the script's.
func TestScriptNodeFlowWritebackAcrossLanguages(t *testing.T) {
	tests := []struct {
		name     string
		language string
		source   string
		wantErr  bool
	}{
		{
			name:     "Python success preserves both writes",
			language: sandbox.LangPython,
			source:   "flow[\"script\"] = \"from-script\"\nnodes.testNode({})\nresult = \"done\"\n",
		},
		{
			name:     "Python failure rolls back only script writes",
			language: sandbox.LangPython,
			source:   "flow[\"script\"] = \"discard\"\nnodes.testNode({})\nraise RuntimeError(\"after action\")\n",
			wantErr:  true,
		},
		{
			name:     "Bash success preserves both writes",
			language: sandbox.LangBash,
			source:   "setvar -s flow script from-script\nnode test-node '{}' >/dev/null || exit 1\nprintf done\n",
		},
		{
			name:     "Bash failure rolls back only script writes",
			language: sandbox.LangBash,
			source:   "setvar -s flow script discard\nnode test-node '{}' >/dev/null || exit 1\nexit 1\n",
			wantErr:  true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ec := newScriptInvokerTestCtx(t, tc.language, tc.source)
			ec.call = func(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
				_ = ec.SetScoped(engine.ScopeFlow, "called", "from-called-node")
				return &nodeapi.NodeCallResult{NodeType: call.Type, Attempts: 1, Outputs: map[string][]*nodeapi.Msg{}}, nil
			}
			outputs, err := ScriptNode{}.Execute(context.Background(), ec, nil)
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, outputs)
			} else {
				require.NoError(t, err)
			}
			called, present := ec.GetScoped(engine.ScopeFlow, "called")
			assert.True(t, present, "the called node's flow write must survive interpretation")
			assert.Equal(t, "from-called-node", called)
			script, present := ec.GetScoped(engine.ScopeFlow, "script")
			if tc.wantErr {
				assert.False(t, present, "an interpreter failure must discard the staged script write")
			} else {
				assert.True(t, present)
				assert.Equal(t, "from-script", script, "writeback must not overwrite the called node's reentrant write")
			}
		})
	}
}
