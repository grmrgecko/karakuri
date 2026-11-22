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
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// newAIEngine wires an engine with the AI node set, a trigger, a sink, and an
// OpenAI-compatible driver pointed at the fixture server.
func newAIEngine(t *testing.T, llmURL string) (*engine.Engine, *sinkNode, uuid.UUID) {
	t.Helper()
	llmID := uuid.New()
	var resolver engine.ConnResolver
	if llmURL != "" {
		drv, err := ai.New(integrations.ConnModel{
			ID: llmID, Kind: ai.KindAI,
			Config: []byte(`{"provider":"openai-compatible","base_url":"` + llmURL + `/v1"}`),
		}, integrations.Deps{Logger: zap.NewNop()})
		require.NoError(t, err)
		resolver = func(id string) (any, error) {
			return map[string]any{llmID.String(): drv}[id], nil
		}
	}

	reg := engine.NewRegistry()
	ainodes.RegisterAll(reg)
	sink := &sinkNode{got: make(chan *engine.Msg, 1)}
	reg.Register(stubTrigger{})
	reg.Register(sink)
	e := engine.New(engine.Options{
		Registry: reg, Clock: engine.NewFakeClock(time.Now()), ConnResolver: resolver,
	})
	e.Start()
	t.Cleanup(e.Stop)
	return e, sink, llmID
}

// TestIngestReplacesSource is the RAG write path end to end: a document is
// chunked into a store with its source stamped, and re-ingesting the same
// source replaces the chunks instead of piling new ones beside stale ones —
// the property that lets a file watcher re-feed a changed file forever.
func TestIngestReplacesSource(t *testing.T) {
	setupMemories(t)
	store, err := memories.S.CreateStore(memories.StoreParams{Name: "Docs"})
	require.NoError(t, err)

	e, sink, _ := newAIEngine(t, "")
	cfg, _ := json.Marshal(map[string]any{"store": "Docs", "source": "manual.txt"})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "ing", Type: "memory-ingest", Config: cfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "ing", TargetHandle: "in"},
			{ID: "e2", Source: "ing", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	out := runGuard(t, e, g, sink, "The boiler is serviced every March.\n\nThe fuse box is in the garage.")
	ingest := out.Meta["ingest"].(map[string]any)
	assert.Equal(t, 1, ingest["chunks"], "a short document packs into one chunk")
	assert.Equal(t, 0, ingest["replaced"], "the first ingest replaces nothing")

	out = runGuard(t, e, g, sink, "The boiler was replaced with a heat pump.")
	ingest = out.Meta["ingest"].(map[string]any)
	assert.Equal(t, 1, ingest["replaced"], "re-ingesting the source removes its previous chunks")

	snap, _ := memories.S.GetStore(store.ID)
	assert.Equal(t, 1, snap.Facts, "the store holds only the latest ingest")
	// The fixture store has no embedding connection, so recall is keyword
	// scoring: the query must share tokens with the chunk.
	results := memories.S.Recall(context.Background(), store.ID, "what replaced the boiler?", memories.RecallOpts{})
	require.NotEmpty(t, results, "the fresh chunk is recallable")
	assert.Contains(t, results[0].Text, "heat pump")
	assert.Equal(t, "manual.txt", results[0].Source, "recall carries the source, so an answer can cite it")
}

// TestExtractStructuredOutput pins the extractor's wire contract against an
// OpenAI-compatible server: the schema built from the declared attributes
// rides the request as a json_schema response format, and the reply lands
// decoded as the payload.
func TestExtractStructuredOutput(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		calls.Add(1)
		var body struct {
			ResponseFormat struct {
				Type       string `json:"type"`
				JSONSchema struct {
					Schema map[string]any `json:"schema"`
				} `json:"json_schema"`
			} `json:"response_format"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "json_schema", body.ResponseFormat.Type,
			"the attribute schema must ride the request natively")
		props := body.ResponseFormat.JSONSchema.Schema["properties"].(map[string]any)
		assert.Contains(t, props, "city", "each declared attribute becomes a schema property")
		assert.Equal(t, "integer", props["guests"].(map[string]any)["type"],
			"the attribute's declared type maps onto its JSON Schema type")
		w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
			"content": "{\"city\": \"Oslo\", \"guests\": 4}"
		}}], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}`))
	}))
	defer llm.Close()

	e, sink, llmID := newAIEngine(t, llm.URL)
	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model"})
	extractCfg, _ := json.Marshal(map[string]any{
		"attributes": []map[string]any{
			{"id": "city", "required": true},
			{"id": "guests", "type": "integer"},
		},
	})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "m", Type: "ai-model", Config: modelCfg},
			{ID: "ex", Type: "ai-extract", Config: extractCfg},
			{ID: "s", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "ex", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "ex",
				TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "ex", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	out := runGuard(t, e, g, sink, "Book a table in Oslo for four people.")
	payload, ok := out.Payload.(map[string]any)
	require.Truef(t, ok, "the extraction is the payload, got %T", out.Payload)
	assert.Equal(t, "Oslo", payload["city"])
	assert.EqualValues(t, 4, payload["guests"])
	assert.EqualValues(t, 1, calls.Load(), "extraction is one model call")
}

// TestClassifyRoutesByCategory covers the classifier's routing contract: the
// verdict names a category and the message leaves on that category's derived
// port, while a verdict of "other" takes the fallback — never a silent drop.
func TestClassifyRoutesByCategory(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		switch calls.Add(1) {
		case 1:
			w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
				"content": "{\"category\": \"Urgent\", \"confidence\": 0.9, \"reason\": \"leak reported\"}"
			}}], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}`))
		default:
			w.Write([]byte(`{"choices": [{"finish_reason": "stop", "message": {
				"content": "{\"category\": \"other\", \"confidence\": 0.4}"
			}}], "usage": {"prompt_tokens": 10, "completion_tokens": 5}}`))
		}
	}))
	defer llm.Close()

	e, sink, llmID := newAIEngine(t, llm.URL)
	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model"})
	classifyCfg, _ := json.Marshal(map[string]any{"categories": "Urgent, Newsletter"})
	g := &flow.Graph{
		Nodes: []flow.Node{
			{ID: "t", Type: "trigger"},
			{ID: "m", Type: "ai-model", Config: modelCfg},
			{ID: "cl", Type: flow.NodeTypeAIClassify, Config: classifyCfg},
			{ID: "su", Type: "sink"},
			{ID: "so", Type: "sink"},
		},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "cl", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "cl",
				TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "cl", SourceHandle: flow.ClassifyPortID("Urgent"), Target: "su", TargetHandle: "in"},
			{ID: "e4", Source: "cl", SourceHandle: "other", Target: "so", TargetHandle: "in"},
		},
	}

	out := runGuard(t, e, g, sink, "Water is pooling under the dishwasher!")
	classification := out.Meta["classification"].(map[string]any)
	assert.Equal(t, "Urgent", classification["category"], "the verdict rides meta.classification")
	assert.Equal(t, 0.9, classification["confidence"])
	assert.Equal(t, "Water is pooling under the dishwasher!", out.Payload,
		"classification routes without changing the payload")

	out = runGuard(t, e, g, sink, "Weekly deals on garden hoses")
	classification = out.Meta["classification"].(map[string]any)
	assert.Equal(t, "other", classification["category"], "text no category fits leaves on Other")
	assert.EqualValues(t, 2, calls.Load(), "each classification is one model call")
}
