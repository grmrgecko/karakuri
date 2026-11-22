package api_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/metrics"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createFlowWithGraph creates a flow and saves the given graph as its draft,
// returning the flow ID.
func createFlowWithGraph(t *testing.T, srv *httptest.Server, token, name string, graph map[string]any) string {
	t.Helper()
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": name})
	require.Equalf(t, http.StatusCreated, status, "the flow %q must be created: %v", name, resp)
	flowID, _ := data(t, resp)["id"].(string)
	require.NotEmpty(t, flowID, "the created flow must have an id")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": graph})
	require.Equalf(t, http.StatusOK, status, "the draft for %q must save: %v", name, resp)
	return flowID
}

// TestRerunReplaysLastRun covers the re-run button, which fires the last run's
// trigger payload against the *deployed* graph rather than the draft — that is
// what distinguishes it from a test run, and it is why it needs both a deployed
// version and a persisted run before it means anything. Both preconditions are
// refused separately here, since either one missing produces the same button on
// the same screen.
//
// The metric is checked at the end because the runs counter is labelled by the
// flow's slug, and a dashboard counting runs per automation reads it: a re-run
// that did not increment it would leave the count quietly wrong.
func TestRerunReplaysLastRun(t *testing.T) {
	srvRaw, token := setupRunServer(t)

	// Manual trigger with a payload, feeding a debug node.
	g := map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"payload": map[string]any{"value": 7}}},
			{"id": "d", "type": "debug", "position": map[string]any{"x": 200, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "d", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	flowID := createFlowWithGraph(t, srvRaw, token, "M7 Rerun", g)

	status, resp := doJSON(t, srvRaw, token, http.MethodPost, "/api/flows/"+flowID+"/rerun", nil)
	require.Equalf(t, http.StatusBadRequest, status,
		"a re-run replays against the deployed graph, and there is none: %v", resp)

	status, resp = doJSON(t, srvRaw, token, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the flow must deploy: %v", resp)

	status, resp = doJSON(t, srvRaw, token, http.MethodPost, "/api/flows/"+flowID+"/rerun", nil)
	require.Equalf(t, http.StatusBadRequest, status,
		"there is a deployed graph now but no recorded run whose payload could be replayed: %v", resp)

	status, resp = doJSON(t, srvRaw, token, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{})
	require.Equalf(t, http.StatusAccepted, status, "a test run must start: %v", resp)

	detail := waitRunStatus(t, srvRaw, token, flowID, "succeeded", "")
	firstID, _ := detail["run"].(map[string]any)["id"].(string)
	require.NotEmpty(t, firstID, "the first run must be persisted for the re-run to replay it")

	status, resp = doJSON(t, srvRaw, token, http.MethodPost, "/api/flows/"+flowID+"/rerun", nil)
	require.Equalf(t, http.StatusAccepted, status, "with both preconditions met the re-run must start: %v", resp)

	rerunID, _ := data(t, resp)["run_id"].(string)
	require.NotEmpty(t, rerunID, "the caller needs the new run's id to follow it")
	assert.NotEqual(t, firstID, rerunID, "a re-run is a new run, not an amendment to the last one")

	detail = waitRunStatus(t, srvRaw, token, flowID, "succeeded", rerunID)
	assert.Equal(t, "t", detail["run"].(map[string]any)["trigger_node_id"],
		"the re-run must start from the same trigger the recorded run did")

	// Both runs are counted under the flow's slug, which is the label a
	// dashboard counting runs per automation groups by.
	assert.GreaterOrEqual(t, testutil.ToFloat64(metrics.RunsTotal.WithLabelValues("m7-rerun", "succeeded")), 2.0,
		"a re-run must be counted like any other run, or the per-flow count quietly under-reports")
}

// TestRetryMetricCounts covers the retry counter, which is how an operator sees
// that a flow is succeeding only because it keeps trying — the failure that
// never shows up as a failed run. It is driven through the real API and engine
// with a node that cannot possibly succeed, so the policy is guaranteed to
// exhaust itself and the counter has to move.
func TestRetryMetricCounts(t *testing.T) {
	srvRaw, token := setupRunServer(t)

	// A wol node with an unusable MAC fails on every attempt, so the retry
	// policy exhausts itself and the counter moves.
	g := map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "f", "type": "wol", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{"mac": "not-a-mac"},
				"retry":  map[string]any{"max_attempts": 3, "delay_ms": 1, "backoff": 1, "max_delay_ms": 5}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "f", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	flowID := createFlowWithGraph(t, srvRaw, token, "M7 Retry Metric", g)

	// The counter is process-wide, so the delta is what matters rather than the
	// absolute value.
	before := testutil.ToFloat64(metrics.NodeRetriesTotal.WithLabelValues("m7-retry-metric", "wol"))

	status, resp := doJSON(t, srvRaw, token, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{})
	require.Equalf(t, http.StatusAccepted, status, "the test run must start: %v", resp)
	waitRunStatus(t, srvRaw, token, flowID, "failed", "")

	after := testutil.ToFloat64(metrics.NodeRetriesTotal.WithLabelValues("m7-retry-metric", "wol"))
	assert.GreaterOrEqual(t, after-before, 1.0,
		"a retry must be counted under the flow's slug and the node's type, or a flow only surviving on retries looks healthy")
}

// TestExportEmbedsSharedSubflowsAndImportRoundTrips covers what makes an
// exported flow portable. A shared subflow is a row in this instance's library
// and means nothing anywhere else, so the export inlines it as a local
// subgraph — otherwise the document would import as a flow with a hole where
// the subflow used to be.
//
// The round trip back through import is what proves the document is complete,
// and the version check is the guard that keeps a future format from being
// half-read by an older build.
func TestExportEmbedsSharedSubflowsAndImportRoundTrips(t *testing.T) {
	srv, token := setupTestServer(t)

	// Build a parent with a local subgraph and promote it to a shared subflow
	// so the parent draft references the shared library flow.
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "M7 Export Parent"})
	require.Equalf(t, http.StatusCreated, status, "the parent flow must be created: %v", resp)
	parentID, _ := data(t, resp)["id"].(string)
	require.NotEmpty(t, parentID, "the parent's id addresses every request below")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+parentID+"/graph",
		map[string]any{"graph": subflowParentGraph()})
	require.Equalf(t, http.StatusOK, status, "the draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+parentID+"/promote_subgraph",
		map[string]any{"subgraph_id": "sg1", "name": "M7 Export Helper"})
	require.Equalf(t, http.StatusCreated, status, "promoting gives the parent a shared reference to export: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+parentID+"/export", nil)
	require.Equalf(t, http.StatusOK, status, "the flow must export: %v", resp)

	doc := data(t, resp)
	assert.Equal(t, float64(1), doc["karakuri_flow"], "the document declares its format version so an importer can refuse a newer one")
	assert.Equal(t, "M7 Export Parent", doc["name"], "the document carries the flow's name for the importer to reuse")

	graph, ok := doc["graph"].(map[string]any)
	require.True(t, ok, "the document carries the graph")
	subgraphs, ok := graph["subgraphs"].(map[string]any)
	require.Truef(t, ok, "the shared subflow must be inlined as a subgraph: %v", graph)
	assert.Len(t, subgraphs, 1, "the one referenced subflow must be embedded: %v", subgraphs)

	for _, n := range graph["nodes"].([]any) {
		node, ok := n.(map[string]any)
		require.True(t, ok, "a node is an object")
		if node["type"] != "subflow" {
			continue
		}
		assert.NotContainsf(t, node, "subflow_id",
			"a library id means nothing on another instance, so the reference must be rewritten: %v", node)
		assert.NotEmptyf(t, node["subgraph_id"],
			"the node must point at the embedded copy, or the imported flow has a hole where the subflow was: %v", node)
	}

	// Import the document back: a new flow is created with a suffixed name
	// (the original name's slug is taken) and the embedded subgraphs intact.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/import",
		map[string]any{"document": doc})
	require.Equalf(t, http.StatusCreated, status, "the exported document must import: %v", resp)

	imported := data(t, resp)
	assert.Equal(t, "M7 Export Parent (imported)", imported["name"],
		"the original name's slug is taken, so the import is suffixed rather than refused or silently renaming the original")

	importedGraph, ok := imported["graph"].(map[string]any)
	require.True(t, ok, "the imported flow carries its graph")
	assert.Len(t, importedGraph["subgraphs"], 1,
		"the embedded subgraph must survive the round trip, or the import is the hole the export was avoiding")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/import",
		map[string]any{"document": map[string]any{"karakuri_flow": 2, "name": "x", "graph": map[string]any{}}})
	assert.Equalf(t, http.StatusBadRequest, status,
		"a newer format must be refused outright: half-reading it would import a flow that is quietly not what was exported: %v", resp)
}

// TestVersionRestoreCopiesGraphToDraft covers restoring from version history.
// Restore copies the old graph into the *draft* and leaves the deployed version
// alone, so looking at an old version — or recovering from a bad edit — never
// changes what is running until the author deploys it themselves.
func TestVersionRestoreCopiesGraphToDraft(t *testing.T) {
	srv, token := setupTestServer(t)

	// Deploy version 1 with three nodes.
	flowID := createFlowWithGraph(t, srv, token, "M7 Restore", validGraph())
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy",
		map[string]any{"comment": "v1"})
	require.Equalf(t, http.StatusOK, status, "the three-node version must deploy: %v", resp)

	// Deploy version 2 with a single node.
	small := map[string]any{
		"nodes": []map[string]any{
			{"id": "only", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
		},
		"edges":    []map[string]any{},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": small})
	require.Equalf(t, http.StatusOK, status, "the one-node draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy",
		map[string]any{"comment": "v2"})
	require.Equalf(t, http.StatusOK, status, "the one-node version must deploy: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/versions/1/restore", nil)
	require.Equalf(t, http.StatusOK, status, "restoring an earlier version must succeed: %v", resp)

	graph, ok := data(t, resp)["graph"].(map[string]any)
	require.True(t, ok, "the restored flow carries its draft graph")
	assert.Len(t, graph["nodes"], 3,
		"the draft must become the restored version, while the deployed one keeps running until the author deploys again")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/versions/9/restore", nil)
	assert.Equalf(t, http.StatusNotFound, status,
		"a version that was never deployed cannot be restored, and blanking the draft instead would lose the author's work: %v", resp)
}

// TestDraftSaveLenientDeployStrict states the validation split in its plainest
// form. A required field is missing, which is what a node looks like for the
// whole time an author is filling it in — so the draft saves and the deploy is
// refused. Reversing the two would either fight the author on every keystroke
// or arm a trigger on a graph that cannot run.
func TestDraftSaveLenientDeployStrict(t *testing.T) {
	srv, token := setupTestServer(t)

	// The delay node's required "duration" field is missing: draft saves
	// accept it, deploy rejects it.
	g := map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "d", "type": "delay", "position": map[string]any{"x": 200, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "d", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "M7 Lenient"})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := data(t, resp)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": g})
	require.Equalf(t, http.StatusOK, status,
		"a half-filled node is what every node looks like mid-edit, so the draft must store it: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusBadRequest, status,
		"deploying would arm a trigger on a graph that cannot run: %v", resp)
	assert.Equal(t, "VALIDATION_ERROR", resp["error"], "the editor renders problems only for this code")
}
