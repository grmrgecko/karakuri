package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupNodeChat uses the production provider driver, router, database and engine.
func setupNodeChat(t *testing.T, peer *httptest.Server, nodeType string, config map[string]any, subgraph bool) (*httptest.Server, string, string, string) {
	t.Helper()
	srv, token := setupTestServer(t)
	for _, nt := range []engine.NodeType{ainodes.ModelNode{}, ainodes.AgentNode{}, ainodes.BasicChainNode{}, ainodes.ToolNode{}} {
		name := nt.Meta().Type
		previous, registered := engine.Default().Get(name)
		engine.Default().Register(nt)
		t.Cleanup(func() {
			if registered {
				engine.Default().Register(previous)
			} else {
				engine.Default().Unregister(name)
			}
		})
	}
	connectionID := uuid.New()
	raw, err := json.Marshal(map[string]any{"provider": "openai", "base_url": peer.URL + "/v1", "api_key": "fixture"})
	require.NoError(t, err)
	driver, err := ai.New(integrations.ConnModel{ID: connectionID, Kind: ai.KindAI, Config: raw}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)
	runner := engine.New(engine.Options{Registry: engine.Default(), ConnResolver: func(id string) (any, error) {
		if id != connectionID.String() {
			return nil, fmt.Errorf("unknown fixture connection %s", id)
		}
		return driver, nil
	}})
	flowops.SetEngine(runner)
	t.Cleanup(func() { flowops.SetEngine(nil); runner.Stop() })
	status, result := doJSON(t, srv, token, http.MethodPost, "/api/flows", map[string]any{"name": "AI chat fixture"})
	require.Equal(t, http.StatusCreated, status)
	flowID := result["data"].(map[string]any)["id"].(string)
	modelConfig, err := json.Marshal(map[string]any{"connection": connectionID.String(), "model": "fixture-model", "temperature": 0.2})
	require.NoError(t, err)
	nodeConfig, err := json.Marshal(config)
	require.NoError(t, err)
	graph := flow.Graph{Nodes: []flow.Node{
		{ID: "model", Type: "ai-model", Config: modelConfig},
		{ID: "agent", Type: nodeType, Config: nodeConfig, Retry: &flow.RetryPolicy{MaxAttempts: 3}},
		{ID: "upstream", Type: "manual"}, {ID: "downstream", Type: "fail", Config: json.RawMessage(`{"message":"ordinary output must not run"}`)},
	}, Edges: []flow.Edge{
		{ID: "model", Source: "model", SourceHandle: "out", Target: "agent", TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
		{ID: "in", Source: "upstream", SourceHandle: "out", Target: "agent", TargetHandle: "in"},
		{ID: "out", Source: "agent", SourceHandle: "out", Target: "downstream", TargetHandle: "in"},
	}}
	if nodeType == "ai-agent" {
		graph.Nodes = append(graph.Nodes, flow.Node{ID: "tool", Type: "tool", Config: json.RawMessage(`{"name":"lookup","description":"Echo query","tool_args":[{"id":"query","type":"string","required":true}]}`)}, flow.Node{ID: "branch", Type: "change", Config: json.RawMessage(`{"rules":[{"op":"set","target":"meta.visited","from":"string","value":"yes"}]}`)})
		graph.Edges = append(graph.Edges,
			flow.Edge{ID: "tools", Source: "tool", SourceHandle: "out", Target: "agent", TargetHandle: flow.ParamPortID("tools"), Kind: flow.EdgeValue},
			flow.Edge{ID: "call", Source: "tool", SourceHandle: flow.ToolCallPort, Target: "branch", TargetHandle: "in"},
			flow.Edge{ID: "return", Source: "branch", SourceHandle: "out", Target: "tool", TargetHandle: flow.ToolReturnPort})
	}
	subgraphID := ""
	if subgraph {
		subgraphID = "local"
		graph = flow.Graph{Nodes: []flow.Node{}, Edges: []flow.Edge{}, Subgraphs: map[string]flow.Graph{subgraphID: graph}}
	}
	status, result = doJSON(t, srv, token, http.MethodPut, "/api/flows/"+flowID+"/graph", map[string]any{"graph": graph})
	require.Equalf(t, http.StatusOK, status, "the draft must save: %v", result)
	return srv, token, flowID, subgraphID
}

// nodeChatEvents decodes the real NDJSON stream through its terminal event.
func nodeChatEvents(t *testing.T, srv *httptest.Server, token, path string, body any) ([]map[string]any, int) {
	t.Helper()
	raw, err := json.Marshal(body)
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, srv.URL+path, bytes.NewReader(raw))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	response, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	var events []map[string]any
	decoder := json.NewDecoder(response.Body)
	for {
		var event map[string]any
		err := decoder.Decode(&event)
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		events = append(events, event)
	}
	return events, response.StatusCode
}

// TestNodeChatContinuation pins configured input rendering and OpenAI replay
// through the authenticated API, including an actual graph tool call/return.
func TestNodeChatContinuation(t *testing.T) {
	for _, tc := range []struct {
		name, nodeType, mode string
		subgraph             bool
	}{
		{"chain prompt", "ai-basic-chain", "prompt", false},
		{"chain transcript in local definition", "ai-basic-chain", "transcript", true},
		{"agent incoming prompt with tool branch", "ai-agent", "input", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var requests []map[string]any
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// The connection's driver lists the provider's models when it
				// starts, racing the chat requests below; that listing is not
				// part of the conversation.
				if r.Method == http.MethodGet {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"data":[{"id":"fixture-model"}]}`)
					return
				}
				var body map[string]any
				require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
				mu.Lock()
				index := len(requests)
				requests = append(requests, body)
				mu.Unlock()
				assert.Equal(t, "fixture-model", body["model"])
				assert.Equal(t, true, body["stream"])
				assert.Equal(t, 0.2, body["temperature"])
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.nodeType == "ai-agent" && index == 0 {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call-1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"query\\\":\\\"rain\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"answer {{ 1 + 2 }}\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
				}
			}))
			t.Cleanup(peer.Close)
			config := map[string]any{"input_mode": tc.mode, "prompt": "request {{ payload }}", "system": "instructions {{ payload }}"}
			if tc.mode == "transcript" {
				config["transcript"] = []map[string]any{{"role": "system", "text": "extra {{ payload }}"}, {"role": "user", "text": "request {{ payload }}"}}
			}
			srv, token, flowID, subgraphID := setupNodeChat(t, peer, tc.nodeType, config, tc.subgraph)
			path := "/api/flows/" + flowID + "/nodes/agent/chat"
			_, before := doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID, nil)
			events, status := nodeChatEvents(t, srv, token, path, map[string]any{"prompt": "first", "subgraph_id": subgraphID})
			require.Equal(t, http.StatusOK, status)
			require.Equalf(t, "done", events[len(events)-1]["type"], "the node must complete: %v", events)
			var replay map[string]any
			var metadata map[string]any
			for _, event := range events {
				if event["type"] == "response.completed" {
					replay = event["response"].(map[string]any)
				}
				if event["type"] == "result" {
					metadata = event["meta"].(map[string]any)
				}
			}
			require.NotNil(t, replay)
			if tc.nodeType == "ai-agent" {
				trace := metadata["tool_calls"].([]any)
				require.Len(t, trace, 1)
				assert.JSONEq(t, `{"query":"rain"}`, trace[0].(map[string]any)["result"].(string), "the graph branch must return the call payload")
			}
			events, status = nodeChatEvents(t, srv, token, path, map[string]any{"prompt": "second", "subgraph_id": subgraphID, "response": replay})
			require.Equal(t, http.StatusOK, status)
			require.Equalf(t, "done", events[len(events)-1]["type"], "the second turn must complete: %v", events)
			mu.Lock()
			last := requests[len(requests)-1]
			mu.Unlock()
			messages := last["messages"].([]any)
			system := messages[0].(map[string]any)["content"].(string)
			assert.Contains(t, system, "instructions second")
			if tc.mode == "transcript" {
				assert.Contains(t, system, "extra second")
			}
			encoded, err := json.Marshal(messages)
			require.NoError(t, err)
			assert.Contains(t, string(encoded), "answer {{ 1 + 2 }}", "provider text must remain data during replay")
			assert.Contains(t, string(encoded), "first")
			assert.Contains(t, string(encoded), "second")
			_, after := doJSON(t, srv, token, http.MethodGet, "/api/flows/"+flowID, nil)
			assert.Equal(t, before, after, "chat must leave the draft and deployment unchanged")
			var count int64
			require.NoError(t, db.DB.Model(&models.Run{}).Count(&count).Error)
			assert.Zero(t, count, "chat must not replace the last-run history")
			_, unauthorized := nodeChatEvents(t, srv, "", path, map[string]any{"prompt": "third"})
			assert.Equal(t, http.StatusUnauthorized, unauthorized)
		})
	}
}

// TestNodeChatDisconnect pins cancellation at the HTTP/provider boundary and
// confirms a failing provider is not retried despite the draft retry policy.
func TestNodeChatDisconnect(t *testing.T) {
	var fail atomic.Bool
	var calls atomic.Int32
	started := make(chan struct{}, 1)
	canceled := make(chan struct{}, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		calls.Add(1)
		if fail.Load() {
			http.Error(w, "provider unavailable", http.StatusServiceUnavailable)
			return
		}
		started <- struct{}{}
		<-r.Context().Done()
		canceled <- struct{}{}
	}))
	t.Cleanup(peer.Close)
	srv, token, flowID, _ := setupNodeChat(t, peer, "ai-basic-chain", map[string]any{"input_mode": "input"}, false)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/flows/"+flowID+"/nodes/agent/chat", bytes.NewBufferString(`{"prompt":"hello"}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer response.Body.Close()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("the provider request did not start")
	}
	cancel()
	select {
	case <-canceled:
	case <-time.After(3 * time.Second):
		t.Fatal("disconnect did not cancel the provider request")
	}
	fail.Store(true)
	events, status := nodeChatEvents(t, srv, token, "/api/flows/"+flowID+"/nodes/agent/chat", map[string]any{"prompt": "fail"})
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, "error", events[len(events)-1]["type"])
	assert.Equal(t, int32(2), calls.Load(), "one canceled request plus one failed request must not trigger retries")
}
