package api_test

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subflowParentGraph builds a manual -> subflow -> debug draft whose subflow
// node references the local subgraph "sg1" (in -> delay -> out).
func subflowParentGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "s1", "type": "subflow", "position": map[string]any{"x": 200, "y": 0}, "subgraph_id": "sg1"},
			{"id": "dbg", "type": "debug", "position": map[string]any{"x": 400, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "s1", "target_handle": "in"},
			{"id": "e2", "source": "s1", "source_handle": "out", "target": "dbg", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
		"subgraphs": map[string]any{
			"sg1": map[string]any{
				"nodes": []map[string]any{
					{"id": "pi", "type": "subflow-input", "position": map[string]any{"x": 0, "y": 0},
						"config": map[string]any{"port_id": "in"}},
					{"id": "d", "type": "delay", "position": map[string]any{"x": 200, "y": 0},
						"config": map[string]any{"duration": "1s"}},
					{"id": "po", "type": "subflow-output", "position": map[string]any{"x": 400, "y": 0},
						"config": map[string]any{"port_id": "out"}},
				},
				"edges": []map[string]any{
					{"id": "se1", "source": "pi", "source_handle": "out", "target": "d", "target_handle": "in"},
					{"id": "se2", "source": "d", "source_handle": "out", "target": "po", "target_handle": "in"},
				},
				"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
			},
		},
	}
}

// data extracts the data object of a success envelope.
func data(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	d, ok := resp["data"].(map[string]any)
	require.Truef(t, ok, "the response carries no data object: %v", resp)
	return d
}

// TestSubflowPromoteImportAndDeleteConflict walks the shared-subflow library
// through its whole life against the real API: a local subgraph is promoted to
// a shared flow, a second flow references it, deleting it while referenced is
// refused, importing copies it back down to a local subgraph, and a shared
// subflow referencing itself is rejected at save.
//
// They belong in one test because each step depends on the last: there is no
// reference to conflict with until a second flow has one, and nothing to import
// until something was promoted. The promotion is the delicate part — the parent
// draft has to be rewired to the new shared flow and lose its local copy in the
// same operation, or the author ends up with two diverging versions of the same
// subflow and no indication which one runs.
func TestSubflowPromoteImportAndDeleteConflict(t *testing.T) {
	srv, token := setupTestServer(t)

	// Create the parent flow with a local subflow in its draft.
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "M6 Parent"})
	require.Equalf(t, http.StatusCreated, status, "the parent flow must be created: %v", resp)
	parentID, _ := data(t, resp)["id"].(string)
	require.NotEmpty(t, parentID, "the parent's id is how the rest of the test addresses it")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+parentID+"/graph",
		map[string]any{"graph": subflowParentGraph()})
	require.Equalf(t, http.StatusOK, status, "a draft with a local subgraph must save: %v", resp)

	// Promote the local subgraph to a shared subflow.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+parentID+"/promote_subgraph",
		map[string]any{"subgraph_id": "sg1", "name": "M6 Retry Helper"})
	require.Equalf(t, http.StatusCreated, status, "promoting a local subgraph must create a shared flow: %v", resp)

	shared := data(t, resp)
	sharedID, _ := shared["id"].(string)
	require.NotEmpty(t, sharedID, "the shared subflow's id is what other flows reference it by")
	assert.Equal(t, true, shared["is_subflow"], "a promoted subgraph becomes a library subflow, not an ordinary flow")
	assert.Equal(t, true, shared["deployed"],
		"a reference resolves against a deployed version, so promotion must deploy it or nothing could use it")

	// The parent draft now references the shared subflow and has no subgraphs.
	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+parentID, nil)
	require.Equalf(t, http.StatusOK, status, "the parent must still be readable: %v", resp)

	graph, ok := data(t, resp)["graph"].(map[string]any)
	require.True(t, ok, "the parent carries its draft graph")
	assert.NotContains(t, graph, "subgraphs",
		"the local copy must be removed, or the author has two diverging versions and no sign which one runs")

	var rewired bool
	for _, n := range graph["nodes"].([]any) {
		node, ok := n.(map[string]any)
		require.True(t, ok, "a node is an object")
		if node["id"] == "s1" && node["subflow_id"] == sharedID {
			rewired = true
		}
	}
	assert.Truef(t, rewired,
		"the placed node must now point at the shared flow, or promotion leaves the parent referencing nothing: %v",
		graph["nodes"])

	// A second flow can reference the shared subflow.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "M6 Consumer"})
	require.Equalf(t, http.StatusCreated, status, "the second flow must be created: %v", resp)
	consumerID, _ := data(t, resp)["id"].(string)
	require.NotEmpty(t, consumerID, "the consumer's id is how the rest of the test addresses it")
	consumerGraph := map[string]any{
		"nodes": []map[string]any{
			{"id": "t", "type": "manual", "position": map[string]any{"x": 0, "y": 0}},
			{"id": "s1", "type": "subflow", "position": map[string]any{"x": 200, "y": 0}, "subflow_id": sharedID},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t", "source_handle": "out", "target": "s1", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+consumerID+"/graph",
		map[string]any{"graph": consumerGraph})
	require.Equalf(t, http.StatusOK, status, "a second flow referencing the shared subflow must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodDelete, "/api/flows/"+sharedID, nil)
	assert.Equalf(t, http.StatusConflict, status,
		"deleting a referenced subflow must be refused, or both flows above would deploy with a hole in them: %v", resp)

	// Importing copies the deployed graph in as a fresh local subgraph.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+consumerID+"/import_subflow",
		map[string]any{"subflow_id": sharedID})
	require.Equalf(t, http.StatusOK, status, "importing a shared subflow back into a flow must succeed: %v", resp)

	subgraphID, _ := data(t, resp)["subgraph_id"].(string)
	require.NotEmpty(t, subgraphID, "the caller needs the new subgraph's id to place a node against it")

	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/flows/"+consumerID, nil)
	require.Equalf(t, http.StatusOK, status, "the consumer must still be readable: %v", resp)

	graph, ok = data(t, resp)["graph"].(map[string]any)
	require.True(t, ok, "the consumer carries its draft graph")
	subgraphs, ok := graph["subgraphs"].(map[string]any)
	require.Truef(t, ok, "importing must add a subgraphs map to the draft: %v", graph)
	assert.Containsf(t, subgraphs, subgraphID,
		"the copy must land in the draft under the returned id, or the import produced nothing usable: %v", subgraphs)

	// A shared subflow referencing itself is rejected at save time.
	selfGraph := map[string]any{
		"nodes": []map[string]any{
			{"id": "pi", "type": "subflow-input", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"port_id": "in"}},
			{"id": "loop", "type": "subflow", "position": map[string]any{"x": 200, "y": 0}, "subflow_id": sharedID},
		},
		"edges":    []map[string]any{},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+sharedID+"/graph",
		map[string]any{"graph": selfGraph})
	assert.Equalf(t, http.StatusBadRequest, status,
		"a subflow placed inside itself would expand without bound, so it must be refused at the save: %v", resp)
}

// TestPromotedSubflowVersionHoldsItsSettings locks that the version a
// promotion deploys records the subflow's name, as every deploy does. Without
// it a rename was never a draft change, and the next start's migration froze
// the undeployed name onto version 1 as if it had been deployed.
func TestPromotedSubflowVersionHoldsItsSettings(t *testing.T) {
	srv, token := setupTestServer(t)
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "Promote Parent"})
	require.Equalf(t, http.StatusCreated, status, "the parent flow must be created: %v", resp)
	parentID, _ := data(t, resp)["id"].(string)
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+parentID+"/graph",
		map[string]any{"graph": subflowParentGraph()})
	require.Equalf(t, http.StatusOK, status, "a draft with a local subgraph must save: %v", resp)
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+parentID+"/promote_subgraph",
		map[string]any{"subgraph_id": "sg1", "name": "Lights"})
	require.Equalf(t, http.StatusCreated, status, "promoting must create a shared flow: %v", resp)
	sharedID, _ := data(t, resp)["id"].(string)
	assert.Equal(t, false, data(t, resp)["has_draft_changes"], "the promoted draft is what version 1 deployed")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+sharedID, map[string]any{"name": "Lamps"})
	require.Equalf(t, http.StatusOK, status, "renaming the subflow must succeed: %v", resp)
	assert.Equal(t, true, data(t, resp)["has_draft_changes"], "a rename is undeployed until the next deploy")
}
