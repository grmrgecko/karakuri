package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// llamaFixture is a chat-completions server that behaves like llama.cpp at a
// fixed window: it counts a request's characters at four per token and
// rejects anything larger with llama.cpp's overflow text. Summary requests
// get a template reply; other requests call a tool until toolRounds calls
// have been served, then answer.
type llamaFixture struct {
	window     int
	toolRounds int
	rejectAll  bool

	mu        sync.Mutex
	served    int
	rejected  int
	largest   int
	summaries []string
}

func (f *llamaFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages []struct {
			Role      string          `json:"role"`
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"messages"`
		Tools json.RawMessage `json:"tools"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	chars := 0
	last := ""
	for _, m := range body.Messages {
		var text string
		_ = json.Unmarshal(m.Content, &text)
		chars += utf8.RuneCountInString(text)
		for _, call := range m.ToolCalls {
			chars += utf8.RuneCountInString(call.Function.Name + call.Function.Arguments)
		}
		if m.Role == "user" {
			last = text
		}
	}
	if len(body.Tools) > 0 {
		chars += len(body.Tools) / 2
	}
	tokens := chars / charsPerToken
	summary := strings.Contains(last, "You MUST summarize the conversation above") || strings.Contains(last, "Update the existing checkpoint")

	f.mu.Lock()
	defer f.mu.Unlock()
	f.largest = max(f.largest, tokens)
	if tokens > f.window || (f.rejectAll && !summary) {
		f.rejected++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = fmt.Fprintf(w, `{"error":{"code":400,"message":"request (%d tokens) exceeds the available context size (%d tokens), try increasing it","type":"exceed_context_size_error"}}`, max(tokens, f.window+1), f.window)
		return
	}
	usage := fmt.Sprintf(`"usage":{"prompt_tokens":%d,"completion_tokens":20}`, tokens)
	switch {
	case summary:
		f.summaries = append(f.summaries, last)
		_, _ = fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":"## Objective\n- Link the office light to the workbench light and lamp.\n\n## Next Move\n1. Patch flow 793672a9."}}],%s}`, usage)
	case f.served < f.toolRounds:
		f.served++
		_, _ = fmt.Fprintf(w, `{"choices":[{"finish_reason":"tool_calls","message":{"content":"","tool_calls":[{"id":"call_%d","function":{"name":"get_ha_states","arguments":"{\"filter\":\"light\"}"}}]}}],%s}`, f.served, usage)
	default:
		_, _ = fmt.Fprintf(w, `{"choices":[{"finish_reason":"stop","message":{"content":"Done."}}],%s}`, usage)
	}
}

// runToolLoop drives requests the way the agent and the editor assistant
// do: send, append the tool round, repeat until an answer.
func runToolLoop(t *testing.T, comp *Compactor, d *Driver, req ChatRequest, resultChars int) ([]Compaction, []Message, error) {
	t.Helper()
	var compactions []Compaction
	for round := 0; round < 30; round++ {
		resp, err := comp.Send(context.Background(), &req, func(r ChatRequest) (*ChatResponse, error) {
			return d.Chat(context.Background(), r)
		}, func(c Compaction) error {
			compactions = append(compactions, c)
			return nil
		})
		if err != nil {
			return compactions, req.Messages, err
		}
		if len(resp.ToolCalls) == 0 {
			return compactions, req.Messages, nil
		}
		req.Messages = append(req.Messages, Message{Role: RoleAssistant, ToolCalls: resp.ToolCalls})
		for _, call := range resp.ToolCalls {
			req.Messages = append(req.Messages, Message{Role: RoleTool, ToolCallID: call.ID, ToolName: call.Name, Text: strings.Repeat("light.office_workbench on; ", resultChars/27)})
		}
	}
	t.Fatal("tool loop never answered")
	return nil, nil, nil
}

// TestCompactorKeepsToolLoopInsideWindow replays the 2026-11-08 editor
// assistant failure: an 18,000-token fixed prompt (72,000 characters) on a
// 32,768-token llama.cpp window, then ten rounds of 7,500-character tool
// results. Each round adds about 1,900 tokens, so the default threshold of
// 24,576 (32,768 minus a 8,192 buffer, a quarter of the window) is crossed
// every few rounds. The turn must finish with no request over the window,
// and the second summary must update the first rather than start over.
func TestCompactorKeepsToolLoopInsideWindow(t *testing.T) {
	fixture := &llamaFixture{window: 32768, toolRounds: 10}
	srv := httptest.NewServer(fixture)
	defer srv.Close()
	d := windowDriver(t, &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}, 32768)

	comp := NewCompactor(d, CompactionSettings{})
	compactions, messages, err := runToolLoop(t, comp, d, ChatRequest{
		Model:    "gemma-4-26B",
		System:   strings.Repeat("Kuri system prompt. ", 3600),
		Messages: []Message{{Role: RoleUser, Text: "When I turn on my office light, also turn on the workbench light and lamp."}},
	}, 7500)
	require.NoError(t, err)

	assert.Zero(t, fixture.rejected, "no request may exceed the window")
	assert.LessOrEqual(t, fixture.largest, 32768)
	require.GreaterOrEqual(t, len(compactions), 2, "ten 1,900-token rounds over a 6,500-token margin need more than one compaction")
	assert.Equal(t, CompactionThreshold, compactions[0].Reason)
	assert.Less(t, compactions[0].AfterTokens, compactions[0].BeforeTokens)
	assert.Positive(t, compactions[0].Usage.InputTokens, "the summary request's tokens are reported")
	require.Len(t, fixture.summaries, len(compactions))
	assert.Contains(t, fixture.summaries[1], "Update the existing checkpoint", "a later compaction updates the previous summary")
	require.NotEmpty(t, messages)
	assert.True(t, strings.HasPrefix(messages[0].Text, checkpointOpen), "history continues from the checkpoint")
	assert.Contains(t, messages[0].Text, "Patch flow 793672a9", "the checkpoint carries the summary")
}

// TestCompactorRecoversFromOverflow pins the reactive path against llama.cpp's
// overflow text when no window is known in advance: compact and resend once,
// learning the 32,768 limit the error names; a provider that rejects the
// resend too fails with its own error after exactly one summary; and with
// compaction off no summary is requested at all.
func TestCompactorRecoversFromOverflow(t *testing.T) {
	// Three earlier exchanges of 48,000 characters each: 36,000 tokens.
	history := []Message{}
	for i := 0; i < 3; i++ {
		history = append(history,
			Message{Role: RoleUser, Text: fmt.Sprintf("Question %d", i)},
			Message{Role: RoleAssistant, Text: strings.Repeat("answer text ", 4000)},
		)
	}
	history = append(history, Message{Role: RoleUser, Text: "And now?"})

	t.Run("one resend after compaction succeeds", func(t *testing.T) {
		fixture := &llamaFixture{window: 32768}
		srv := httptest.NewServer(fixture)
		defer srv.Close()
		d := windowDriver(t, &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}, 0)
		comp := NewCompactor(d, CompactionSettings{})
		compactions, _, err := runToolLoop(t, comp, d, ChatRequest{Model: "m", Messages: append([]Message(nil), history...)}, 0)
		require.NoError(t, err)
		require.Len(t, compactions, 1)
		assert.Equal(t, CompactionOverflow, compactions[0].Reason)
		assert.Equal(t, 1, fixture.rejected, "only the original request is rejected")
		assert.Equal(t, 32768, d.ContextWindow(context.Background(), "m"), "the window named by the error is learned")
	})

	t.Run("a second rejection returns the provider error", func(t *testing.T) {
		fixture := &llamaFixture{window: 32768, rejectAll: true}
		srv := httptest.NewServer(fixture)
		defer srv.Close()
		d := windowDriver(t, &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}, 0)
		comp := NewCompactor(d, CompactionSettings{})
		_, _, err := runToolLoop(t, comp, d, ChatRequest{Model: "m", Messages: append([]Message(nil), history...)}, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exceeds the available context size")
		assert.Len(t, fixture.summaries, 1, "exactly one summary request")
	})

	t.Run("disabled compaction makes no summary request", func(t *testing.T) {
		fixture := &llamaFixture{window: 32768}
		srv := httptest.NewServer(fixture)
		defer srv.Close()
		d := windowDriver(t, &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}, 32768)
		comp := NewCompactor(d, CompactionSettings{Disabled: true})
		_, _, err := runToolLoop(t, comp, d, ChatRequest{Model: "m", Messages: append([]Message(nil), history...)}, 0)
		require.Error(t, err)
		assert.Empty(t, fixture.summaries)
	})
}

// TestCompactionKeepsThinkingBudgetBelowMaxTokens pins Anthropic's manual
// thinking rule: a request whose max_tokens is not above
// thinking.budget_tokens is rejected with a 400. The summary request inherits
// the model's budget, so its max tokens must leave room above it, and an
// unset max tokens must not default below the budget. The window, 200,000
// tokens, is the Claude window; history is 800,000 characters, 200,000 tokens
// at four per token, over the 170,000 threshold (200,000 minus a 30,000 buffer).
func TestCompactionKeepsThinkingBudgetBelowMaxTokens(t *testing.T) {
	history := []Message{}
	for i := 0; i < 4; i++ {
		history = append(history,
			Message{Role: RoleUser, Text: fmt.Sprintf("Question %d", i)},
			Message{Role: RoleAssistant, Text: strings.Repeat("answer text ", 16667)},
		)
	}
	history = append(history, Message{Role: RoleUser, Text: "And now?"})

	for _, tc := range []struct {
		name      string
		maxTokens int
	}{
		{name: "max tokens above the budget", maxTokens: 16000},
		{name: "max tokens unset", maxTokens: 0},
	} {
		var mu sync.Mutex
		var summaries, rejected int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				MaxTokens int `json:"max_tokens"`
				Thinking  struct {
					BudgetTokens int `json:"budget_tokens"`
				} `json:"thinking"`
				Messages json.RawMessage `json:"messages"`
			}
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			mu.Lock()
			defer mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			if body.Thinking.BudgetTokens > 0 && body.MaxTokens <= body.Thinking.BudgetTokens {
				rejected++
				w.WriteHeader(http.StatusBadRequest)
				_, _ = fmt.Fprintf(w, `{"type":"error","error":{"type":"invalid_request_error","message":"max_tokens must be greater than thinking.budget_tokens"}}`)
				return
			}
			text := "Done."
			if strings.Contains(string(body.Messages), "You MUST summarize the conversation above") {
				summaries++
				text = "## Objective\n- Link the office light to the workbench light and lamp.\n\n## Next Move\n1. Patch flow 793672a9."
			}
			_, _ = fmt.Fprintf(w, `{"content":[{"type":"text","text":%q}],"stop_reason":"end_turn","usage":{"input_tokens":10,"output_tokens":5}}`, text)
		}))

		d := windowDriver(t, &anthropicWire{base: srv.URL}, 200000)
		comp := NewCompactor(d, CompactionSettings{})
		req := ChatRequest{Model: "claude-sonnet-4-5", MaxTokens: tc.maxTokens, ReasoningBudget: 10000, Messages: append([]Message(nil), history...)}
		compactions, _, err := runToolLoop(t, comp, d, req, 0)
		srv.Close()
		assert.NoErrorf(t, err, "%s: compaction and the resend must both be accepted", tc.name)
		assert.Lenf(t, compactions, 1, "%s: the oversized history must be compacted once", tc.name)
		assert.Equalf(t, 1, summaries, "%s: the summary request must reach the model", tc.name)
		assert.Zerof(t, rejected, "%s: no request may set max tokens at or below the thinking budget", tc.name)
	}
}
