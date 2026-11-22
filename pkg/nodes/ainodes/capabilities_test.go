package ainodes_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

type capabilityToolSource struct{}

type streamSinkNode struct{ got chan *engine.Msg }

func (capabilityToolSource) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "capability-tool", Label: "Capability tool", Pure: true,
		Outputs: []engine.PortSpec{{ID: "out", Type: "ai-tool"}},
	}
}

func (capabilityToolSource) Execute(context.Context, engine.ExecContext, map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", &engine.Msg{Payload: map[string]any{
		"tools": []any{map[string]any{"name": "weather"}},
	}}), nil
}

func (s *streamSinkNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "stream-sink", Label: "Stream sink",
		Inputs: []engine.PortSpec{{ID: "in", Type: ainodes.PortAIStream, Optional: true}},
	}
}

func (s *streamSinkNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	s.got <- inputs["in"]
	return nil, nil
}

// TestHermesModelCapability pins the Hermes gateway boundary: its profile
// handles tools internally, so a plain model call works while a Karakuri tool
// wire is rejected before any HTTP request reaches Hermes.
func TestHermesModelCapability(t *testing.T) {
	var calls atomic.Int32
	var completions atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		switch r.URL.Path {
		case "/v1/chat/completions":
			completion := completions.Add(1)
			if completion == 1 {
				assert.Empty(t, r.Header.Get("X-Hermes-Session-Id"), "the first turn has no provider session to resume")
			} else {
				assert.Equal(t, "tool-session", r.Header.Get("X-Hermes-Session-Id"), "the next Basic Chain turn must resume Hermes' session")
			}
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			assert.Equal(t, "hermes-profile", body["model"], "the model selected on AI Model must reach Hermes")
			assert.NotContains(t, body, "tools", "Hermes owns its tool catalog")
			w.Header().Set("X-Hermes-Session-Id", "tool-session")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"done"}}],"usage":{"prompt_tokens":2,"completion_tokens":1}}`))
		case "/api/sessions/tool-session/messages":
			_, _ = w.Write([]byte(`{"data":[
				{"role":"user","content":"hello"},
				{"role":"assistant","content":"","tool_calls":[{"id":"call_1","function":{"name":"built_in","arguments":"{}"}}]},
				{"role":"tool","content":"worked","tool_call_id":"call_1","tool_name":"built_in"},
				{"role":"assistant","content":"done"}
			]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	connectionID := uuid.New()
	driver, err := ai.New(integrations.ConnModel{
		ID: connectionID, Kind: ai.KindAI,
		Config: []byte(`{"provider":"hermes","base_url":"` + srv.URL + `/v1","api_key":"test-key"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	reg.Register(stubTrigger{})
	reg.Register(capabilityToolSource{})
	sink := &sinkNode{got: make(chan *engine.Msg, 4)}
	reg.Register(sink)
	runner := engine.New(engine.Options{
		Registry: reg, Clock: engine.NewFakeClock(time.Now()),
		ConnResolver: func(string) (any, error) { return driver, nil },
	})
	runner.Start()
	defer runner.Stop()

	modelConfig, err := json.Marshal(map[string]any{"connection": connectionID.String(), "model": "hermes-profile"})
	require.NoError(t, err)
	chainConfig, err := json.Marshal(map[string]any{"input_mode": "input"})
	require.NoError(t, err)

	chainGraph := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "trigger", Type: "trigger"},
			{ID: "model", Type: "ai-model", Config: modelConfig},
			{ID: "chain", Type: "ai-basic-chain", Config: chainConfig},
			{ID: "sink", Type: "sink"},
			{ID: "transcript-sink", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "trigger-chain", Source: "trigger", SourceHandle: "out", Target: "chain", TargetHandle: "in"},
			{ID: "model-chain", Source: "model", SourceHandle: "out", Target: "chain", TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "chain-sink", Source: "chain", SourceHandle: "out", Target: "sink", TargetHandle: "in"},
			{ID: "chain-transcript-sink", Source: "chain", SourceHandle: "transcript", Target: "transcript-sink", TargetHandle: "in"},
		},
	}
	handle, err := runner.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "hermes-basic-chain", FlowVersionID: 1,
		TriggerNodeID: "trigger", Graph: chainGraph, InitialMsg: &engine.Msg{Payload: "hello"},
	})
	require.NoError(t, err)
	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the basic chain run did not settle")
	}
	require.Equalf(t, "succeeded", handle.Snapshot().Status, "the basic chain must accept Hermes: %s", handle.Snapshot().Error)
	first, second := <-sink.got, <-sink.got
	out, transcript := first, second
	if payload, ok := first.Payload.(map[string]any); ok && payload["transcript"] != nil {
		out, transcript = second, first
	}
	assert.Equal(t, "done", out.Payload)
	envelope, ok := transcript.Payload.(map[string]any)
	require.True(t, ok, "the basic chain transcript output must be an envelope")
	transcriptEntries, ok := envelope["transcript"].([]map[string]any)
	require.True(t, ok)
	require.Len(t, transcriptEntries, 4)
	assert.Contains(t, transcriptEntries[1], "tool_calls", "Hermes' persisted built-in tool chain must replace the final-only completion transcript")
	responses, ok := envelope["responses"].([]json.RawMessage)
	require.True(t, ok, "the basic chain envelope must retain raw provider responses")
	require.Len(t, responses, 2, "the raw completion and session history must both be retained")
	var providerResponse map[string]any
	require.NoError(t, json.Unmarshal(responses[0], &providerResponse))
	assert.Equal(t, float64(2), providerResponse["usage"].(map[string]any)["prompt_tokens"])
	assert.Equal(t, int32(2), calls.Load())

	continued := map[string]any{}
	for key, value := range envelope {
		continued[key] = value
	}
	continuedEntries := append([]map[string]any(nil), transcriptEntries...)
	continued["transcript"] = append(continuedEntries, map[string]any{"role": "user", "text": "what happened?"})
	handle, err = runner.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "hermes-basic-chain-continuation", FlowVersionID: 1,
		TriggerNodeID: "trigger", Graph: chainGraph, InitialMsg: &engine.Msg{Payload: continued},
	})
	require.NoError(t, err)
	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the continued basic chain run did not settle")
	}
	require.Equalf(t, "succeeded", handle.Snapshot().Status, "the continued basic chain must settle: %s", handle.Snapshot().Error)
	<-sink.got
	<-sink.got
	assert.Equal(t, int32(4), calls.Load(), "each wired turn makes one completion and one Hermes history request")

	agentConfig, err := json.Marshal(map[string]any{"prompt": "hello"})
	require.NoError(t, err)
	agentGraph := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "trigger", Type: "trigger"},
			{ID: "model", Type: "ai-model", Config: modelConfig},
			{ID: "tools", Type: "capability-tool"},
			{ID: "agent", Type: "ai-agent", Config: agentConfig},
		},
		Edges: []flow.Edge{
			{ID: "trigger-agent", Source: "trigger", SourceHandle: "out", Target: "agent", TargetHandle: "in"},
			{ID: "model-agent", Source: "model", SourceHandle: "out", Target: "agent", TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "tools-agent", Source: "tools", SourceHandle: "out", Target: "agent", TargetHandle: flow.ParamPortID("tools"), Kind: flow.EdgeValue},
		},
	}
	handle, err = runner.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "hermes-tool-capability", FlowVersionID: 1,
		TriggerNodeID: "trigger", Graph: agentGraph, InitialMsg: &engine.Msg{},
	})
	require.NoError(t, err)
	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the agent capability run did not settle")
	}
	snapshot := handle.Snapshot()
	assert.Equal(t, "failed", snapshot.Status)
	assert.Contains(t, snapshot.Error, `model "hermes-profile" does not support tools`)
	assert.Equal(t, int32(4), calls.Load(), "the capability check must run before a request reaches Hermes")
}

// TestBasicChainStreamingResponse covers the node boundary rather than only
// the provider parser: enabling Stream changes the Response port's declared
// type and emits deltas before a terminal event containing the intact envelope.
func TestBasicChainStreamingResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/chat/completions":
			body := captureRequest(t, r)
			assert.Equal(t, true, body["stream"])
			w.Header().Set("X-Hermes-Session-Id", "stream-session")
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(
				"event: hermes.tool.progress\n" +
					"data: {\"toolCallId\":\"call-ha-1\",\"tool\":\"ha_call_service\",\"label\":\"Turn on light\",\"status\":\"running\"}\n\n" +
					"event: hermes.tool.progress\n" +
					"data: {\"toolCallId\":\"call-ha-1\",\"tool\":\"ha_call_service\",\"status\":\"completed\"}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"Lights \"}}]}\n\n" +
					"data: {\"choices\":[{\"delta\":{\"content\":\"on\"},\"finish_reason\":\"stop\"}]}\n\n" +
					"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2}}\n\n" +
					"data: [DONE]\n\n",
			))
		case "/api/sessions/stream-session/messages":
			_, _ = w.Write([]byte(`{"data":[{"role":"user","content":"turn on the lights"},{"role":"assistant","content":"Lights on"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	connectionID := uuid.New()
	driver, err := ai.New(integrations.ConnModel{
		ID: connectionID, Kind: ai.KindAI,
		Config: []byte(`{"provider":"hermes","base_url":"` + srv.URL + `/v1","api_key":"test-key"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	reg.Register(stubTrigger{})
	sink := &streamSinkNode{got: make(chan *engine.Msg, 6)}
	reg.Register(sink)
	runner := engine.New(engine.Options{
		Registry: reg, Clock: engine.NewFakeClock(time.Now()),
		ConnResolver: func(string) (any, error) { return driver, nil },
	})
	runner.Start()
	defer runner.Stop()

	modelConfig, err := json.Marshal(map[string]any{"connection": connectionID.String(), "model": "hermes-profile"})
	require.NoError(t, err)
	chainConfig := json.RawMessage(`{"input_mode":"input","stream":true}`)
	responsePort := ainodes.BasicChainNode{}.Meta().Outputs[1]
	assert.Equal(t, ainodes.PortAIStream, flow.ResolvePortType(responsePort, chainConfig))
	agentResponsePort := ainodes.AgentNode{}.Meta().Outputs[1]
	assert.Equal(t, ainodes.PortAIStream, flow.ResolvePortType(agentResponsePort, chainConfig), "AI Agent and AI Basic Chain must expose the same streaming response contract")
	assert.Equal(t, flow.PortObject, flow.ResolvePortType(responsePort, json.RawMessage(`{"stream":false}`)))

	graph := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "trigger", Type: "trigger"},
			{ID: "model", Type: "ai-model", Config: modelConfig},
			{ID: "chain", Type: "ai-basic-chain", Config: chainConfig},
			{ID: "sink", Type: "stream-sink"},
		},
		Edges: []flow.Edge{
			{ID: "trigger-chain", Source: "trigger", SourceHandle: "out", Target: "chain", TargetHandle: "in"},
			{ID: "model-chain", Source: "model", SourceHandle: "out", Target: "chain", TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "response-sink", Source: "chain", SourceHandle: "transcript", Target: "sink", TargetHandle: "in"},
		},
	}
	handle, err := runner.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "streaming-basic-chain", FlowVersionID: 1,
		TriggerNodeID: "trigger", Graph: graph, InitialMsg: &engine.Msg{Payload: "turn on the lights"},
	})
	require.NoError(t, err)
	select {
	case <-handle.Done():
	case <-time.After(5 * time.Second):
		require.FailNow(t, "the streaming basic chain did not settle")
	}
	require.Equalf(t, "succeeded", handle.Snapshot().Status, "the streaming chain must settle: %s", handle.Snapshot().Error)

	events := make(map[int]map[string]any)
	for range 5 {
		msg := <-sink.got
		event, ok := msg.Payload.(map[string]any)
		require.True(t, ok)
		index, ok := event["index"].(int)
		require.True(t, ok)
		events[index] = event
	}
	assert.Equal(t, map[string]any{
		"type": "tool.progress", "tool_call_id": "call-ha-1", "tool_name": "ha_call_service",
		"label": "Turn on light", "status": "running", "index": 0,
	}, events[0])
	assert.Equal(t, map[string]any{
		"type": "tool.progress", "tool_call_id": "call-ha-1", "tool_name": "ha_call_service",
		"label": "", "status": "completed", "index": 1,
	}, events[1])
	assert.Equal(t, map[string]any{"type": "content.delta", "delta": "Lights ", "index": 2}, events[2])
	assert.Equal(t, map[string]any{"type": "content.delta", "delta": "on", "index": 3}, events[3])
	completed := events[4]
	assert.Equal(t, "response.completed", completed["type"])
	assert.Equal(t, "Lights on", completed["output"])
	envelope, ok := completed["response"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "stream-session", envelope["session_id"])
	assert.Equal(t, []map[string]any{{"role": "user", "text": "turn on the lights"}, {"role": "assistant", "text": "Lights on"}}, envelope["transcript"])
	responses, ok := envelope["responses"].([]json.RawMessage)
	require.True(t, ok)
	assert.Len(t, responses, 2, "the SSE capture and Hermes history response must remain available for continuation")
}

func captureRequest(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
	return body
}
