//go:build karakuri_integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFlowLifecycle pushes a flow through the loop an operator actually uses —
// create, save a graph, deploy, run — against the real server, and checks the
// run is counted where the dashboards read it from. It is the one test that
// proves the router, the engine, the version store, and the metrics registry
// are wired to each other; every piece is exercised in isolation elsewhere,
// which is exactly why nothing else catches a break between them.
func TestFlowLifecycle(t *testing.T) {
	const graph = `{"nodes":[
	  {"id":"t","type":"manual","position":{"x":0,"y":0},"config":{"payload":{"smoke":true}}},
	  {"id":"d","type":"debug","position":{"x":240,"y":0}}],
	 "edges":[{"id":"e1","source":"t","source_handle":"out","target":"d","target_handle":"in"}],
	 "viewport":{"x":0,"y":0,"zoom":1}}`

	flowID := srv.createFlow(t, "Lifecycle Flow", graph)
	srv.deploy(t, flowID)
	srv.data(t, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{}, nil)
	srv.waitForRunStatus(t, flowID, "succeeded")

	// The flow's name is slugged into the metric label, so the counter names
	// the flow an operator would look for rather than its UUID.
	status, body := srv.do(t, http.MethodGet, "/metrics", nil)
	require.Equal(t, http.StatusOK, status, "the metrics endpoint must be served")
	assert.Contains(t, string(body),
		`karakuri_runs_total{flow="lifecycle-flow",status="succeeded"} 1`,
		"a succeeded run must be counted against its flow in the exposition")
}

// TestSynchronousWebhookIsAnsweredByTheGraph covers the request that stays open
// for the length of a run: a webhook in synchronous mode starts the flow, a
// JavaScript node computes the reply, and its output returns to the Webhook
// to the caller still waiting on the socket. The sandbox, the response
// registry, and the /hook route have to agree on one request for this to
// answer at all, and nothing short of a real HTTP call proves they do.
func TestSynchronousWebhookIsAnsweredByTheGraph(t *testing.T) {
	const token = "integration-script-hook"
	script := "var n = Number(payload.value || 0); console.log('doubling', n); " +
		"return { doubled: n * 2, method: meta.method };"

	flowID := srv.createFlow(t, "Webhook Script", webhookGraph(t, token, true, map[string]any{
		"id":       "s",
		"type":     "script",
		"position": map[string]any{"x": 240, "y": 0},
		"config":   map[string]any{"language": "javascript", "script": script},
	}))
	srv.deploy(t, flowID)
	srv.enable(t, flowID)

	resp, err := srv.client.Post(srv.baseURL+"/hook/"+token, "application/json",
		strings.NewReader(`{"value":21}`))
	require.NoError(t, err, "the webhook must accept the request\n%s", srv.tail())
	defer func() { _ = resp.Body.Close() }()

	var reply struct {
		Doubled int    `json:"doubled"`
		Method  string `json:"method"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reply),
		"the webhook must answer with the script's JSON return value")
	assert.Equal(t, 42, reply.Doubled, "the script's computed value must reach the still-open request")
	assert.Equal(t, http.MethodPost, reply.Method,
		"request metadata must reach the script, not just the body")
}

// TestRunVariablesShapeTheWebhookReply covers the answer path with no HTTP
// Response node in the graph at all: a script sets the response_code and
// response_body run variables, and completing the run is what answers the
// caller. The status code is the assertion that matters — it can only come
// from the variable, since nothing else in the graph names 202.
func TestRunVariablesShapeTheWebhookReply(t *testing.T) {
	const token = "integration-vars-hook"
	script := "vars.response_code = 202; " +
		"vars.response_body = { queued: true, value: payload.value }; return payload;"

	flowID := srv.createFlow(t, "Webhook Vars", webhookGraph(t, token, false, map[string]any{
		"id":       "s",
		"type":     "script",
		"position": map[string]any{"x": 240, "y": 0},
		"config":   map[string]any{"language": "javascript", "script": script},
	}))
	srv.deploy(t, flowID)
	srv.enable(t, flowID)

	resp, err := srv.client.Post(srv.baseURL+"/hook/"+token, "application/json",
		strings.NewReader(`{"value":7}`))
	require.NoError(t, err, "the webhook must accept the request\n%s", srv.tail())
	defer func() { _ = resp.Body.Close() }()

	var reply struct {
		Queued bool `json:"queued"`
		Value  int  `json:"value"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&reply),
		"the response_body variable must be served as the reply body")
	assert.Equal(t, http.StatusAccepted, resp.StatusCode,
		"the response_code variable must set the status, since no node in the graph does")
	assert.True(t, reply.Queued, "the response_body variable must reach the caller")
	assert.Equal(t, 7, reply.Value, "the reply must carry the payload the script read")
}

// TestSandboxDeniesFilesystemAccess pins the sandbox boundary against the real
// server's own policy rather than a test's: a bash script reading /etc/passwd
// has to fail the run. The unit suite covers the sandbox's own refusal, but
// only a deployed run proves the serve command installed the policy in the
// first place — an instance that shipped with it unset would pass every unit
// test and read the file here.
func TestSandboxDeniesFilesystemAccess(t *testing.T) {
	const graph = `{"nodes":[
	  {"id":"t","type":"manual","position":{"x":0,"y":0},"config":{"payload":{}}},
	  {"id":"s","type":"script","position":{"x":240,"y":0},
	   "config":{"language":"bash","script":"cat /etc/passwd"}}],
	 "edges":[{"id":"e1","source":"t","source_handle":"out","target":"s","target_handle":"in"}],
	 "viewport":{"x":0,"y":0,"zoom":1}}`

	flowID := srv.createFlow(t, "Sandbox Deny", graph)
	srv.deploy(t, flowID)
	srv.data(t, http.MethodPost, "/api/flows/"+flowID+"/test", map[string]any{}, nil)
	srv.waitForRunStatus(t, flowID, "failed")
}

// webhookGraph builds a graph fed by a synchronous webhook, chaining the given
// nodes after it in order. The timeout is generous because the caller blocks
// on the whole run, and a slow first sandbox start would otherwise read as a
// broken response path.
func webhookGraph(t *testing.T, token string, returnToWebhook bool, chain ...map[string]any) string {
	t.Helper()

	nodes := []map[string]any{{
		"id":       "w",
		"type":     "webhook",
		"position": map[string]any{"x": 0, "y": 0},
		"config": map[string]any{
			"token":            token,
			"methods":          http.MethodPost,
			"response_mode":    "sync",
			"response_timeout": 30,
		},
	}}
	var edges []map[string]any
	previous := "w"
	for i, node := range chain {
		nodes = append(nodes, node)
		id, _ := node["id"].(string)
		edges = append(edges, map[string]any{
			"id": fmt.Sprintf("e%d", i+1), "source": previous, "source_handle": "out",
			"target": id, "target_handle": "in",
		})
		previous = id
	}
	if returnToWebhook {
		edges = append(edges, map[string]any{
			"id": fmt.Sprintf("e%d", len(edges)+1), "source": previous, "source_handle": "out",
			"target": "w", "target_handle": "response",
		})
	}

	graph, err := json.Marshal(map[string]any{
		"nodes":    nodes,
		"edges":    edges,
		"viewport": map[string]any{"x": 0, "y": 0, "zoom": 1},
	})
	require.NoError(t, err, "the webhook graph fixture must encode")
	return string(graph)
}
