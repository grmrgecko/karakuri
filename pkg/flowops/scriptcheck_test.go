package flowops

import (
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/core"
	"github.com/grmrgecko/karakuri/pkg/nodes/hanodes"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStrictScriptValidationPreservesDrafts checks the lifecycle boundary in
// every language: drafts retain mistakes, strict paths reject them, and computed
// calls remain runtime work rather than being executed by verification.
func TestStrictScriptValidationPreservesDrafts(t *testing.T) {
	core.RegisterAll(engine.Default())
	hanodes.RegisterAll(engine.Default())
	f := &models.Flow{ID: uuid.New()}
	tests := []struct {
		language string
		invalid  string
		dynamic  string
	}{
		{language: "javascript", invalid: `nodes.haGetState();`, dynamic: `const config = buildConfig(payload); nodes.haGetState(config);`},
		{language: "python", invalid: `nodes.haGetState()`, dynamic: `nodes.haGetState(build_config())`},
		{language: "bash", invalid: `node haGetState`, dynamic: `node haGetState "$CONFIG"`},
	}
	for _, tc := range tests {
		t.Run(tc.language, func(t *testing.T) {
			if tc.language == sandbox.LangPython {
				requireFlowopsPython(t)
			}
			graph := scriptGraph(tc.language, tc.invalid)
			require.NoError(t, ValidateDraft(f, graph), "a draft must retain an incomplete or mistaken script")
			require.NoError(t, Validate(graph, f.ID, false, false), "the unsaved-document draft check must stay lenient")

			for name, validate := range map[string]func() error{
				"full flow":       func() error { return ValidateFull(f, graph) },
				"document strict": func() error { return Validate(graph, f.ID, false, true) },
			} {
				err := validate()
				require.Error(t, err, "strict %s validation must reject the wrong argument count", name)
				var app *apperrors.AppError
				require.ErrorAs(t, err, &app)
				assert.Contains(t, app.Details["node:script"], "node-arguments")
				assert.Contains(t, app.Details["node:script"], "line 1, column 1")
			}

			dynamic := scriptGraph(tc.language, tc.dynamic)
			assert.NoError(t, ValidateFull(f, dynamic), "computed config is checked at invocation rather than executed by static verification")
		})
	}
}

// TestStrictScriptValidationReportsPythonRuntimeFailures keeps a missing wasm
// module actionable in strict checks without making it a draft-save error.
func TestStrictScriptValidationReportsPythonRuntimeFailures(t *testing.T) {
	core.RegisterAll(engine.Default())
	f := &models.Flow{ID: uuid.New()}
	previous, existed := sandbox.Get(sandbox.LangPython)
	sandbox.Register(sandbox.NewPythonRuntime("", "", 0))
	t.Cleanup(func() {
		if existed {
			sandbox.Register(previous)
		}
	})
	graph := scriptGraph("python", "result = payload")
	require.NoError(t, ValidateDraft(f, graph), "draft saves must not initialize CPython")
	err := ValidateFull(f, graph)
	require.Error(t, err, "strict validation must not silently skip Python when wasm is missing")
	assert.Contains(t, err.Error(), "make runtimes")
	assert.Contains(t, err.Error(), "node:script")
}

func scriptGraph(language, source string) *flow.Graph {
	raw, _ := json.Marshal(map[string]any{"language": language, "script": source})
	return &flow.Graph{Nodes: []flow.Node{{ID: "script", Type: flow.NodeTypeScript, Config: raw}}}
}

func requireFlowopsPython(t *testing.T) {
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

// TestValidateExecutableIgnoresFragmentEdgesWithoutIDs locks that deploy
// checks only the branches a trigger reaches. Edge IDs are optional, and the
// executable part was collected by edge ID, so one reachable edge without an
// ID pulled in every other edge without one. A draft fragment's edge then
// arrived without its nodes and the deploy failed with "source node does not
// exist".
func TestValidateExecutableIgnoresFragmentEdgesWithoutIDs(t *testing.T) {
	core.RegisterAll(engine.Default())
	f := &models.Flow{ID: uuid.New()}
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "schedule", Config: json.RawMessage(`{"cron":"0 * * * *"}`)}, {ID: "d", Type: "debug"},
			// An unfinished fragment no trigger reaches.
			{ID: "x", Type: "delay"}, {ID: "y", Type: "debug"},
		},
		Edges: []flow.Edge{
			{Source: "t", SourceHandle: "out", Target: "d", TargetHandle: "in"},
			{Source: "x", SourceHandle: "out", Target: "y", TargetHandle: "in"},
		},
	}
	require.NoError(t, ValidateDraft(f, g), "the graph must be a valid draft for the deploy check to be the one under test")
	err := ValidateExecutable(f, g)
	var app *apperrors.AppError
	if errors.As(err, &app) {
		assert.Empty(t, app.Details, "the fragment's edge must not be checked without its nodes")
	}
	assert.NoError(t, err, "an edge of an unreachable fragment is not part of what deploys, whatever its ID")
}

// TestValidateExecutableIgnoresUnreachedSubgraph locks that deploy checks only
// the local subflow bodies a running branch places. A subflow node no trigger
// reaches is dropped as draft material, and its body used to be validated
// strictly all the same, so an unfinished body blocked a working flow. A body
// a trigger does reach still has to be complete.
func TestValidateExecutableIgnoresUnreachedSubgraph(t *testing.T) {
	core.RegisterAll(engine.Default())
	f := &models.Flow{ID: uuid.New()}
	// The body holds a delay with no duration set yet.
	body := flow.Graph{
		Nodes: []flow.Node{
			{ID: "pi", Type: flow.NodeTypeSubflowInput, Config: json.RawMessage(`{"port_id":"in"}`)},
			{ID: "wait", Type: "delay"},
			{ID: "po", Type: flow.NodeTypeSubflowOutput, Config: json.RawMessage(`{"port_id":"out"}`)},
		},
		Edges: []flow.Edge{
			{ID: "pw", Source: "pi", SourceHandle: "out", Target: "wait", TargetHandle: "in"},
			{ID: "wp", Source: "wait", SourceHandle: "out", Target: "po", TargetHandle: "in"},
		},
	}
	graph := func(edges ...flow.Edge) *flow.Graph {
		return &flow.Graph{
			Nodes: []flow.Node{
				{ID: "t", Type: "schedule", Config: json.RawMessage(`{"cron":"0 * * * *"}`)},
				{ID: "d", Type: "debug"},
				{ID: "draft", Type: flow.NodeTypeSubflow, SubgraphID: "wip"},
			},
			Edges:     edges,
			Subgraphs: map[string]flow.Graph{"wip": body},
		}
	}

	unreached := graph(flow.Edge{ID: "td", Source: "t", SourceHandle: "out", Target: "d", TargetHandle: "in"})
	assert.NoError(t, ValidateExecutable(f, unreached),
		"an unfinished body that only a disconnected subflow node places must not block deploy")
	assert.Contains(t, unreached.Subgraphs, "wip", "the check must leave the document's subgraphs in place")

	reached := graph(
		flow.Edge{ID: "ts", Source: "t", SourceHandle: "out", Target: "draft", TargetHandle: "in"},
		flow.Edge{ID: "sd", Source: "draft", SourceHandle: "out", Target: "d", TargetHandle: "in"},
	)
	err := ValidateExecutable(f, reached)
	var app *apperrors.AppError
	require.True(t, errors.As(err, &app), "an unfinished body on a running branch must be refused")
	assert.Contains(t, app.Details, "subgraph:wip/node:wait", "the refusal names the unfinished node inside the body")
}
