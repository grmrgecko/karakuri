package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// anthropicVersion is the API revision every request declares.
const anthropicVersion = "2023-06-01"

// anthropicDefaultMaxTokens fills the max_tokens the messages API requires
// when the request leaves it unset.
const anthropicDefaultMaxTokens = 4096

// anthropicWire speaks Anthropic's messages API: content blocks instead of
// plain strings, tool_use blocks on assistant turns, and tool results carried
// as user-role tool_result blocks — consecutive results merged into one user
// message, because the API insists on alternating roles.
type anthropicWire struct {
	base string
	key  string
}

var olderClaudeOpus = regexp.MustCompile(`^claude-opus-4(-[0-6])?(-\d{8})?$`)

// anthropicSupportsTemperature follows Anthropic's model-family boundary:
// direct Claude generations through 4.6 accept sampling parameters, while
// newer and unknown Claude families reject them. Gateway IDs remain opaque.
func anthropicSupportsTemperature(model string) bool {
	if !strings.HasPrefix(model, "claude-") {
		return true
	}
	if strings.HasPrefix(model, "claude-2") || strings.HasPrefix(model, "claude-instant") || strings.HasPrefix(model, "claude-3") {
		return true
	}
	if strings.HasPrefix(model, "claude-sonnet-4") || strings.HasPrefix(model, "claude-haiku-4") {
		return true
	}
	return olderClaudeOpus.MatchString(model)
}

func (w *anthropicWire) headers() map[string]string {
	return map[string]string{
		"x-api-key":         w.key,
		"anthropic-version": anthropicVersion,
	}
}

// Chat implements wire over POST /v1/messages.
func (w *anthropicWire) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := w.requestBody(req)

	var resp struct {
		Content    []json.RawMessage `json:"content"`
		StopReason string            `json:"stop_reason"`
		Usage      struct {
			InputTokens              int `json:"input_tokens"`
			OutputTokens             int `json:"output_tokens"`
			CacheReadInputTokens     int `json:"cache_read_input_tokens"`
			CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	}
	rawResponse, err := doJSONRaw(ctx, "POST", w.base+"/v1/messages", w.headers(), body, &resp)
	if err != nil {
		return nil, err
	}

	out := &ChatResponse{
		StopReason: resp.StopReason,
		Usage: Usage{
			InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens,
			CachedInputTokens:     resp.Usage.CacheReadInputTokens,
			CacheWriteInputTokens: resp.Usage.CacheCreationInputTokens,
		},
		ProviderResponse: rawResponse,
	}
	var text strings.Builder
	var summaries []string
	for _, raw := range resp.Content {
		var block struct {
			Type     string         `json:"type"`
			Text     string         `json:"text"`
			Thinking string         `json:"thinking"`
			ID       string         `json:"id"`
			Name     string         `json:"name"`
			Input    map[string]any `json:"input"`
		}
		if json.Unmarshal(raw, &block) != nil {
			continue
		}
		switch block.Type {
		case "thinking":
			summaries = append(summaries, block.Thinking)
		case "text":
			text.WriteString(block.Text)
		case "tool_use":
			args := block.Input
			if args == nil {
				args = map[string]any{}
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.ID, Name: block.Name, Args: args})
		}
	}
	out.Text = text.String()
	out.ReasoningSummary = joinReasoningSummary(summaries)
	if len(resp.Content) > 0 {
		if raw, err := json.Marshal(resp.Content); err == nil {
			if raw = boundedOpaque(raw); raw != nil {
				out.OpaqueState = map[string]json.RawMessage{"anthropic": raw}
			}
		}
	}
	return out, nil
}

func (w *anthropicWire) requestBody(req ChatRequest) map[string]any {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		// The API rejects a thinking budget that is not below max_tokens.
		maxTokens = anthropicDefaultMaxTokens + req.ReasoningBudget
	}
	body := map[string]any{
		"model":      req.Model,
		"max_tokens": maxTokens,
		"messages":   w.encodeMessages(req.Messages),
		"cache_control": map[string]any{
			"type": "ephemeral",
		},
	}
	// The messages API has no JSON response switch, so a schema is stated in
	// the system prompt — the caller validates the reply either way.
	system := req.System
	if req.JSONOutput && len(req.JSONSchema) > 0 {
		system = schemaInstruction(system, req.JSONSchema)
	}
	if system != "" {
		body["system"] = system
	}
	reasoning := req.ReasoningEffort != "" || req.ReasoningBudget > 0
	if req.ReasoningBudget > 0 {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": req.ReasoningBudget}
	} else if req.ReasoningEffort != "" {
		body["thinking"] = map[string]any{"type": "adaptive", "display": "summarized"}
	}
	if req.ReasoningEffort != "" && req.ReasoningBudget == 0 {
		body["output_config"] = map[string]any{"effort": req.ReasoningEffort}
	}
	if req.Temperature != nil && !reasoning && anthropicSupportsTemperature(req.Model) {
		body["temperature"] = *req.Temperature
	}
	if len(req.Tools) > 0 {
		tools := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Schema,
			})
		}
		body["tools"] = tools
	}

	return body
}

// ChatStream implements Anthropic's Messages SSE event sequence.
func (w *anthropicWire) ChatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	body := w.requestBody(req)
	body["stream"] = true
	type pendingBlock struct {
		kind, id, name                  string
		text, thinking, args, signature strings.Builder
		input                           map[string]any
		// data is a redacted_thinking block's encrypted content, which arrives
		// whole on the block's start event.
		data string
	}
	var blocks []pendingBlock
	out := &ChatResponse{}
	completed := false
	_, raw, err := doSSE(ctx, w.base+"/v1/messages", w.headers(), body, func(_ string, data json.RawMessage) error {
		var event struct {
			Type    string `json:"type"`
			Index   int    `json:"index"`
			Message struct {
				Usage struct {
					InputTokens              int `json:"input_tokens"`
					CacheReadInputTokens     int `json:"cache_read_input_tokens"`
					CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
			ContentBlock struct {
				Type     string         `json:"type"`
				ID       string         `json:"id"`
				Name     string         `json:"name"`
				Text     string         `json:"text"`
				Thinking string         `json:"thinking"`
				Data     string         `json:"data"`
				Input    map[string]any `json:"input"`
			} `json:"content_block"`
			Delta struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Thinking    string `json:"thinking"`
				PartialJSON string `json:"partial_json"`
				Signature   string `json:"signature"`
				StopReason  string `json:"stop_reason"`
			} `json:"delta"`
			Usage struct {
				OutputTokens int `json:"output_tokens"`
			} `json:"usage"`
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &event) != nil {
			return nil
		}
		switch event.Type {
		case "message_start":
			out.Usage.InputTokens = event.Message.Usage.InputTokens
			out.Usage.CachedInputTokens = event.Message.Usage.CacheReadInputTokens
			out.Usage.CacheWriteInputTokens = event.Message.Usage.CacheCreationInputTokens
		case "content_block_start":
			if event.Index < 0 || event.Index > maxStreamIndex {
				return fmt.Errorf("content block index %d is out of range", event.Index)
			}
			for len(blocks) <= event.Index {
				blocks = append(blocks, pendingBlock{})
			}
			block := &blocks[event.Index]
			block.kind = event.ContentBlock.Type
			block.id = event.ContentBlock.ID
			block.name = event.ContentBlock.Name
			block.input = event.ContentBlock.Input
			block.data = event.ContentBlock.Data
			block.text.WriteString(event.ContentBlock.Text)
			block.thinking.WriteString(event.ContentBlock.Thinking)
		case "content_block_delta":
			if event.Index < 0 || event.Index > maxStreamIndex {
				return fmt.Errorf("content block index %d is out of range", event.Index)
			}
			for len(blocks) <= event.Index {
				blocks = append(blocks, pendingBlock{})
			}
			block := &blocks[event.Index]
			switch event.Delta.Type {
			case "text_delta":
				block.text.WriteString(event.Delta.Text)
				if event.Delta.Text != "" {
					return emit(event.Delta.Text)
				}
			case "thinking_delta":
				block.thinking.WriteString(event.Delta.Thinking)
			case "input_json_delta":
				block.args.WriteString(event.Delta.PartialJSON)
			case "signature_delta":
				block.signature.WriteString(event.Delta.Signature)
			}
		case "message_delta":
			out.StopReason = event.Delta.StopReason
			out.Usage.OutputTokens = event.Usage.OutputTokens
		case "message_stop":
			completed = true
		case "error":
			if event.Error.Message != "" {
				return fmt.Errorf("anthropic: %s", event.Error.Message)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if !completed {
		return nil, fmt.Errorf("stream ended without a response")
	}
	var text strings.Builder
	var summaries []string
	var replay []map[string]any
	for _, block := range blocks {
		switch block.kind {
		case "text":
			value := block.text.String()
			text.WriteString(value)
			replay = append(replay, map[string]any{"type": "text", "text": value})
		case "thinking":
			value := block.thinking.String()
			summaries = append(summaries, value)
			item := map[string]any{"type": "thinking", "thinking": value}
			if block.signature.Len() > 0 {
				item["signature"] = block.signature.String()
			}
			replay = append(replay, item)
		case "redacted_thinking":
			// The API requires thinking blocks back unchanged on the next tool
			// round, encrypted ones included.
			replay = append(replay, map[string]any{"type": "redacted_thinking", "data": block.data})
		case "tool_use":
			args := block.input
			if block.args.Len() > 0 {
				args = map[string]any{}
				if err := json.Unmarshal([]byte(block.args.String()), &args); err != nil {
					return nil, fmt.Errorf("tool call %q has invalid arguments: %w", block.name, err)
				}
			}
			if args == nil {
				args = map[string]any{}
			}
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: block.id, Name: block.name, Args: args})
			replay = append(replay, map[string]any{"type": "tool_use", "id": block.id, "name": block.name, "input": args})
		}
	}
	out.Text = text.String()
	out.ReasoningSummary = joinReasoningSummary(summaries)
	out.ProviderResponse = raw
	if len(replay) > 0 {
		if replayRaw, err := json.Marshal(replay); err == nil {
			if replayRaw = boundedOpaque(replayRaw); replayRaw != nil {
				out.OpaqueState = map[string]json.RawMessage{"anthropic": replayRaw}
			}
		}
	}
	return out, nil
}

// anthropicReplayBlocks returns an assistant turn's stored content blocks for
// replay, without the text blocks that hold nothing but whitespace: the API
// returns them now and then and refuses them on the way back in. The stored
// bytes are returned unchanged when no block is dropped. The result is nil
// when the state is not a block list or no block remains.
func anthropicReplayBlocks(raw json.RawMessage) json.RawMessage {
	var blocks []json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return nil
	}
	kept := make([]json.RawMessage, 0, len(blocks))
	for _, block := range blocks {
		var head struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(block, &head) == nil && head.Type == "text" && strings.TrimSpace(head.Text) == "" {
			continue
		}
		kept = append(kept, block)
	}
	if len(kept) == 0 {
		return nil
	}
	if len(kept) == len(blocks) {
		return raw
	}
	trimmed, err := json.Marshal(kept)
	if err != nil {
		return nil
	}
	return trimmed
}

// encodeMessages renders the conversation as messages-API turns. Tool results
// become user-role tool_result blocks, and a run of them collapses into one
// user message so the roles keep alternating.
func (w *anthropicWire) encodeMessages(messages []Message) []map[string]any {
	var out []map[string]any
	var pendingResults []map[string]any
	flushResults := func() {
		if len(pendingResults) == 0 {
			return
		}
		out = append(out, map[string]any{"role": "user", "content": pendingResults})
		pendingResults = nil
	}

	for _, m := range messages {
		switch m.Role {
		case RoleAssistant:
			flushResults()
			if raw := m.OpaqueState["anthropic"]; len(raw) > 0 {
				if replay := anthropicReplayBlocks(raw); replay != nil {
					out = append(out, map[string]any{"role": "assistant", "content": replay})
					continue
				}
			}
			var blocks []map[string]any
			if strings.TrimSpace(m.Text) != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Args
				if args == nil {
					args = map[string]any{}
				}
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": args,
				})
			}
			// The API refuses an assistant turn with no content, so a reply
			// that said nothing is left out of the history.
			if len(blocks) == 0 {
				continue
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		case RoleTool:
			result := map[string]any{
				"type":        "tool_result",
				"tool_use_id": m.ToolCallID,
			}
			// The messages API lets a tool_result hold a content array of text
			// and image blocks, so a screenshot stays attached to the call it
			// answers rather than drifting to a separate turn.
			if len(m.Images) > 0 {
				blocks := []map[string]any{}
				if m.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
				}
				for _, img := range m.Images {
					blocks = append(blocks, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": img.mimeOrDefault(),
							"data":       img.base64Data(),
						},
					})
				}
				result["content"] = blocks
			} else {
				result["content"] = m.Text
			}
			pendingResults = append(pendingResults, result)
		default:
			flushResults()
			if len(m.Images) > 0 {
				blocks := []map[string]any{}
				if m.Text != "" {
					blocks = append(blocks, map[string]any{"type": "text", "text": m.Text})
				}
				for _, img := range m.Images {
					blocks = append(blocks, map[string]any{
						"type": "image",
						"source": map[string]any{
							"type":       "base64",
							"media_type": img.mimeOrDefault(),
							"data":       img.base64Data(),
						},
					})
				}
				out = append(out, map[string]any{"role": "user", "content": blocks})
			} else {
				out = append(out, map[string]any{"role": "user", "content": m.Text})
			}
		}
	}
	flushResults()
	return out
}

// Models implements wire over GET /v1/models, following the listing's pages
// to the end: the API answers 20 models a page unless asked for more.
func (w *anthropicWire) Models(ctx context.Context) ([]string, error) {
	ids := []string{}
	after := ""
	for page := 0; page < maxModelPages; page++ {
		query := "?limit=1000"
		if after != "" {
			query += "&after_id=" + url.QueryEscape(after)
		}
		var resp struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
			HasMore bool   `json:"has_more"`
			LastID  string `json:"last_id"`
		}
		if err := doJSON(ctx, "GET", w.base+"/v1/models"+query, w.headers(), nil, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.Data {
			ids = append(ids, m.ID)
		}
		if !resp.HasMore || resp.LastID == "" || resp.LastID == after {
			break
		}
		after = resp.LastID
	}
	return ids, nil
}

// maxModelPages bounds how many pages of a model listing are followed, so a
// server that never ends its listing cannot hold a request forever.
const maxModelPages = 50
