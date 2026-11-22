package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// captureJSON decodes one request body into a map for request-shape asserts.
func captureJSON(t *testing.T, r *http.Request) map[string]any {
	t.Helper()
	var body map[string]any
	require.NoError(t, json.NewDecoder(r.Body).Decode(&body), "request body must be JSON")
	return body
}

// TestOpenAIWireChatToolRoundTrip pins the chat-completions contract this
// module encodes by hand: tool definitions ride as type "function", an
// assistant turn's calls carry their arguments as a JSON *string*, results go
// back as role "tool" with tool_call_id, and the response's string-encoded
// arguments decode into a map. The fixture response mirrors what
// api.openai.com actually returns for a tool call.
func TestOpenAIWireChatToolRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		assert.Equal(t, "Bearer sk-test", r.Header.Get("Authorization"))
		body := captureJSON(t, r)

		assert.Equal(t, "gpt-test", body["model"])
		assert.EqualValues(t, 512, body["max_completion_tokens"],
			"OpenAI proper takes max_completion_tokens; max_tokens is retired on new models")
		assert.Equal(t, "high", body["reasoning_effort"], "chat-completions uses the documented reasoning_effort field")
		assert.Equal(t, "conversation-1", body["prompt_cache_key"], "OpenAI buckets repeated conversation prefixes by the stable key")
		msgs := body["messages"].([]any)
		require.Len(t, msgs, 4, "system + user + assistant tool call + tool result")
		assert.Equal(t, "system", msgs[0].(map[string]any)["role"])
		assistant := msgs[2].(map[string]any)
		call := assistant["tool_calls"].([]any)[0].(map[string]any)
		assert.Equal(t, "function", call["type"])
		assert.JSONEq(t, `{"city":"Oslo"}`, call["function"].(map[string]any)["arguments"].(string),
			"chat-completions carries call arguments as a JSON string, not an object")
		result := msgs[3].(map[string]any)
		assert.Equal(t, "tool", result["role"])
		assert.Equal(t, "call_1", result["tool_call_id"])
		toolDef := body["tools"].([]any)[0].(map[string]any)
		assert.Equal(t, "function", toolDef["type"])
		assert.Equal(t, "weather", toolDef["function"].(map[string]any)["name"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"finish_reason": "tool_calls", "message": {
				"content": "",
				"tool_calls": [{"id": "call_2", "function": {"name": "weather", "arguments": "{\"city\":\"Bergen\"}"}}]
			}}],
			"usage": {"prompt_tokens": 11, "completion_tokens": 7, "prompt_tokens_details":{"cached_tokens":9}}
		}`))
	}))
	defer srv.Close()

	w := &openaiWire{base: srv.URL + "/v1", key: "sk-test", maxTokensKey: "max_completion_tokens", promptCache: true}
	resp, err := w.Chat(context.Background(), ChatRequest{
		Model:           "gpt-test",
		CacheKey:        "conversation-1",
		System:          "be brief",
		MaxTokens:       512,
		ReasoningEffort: "high",
		Tools:           []ToolDef{{Name: "weather", Description: "current weather", Schema: map[string]any{"type": "object"}}},
		Messages: []Message{
			{Role: RoleUser, Text: "weather in Oslo?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "weather", Args: map[string]any{"city": "Oslo"}}}},
			{Role: RoleTool, ToolCallID: "call_1", ToolName: "weather", Text: "rain"},
		},
	})
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, map[string]any{"city": "Bergen"}, resp.ToolCalls[0].Args,
		"string-encoded arguments must decode into the shared map shape")
	assert.Equal(t, "tool_calls", resp.StopReason)
	assert.Equal(t, Usage{InputTokens: 11, OutputTokens: 7, CachedInputTokens: 9}, resp.Usage)
}

// TestOpenAIWireStreamingContract pins Chat Completions SSE framing, text
// deltas, fragmented tool arguments, terminal usage, and the retained event
// sequence documented by OpenAI.
func TestOpenAIWireStreamingContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := captureJSON(t, r)
		assert.Equal(t, true, body["stream"])
		assert.Equal(t, map[string]any{"include_usage": true}, body["stream_options"])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"choices\":[{\"delta\":{\"content\":\"Checking \"}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_7\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"Oslo\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":9,\"completion_tokens\":4}}\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer srv.Close()

	w := &openaiWire{base: srv.URL, key: "sk-test", maxTokensKey: "max_completion_tokens"}
	var deltas []string
	resp, err := w.ChatStream(context.Background(), ChatRequest{
		Model: "gpt-test", Messages: []Message{{Role: RoleUser, Text: "weather?"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Checking "}, deltas)
	assert.Equal(t, "Checking ", resp.Text)
	assert.Equal(t, Usage{InputTokens: 9, OutputTokens: 4}, resp.Usage)
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, map[string]any{"city": "Oslo"}, resp.ToolCalls[0].Args)
	var events []capturedSSEEvent
	require.NoError(t, json.Unmarshal(resp.ProviderResponse, &events))
	assert.Len(t, events, 5, "the raw provider response must retain every SSE event, including DONE")
}

// TestOpenAIWireStreamSeparatesToolCallsSharingAnIndex pins that streamed tool
// calls are told apart by id as well as by index. Some compatible servers
// send every parallel call whole under index 0 with its own id, which merged
// the calls into one whose name was "weatherclock" and whose arguments were two
// objects back to back, failing the request as invalid arguments. A fragment
// without an id still continues the call its index last named.
func TestOpenAIWireStreamSeparatesToolCallsSharingAnIndex(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		want   []ToolCall
	}{
		{
			name: "whole calls under one index",
			stream: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\\\"Oslo\\\"}\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_b\",\"function\":{\"name\":\"clock\",\"arguments\":\"{\\\"zone\\\":\\\"UTC\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: [DONE]\n\n",
			want: []ToolCall{
				{ID: "call_a", Name: "weather", Args: map[string]any{"city": "Oslo"}},
				{ID: "call_b", Name: "clock", Args: map[string]any{"zone": "UTC"}},
			},
		},
		{
			name: "fragments follow the newest call of their index",
			stream: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"weather\",\"arguments\":\"{}\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_b\",\"function\":{\"name\":\"clock\",\"arguments\":\"{\\\"zone\\\":\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"UTC\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: [DONE]\n\n",
			want: []ToolCall{
				{ID: "call_a", Name: "weather", Args: map[string]any{}},
				{ID: "call_b", Name: "clock", Args: map[string]any{"zone": "UTC"}},
			},
		},
		{
			name: "one call repeating its id on every fragment",
			stream: "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"name\":\"weather\",\"arguments\":\"{\\\"city\\\":\"}}]}}]}\n\n" +
				"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_a\",\"function\":{\"arguments\":\"\\\"Oslo\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
				"data: [DONE]\n\n",
			want: []ToolCall{{ID: "call_a", Name: "weather", Args: map[string]any{"city": "Oslo"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(tt.stream))
			}))
			defer srv.Close()

			w := &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}
			resp, err := w.ChatStream(context.Background(), ChatRequest{
				Model: "local-test", Messages: []Message{{Role: RoleUser, Text: "weather and time?"}},
			}, func(string) error { return nil })
			require.NoError(t, err, "calls that share an index must not be joined into one unparseable call")
			assert.Equal(t, tt.want, resp.ToolCalls, "each id must come back as its own call, in the order the stream opened them")
		})
	}
}

// TestOpenAIWireReadsReasoningExtensions pins the two non-standard reasoning
// fields chat-completions servers return. OpenRouter (captured 2026-11-08,
// deepseek/deepseek-r1-0528 and google/gemini-3-flash-preview) sends
// reasoning plus a reasoning_details array, which can be encrypted-only with
// a string index. DeepSeek, vLLM, and llama.cpp send reasoning_content. The
// details array must replay unchanged on the assistant turn.
func TestOpenAIWireReadsReasoningExtensions(t *testing.T) {
	cases := []struct {
		name        string
		message     string
		wantSummary string
		wantReplay  string
	}{
		{
			name:        "OpenRouter reasoning text",
			message:     `{"content":"391","reasoning":"20 squared minus 9.","reasoning_details":[{"type":"reasoning.text","text":"20 squared minus 9.","format":"unknown","index":0}]}`,
			wantSummary: "20 squared minus 9.",
			wantReplay:  `[{"type":"reasoning.text","text":"20 squared minus 9.","format":"unknown","index":0}]`,
		},
		{
			name:       "OpenRouter Gemini encrypted only",
			message:    `{"content":null,"reasoning":null,"reasoning_details":[{"type":"reasoning.encrypted","data":"AY89a19T","format":"google-gemini-v1","id":"call_1","index":"0"}]}`,
			wantReplay: `[{"type":"reasoning.encrypted","data":"AY89a19T","format":"google-gemini-v1","id":"call_1","index":"0"}]`,
		},
		{
			name:        "reasoning_content servers",
			message:     `{"content":"391","reasoning_content":"20 squared minus 9."}`,
			wantSummary: "20 squared minus 9.",
		},
	}
	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"finish_reason":"stop","message":` + tc.message + `}]}`))
		}))
		w := &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}
		resp, err := w.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: RoleUser, Text: "q"}}})
		srv.Close()
		require.NoError(t, err, tc.name)
		assert.Equal(t, tc.wantSummary, resp.ReasoningSummary, "readable reasoning for %s", tc.name)
		replayed := w.encodeMessages(ChatRequest{Messages: []Message{{Role: RoleAssistant, Text: resp.Text, OpaqueState: resp.OpaqueState}}})
		if tc.wantReplay == "" {
			assert.NotContains(t, replayed[0], "reasoning_details", "%s has no details to replay", tc.name)
			continue
		}
		raw, err := json.Marshal(replayed[0]["reasoning_details"])
		require.NoError(t, err, tc.name)
		assert.JSONEq(t, tc.wantReplay, string(raw), "reasoning_details must replay unchanged for %s", tc.name)
	}
}

// TestOpenAIWireStreamsOpenRouterReasoning pins OpenRouter's streamed
// reasoning as captured 2026-11-08 from anthropic/claude-haiku-4.5 through
// Amazon Bedrock: reasoning text arrives in fragments that share one
// reasoning_details index, and the signature arrives later as its own delta
// for the same entry. The rebuilt entry must hold the whole text and the
// signature, because Anthropic verifies the pair.
func TestOpenAIWireStreamsOpenRouterReasoning(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			": OPENROUTER PROCESSING\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":"","role":"assistant","reasoning":"Call the","reasoning_details":[{"type":"reasoning.text","text":"Call the","format":"anthropic-claude-v1","index":0}]},"finish_reason":null}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":"","role":"assistant","reasoning":" tool.","reasoning_details":[{"type":"reasoning.text","text":" tool.","format":"anthropic-claude-v1","index":0}]},"finish_reason":null}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":"","role":"assistant","reasoning_details":[{"type":"reasoning.text","signature":"Er4DCpwB","format":"anthropic-claude-v1","index":0}]},"finish_reason":null}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":null,"role":"assistant","tool_calls":[{"index":0,"id":"toolu_1","type":"function","function":{"name":"weather","arguments":"{\"city\": \"Oslo\"}"}}]},"finish_reason":null}]}` + "\n\n" +
				`data: {"choices":[{"index":0,"delta":{"content":"","role":"assistant","reasoning":null},"finish_reason":"tool_calls"}]}` + "\n\n" +
				"data: [DONE]\n\n",
		))
	}))
	defer srv.Close()

	w := &openaiWire{base: srv.URL, maxTokensKey: "max_tokens"}
	var deltas []string
	resp, err := w.ChatStream(context.Background(), ChatRequest{Model: "anthropic/claude-haiku-4.5", Messages: []Message{{Role: RoleUser, Text: "weather?"}}},
		func(delta string) error {
			deltas = append(deltas, delta)
			return nil
		})
	require.NoError(t, err)
	assert.Empty(t, deltas, "reasoning fragments are not answer text")
	assert.Equal(t, "Call the tool.", resp.ReasoningSummary)
	require.Len(t, resp.ToolCalls, 1)
	replayed := w.encodeMessages(ChatRequest{Messages: []Message{{Role: RoleAssistant, ToolCalls: resp.ToolCalls, OpaqueState: resp.OpaqueState}}})
	raw, err := json.Marshal(replayed[0]["reasoning_details"])
	require.NoError(t, err)
	assert.JSONEq(t, `[{"type":"reasoning.text","text":"Call the tool.","signature":"Er4DCpwB","format":"anthropic-claude-v1","index":0}]`, string(raw),
		"fragments of one entry must merge into one signed entry")
}

// TestHermesWireReadsBuiltInToolChain pins the Hermes extension to Chat
// Completions: the completion remains OpenAI-compatible, while the returned
// session ID selects the message history containing server-side tool work.
func TestHermesWireReadsBuiltInToolChain(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "Bearer howl-key", r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/chat/completions":
			assert.Empty(t, r.Header.Get("X-Hermes-Session-Key"), "the node has no long-term memory session option")
			assert.Equal(t, "previous-session", r.Header.Get("X-Hermes-Session-Id"), "Hermes resumes the provider-managed session")
			body := captureJSON(t, r)
			assert.Equal(t, "hermes-agent", body["model"])
			messages := body["messages"].([]any)
			require.Len(t, messages, 2, "Hermes receives the system prompt and user turn")
			w.Header().Set("X-Hermes-Session-Id", "continued-session")
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"id":"chatcmpl-hermes","model":"hermes-agent",
				"choices":[{"finish_reason":"stop","message":{"content":"The office light is off."}}],
				"usage":{"prompt_tokens":31,"completion_tokens":8}
			}`))
		case "/api/sessions/continued-session/messages":
			_, _ = w.Write([]byte(`{
				"session_id":"continued-session",
				"data":[
					{"role":"user","content":"turn off the office light"},
					{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"ha_control","arguments":"{\"entity_id\":\"light.office\",\"state\":\"off\"}"}}]},
					{"role":"tool","content":[{"type":"text","text":"done"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}],"tool_call_id":"call_1","tool_name":"ha_control"},
					{"role":"assistant","content":"The office light is off."}
				]
			}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	w := &hermesWire{openaiWire: openaiWire{base: srv.URL + "/v1", key: "howl-key", maxTokensKey: "max_tokens"}}
	resp, err := w.Chat(context.Background(), ChatRequest{
		Model: "hermes-agent", System: "Keep answers short.", ReadTranscript: true, SessionID: "previous-session",
		Messages: []Message{{Role: RoleUser, Text: "turn off the office light"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 2, calls, "a requested transcript adds one message-history request")
	assert.Equal(t, "The office light is off.", resp.Text)
	assert.Equal(t, "continued-session", resp.SessionID, "the next turn must use the session ID Hermes returned")
	assert.JSONEq(t, `{"id":"chatcmpl-hermes","model":"hermes-agent","choices":[{"finish_reason":"stop","message":{"content":"The office light is off."}}],"usage":{"prompt_tokens":31,"completion_tokens":8}}`, string(resp.ProviderResponse))
	require.Len(t, resp.ProviderSupplementalResponses, 1)
	assert.JSONEq(t, `{"session_id":"continued-session","data":[{"role":"user","content":"turn off the office light"},{"role":"assistant","content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"ha_control","arguments":"{\"entity_id\":\"light.office\",\"state\":\"off\"}"}}]},{"role":"tool","content":[{"type":"text","text":"done"},{"type":"image_url","image_url":{"url":"data:image/png;base64,AA=="}}],"tool_call_id":"call_1","tool_name":"ha_control"},{"role":"assistant","content":"The office light is off."}]}`, string(resp.ProviderSupplementalResponses[0]))
	require.Len(t, resp.ProviderTranscript, 4)
	callsOut := resp.ProviderTranscript[1]["tool_calls"].([]map[string]any)
	assert.Equal(t, "ha_control", callsOut[0]["name"])
	assert.Equal(t, map[string]any{"entity_id": "light.office", "state": "off"}, callsOut[0]["args"])
	assert.Equal(t, 1, resp.ProviderTranscript[2]["images"], "image bytes stay out of the transcript")
}

// TestAnthropicWireChatToolRoundTrip pins the messages-API contract: version
// and key headers, the required max_tokens defaulted when unset, tools keyed
// input_schema, tool calls as tool_use content blocks, and — the part that
// bites — consecutive tool results merged into ONE user message of
// tool_result blocks, because the API rejects non-alternating roles.
func TestAnthropicWireChatToolRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		assert.Equal(t, "sk-ant", r.Header.Get("x-api-key"))
		assert.Equal(t, "2023-06-01", r.Header.Get("anthropic-version"))
		body := captureJSON(t, r)

		assert.EqualValues(t, anthropicDefaultMaxTokens, body["max_tokens"],
			"the messages API requires max_tokens, so an unset cap must be filled in")
		assert.Equal(t, "be brief", body["system"], "the system prompt is a top-level field, not a message")
		assert.NotContains(t, body, "temperature", "Anthropic reasoning cannot be combined with temperature")
		assert.Equal(t, map[string]any{"type": "adaptive", "display": "summarized"}, body["thinking"])
		assert.Equal(t, map[string]any{"effort": "high"}, body["output_config"])
		assert.Equal(t, map[string]any{"type": "ephemeral"}, body["cache_control"], "Anthropic automatic caching follows the growing message prefix")
		tool := body["tools"].([]any)[0].(map[string]any)
		assert.Contains(t, tool, "input_schema", "Anthropic spells the schema key input_schema")
		msgs := body["messages"].([]any)
		require.Len(t, msgs, 3, "user + assistant + one merged tool-result user message")
		last := msgs[2].(map[string]any)
		assert.Equal(t, "user", last["role"], "tool results ride user-role turns")
		blocks := last["content"].([]any)
		require.Len(t, blocks, 2, "two consecutive results must merge into one user message")
		assert.Equal(t, "tool_result", blocks[0].(map[string]any)["type"])
		assert.Equal(t, "toolu_1", blocks[0].(map[string]any)["tool_use_id"])

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"content": [
				{"type": "thinking", "thinking": "Checked the forecast.", "signature": "sig-1"},
				{"type": "thinking", "thinking": "Compared both cities.", "signature": "sig-2"},
				{"type": "text", "text": "Rainy."},
				{"type": "tool_use", "id": "toolu_9", "name": "weather", "input": {"city": "Bergen"}}
			],
			"stop_reason": "tool_use",
			"usage": {"input_tokens": 20, "output_tokens": 5, "cache_read_input_tokens":16, "cache_creation_input_tokens":2}
		}`))
	}))
	defer srv.Close()

	w := &anthropicWire{base: srv.URL, key: "sk-ant"}
	resp, err := w.Chat(context.Background(), ChatRequest{
		Model:           "claude-test",
		System:          "be brief",
		Temperature:     func() *float64 { v := 0.2; return &v }(),
		ReasoningEffort: "high",
		Tools:           []ToolDef{{Name: "weather", Description: "current weather", Schema: map[string]any{"type": "object"}}},
		Messages: []Message{
			{Role: RoleUser, Text: "weather in Oslo and Bergen?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{
				{ID: "toolu_1", Name: "weather", Args: map[string]any{"city": "Oslo"}},
				{ID: "toolu_2", Name: "weather", Args: map[string]any{"city": "Bergen"}},
			}},
			{Role: RoleTool, ToolCallID: "toolu_1", ToolName: "weather", Text: "rain"},
			{Role: RoleTool, ToolCallID: "toolu_2", ToolName: "weather", Text: "more rain"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, Usage{InputTokens: 20, OutputTokens: 5, CachedInputTokens: 16, CacheWriteInputTokens: 2}, resp.Usage)
	assert.Equal(t, "Rainy.", resp.Text, "text blocks concatenate into the response text")
	assert.Equal(t, "Checked the forecast.\n\nCompared both cities.", resp.ReasoningSummary,
		"only readable thinking text becomes the normalized summary")
	require.Len(t, resp.ToolCalls, 1)
	assert.Equal(t, "toolu_9", resp.ToolCalls[0].ID)
	assert.Equal(t, map[string]any{"city": "Bergen"}, resp.ToolCalls[0].Args,
		"tool_use input is already an object on this API")
	assert.Equal(t, "tool_use", resp.StopReason)
	replayed := w.encodeMessages([]Message{{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls, OpaqueState: resp.OpaqueState}})
	var blocks []map[string]any
	require.NoError(t, json.Unmarshal(replayed[0]["content"].(json.RawMessage), &blocks))
	assert.Equal(t, "sig-1", blocks[0]["signature"], "thinking blocks must replay byte-for-byte in the assistant turn")
}

// TestAnthropicWireStreamingContract covers the Messages event sequence that
// carries visible text, usage, and the terminal completion marker.
func TestAnthropicWireStreamingContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := captureJSON(t, r)
		assert.Equal(t, true, body["stream"])
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12,\"cache_read_input_tokens\":8}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello \"}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"there\"}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		))
	}))
	defer srv.Close()

	var deltas []string
	resp, err := (&anthropicWire{base: srv.URL}).ChatStream(context.Background(), ChatRequest{
		Model: "claude-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Hello ", "there"}, deltas)
	assert.Equal(t, "Hello there", resp.Text)
	assert.Equal(t, Usage{InputTokens: 12, OutputTokens: 3, CachedInputTokens: 8}, resp.Usage)
	assert.JSONEq(t, `[{"event":"message_start","data":{"type":"message_start","message":{"usage":{"input_tokens":12,"cache_read_input_tokens":8}}}},{"event":"content_block_start","data":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}},{"event":"content_block_delta","data":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}},{"event":"content_block_delta","data":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"there"}}},{"event":"message_delta","data":{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":3}}},{"event":"message_stop","data":{"type":"message_stop"}}]`, string(resp.ProviderResponse))
}

// TestAnthropicWireTemperatureCompatibility pins the direct-Claude family
// boundary. Older Claude models keep configured sampling, newer or unknown
// Claude IDs omit it, and gateway IDs retain it because their capability is
// not inferable from the name.
func TestAnthropicWireTemperatureCompatibility(t *testing.T) {
	cases := []struct {
		name            string
		model           string
		wantTemperature bool
	}{
		{name: "Opus 4.6 dated", model: "claude-opus-4-6-20260315", wantTemperature: true},
		{name: "Sonnet 4.6", model: "claude-sonnet-4-6", wantTemperature: true},
		{name: "Opus 4.7", model: "claude-opus-4-7", wantTemperature: false},
		{name: "Opus 5", model: "claude-opus-5", wantTemperature: false},
		{name: "Sonnet 5", model: "claude-sonnet-5", wantTemperature: false},
		{name: "Fable 5.1", model: "claude-fable-5-1", wantTemperature: false},
		{name: "Mythos preview", model: "claude-mythos-preview", wantTemperature: false},
		{name: "unknown direct Claude", model: "claude-new-family", wantTemperature: false},
		{name: "gateway ID", model: "anthropic/claude-opus-5", wantTemperature: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var captured map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				captured = captureJSON(t, r)
				_, _ = w.Write([]byte(`{"content":[],"stop_reason":"end_turn"}`))
			}))
			defer srv.Close()

			temperature := 0.3
			_, err := (&anthropicWire{base: srv.URL}).Chat(context.Background(), ChatRequest{
				Model: tc.model, Temperature: &temperature,
			})
			require.NoError(t, err)
			if tc.wantTemperature {
				assert.Equal(t, 0.3, captured["temperature"], "temperature presence differs for %s", tc.model)
			} else {
				assert.NotContains(t, captured, "temperature", "temperature presence differs for %s", tc.model)
			}
		})
	}
}

// TestGeminiWireChatToolRoundTrip pins the generateContent contract: the
// model rides in the URL, the key in x-goog-api-key, tools nest under
// functionDeclarations, the assistant role is "model", and a tool result goes
// back as a functionResponse part whose response value must be an object —
// so a bare string is wrapped. Gemini sends no call IDs; the wire synthesizes
// them so the shared vocabulary still correlates calls with results.
func TestGeminiWireChatToolRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1beta/models/gemini-test:generateContent", r.URL.Path)
		assert.Equal(t, "goog-key", r.Header.Get("x-goog-api-key"))
		body := captureJSON(t, r)
		gen := body["generationConfig"].(map[string]any)
		assert.Equal(t, map[string]any{"thinkingBudget": float64(2048)}, gen["thinkingConfig"],
			"Gemini budget takes precedence because thinkingBudget and thinkingLevel target different model generations")

		decls := body["tools"].([]any)[0].(map[string]any)["functionDeclarations"].([]any)
		assert.Equal(t, "weather", decls[0].(map[string]any)["name"])
		contents := body["contents"].([]any)
		require.Len(t, contents, 3, "user + model + functionResponse user turn")
		assert.Equal(t, "model", contents[1].(map[string]any)["role"], "Gemini calls the assistant role model")
		fr := contents[2].(map[string]any)["parts"].([]any)[0].(map[string]any)["functionResponse"].(map[string]any)
		assert.Equal(t, "weather", fr["name"], "results correlate by function name, Gemini has no call IDs")
		assert.Equal(t, map[string]any{"result": "rain"}, fr["response"],
			"a bare result must be wrapped: the API requires an object response")

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"candidates": [{"finishReason": "STOP", "content": {"parts": [
				{"thought": true, "thoughtSignature": "opaque-sig", "text": "Read the forecast."},
				{"thought": true, "thoughtSignature": "opaque-sig-2", "text": "Checked the location."},
				{"text": "It "}, {"text": "rains."},
				{"functionCall": {"name": "weather", "args": {"city": "Bergen"}}}
			]}}],
			"usageMetadata": {"promptTokenCount": 9, "candidatesTokenCount": 4, "cachedContentTokenCount": 7}
		}`))
	}))
	defer srv.Close()

	w := &geminiWire{base: srv.URL, key: "goog-key"}
	resp, err := w.Chat(context.Background(), ChatRequest{
		Model:           "gemini-test",
		ReasoningEffort: "high", ReasoningBudget: 2048,
		Tools: []ToolDef{{Name: "weather", Description: "current weather", Schema: map[string]any{"type": "object"}}},
		Messages: []Message{
			{Role: RoleUser, Text: "weather in Oslo?"},
			{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_0", Name: "weather", Args: map[string]any{"city": "Oslo"}}}},
			{Role: RoleTool, ToolCallID: "call_0", ToolName: "weather", Text: "rain"},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, Usage{InputTokens: 9, OutputTokens: 4, CachedInputTokens: 7}, resp.Usage)
	assert.Equal(t, "It rains.", resp.Text, "multiple text parts concatenate")
	assert.Equal(t, "Read the forecast.\n\nChecked the location.", resp.ReasoningSummary,
		"thought text is display metadata while thought signatures remain opaque")
	require.Len(t, resp.ToolCalls, 1)
	assert.NotEmpty(t, resp.ToolCalls[0].ID, "a synthesized ID keeps the shared vocabulary correlated")
	assert.Equal(t, map[string]any{"city": "Bergen"}, resp.ToolCalls[0].Args)
	replayed := w.encodeMessages([]Message{{Role: RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls, OpaqueState: resp.OpaqueState}})
	var parts []map[string]any
	require.NoError(t, json.Unmarshal(replayed[0]["parts"].(json.RawMessage), &parts))
	assert.Equal(t, "opaque-sig", parts[0]["thoughtSignature"], "Gemini thought parts must replay unchanged")
}

// TestGeminiWireStreamingContract covers Gemini's SSE endpoint and confirms
// that its chunks are both forwarded and retained in the completed response.
func TestGeminiWireStreamingContract(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/v1beta/models/gemini-test:streamGenerateContent", r.URL.Path)
		assert.Equal(t, "sse", r.URL.Query().Get("alt"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Hello \"}]}}]}\n\n" +
				"data: {\"candidates\":[{\"finishReason\":\"STOP\",\"content\":{\"parts\":[{\"text\":\"there\"}]}}],\"usageMetadata\":{\"promptTokenCount\":6,\"candidatesTokenCount\":2,\"cachedContentTokenCount\":4}}\n\n",
		))
	}))
	defer srv.Close()

	var deltas []string
	resp, err := (&geminiWire{base: srv.URL}).ChatStream(context.Background(), ChatRequest{
		Model: "gemini-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
	}, func(delta string) error {
		deltas = append(deltas, delta)
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"Hello ", "there"}, deltas)
	assert.Equal(t, "Hello there", resp.Text)
	assert.Equal(t, Usage{InputTokens: 6, OutputTokens: 2, CachedInputTokens: 4}, resp.Usage)
	var events []capturedSSEEvent
	require.NoError(t, json.Unmarshal(resp.ProviderResponse, &events))
	assert.Len(t, events, 2)
}

// TestAPIErrorMessage pins the error spellings the real endpoints use, so a
// 401 surfaces the provider's own sentence instead of a bare status line:
// OpenAI/Gemini/Anthropic nest {"error":{"message":...}}, and the compatible
// local servers flatten it.
// TestToolResultImagePlacement pins where each provider's wire puts an image a
// tool returned — the three APIs disagree, and getting it wrong means a
// screenshot the model never sees. The bytes "PNG!" stand in for image data;
// Go base64 of that is "UE5HIQ==", which each encoding must carry.
//
//   - chat-completions has no image slot on a tool message, so the image
//     follows as its own user turn of image_url parts (a data: URL).
//   - Anthropic's tool_result block takes a content array, so the image stays
//     inside the result it answers, as a base64 image source.
//   - Gemini's functionResponse holds JSON only, so the image rides beside it
//     as an inlineData part in the same user turn.
func TestToolResultImagePlacement(t *testing.T) {
	const wantB64 = "UE5HIQ==" // base64("PNG!")
	msgs := []Message{
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "screenshot"}}},
		{Role: RoleTool, ToolCallID: "call_1", ToolName: "screenshot", Text: "1720x1304 png",
			Images: []Image{{MIME: "image/png", Data: []byte("PNG!")}}},
	}

	t.Run("chat-completions puts the image in a following user turn", func(t *testing.T) {
		w := &openaiWire{}
		out := w.encodeMessages(ChatRequest{Messages: msgs})
		require.Len(t, out, 3, "assistant call, tool result, then a user turn for the image")
		tool := out[1]
		assert.Equal(t, "tool", tool["role"])
		user := out[2]
		assert.Equal(t, "user", user["role"], "the image cannot ride the tool message")
		parts := user["content"].([]map[string]any)
		img := parts[len(parts)-1]
		assert.Equal(t, "image_url", img["type"])
		assert.Equal(t, "data:image/png;base64,"+wantB64, img["image_url"].(map[string]any)["url"])
	})

	t.Run("anthropic keeps the image inside the tool_result", func(t *testing.T) {
		w := &anthropicWire{}
		out := w.encodeMessages(msgs)
		// assistant turn, then one user turn holding the tool_result.
		result := out[1]["content"].([]map[string]any)[0]
		assert.Equal(t, "tool_result", result["type"])
		blocks := result["content"].([]map[string]any)
		img := blocks[len(blocks)-1]
		assert.Equal(t, "image", img["type"])
		src := img["source"].(map[string]any)
		assert.Equal(t, "base64", src["type"])
		assert.Equal(t, "image/png", src["media_type"])
		assert.Equal(t, wantB64, src["data"])
	})

	t.Run("gemini rides the image beside the functionResponse", func(t *testing.T) {
		w := &geminiWire{}
		out := w.encodeMessages(msgs)
		parts := out[1]["parts"].([]map[string]any)
		require.Len(t, parts, 2, "functionResponse then inlineData in one user turn")
		assert.Contains(t, parts[0], "functionResponse")
		inline := parts[1]["inlineData"].(map[string]any)
		assert.Equal(t, "image/png", inline["mimeType"])
		assert.Equal(t, wantB64, inline["data"])
	})
}

func TestAPIErrorMessage(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"openai nested", `{"error": {"message": "Incorrect API key provided"}}`, "Incorrect API key provided"},
		{"flat error string", `{"error": "model not found"}`, "model not found"},
		{"flat message", `{"message": "unauthorized"}`, "unauthorized"},
		{"not json", `gateway timeout`, ""},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, apiErrorMessage([]byte(tc.body)), "wrong extraction for %s", tc.name)
	}
}

// TestStreamReportsMidStreamProviderError pins that an error object sent
// inside a 200 event stream fails the call. OpenAI, OpenRouter, and llama.cpp
// send {"error": {...}} as a data event once the status line is gone, and
// Gemini does the same on streamGenerateContent; reading past it returned the
// text cut off before the failure as a finished answer.
func TestStreamReportsMidStreamProviderError(t *testing.T) {
	tests := []struct {
		name   string
		stream string
		chat   func(base string, emit func(string) error) (*ChatResponse, error)
	}{
		{
			name: "chat completions",
			stream: "data: {\"choices\":[{\"delta\":{\"content\":\"The answer is \"}}]}\n\n" +
				"data: {\"error\":{\"message\":\"upstream model crashed\",\"code\":502}}\n\n",
			chat: func(base string, emit func(string) error) (*ChatResponse, error) {
				w := &openaiWire{base: base, maxTokensKey: "max_tokens"}
				return w.ChatStream(context.Background(), ChatRequest{
					Model: "gpt-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
				}, emit)
			},
		},
		{
			name: "gemini",
			stream: "data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"The answer is \"}]}}]}\n\n" +
				"data: {\"error\":{\"code\":503,\"message\":\"upstream model crashed\",\"status\":\"UNAVAILABLE\"}}\n\n",
			chat: func(base string, emit func(string) error) (*ChatResponse, error) {
				return (&geminiWire{base: base}).ChatStream(context.Background(), ChatRequest{
					Model: "gemini-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
				}, emit)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte(tt.stream))
			}))
			defer srv.Close()

			resp, err := tt.chat(srv.URL, func(string) error { return nil })
			require.Error(t, err, "a provider error inside the stream must fail the call, not end it as a short answer")
			assert.Nil(t, resp, "a failed stream must not hand back the partial text as a response")
			assert.Contains(t, err.Error(), "upstream model crashed", "the error must carry the provider's own message")
		})
	}
}

// TestAnthropicWireStreamReplaysRedactedThinking pins that a streamed
// redacted_thinking block is kept in the replay state. The Messages API
// rejects a tool round whose assistant turn lost a thinking block, and the
// stream assembler kept only text, thinking, and tool_use blocks, so the
// encrypted block was dropped and the next request failed.
func TestAnthropicWireStreamReplaysRedactedThinking(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(
			"event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12}}}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"EmwKAhgBEgy3va3pzix\"}}\n\n" +
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
				"event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"weather\",\"input\":{}}}\n\n" +
				"event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"city\\\":\\\"Oslo\\\"}\"}}\n\n" +
				"event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":1}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":9}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
		))
	}))
	defer srv.Close()

	resp, err := (&anthropicWire{base: srv.URL}).ChatStream(context.Background(), ChatRequest{
		Model: "claude-test", Messages: []Message{{Role: RoleUser, Text: "weather?"}}, ReasoningBudget: 2048,
	}, func(string) error { return nil })
	require.NoError(t, err)
	require.Len(t, resp.ToolCalls, 1, "the tool call after the redacted block must still be read")
	assert.JSONEq(t,
		`[{"type":"redacted_thinking","data":"EmwKAhgBEgy3va3pzix"},{"type":"tool_use","id":"toolu_1","name":"weather","input":{"city":"Oslo"}}]`,
		string(resp.OpaqueState["anthropic"]),
		"the redacted thinking block must replay unchanged ahead of the tool call")
}

// TestWiresOmitEmptyAssistantTurns pins that an assistant turn with nothing to
// say is left out of an Anthropic or Gemini request. A model that answers with
// no content still becomes a turn in the transcript a chat replays, and both
// APIs refuse a turn with no content, an empty content list, or a blank text
// block, so one empty reply failed every later request of that conversation
// with a 400. Turns that carry text or a tool call are still sent.
func TestWiresOmitEmptyAssistantTurns(t *testing.T) {
	tests := []struct {
		name      string
		assistant Message
		// wantAnthropic and wantGemini are the assistant turn each wire sends,
		// as JSON, or empty when the turn must be left out.
		wantAnthropic string
		wantGemini    string
	}{
		{
			name:      "no text and no tool calls",
			assistant: Message{Role: RoleAssistant},
		},
		{
			name: "replay state recorded from an empty reply",
			assistant: Message{Role: RoleAssistant, OpaqueState: map[string]json.RawMessage{
				"anthropic": json.RawMessage(`[]`), "gemini": json.RawMessage(`null`),
			}},
		},
		{
			name: "blank text block beside a tool call",
			assistant: Message{
				Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "toolu_1", Name: "weather", Args: map[string]any{}}},
				OpaqueState: map[string]json.RawMessage{
					"anthropic": json.RawMessage(`[{"type":"text","text":"\n\n"},{"type":"tool_use","id":"toolu_1","name":"weather","input":{}}]`),
				},
			},
			wantAnthropic: `{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"weather","input":{}}]}`,
			wantGemini:    `{"role":"model","parts":[{"functionCall":{"name":"weather","args":{}}}]}`,
		},
		{
			name:          "text",
			assistant:     Message{Role: RoleAssistant, Text: "It rains."},
			wantAnthropic: `{"role":"assistant","content":[{"type":"text","text":"It rains."}]}`,
			wantGemini:    `{"role":"model","parts":[{"text":"It rains."}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			messages := []Message{{Role: RoleUser, Text: "weather?"}, tt.assistant, {Role: RoleUser, Text: "and tomorrow?"}}

			var sent map[string]any
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				sent = captureJSON(t, r)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"candidates":[{"content":{"parts":[{"text":"ok"}]}}]}`))
			}))
			defer srv.Close()

			_, err := (&anthropicWire{base: srv.URL}).Chat(context.Background(), ChatRequest{Model: "claude-test", Messages: messages})
			require.NoError(t, err)
			assertAssistantTurn(t, sent["messages"], "assistant", tt.wantAnthropic)

			_, err = (&geminiWire{base: srv.URL}).Chat(context.Background(), ChatRequest{Model: "gemini-test", Messages: messages})
			require.NoError(t, err)
			assertAssistantTurn(t, sent["contents"], "model", tt.wantGemini)
		})
	}
}

// assertAssistantTurn checks the one assistant turn of a three-turn request:
// equal to want, or absent when want is empty.
func assertAssistantTurn(t *testing.T, turns any, role, want string) {
	t.Helper()
	list, ok := turns.([]any)
	require.True(t, ok, "the request must carry its turns as a list")
	if want == "" {
		require.Len(t, list, 2, "the empty assistant turn must be left out, keeping both user turns")
		for _, turn := range list {
			assert.Equal(t, "user", turn.(map[string]any)["role"], "only the user turns may remain")
		}
		return
	}
	require.Len(t, list, 3, "a turn with content must still be sent")
	raw, err := json.Marshal(list[1])
	require.NoError(t, err)
	assert.Equal(t, role, list[1].(map[string]any)["role"])
	assert.JSONEq(t, want, string(raw), "the assistant turn must carry only content the API accepts")
}

// TestWiresRecordNoReplayStateForEmptyReply pins that a reply with no content
// leaves the replay state unset. It was stored as an empty list or null, which
// the next request then sent as the turn's whole content.
func TestWiresRecordNoReplayStateForEmptyReply(t *testing.T) {
	tests := []struct {
		name string
		body string
		chat func(base string) (*ChatResponse, error)
	}{
		{
			name: "anthropic",
			body: `{"content":[],"stop_reason":"end_turn","usage":{"input_tokens":3}}`,
			chat: func(base string) (*ChatResponse, error) {
				return (&anthropicWire{base: base}).Chat(context.Background(), ChatRequest{
					Model: "claude-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
				})
			},
		},
		{
			name: "anthropic stream",
			body: "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3}}}\n\n" +
				"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":0}}\n\n" +
				"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n",
			chat: func(base string) (*ChatResponse, error) {
				return (&anthropicWire{base: base}).ChatStream(context.Background(), ChatRequest{
					Model: "claude-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
				}, func(string) error { return nil })
			},
		},
		{
			name: "gemini",
			body: `{"candidates":[{"finishReason":"MAX_TOKENS","content":{"role":"model"}}],"usageMetadata":{"promptTokenCount":3}}`,
			chat: func(base string) (*ChatResponse, error) {
				return (&geminiWire{base: base}).Chat(context.Background(), ChatRequest{
					Model: "gemini-test", Messages: []Message{{Role: RoleUser, Text: "hello"}},
				})
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			resp, err := tt.chat(srv.URL)
			require.NoError(t, err, "an empty reply is still a reply")
			assert.Empty(t, resp.Text)
			assert.Empty(t, resp.OpaqueState, "a reply with no content has nothing to replay")
		})
	}
}
