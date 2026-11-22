package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// openaiWire speaks the OpenAI chat-completions API, which is also the lingua
// franca of the local servers: LM Studio, llama-swap, vLLM, LocalAI, and
// Ollama's /v1 endpoints all implement it. The base URL includes the version
// prefix ("https://api.openai.com/v1", "http://localhost:1234/v1") so one
// path join serves them all.
type openaiWire struct {
	base string
	key  string
	// promptCache enables OpenAI-only request fields. Compatible servers may
	// reject fields outside the older Chat Completions contract.
	promptCache bool
	// maxTokensKey names the response-cap parameter: max_completion_tokens on
	// OpenAI proper, max_tokens on the compatible servers that predate the
	// rename and reject it.
	maxTokensKey string
	// probeProps asks llama.cpp and llama-swap for the runtime window when
	// the model listing names none. Only generic compatible servers run them.
	probeProps bool
}

func (w *openaiWire) headers() map[string]string {
	h := map[string]string{}
	if w.key != "" {
		h["Authorization"] = "Bearer " + w.key
	}
	return h
}

// Chat implements wire over POST /chat/completions.
func (w *openaiWire) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	resp, _, err := w.chat(ctx, req)
	return resp, err
}

func (w *openaiWire) chat(ctx context.Context, req ChatRequest) (*ChatResponse, http.Header, error) {
	return w.chatWithHeaders(ctx, req, w.headers())
}

func (w *openaiWire) chatWithHeaders(ctx context.Context, req ChatRequest, headers map[string]string) (*ChatResponse, http.Header, error) {
	body := w.chatBody(req)

	var resp struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content          string          `json:"content"`
				Reasoning        string          `json:"reasoning"`
				ReasoningContent string          `json:"reasoning_content"`
				ReasoningDetails json.RawMessage `json:"reasoning_details"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			PromptDetails    struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}
	rawResponse, responseHeaders, err := doJSONRawHeaders(ctx, "POST", w.base+"/chat/completions", headers, body, &resp)
	if err != nil {
		return nil, nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, nil, fmt.Errorf("response has no choices")
	}
	choice := resp.Choices[0]

	out := &ChatResponse{
		Text:       choice.Message.Content,
		StopReason: choice.FinishReason,
		Usage: Usage{
			InputTokens: resp.Usage.PromptTokens, OutputTokens: resp.Usage.CompletionTokens,
			CachedInputTokens: resp.Usage.PromptDetails.CachedTokens,
		},
		ProviderResponse: rawResponse,
	}
	setChatReasoning(out, choice.Message.Reasoning, choice.Message.ReasoningContent, choice.Message.ReasoningDetails)
	for i, tc := range choice.Message.ToolCalls {
		args, err := decodeArgs(tc.Function.Arguments)
		if err != nil {
			return nil, nil, fmt.Errorf("tool call %q has invalid arguments: %w", tc.Function.Name, err)
		}
		id := tc.ID
		if id == "" {
			id = "call_" + strconv.Itoa(i)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: tc.Function.Name, Args: args})
	}
	return out, responseHeaders, nil
}

func (w *openaiWire) chatBody(req ChatRequest) map[string]any {
	body := map[string]any{
		"model":    req.Model,
		"messages": w.encodeMessages(req),
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Schema,
				},
			})
		}
		body["tools"] = tools
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		body[w.maxTokensKey] = req.MaxTokens
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = req.ReasoningEffort
	}
	if w.promptCache && req.CacheKey != "" {
		body["prompt_cache_key"] = req.CacheKey
	}
	if req.JSONOutput {
		if len(req.JSONSchema) > 0 {
			body["response_format"] = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   "response",
					"schema": req.JSONSchema,
				},
			}
		} else {
			body["response_format"] = map[string]any{"type": "json_object"}
		}
	}

	return body
}

// ChatStream implements the Chat Completions SSE contract used by OpenAI,
// compatible servers, Ollama, and Hermes.
func (w *openaiWire) ChatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	resp, _, err := w.chatStream(ctx, req, emit)
	return resp, err
}

func (w *openaiWire) chatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, http.Header, error) {
	return w.chatStreamWithHeaders(ctx, req, w.headers(), emit)
}

func (w *openaiWire) chatStreamWithHeaders(ctx context.Context, req ChatRequest, headers map[string]string, emit func(string) error) (*ChatResponse, http.Header, error) {
	return w.chatStreamWithHeadersAndEvents(ctx, req, headers, emit, nil)
}

func (w *openaiWire) chatStreamWithHeadersAndEvents(ctx context.Context, req ChatRequest, headers map[string]string, emit func(string) error, handleEvent func(string, json.RawMessage) error) (*ChatResponse, http.Header, error) {
	body := w.chatBody(req)
	body["stream"] = true
	body["stream_options"] = map[string]any{"include_usage": true}
	type pendingCall struct {
		id, name string
		args     strings.Builder
	}
	var text, reasoning, reasoningContent strings.Builder
	var calls []pendingCall
	// moved maps a stream index to the slot holding its newest call, for
	// servers that reuse one index for several calls.
	moved := map[int]int{}
	var details reasoningDetailsAccumulator
	out := &ChatResponse{}
	seen := false
	responseHeaders, raw, err := doSSE(ctx, w.base+"/chat/completions", headers, body, func(event string, data json.RawMessage) error {
		if handleEvent != nil {
			if err := handleEvent(event, data); err != nil {
				return err
			}
		}
		// Provider-specific named events carry their own payloads; only the
		// completion chunks and a named error event report a stream failure.
		if event == "message" || event == "error" {
			if err := streamError(data); err != nil {
				return err
			}
		}
		var chunk struct {
			Choices []struct {
				FinishReason string `json:"finish_reason"`
				Delta        struct {
					Content          string            `json:"content"`
					Reasoning        string            `json:"reasoning"`
					ReasoningContent string            `json:"reasoning_content"`
					ReasoningDetails []json.RawMessage `json:"reasoning_details"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
			Usage struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				PromptDetails    struct {
					CachedTokens int `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(data, &chunk); err != nil {
			return nil
		}
		if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
			out.Usage = Usage{
				InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens,
				CachedInputTokens: chunk.Usage.PromptDetails.CachedTokens,
			}
		}
		for _, choice := range chunk.Choices {
			seen = true
			reasoning.WriteString(choice.Delta.Reasoning)
			reasoningContent.WriteString(choice.Delta.ReasoningContent)
			for _, detail := range choice.Delta.ReasoningDetails {
				details.add(detail)
			}
			if choice.Delta.Content != "" {
				text.WriteString(choice.Delta.Content)
				if err := emit(choice.Delta.Content); err != nil {
					return err
				}
			}
			if choice.FinishReason != "" {
				out.StopReason = choice.FinishReason
			}
			for _, delta := range choice.Delta.ToolCalls {
				if delta.Index < 0 || delta.Index > maxStreamIndex {
					return fmt.Errorf("tool call index %d is out of range", delta.Index)
				}
				slot := delta.Index
				if newer, ok := moved[delta.Index]; ok {
					slot = newer
				}
				for len(calls) <= slot {
					calls = append(calls, pendingCall{})
				}
				// The index identifies a call on OpenAI, but some compatible
				// servers send every parallel call under index 0 with its own
				// id. A different id on a slot that already has one starts
				// another call, and later fragments of that index follow it.
				if delta.ID != "" && calls[slot].id != "" && calls[slot].id != delta.ID {
					calls = append(calls, pendingCall{})
					slot = len(calls) - 1
					moved[delta.Index] = slot
				}
				call := &calls[slot]
				if delta.ID != "" {
					call.id = delta.ID
				}
				call.name += delta.Function.Name
				call.args.WriteString(delta.Function.Arguments)
			}
		}
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if !seen {
		return nil, nil, fmt.Errorf("stream ended without a response")
	}
	out.Text = text.String()
	out.ProviderResponse = raw
	setChatReasoning(out, reasoning.String(), reasoningContent.String(), details.encode())
	for i, pending := range calls {
		args, err := decodeArgs(json.RawMessage(pending.args.String()))
		if err != nil {
			return nil, nil, fmt.Errorf("tool call %q has invalid arguments: %w", pending.name, err)
		}
		id := pending.id
		if id == "" {
			id = "call_" + strconv.Itoa(i)
		}
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: id, Name: pending.name, Args: args})
	}
	return out, responseHeaders, nil
}

// encodeMessages renders the conversation in the chat-completions shape:
// system first, tool calls on assistant turns, tool results as role "tool".
// A tool result's images cannot ride the tool message — the API has no image
// slot there — so they follow as a user turn of image_url parts, which is how
// chat-completions vision input is delivered.
func (w *openaiWire) encodeMessages(req ChatRequest) []map[string]any {
	msgs := make([]map[string]any, 0, len(req.Messages)+1)
	if req.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": req.System})
	}
	for _, m := range req.Messages {
		switch m.Role {
		case RoleAssistant:
			enc := map[string]any{"role": "assistant", "content": m.Text}
			if raw := m.OpaqueState[opaqueChatReasoningDetails]; len(raw) > 0 && json.Valid(raw) {
				enc["reasoning_details"] = json.RawMessage(raw)
			}
			if len(m.ToolCalls) > 0 {
				calls := make([]map[string]any, 0, len(m.ToolCalls))
				for _, tc := range m.ToolCalls {
					args, _ := json.Marshal(tc.Args)
					calls = append(calls, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(args),
						},
					})
				}
				enc["tool_calls"] = calls
			}
			msgs = append(msgs, enc)
		case RoleTool:
			msgs = append(msgs, map[string]any{
				"role":         "tool",
				"tool_call_id": m.ToolCallID,
				"content":      m.Text,
			})
			if len(m.Images) > 0 {
				parts := []map[string]any{{
					"type": "text",
					"text": fmt.Sprintf("Image output from the %q tool call above:", m.ToolName),
				}}
				for _, img := range m.Images {
					parts = append(parts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": img.dataURL()},
					})
				}
				msgs = append(msgs, map[string]any{"role": "user", "content": parts})
			}
		default:
			if len(m.Images) > 0 {
				parts := []map[string]any{}
				if m.Text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": m.Text})
				}
				for _, img := range m.Images {
					parts = append(parts, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": img.dataURL()},
					})
				}
				msgs = append(msgs, map[string]any{"role": "user", "content": parts})
				continue
			}
			msgs = append(msgs, map[string]any{"role": "user", "content": m.Text})
		}
	}
	return msgs
}

// Models implements wire over GET /models.
func (w *openaiWire) Models(ctx context.Context) ([]string, error) {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := doJSON(ctx, "GET", w.base+"/models", w.headers(), nil, &resp); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(resp.Data))
	for _, m := range resp.Data {
		ids = append(ids, m.ID)
	}
	return ids, nil
}

// decodeArgs parses a tool call's arguments into a map. Providers send an
// object; an empty or absent body means a no-argument call, and anything else
// is the model misbehaving, reported rather than passed along.
func decodeArgs(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 {
		return map[string]any{}, nil
	}
	// Chat-completions sends arguments as a JSON string holding JSON.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		if s == "" {
			return map[string]any{}, nil
		}
		raw = json.RawMessage(s)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, err
	}
	if args == nil {
		args = map[string]any{}
	}
	return args, nil
}

// maxStreamIndex bounds the slot a streamed tool call or content block may
// name. The index sizes a slice, so an endpoint the operator pointed at over
// plain HTTP could otherwise make one chunk allocate billions of entries.
const maxStreamIndex = 64

// opaqueChatReasoningDetails keys the reasoning_details array OpenRouter
// returns on chat-completions assistant turns. OpenRouter asks for the array
// back unchanged on the next request so signed or encrypted reasoning carries
// across tool rounds.
const opaqueChatReasoningDetails = "chat-completions-reasoning-details"

// setChatReasoning fills the response's readable reasoning and replay state
// from the chat-completions reasoning extensions. OpenRouter sends reasoning,
// while DeepSeek, vLLM, and llama.cpp send reasoning_content; the
// reasoning_details texts are a fallback for responses that carry only the
// structured form.
func setChatReasoning(out *ChatResponse, reasoning, reasoningContent string, details json.RawMessage) {
	summary := reasoning
	if strings.TrimSpace(summary) == "" {
		summary = reasoningContent
	}
	var entries []map[string]any
	if len(details) > 0 && json.Unmarshal(details, &entries) == nil && len(entries) > 0 {
		if strings.TrimSpace(summary) == "" {
			var parts []string
			for _, entry := range entries {
				switch entry["type"] {
				case "reasoning.text":
					parts = append(parts, stringField(entry, "text"))
				case "reasoning.summary":
					parts = append(parts, stringField(entry, "summary"))
				}
			}
			summary = joinReasoningSummary(parts)
		}
		if raw := boundedOpaque(details); raw != nil {
			out.OpaqueState = map[string]json.RawMessage{opaqueChatReasoningDetails: raw}
		}
	}
	out.ReasoningSummary = strings.TrimSpace(summary)
}

// reasoningDetailsAccumulator rebuilds OpenRouter's reasoning_details array
// from stream deltas. One entry arrives in many chunks that share a type and
// index: text and summary fragments concatenate, and later fields such as
// the signature fill in. The index is kept raw because providers disagree
// on its JSON type (Anthropic sends 0, Gemini sends "0").
type reasoningDetailsAccumulator struct {
	entries []map[string]any
	keys    []string
}

func (a *reasoningDetailsAccumulator) add(raw json.RawMessage) {
	var delta map[string]any
	if json.Unmarshal(raw, &delta) != nil || len(delta) == 0 {
		return
	}
	index, _ := json.Marshal(delta["index"])
	key := stringField(delta, "type") + "\x00" + strings.Trim(string(index), `"`)
	for i := range a.keys {
		if a.keys[i] != key {
			continue
		}
		entry := a.entries[i]
		for field, value := range delta {
			fragment, isString := value.(string)
			if prior, ok := entry[field].(string); ok && isString && (field == "text" || field == "summary") {
				entry[field] = prior + fragment
				continue
			}
			entry[field] = value
		}
		return
	}
	a.keys = append(a.keys, key)
	a.entries = append(a.entries, delta)
}

func (a *reasoningDetailsAccumulator) encode() json.RawMessage {
	if len(a.entries) == 0 {
		return nil
	}
	raw, err := json.Marshal(a.entries)
	if err != nil {
		return nil
	}
	return raw
}

func stringField(entry map[string]any, field string) string {
	value, _ := entry[field].(string)
	return value
}
