package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupRunServer boots the API test server with a live execution engine
// wired to the shared in-memory database.
func setupRunServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	srv, token := setupTestServer(t)
	eng := engine.New(engine.Options{
		Registry: engine.Default(),
		Clock:    engine.RealClock{},
		Logger:   zap.NewNop(),
		History:  engine.NewDBHistory(zap.NewNop()),
	})
	eng.Start()
	flowops.SetEngine(eng)
	t.Cleanup(func() {
		flowops.SetEngine(nil)
		eng.Stop()
	})
	return srv, token
}

// switchGraph is a manual -> switch(expression) graph with debug nodes on
// both branches, used to exercise run-from-node seeding.
func switchGraph() map[string]any {
	return map[string]any{
		"nodes": []map[string]any{
			{"id": "n1", "type": "manual", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"payload": map[string]any{"value": 42}}},
			{"id": "n2", "type": "switch", "position": map[string]any{"x": 200, "y": 0},
				"config": map[string]any{"mode": "expression", "condition": "payload.value > 10"}},
			{"id": "n3", "type": "debug", "position": map[string]any{"x": 400, "y": 0}},
			{"id": "n4", "type": "debug", "position": map[string]any{"x": 400, "y": 100}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "n1", "source_handle": "out", "target": "n2", "target_handle": "in"},
			{"id": "e2", "source": "n2", "source_handle": "true", "target": "n3", "target_handle": "in"},
			{"id": "e3", "source": "n2", "source_handle": "false", "target": "n4", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
}

// waitRunStatus polls the flow's last run until it reaches the wanted status
// (and, when wantRunID is non-empty, is that specific run), returning the run
// detail. Polling absorbs the async history writer.
func waitRunStatus(t *testing.T, srv *httptest.Server, token, flowID, wantStatus, wantRunID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var last map[string]any
	for time.Now().Before(deadline) {
		status, resp := doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID+"/run", nil)
		if status == http.StatusOK {
			data, _ := resp["data"].(map[string]any)
			run, _ := data["run"].(map[string]any)
			if run["status"] == wantStatus && (wantRunID == "" || run["id"] == wantRunID) {
				return data
			}
			last = data
		}
		time.Sleep(25 * time.Millisecond)
	}
	require.FailNowf(t, "the run never reached the expected status",
		"want %q, last seen: %v", wantStatus, last)
	return nil
}

// nodeState extracts one node's latest state from a run detail.
func nodeState(t *testing.T, detail map[string]any, nodeID string) string {
	t.Helper()
	states, _ := detail["node_states"].(map[string]any)
	ns, _ := states[nodeID].(map[string]any)
	if ns == nil {
		return ""
	}
	s, _ := ns["state"].(string)
	return s
}

// TestRunFromNodeSeedsFromLastRun covers the editor's replay against the real
// API and a live engine. An author picks a node partway down a graph and runs
// from there, seeded by what the last run recorded — which is why the first
// attempt, before any run exists, has to be refused rather than run with
// nothing.
//
// The false-branch case is the one worth the setup: the previous run took the
// true branch, so there is no recorded output on the false port to seed from.
// That is a distinct error code rather than a generic 400, because the editor
// tells the author which node they cannot replay from and why.
func TestRunFromNodeSeedsFromLastRun(t *testing.T) {
	srv, token := setupRunServer(t)

	// Create the flow and save the graph.
	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "RunFromNode", "description": ""})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")

	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": switchGraph()})
	require.Equalf(t, http.StatusOK, status, "the draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test",
		map[string]any{"start_node_id": "n2"})
	require.Equalf(t, http.StatusBadRequest, status,
		"there is no recorded run to seed from, so replaying must be refused rather than run with nothing: %v", resp)

	// Full test run: payload.value is 42, so the switch takes the true branch.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{})
	require.Equalf(t, http.StatusAccepted, status, "starting a run answers 202: it is scheduled, not finished: %v", resp)

	detail := waitRunStatus(t, srv, token, flowID, "succeeded", "")
	require.Equal(t, "succeeded", nodeState(t, detail, "n3"), "42 is over 10, so the true branch runs")
	require.Equal(t, "skipped", nodeState(t, detail, "n4"),
		"the false branch is skipped, which is what leaves it with no recorded output to replay from")
	fullRunID, _ := detail["run"].(map[string]any)["id"].(string)

	// Run from the switch node, seeded by the manual node's stored output.
	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test",
		map[string]any{"start_node_id": "n2"})
	require.Equalf(t, http.StatusAccepted, status, "replaying from a node with recorded upstream must be accepted: %v", resp)

	partialRunID, _ := resp["data"].(map[string]any)["run_id"].(string)
	require.NotEmpty(t, partialRunID, "the caller needs the new run's id to follow it")
	assert.NotEqual(t, fullRunID, partialRunID, "a replay is a new run, not an amendment to the last one")

	detail = waitRunStatus(t, srv, token, flowID, "succeeded", partialRunID)
	assert.Equal(t, partialRunID, detail["run"].(map[string]any)["id"],
		"the last-run endpoint must select the replay while retaining earlier run history")

	assert.Equal(t, "skipped", nodeState(t, detail, "n1"),
		"the trigger must not re-run: replaying is for the graph below the chosen node, not for its side effects above")
	for _, id := range []string{"n2", "n3"} {
		assert.Equalf(t, "succeeded", nodeState(t, detail, id),
			"node %s is at or below the start node and must execute", id)
	}

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test",
		map[string]any{"start_node_id": "n4"})
	require.Equalf(t, http.StatusBadRequest, status,
		"the false branch was skipped last run, so there is nothing recorded to seed it with: %v", resp)
	assert.Equal(t, "MISSING_UPSTREAM_DATA", resp["error"],
		"the editor names the reason to the author, so this needs its own code rather than a generic 400")
}

// TestRunIgnoresUnreachableDraftNodes covers testing one usable branch while
// another node is still being configured on the canvas. Strict validation
// belongs to the selected execution path, not every piece of the draft.
func TestRunIgnoresUnreachableDraftNodes(t *testing.T) {
	srv, token := setupRunServer(t)

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "Unfinished draft branch", "description": ""})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")

	g := map[string]any{
		"nodes": []map[string]any{
			{"id": "value", "type": "webhook", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"token": "unfinished-draft-test"}},
			{"id": "debug", "type": "debug", "position": map[string]any{"x": 200, "y": 0}},
			{"id": "unfinished", "type": "ai-agent", "position": map[string]any{"x": 0, "y": 200}},
		},
		"edges": []map[string]any{
			{"id": "working", "source": "value", "source_handle": "out", "target": "debug", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": g})
	require.Equalf(t, http.StatusOK, status, "an unfinished node is valid draft material: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test",
		map[string]any{"trigger_node_id": "value"})
	require.Equalf(t, http.StatusAccepted, status,
		"an unreachable unfinished node must not block the selected test branch: %v", resp)

	detail := waitRunStatus(t, srv, token, flowID, "succeeded", "")
	assert.Equal(t, "succeeded", nodeState(t, detail, "debug"), "the selected branch must execute")
	assert.Empty(t, nodeState(t, detail, "unfinished"), "the unfinished node is not part of the selected run")

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/deploy",
		map[string]any{"comment": "working branch"})
	require.Equalf(t, http.StatusOK, status,
		"an unreachable unfinished node must not block deployment: %v", resp)
}

// TestScheduleTriggerTestRunsWithoutParams covers testing a scheduled flow from
// the editor. Waiting for the next tick is not an option, and a schedule has no
// payload to seed, so the run has to start with nothing supplied — a test-run
// dialog that demanded parameters here would make every scheduled flow
// untestable.
func TestScheduleTriggerTestRunsWithoutParams(t *testing.T) {
	srv, token := setupRunServer(t)

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "ScheduleTest", "description": ""})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")
	g := map[string]any{
		"nodes": []map[string]any{
			{"id": "s1", "type": "schedule", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{"cron": "*/5 * * * * *"}},
			{"id": "d1", "type": "debug", "position": map[string]any{"x": 200, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "s1", "source_handle": "out", "target": "d1", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": g})
	require.Equalf(t, http.StatusOK, status, "the draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{})
	require.Equalf(t, http.StatusAccepted, status,
		"a schedule has no payload to seed, so a test run with nothing supplied must be accepted: %v", resp)

	detail := waitRunStatus(t, srv, token, flowID, "succeeded", "")
	assert.Equal(t, "succeeded", nodeState(t, detail, "d1"),
		"the synthesized tick must reach the rest of the graph, or testing a scheduled flow proves nothing")
}

// TestWebhookTestRunUsesTestParams covers the other half of testing a trigger
// that cannot be waited for. A webhook does have a payload, so the author types
// a sample body into the node and the test run uses it — and the stand-in
// request is filled in around it, so a rule reading meta.path behaves in the
// editor as it will in production.
func TestWebhookTestRunUsesTestParams(t *testing.T) {
	srv, token := setupRunServer(t)

	status, resp := doJSON(t, srv, token, http.MethodPost, "/api/flows",
		map[string]any{"name": "WebhookTest", "description": ""})
	require.Equalf(t, http.StatusCreated, status, "the flow must be created: %v", resp)
	flowID, _ := resp["data"].(map[string]any)["id"].(string)
	require.NotEmpty(t, flowID, "the flow's id addresses every request below")
	g := map[string]any{
		"nodes": []map[string]any{
			{"id": "w1", "type": "webhook", "position": map[string]any{"x": 0, "y": 0},
				"config": map[string]any{
					"token":       "test-run-token",
					"test_params": map[string]any{"state": "ON"},
				}},
			{"id": "d1", "type": "debug", "position": map[string]any{"x": 200, "y": 0}},
		},
		"edges": []map[string]any{
			{"id": "e1", "source": "w1", "source_handle": "out", "target": "d1", "target_handle": "in"},
		},
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	}
	status, resp = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph",
		map[string]any{"graph": g})
	require.Equalf(t, http.StatusOK, status, "the draft must save: %v", resp)

	status, resp = doJSON(t, srv, token, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{})
	require.Equalf(t, http.StatusAccepted, status, "the stored test body is used, so nothing need be supplied: %v", resp)

	detail := waitRunStatus(t, srv, token, flowID, "succeeded", "")
	require.Equal(t, "succeeded", nodeState(t, detail, "d1"), "the stand-in request must reach the rest of the graph")

	payload, meta := debugMessage(t, detail, "w1")
	body, ok := payload.(map[string]any)
	require.Truef(t, ok, "the webhook's output payload is an object: %v", payload)
	assert.Equal(t, "ON", body["state"], "the body the author typed into the node is what the run carried")

	assert.Equal(t, "/hook/test-run-token", meta["path"],
		"the stand-in request is filled in around the body, so a rule reading meta.path works in the editor too")
	assert.JSONEq(t, `{"state":"ON"}`, meta["body"].(string),
		"the raw body is present as well, for a rule that reads it rather than the parsed payload")
}

// debugMessage returns the payload and meta a node emitted on its first output
// port, read out of the run's node_state events.
func debugMessage(t *testing.T, detail map[string]any, nodeID string) (any, map[string]any) {
	t.Helper()
	events, _ := detail["events"].([]any)
	for i := len(events) - 1; i >= 0; i-- {
		ev, _ := events[i].(map[string]any)
		if ev == nil || ev["node_id"] != nodeID || ev["event"] != "node_state" {
			continue
		}
		raw, _ := ev["payload"].(string)
		var body struct {
			Outputs map[string]struct {
				Payload any            `json:"payload"`
				Meta    map[string]any `json:"meta"`
			} `json:"outputs"`
		}
		if json.Unmarshal([]byte(raw), &body) != nil {
			continue
		}
		for _, out := range body.Outputs {
			return out.Payload, out.Meta
		}
	}
	require.FailNowf(t, "no output message was recorded", "node %s emitted nothing the run history captured", nodeID)
	return nil, nil
}
