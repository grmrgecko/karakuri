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
	db "github.com/grmrgecko/karakuri/pkg/database"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/migrate"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
	"github.com/ncruces/go-sqlite3/vfs/memdb"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// setupMemories points the package-level store the nodes read at a fresh
// database, the way serve.go does at boot.
func setupMemories(t *testing.T) {
	t.Helper()
	require.NoError(t, db.Init(db.Options{
		Type:         "sqlite",
		DSN:          memdb.TestDB(t),
		MaxOpenConns: 1,
		MaxIdleConns: 1,
	}))
	require.NoError(t, migrate.AutoMigrate())
	memories.S = memories.NewStore()
	require.NoError(t, memories.S.Load())
}

// TestAgentConversationMemory is the memory loop end to end across two runs:
// the first exchange is saved to the session's transcript, the second run
// replays it ahead of the new prompt, the model saves a fact through the
// remember tool the memory offers, and the fact lands in the store.
func TestAgentConversationMemory(t *testing.T) {
	setupMemories(t)
	store, err := memories.S.CreateStore(memories.StoreParams{Name: "House"})
	require.NoError(t, err)

	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
			Tools    []map[string]any `json:"tools"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))

		switch calls.Add(1) {
		case 1:
			// Run one: a fresh conversation is one user turn, but the memory's
			// own tools are already offered.
			toolNames := []string{}
			for _, tool := range body.Tools {
				toolNames = append(toolNames, tool["function"].(map[string]any)["name"].(string))
			}
			assert.ElementsMatch(t, []string{"remember", "recall"}, toolNames,
				"the memory contributes its tools without any tool node wired")
			require.Len(t, body.Messages, 1, "nothing to replay on the first run")
			w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
				"content": "The heating is set to 21."
			}}], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}`))
		case 2:
			// Run two: the first exchange replays ahead of the new prompt.
			require.Len(t, body.Messages, 3, "the saved exchange must replay: user, assistant, new user")
			assert.Equal(t, "user", body.Messages[0]["role"])
			assert.Equal(t, "What is the heating at?", body.Messages[0]["content"])
			assert.Equal(t, "assistant", body.Messages[1]["role"])
			assert.Equal(t, "The heating is set to 21.", body.Messages[1]["content"])
			assert.Equal(t, "Remember I prefer 20 degrees.", body.Messages[2]["content"])
			w.Write([]byte(`{"choices": [{"finish_reason": "tool_calls", "message": {
				"tool_calls": [{"id": "call_1", "function": {"name": "remember",
					"arguments": "{\"text\":\"The user prefers the heating at 20 degrees\",\"importance\":0.8}"}}]
			}}], "usage": {"prompt_tokens": 20, "completion_tokens": 6}}`))
		default:
			last := body.Messages[len(body.Messages)-1]
			require.Equal(t, "tool", last["role"])
			assert.Contains(t, last["content"], "Remembered",
				"the remember tool must confirm to the model")
			w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
				"content": "Noted, 20 degrees from now on."
			}}], "usage": {"prompt_tokens": 30, "completion_tokens": 7}}`))
		}
	}))
	defer llm.Close()

	llmID := uuid.New()
	llmDrv, err := ai.New(integrations.ConnModel{
		ID: llmID, Kind: ai.KindAI,
		Config: []byte(`{"provider":"openai-compatible","base_url":"` + llm.URL + `/v1"}`),
	}, integrations.Deps{Logger: zap.NewNop()})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	sink := &sinkNode{got: make(chan *engine.Msg, 1)}
	reg.Register(stubTrigger{})
	reg.Register(sink)
	e := engine.New(engine.Options{
		Registry: reg,
		Clock:    engine.NewFakeClock(time.Now()),
		ConnResolver: func(id string) (any, error) {
			return map[string]any{llmID.String(): llmDrv}[id], nil
		},
	})
	e.Start()
	defer e.Stop()

	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model"})
	memoryCfg, _ := json.Marshal(map[string]any{"store": "House", "session": "{{ topic }}"})
	agentCfg, _ := json.Marshal(map[string]any{"prompt": "{{ payload }}"})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "m", Type: "ai-model", Config: modelCfg},
			{ID: "mem", Type: "ai-memory", Config: memoryCfg},
			{ID: "a", Type: "ai-agent", Config: agentCfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "a", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "mem", SourceHandle: "out", Target: "a",
				TargetHandle: flow.ParamPortID("memory"), Kind: flow.EdgeValue},
			{ID: "e4", Source: "a", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	run := func(prompt string) *engine.Msg {
		h, err := e.RunFlow(engine.RunOptions{
			FlowID: uuid.New(), FlowName: "memory-test", FlowVersionID: 1,
			TriggerNodeID: "t", Graph: g,
			InitialMsg: &engine.Msg{Payload: prompt, Topic: "kitchen"},
		})
		require.NoError(t, err)
		select {
		case <-h.Done():
		case <-time.After(30 * time.Second):
			require.FailNowf(t, "the run never settled", "snapshot: %+v", h.Snapshot())
		}
		snap := h.Snapshot()
		require.Equalf(t, "succeeded", snap.Status, "run must succeed: %s", snap.Error)
		return <-sink.got
	}

	out := run("What is the heating at?")
	assert.Equal(t, "The heating is set to 21.", out.Payload)
	memMeta := out.Meta["memory"].(map[string]any)
	assert.Equal(t, "House", memMeta["store"])
	assert.Equal(t, "kitchen", memMeta["session"], "the session template renders against the incoming message")

	out = run("Remember I prefer 20 degrees.")
	assert.Equal(t, "Noted, 20 degrees from now on.", out.Payload)

	// The fact saved through the tool is in the store, and the transcript
	// holds both exchanges.
	facts := memories.S.Browse(context.Background(), store.ID, "", "", "fact", 0)
	require.Len(t, facts, 1, "the remember tool call must land in the store")
	assert.Contains(t, facts[0].Text, "20 degrees")
	assert.Equal(t, 0.8, facts[0].Importance)
	turns := memories.S.Transcript(store.ID, "kitchen", 0)
	require.Len(t, turns, 4, "both exchanges are saved")

	recalled := memories.S.Recall(context.Background(), store.ID, "what heating temperature does the user prefer?", memories.RecallOpts{})
	require.NotEmpty(t, recalled, "the saved fact is recallable")
}

// TestMemoryFlowNodes covers the two flow-side nodes with no model: Remember
// stores an interpolated fact and passes the message through, Recall finds it
// and emits the results as the payload.
func TestMemoryFlowNodes(t *testing.T) {
	setupMemories(t)
	store, err := memories.S.CreateStore(memories.StoreParams{Name: "Notes"})
	require.NoError(t, err)

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	sink := &sinkNode{got: make(chan *engine.Msg, 1)}
	reg.Register(stubTrigger{})
	reg.Register(sink)
	e := engine.New(engine.Options{Registry: reg, Clock: engine.NewFakeClock(time.Now())})
	e.Start()
	defer e.Stop()

	rememberCfg, _ := json.Marshal(map[string]any{
		"store": "Notes", "text": "Package delivered: {{ payload.item }}", "tags": []string{"delivery"},
	})
	recallCfg, _ := json.Marshal(map[string]any{
		"store": "Notes", "query": "what package was delivered?",
	})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "r", Type: "memory-remember", Config: rememberCfg},
			{ID: "q", Type: "memory-recall", Config: recallCfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "r", TargetHandle: "in"},
			{ID: "e2", Source: "r", SourceHandle: "out", Target: "q", TargetHandle: "in"},
			{ID: "e3", Source: "q", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	h, err := e.RunFlow(engine.RunOptions{
		FlowID: uuid.New(), FlowName: "memnode-test", FlowVersionID: 1,
		TriggerNodeID: "t", Graph: g,
		InitialMsg: &engine.Msg{Payload: map[string]any{"item": "a garden hose"}},
	})
	require.NoError(t, err)
	select {
	case <-h.Done():
	case <-time.After(30 * time.Second):
		require.FailNow(t, "the run never settled")
	}
	require.Equal(t, "succeeded", h.Snapshot().Status)

	out := <-sink.got
	results, ok := out.Payload.([]any)
	require.True(t, ok, "recall emits the result list as the payload")
	require.Len(t, results, 1)
	entry := results[0].(map[string]any)
	assert.Contains(t, entry["text"], "garden hose", "the remembered text was interpolated")
	assert.Equal(t, []any{"delivery"}, entry["tags"],
		"The tags must be a JSON array, which is what a split or a property path reads.")
	created, ok := entry["created_at"].(string)
	require.Truef(t, ok, "The creation time must be JSON text as history stores it, got %T.", entry["created_at"])
	_, err = time.Parse(time.RFC3339Nano, created)
	assert.NoError(t, err, "The creation time must be an RFC 3339 timestamp.")

	snap, _ := memories.S.GetStore(store.ID)
	assert.Equal(t, 1, snap.Facts)
}
