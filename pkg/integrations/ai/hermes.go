package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// hermesWire uses the OpenAI-compatible completion endpoint and, on demand,
// reads Hermes' persisted session messages to expose its built-in tool chain.
type hermesWire struct {
	openaiWire openaiWire
}

func (w *hermesWire) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	resp, headers, err := w.chat(ctx, req, nil)
	return w.attachTranscript(ctx, req, resp, headers, err)
}

// ChatStream preserves Hermes' custom SSE events in the provider response;
// visible Chat Completions deltas are emitted as text.
func (w *hermesWire) ChatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	return w.ChatEventStream(ctx, req, func(event StreamEvent) error {
		if event.Type != StreamEventContentDelta {
			return nil
		}
		return emit(event.Delta)
	})
}

// ChatEventStream forwards Hermes tool progress beside visible text.
func (w *hermesWire) ChatEventStream(ctx context.Context, req ChatRequest, emit func(StreamEvent) error) (*ChatResponse, error) {
	headers := w.openaiWire.headers()
	if req.SessionID != "" {
		headers["X-Hermes-Session-Id"] = req.SessionID
	}
	resp, responseHeaders, err := w.openaiWire.chatStreamWithHeadersAndEvents(
		ctx,
		req,
		headers,
		func(delta string) error {
			return emit(StreamEvent{Type: StreamEventContentDelta, Delta: delta})
		},
		func(event string, data json.RawMessage) error {
			if event != "hermes.tool.progress" {
				return nil
			}
			var progress struct {
				ToolCallID string `json:"toolCallId"`
				Tool       string `json:"tool"`
				Label      string `json:"label"`
				Status     string `json:"status"`
			}
			if err := json.Unmarshal(data, &progress); err != nil {
				return nil
			}
			if progress.ToolCallID == "" || progress.Tool == "" ||
				(progress.Status != "running" && progress.Status != "completed") {
				return nil
			}
			return emit(StreamEvent{
				Type: StreamEventToolProgress, ToolCallID: progress.ToolCallID,
				ToolName: progress.Tool, Label: progress.Label, Status: progress.Status,
			})
		},
	)
	return w.attachTranscript(ctx, req, resp, responseHeaders, err)
}

func (w *hermesWire) chat(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, http.Header, error) {
	headers := w.openaiWire.headers()
	if req.SessionID != "" {
		headers["X-Hermes-Session-Id"] = req.SessionID
	}
	if emit == nil {
		return w.openaiWire.chatWithHeaders(ctx, req, headers)
	}
	return w.openaiWire.chatStreamWithHeaders(ctx, req, headers, emit)
}

func (w *hermesWire) attachTranscript(ctx context.Context, req ChatRequest, resp *ChatResponse, headers http.Header, requestErr error) (*ChatResponse, error) {
	if requestErr != nil {
		return resp, requestErr
	}

	sessionID := strings.TrimSpace(headers.Get("X-Hermes-Session-Id"))
	if sessionID != "" {
		resp.SessionID = sessionID
	}
	if !req.ReadTranscript {
		return resp, nil
	}
	if sessionID == "" {
		return nil, fmt.Errorf("Hermes response did not identify its session")
	}
	endpoint, err := hermesSessionMessagesURL(w.openaiWire.base, sessionID)
	if err != nil {
		return nil, err
	}
	var history struct {
		Data []hermesMessage `json:"data"`
	}
	rawHistory, err := doJSONRaw(ctx, http.MethodGet, endpoint, w.openaiWire.headers(), nil, &history)
	if err != nil {
		return nil, fmt.Errorf("reading Hermes transcript: %w", err)
	}
	resp.ProviderSupplementalResponses = append(resp.ProviderSupplementalResponses, rawHistory)
	resp.ProviderTranscript = make([]map[string]any, 0, len(history.Data))
	for _, message := range history.Data {
		if strings.EqualFold(strings.TrimSpace(message.Role), "system") {
			continue
		}
		resp.ProviderTranscript = append(resp.ProviderTranscript, message.transcriptEntry())
	}
	return resp, nil
}

func (w *hermesWire) Models(ctx context.Context) ([]string, error) {
	return w.openaiWire.Models(ctx)
}

type hermesMessage struct {
	Role       string           `json:"role"`
	Content    any              `json:"content"`
	ToolCallID string           `json:"tool_call_id"`
	ToolName   string           `json:"tool_name"`
	ToolCalls  []hermesToolCall `json:"tool_calls"`
}

type hermesToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	Function  struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

func (m hermesMessage) transcriptEntry() map[string]any {
	text, images := hermesContent(m.Content)
	entry := map[string]any{"role": m.Role, "text": text}
	if images > 0 {
		entry["images"] = images
	}
	if m.ToolCallID != "" || m.ToolName != "" {
		entry["tool_call_id"] = m.ToolCallID
		entry["tool_name"] = m.ToolName
	}
	if len(m.ToolCalls) > 0 {
		calls := make([]map[string]any, 0, len(m.ToolCalls))
		for _, call := range m.ToolCalls {
			name, raw := call.Name, call.Arguments
			if call.Function.Name != "" {
				name, raw = call.Function.Name, call.Function.Arguments
			}
			args := map[string]any{}
			if len(raw) > 0 && json.Unmarshal(raw, &args) != nil {
				var encoded string
				if json.Unmarshal(raw, &encoded) == nil {
					_ = json.Unmarshal([]byte(encoded), &args)
				}
			}
			calls = append(calls, map[string]any{"id": call.ID, "name": name, "args": args})
		}
		entry["tool_calls"] = calls
	}
	return entry
}

func hermesContent(content any) (string, int) {
	switch value := content.(type) {
	case nil:
		return "", 0
	case string:
		return value, 0
	case []any:
		var text []string
		images := 0
		for _, raw := range value {
			part, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			switch part["type"] {
			case "text", "input_text", "output_text":
				if s, ok := part["text"].(string); ok && s != "" {
					text = append(text, s)
				}
			case "image", "image_url", "input_image":
				images++
			}
		}
		return strings.Join(text, "\n"), images
	default:
		raw, _ := json.Marshal(value)
		return string(raw), 0
	}
}

func hermesSessionMessagesURL(base, sessionID string) (string, error) {
	u, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("parsing Hermes base URL: %w", err)
	}
	u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/v1") + "/api/sessions/" + url.PathEscape(sessionID) + "/messages"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}
