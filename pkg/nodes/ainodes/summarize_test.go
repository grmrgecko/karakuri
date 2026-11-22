package ainodes_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSummarizeMapReduce runs two source chunks through sequential map calls
// and one reduce call, pinning the graph output and aggregate accounting.
func TestSummarizeMapReduce(t *testing.T) {
	var calls atomic.Int32
	llm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			w.Write([]byte(`{"data": []}`))
			return
		}
		var body struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
			ReasoningEffort string `json:"reasoning_effort"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		assert.Equal(t, "high", body.ReasoningEffort, "model settings must reach map and reduce calls")
		call := calls.Add(1)
		content := "first summary"
		if call == 2 {
			content = "second summary"
		} else if call == 3 {
			assert.Contains(t, body.Messages[len(body.Messages)-1].Content, "first summary")
			assert.Contains(t, body.Messages[len(body.Messages)-1].Content, "second summary")
			content = "combined summary"
		}
		w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":{"content":"` + content + `"}}],"usage":{"prompt_tokens":10,"completion_tokens":2}}`))
	}))
	defer llm.Close()

	e, sink, llmID := newAIEngine(t, llm.URL)
	modelCfg, _ := json.Marshal(map[string]any{"connection": llmID.String(), "model": "test-model", "reasoning_effort": "high"})
	summaryCfg, _ := json.Marshal(map[string]any{"chunk_size": 40})
	g := &flow.Graph{
		Nodes: []flow.Node{{ID: "t", Type: "trigger"}, {ID: "m", Type: "ai-model", Config: modelCfg}, {ID: "sum", Type: "ai-summarize", Config: summaryCfg}, {ID: "s", Type: "sink"}},
		Edges: []flow.Edge{
			{ID: "e1", Source: "t", SourceHandle: "out", Target: "sum", TargetHandle: "in"},
			{ID: "e2", Source: "m", SourceHandle: "out", Target: "sum", TargetHandle: flow.ParamPortID("model"), Kind: flow.EdgeValue},
			{ID: "e3", Source: "sum", SourceHandle: "out", Target: "s", TargetHandle: "in"},
		},
	}

	out := runGuard(t, e, g, sink, "Alpha paragraph has source facts.\n\nBeta paragraph has more facts.")
	assert.Equal(t, "combined summary", out.Payload)
	assert.Equal(t, map[string]any{"input_tokens": float64(30), "output_tokens": float64(6)}, out.Meta["usage"],
		"usage is a JSON object so meta.usage.input_tokens resolves as a property path")
	assert.Equal(t, map[string]any{"chunks": 2, "calls": 3, "rounds": 2}, out.Meta["summarize"])
}
