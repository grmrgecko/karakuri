package api_test

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// scriptVerificationAction makes accidental execution observable at the HTTP
// boundary without giving the verifier a node invoker.
type scriptVerificationAction struct {
	calls atomic.Int32
}

func (a *scriptVerificationAction) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type: "verify-action", Label: "Verify Action",
		ConfigSpec: []flow.FieldSpec{{ID: "message", Type: flow.FieldString, Required: true}},
	}
}

func (a *scriptVerificationAction) Execute(context.Context, nodeapi.ExecContext, map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	a.calls.Add(1)
	return nil, nil
}

// TestScriptVerifyReturnsDiagnosticsWithoutExecution drives every language
// through the real HTTP stack. Authoring mistakes and dynamic acceptance both
// stay separate from request and runtime failures, and neither executes a node.
func TestScriptVerifyReturnsDiagnosticsWithoutExecution(t *testing.T) {
	srv, token := setupTestServer(t)
	action := &scriptVerificationAction{}
	previous, existed := engine.Default().Get(action.Meta().Type)
	engine.Default().Register(action)
	t.Cleanup(func() {
		if existed {
			engine.Default().Register(previous)
		} else {
			engine.Default().Unregister(action.Meta().Type)
		}
	})

	tests := []struct {
		name     string
		language string
		invalid  string
		dynamic  string
	}{
		{name: "javascript", language: "javascript", invalid: `nodes.verifyAction({});`, dynamic: `nodes.verifyAction(buildConfig());`},
		{name: "empty language defaults to javascript", invalid: `nodes.verifyAction({});`, dynamic: `nodes.verifyAction(buildConfig());`},
		{name: "python", language: "python", invalid: `nodes.verifyAction({})`, dynamic: `nodes.verifyAction(build_config())`},
		{name: "bash", language: "bash", invalid: `node verifyAction '{}'`, dynamic: `node verifyAction "$CONFIG"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.language == sandbox.LangPython {
				requireScriptVerifyPython(t)
			}
			status, resp := doJSON(t, srv, token, http.MethodPost, "/api/scripts/verify", map[string]any{
				"language": tc.language, "source": tc.invalid,
			})
			require.Equal(t, http.StatusOK, status, "authoring mistakes must be diagnostic responses: %v", resp)
			diagnostics := scriptVerifyDiagnostics(t, resp)
			require.Len(t, diagnostics, 1)
			finding, ok := diagnostics[0].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "node-config-required", finding["code"])
			assert.Equal(t, "verify-action", finding["node_type"])
			assert.Equal(t, "error", finding["severity"])

			status, resp = doJSON(t, srv, token, http.MethodPost, "/api/scripts/verify", map[string]any{
				"language": tc.language, "source": tc.dynamic,
			})
			require.Equal(t, http.StatusOK, status, "computed calls belong to runtime validation: %v", resp)
			assert.Empty(t, scriptVerifyDiagnostics(t, resp), "dynamic input must not fabricate a static error")
			assert.Zero(t, action.calls.Load(), "verification must never execute the called action")
		})
	}

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/scripts/verify", map[string]any{
		"language": "ruby", "source": "puts 1",
	})
	assert.Equal(t, http.StatusBadRequest, status, "unsupported languages are request errors: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/scripts/verify", map[string]any{
		"language": "javascript", "source": strings.Repeat(" ", 256<<10+1),
	})
	assert.Equal(t, http.StatusBadRequest, status, "verification must retain the 256 KiB source cap: %v", resp)

	previousPython, hadPython := sandbox.Get(sandbox.LangPython)
	sandbox.Register(sandbox.NewPythonRuntime("", "", 0))
	t.Cleanup(func() {
		if hadPython {
			sandbox.Register(previousPython)
		}
	})
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/scripts/verify", map[string]any{
		"language": "python", "source": "result = payload",
	})
	assert.Equal(t, http.StatusServiceUnavailable, status, "a missing CPython module is an operational failure: %v", resp)
	assert.Contains(t, fmt.Sprint(resp), "make runtimes", "the response must tell the operator how to install Python")
	assert.Zero(t, action.calls.Load(), "operational failures must not execute actions either")
}

func scriptVerifyDiagnostics(t *testing.T, resp map[string]any) []any {
	t.Helper()
	data, ok := resp["data"].(map[string]any)
	require.True(t, ok, "verification response must be an object: %v", resp)
	diagnostics, ok := data["diagnostics"].([]any)
	require.True(t, ok, "diagnostics must be a list: %v", data)
	return diagnostics
}

func requireScriptVerifyPython(t *testing.T) {
	t.Helper()
	if runtime, ok := sandbox.Get(sandbox.LangPython); ok && runtime.Available() == nil {
		return
	}
	path := os.Getenv("KARAKURI_TEST_PYTHON_WASM")
	if path == "" {
		path = sandbox.ResolvePythonWasm("", []string{"../../build/runtimes"})
	}
	if path == "" {
		t.Skip("python.wasm not installed; set KARAKURI_TEST_PYTHON_WASM or run make runtimes")
	}
	sandbox.Register(sandbox.NewPythonRuntime(path, "", 0))
}
