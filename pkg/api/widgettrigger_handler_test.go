package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// widgetTriggerGraph is a flow with two named widget triggers, one of them
// public, each feeding a debug node.
func widgetTriggerGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "t1", "type": "widget-trigger", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"name": "Movie night"}},
			{"id": "t2", "type": "widget-trigger", "position": map[string]any{"x": 0, "y": 150},
				"config": map[string]any{"name": "Doorbell", "allow_public": true}},
			{"id": "d1", "type": "debug", "position": map[string]any{"x": 250, "y": 0}},
			{"id": "d2", "type": "debug", "position": map[string]any{"x": 250, "y": 150}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t1", "source_handle": "out", "target": "d1", "target_handle": "in"},
			{"id": "e2", "source": "t2", "source_handle": "out", "target": "d2", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// TestWidgetTriggers covers the dashboard's way into a flow, end to end. A
// widget trigger is named rather than secret — unlike a webhook there is no
// token to guess — so the API resolves the name against the *deployed* graph,
// and that single fact produces most of what is checked here: a draft trigger
// is not listed, disabling the flow removes it again, and pressing one on a
// disabled flow fails.
//
// The anonymous half is the security boundary. A public dashboard is served to
// anyone, so an anonymous press must reach a trigger marked public and be
// refused on one that is not; getting that backwards would let a passer-by run
// any automation in the house.
func TestWidgetTriggers(t *testing.T) {
	srv, token := setupRunServer(t)

	// Deploy and enable a flow declaring two widget triggers.
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "Widget Flow"})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": widgetTriggerGraph()})
	require.Equalf(t, http.StatusOK, status, "the draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/widget_triggers", nil)
	require.Equalf(t, http.StatusOK, status, "the trigger list must be served: %v", resp)
	assert.Empty(t, resp["data"],
		"a trigger only in the draft has no deployed graph behind it, so a dashboard must not offer it")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusOK, status, "the flow must deploy: %v", resp)
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/enable", nil)
	require.Equalf(t, http.StatusOK, status, "the flow must enable: %v", resp)

	// The deployed triggers are listed with their flow, sorted by name.
	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/widget_triggers", nil)
	require.Equalf(t, http.StatusOK, status, "the trigger list must be served: %v", resp)

	items, ok := resp["data"].([]any)
	require.True(t, ok, "the trigger list is an array")
	require.Len(t, items, 2, "both declared triggers must be offered once deployed: %v", items)

	// Sorted by name, so Doorbell comes before Movie night despite being the
	// second node in the graph.
	first, ok := items[0].(map[string]any)
	require.True(t, ok, "a trigger entry is an object")
	assert.Equal(t, "Doorbell", first["name"], "triggers are listed by name, not in graph order")
	assert.Equal(t, flowID, first["flow_id"], "the row needs the flow to press against")
	assert.Equal(t, "Widget Flow", first["flow_name"],
		"the config panel shows the flow name, since a trigger name alone is ambiguous across flows")
	assert.Equal(t, true, first["allow_public"],
		"the dashboard needs to know which triggers an anonymous viewer may press")

	second, ok := items[1].(map[string]any)
	require.True(t, ok, "a trigger entry is an object")
	assert.Equal(t, "Movie night", second["name"], "the second trigger follows alphabetically")
	assert.Equal(t, "t1", second["node_id"], "the node id is what the run is started on")
	assert.Equal(t, false, second["allow_public"], "a trigger that did not opt in is private")

	// Pressing a trigger starts a run of the deployed graph on that node.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Movie night", "payload": map[string]any{"scene": "movie"}})
	require.Equalf(t, http.StatusOK, status, "pressing a deployed trigger must start a run: %v", resp)

	runID, _ := resp["data"].(map[string]any)["run_id"].(string)
	require.NotEmpty(t, runID, "the caller needs the run's id to follow it")

	detail := waitRunStatus(t, srv, token, flowID, "succeeded", runID)
	assert.Equal(t, "succeeded", nodeState(t, detail, "d1"), "the run must reach what the pressed trigger feeds")
	assert.Equal(t, "t1", detail["run"].(map[string]any)["trigger_node_id"],
		"the name resolves to a specific node, so the run starts there rather than at whichever trigger came first")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Nothing here"})
	assert.Equalf(t, http.StatusNotFound, status,
		"a name the deployed graph does not declare resolves to nothing, so there is no run to start: %v", resp)

	status, resp = doJSON(t, srv, "", http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Movie night"})
	assert.Equalf(t, http.StatusUnauthorized, status,
		"a public dashboard is served to anyone, so a private trigger must not be pressable from it: %v", resp)

	status, resp = doJSON(t, srv, "", http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Doorbell"})
	require.Equalf(t, http.StatusOK, status,
		"a trigger that opted in must be pressable anonymously, or a public dashboard has no working buttons: %v", resp)
	waitRunStatus(t, srv, token, flowID, "succeeded", resp["data"].(map[string]any)["run_id"].(string))

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Movie night", "payload": "not an object"})
	assert.Equalf(t, http.StatusBadRequest, status,
		"the trigger's port declares an object, so anything else must be refused rather than emitted: %v", resp)

	// A client with no parameters often sends payload: null. That decoded into
	// a nil map and the run was seeded with a null payload, on a port that
	// promises an object; it must read as no parameters.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Movie night", "payload": nil})
	require.Equalf(t, http.StatusOK, status, "a null payload is a press with no parameters: %v", resp)
	detail = waitRunStatus(t, srv, token, flowID, "succeeded", resp["data"].(map[string]any)["run_id"].(string))
	payload, _ := debugMessage(t, detail, "t1")
	assert.Equal(t, map[string]any{}, payload, "the trigger emits an empty object, never null")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/disable", nil)
	require.Equalf(t, http.StatusOK, status, "the flow must disable: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodGet, "/api/widget_triggers", nil)
	require.Equalf(t, http.StatusOK, status, "the trigger list must still be served: %v", resp)
	assert.Empty(t, resp["data"],
		"a disabled flow's triggers must leave the list, or a dashboard keeps offering buttons that do nothing")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/widget_triggers/"+flowID+"/fire",
		map[string]any{"name": "Movie night"})
	assert.Equalf(t, http.StatusBadRequest, status,
		"a dashboard holding a stale list must not be able to run a flow its owner switched off: %v", resp)
}

// TestWidgetTriggerDuplicateNameRejected covers two triggers claiming one name.
// A press names a trigger, so two of them make the press ambiguous — but the
// draft still saves, because a name being typed collides with its neighbour
// routinely and refusing the save would fight the author mid-edit. Deploy is
// where it is refused, which is the lenient-save, strict-deploy split applied
// to exactly the case it exists for.
func TestWidgetTriggerDuplicateNameRejected(t *testing.T) {
	srv, token := setupTestServer(t)

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "Duplicate Trigger Flow"})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")

	graph := map[string]any{
		"nodes": []map[string]any{
			{"id": "t1", "type": "widget-trigger", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"name": "Press me"}},
			{"id": "t2", "type": "widget-trigger", "position": map[string]any{"x": 0, "y": 150},
				"config": map[string]any{"name": "Press me"}},
			{"id": "d1", "type": "debug", "position": map[string]any{"x": 250, "y": 0}},
			{"id": "d2", "type": "debug", "position": map[string]any{"x": 250, "y": 150}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "t1", "source_handle": "out", "target": "d1", "target_handle": "in"},
			{"id": "e2", "source": "t2", "source_handle": "out", "target": "d2", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": graph})
	require.Equalf(t, http.StatusOK, status,
		"a draft save is lenient: a half-typed name collides with its neighbour routinely: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy", map[string]any{})
	require.Equalf(t, http.StatusBadRequest, status,
		"deploy is strict: two triggers of one name make every press ambiguous: %v", resp)
	assert.Contains(t, fmt.Sprint(resp["details"]), "widget trigger name",
		"the editor shows this to the author, so it must name what is duplicated rather than just refusing")
}
