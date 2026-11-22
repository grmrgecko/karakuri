package ai

import (
	"context"
	"encoding/json"
	"fmt"
	neturl "net/url"
	"strconv"
	"strings"
)

// geminiWire speaks the Gemini generateContent API: roles are user/model,
// tool definitions are functionDeclarations, and a tool result rides back as
// a functionResponse part correlated by name and order — Gemini sends no call
// IDs, so the driver synthesizes them for the shared vocabulary and matches
// results by ToolName on the way out.
type geminiWire struct {
	base string
	key  string
}

func (w *geminiWire) headers() map[string]string {
	return map[string]string{"x-goog-api-key": w.key}
}

// Chat implements wire over POST /v1beta/models/{model}:generateContent.
func (w *geminiWire) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	body := w.requestBody(req)

	var resp geminiResponse
	url := w.base + "/v1beta/models/" + neturl.PathEscape(req.Model) + ":generateContent"
	rawResponse, err := doJSONRaw(ctx, "POST", url, w.headers(), body, &resp)
	if err != nil {
		return nil, err
	}
	out, err := parseGeminiResponses([]geminiResponse{resp})
	if err != nil {
		return nil, err
	}
	out.ProviderResponse = rawResponse
	return out, nil
}

func (w *geminiWire) requestBody(req ChatRequest) map[string]any {
	body := map[string]any{
		"contents": w.encodeMessages(req.Messages),
	}
	system := req.System
	// With tools attached the native response schema is unavailable, so the
	// schema is stated in the system prompt instead.
	if req.JSONOutput && len(req.JSONSchema) > 0 && len(req.Tools) > 0 {
		system = schemaInstruction(system, req.JSONSchema)
	}
	if system != "" {
		body["systemInstruction"] = map[string]any{"parts": []map[string]any{{"text": system}}}
	}
	if len(req.Tools) > 0 {
		decls := make([]map[string]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			decl := map[string]any{"name": t.Name, "description": t.Description}
			if len(t.Schema) > 0 {
				decl["parameters"] = t.Schema
			}
			decls = append(decls, decl)
		}
		body["tools"] = []map[string]any{{"functionDeclarations": decls}}
	}
	gen := map[string]any{}
	if req.Temperature != nil {
		gen["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		gen["maxOutputTokens"] = req.MaxTokens
	}
	if req.ReasoningEffort != "" || req.ReasoningBudget > 0 {
		thinking := map[string]any{}
		if req.ReasoningBudget > 0 {
			thinking["thinkingBudget"] = req.ReasoningBudget
		} else {
			thinking["thinkingLevel"] = strings.ToUpper(req.ReasoningEffort)
		}
		gen["thinkingConfig"] = thinking
	}
	// The JSON response switch and tool calling are mutually exclusive on
	// this API; with tools attached the format stays prompt discipline.
	if req.JSONOutput && len(req.Tools) == 0 {
		gen["responseMimeType"] = "application/json"
		if len(req.JSONSchema) > 0 {
			gen["responseSchema"] = geminiSchema(req.JSONSchema)
		}
	}
	if len(gen) > 0 {
		body["generationConfig"] = gen
	}

	return body
}

type geminiResponse struct {
	Candidates []struct {
		FinishReason string `json:"finishReason"`
		Content      struct {
			Parts []json.RawMessage `json:"parts"`
		} `json:"content"`
	} `json:"candidates"`
	UsageMetadata struct {
		PromptTokenCount     int `json:"promptTokenCount"`
		CandidatesTokenCount int `json:"candidatesTokenCount"`
		CachedContentTokens  int `json:"cachedContentTokenCount"`
	} `json:"usageMetadata"`
}

type geminiPart struct {
	Text         string `json:"text"`
	Thought      bool   `json:"thought"`
	FunctionCall *struct {
		Name string         `json:"name"`
		Args map[string]any `json:"args"`
	} `json:"functionCall"`
}

// ChatStream implements Gemini's streamGenerateContent SSE endpoint.
func (w *geminiWire) ChatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error) {
	body := w.requestBody(req)
	var chunks []geminiResponse
	url := w.base + "/v1beta/models/" + neturl.PathEscape(req.Model) + ":streamGenerateContent?alt=sse"
	_, raw, err := doSSE(ctx, url, w.headers(), body, func(_ string, data json.RawMessage) error {
		if err := streamError(data); err != nil {
			return err
		}
		var chunk geminiResponse
		if json.Unmarshal(data, &chunk) != nil {
			return nil
		}
		chunks = append(chunks, chunk)
		for _, candidate := range chunk.Candidates {
			for _, rawPart := range candidate.Content.Parts {
				var part geminiPart
				if json.Unmarshal(rawPart, &part) == nil && !part.Thought && part.FunctionCall == nil && part.Text != "" {
					if err := emit(part.Text); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := parseGeminiResponses(chunks)
	if err != nil {
		return nil, err
	}
	out.ProviderResponse = raw
	return out, nil
}

func parseGeminiResponses(responses []geminiResponse) (*ChatResponse, error) {
	out := &ChatResponse{}
	var text strings.Builder
	var summaries []string
	var replay []json.RawMessage
	seen := false
	callIndex := 0
	for _, resp := range responses {
		if resp.UsageMetadata.PromptTokenCount > 0 || resp.UsageMetadata.CandidatesTokenCount > 0 {
			out.Usage = Usage{
				InputTokens: resp.UsageMetadata.PromptTokenCount, OutputTokens: resp.UsageMetadata.CandidatesTokenCount,
				CachedInputTokens: resp.UsageMetadata.CachedContentTokens,
			}
		}
		for _, candidate := range resp.Candidates {
			seen = true
			if candidate.FinishReason != "" {
				out.StopReason = candidate.FinishReason
			}
			for _, rawPart := range candidate.Content.Parts {
				replay = append(replay, rawPart)
				var part geminiPart
				if json.Unmarshal(rawPart, &part) != nil {
					continue
				}
				if part.FunctionCall != nil {
					args := part.FunctionCall.Args
					if args == nil {
						args = map[string]any{}
					}
					out.ToolCalls = append(out.ToolCalls, ToolCall{
						ID: "call_" + strconv.Itoa(callIndex), Name: part.FunctionCall.Name, Args: args,
					})
					callIndex++
				} else if part.Thought {
					summaries = append(summaries, part.Text)
				} else {
					text.WriteString(part.Text)
				}
			}
		}
	}
	if !seen {
		return nil, fmt.Errorf("response has no candidates")
	}
	out.Text = text.String()
	out.ReasoningSummary = joinReasoningSummary(summaries)
	if len(replay) > 0 {
		if raw, err := json.Marshal(replay); err == nil {
			if raw = boundedOpaque(raw); raw != nil {
				out.OpaqueState = map[string]json.RawMessage{"gemini": raw}
			}
		}
	}
	return out, nil
}

// encodeMessages renders the conversation as Gemini contents. Tool results
// become functionResponse parts on a user turn; the response value must be an
// object, so a bare value is wrapped as {"result": value}.
func (w *geminiWire) encodeMessages(messages []Message) []map[string]any {
	var out []map[string]any
	var pendingResults []map[string]any
	flushResults := func() {
		if len(pendingResults) == 0 {
			return
		}
		out = append(out, map[string]any{"role": "user", "parts": pendingResults})
		pendingResults = nil
	}

	for _, m := range messages {
		switch m.Role {
		case RoleAssistant:
			flushResults()
			if raw := m.OpaqueState["gemini"]; len(raw) > 0 {
				// Stored parts replay unchanged. State that holds none falls
				// through to the turn's own text and calls.
				var replay []json.RawMessage
				if json.Unmarshal(raw, &replay) == nil && len(replay) > 0 {
					out = append(out, map[string]any{"role": "model", "parts": json.RawMessage(raw)})
					continue
				}
			}
			var parts []map[string]any
			if m.Text != "" {
				parts = append(parts, map[string]any{"text": m.Text})
			}
			for _, tc := range m.ToolCalls {
				args := tc.Args
				if args == nil {
					args = map[string]any{}
				}
				parts = append(parts, map[string]any{
					"functionCall": map[string]any{"name": tc.Name, "args": args},
				})
			}
			// The API refuses a turn with no parts, so a reply that said
			// nothing is left out of the history.
			if len(parts) == 0 {
				continue
			}
			out = append(out, map[string]any{"role": "model", "parts": parts})
		case RoleTool:
			pendingResults = append(pendingResults, map[string]any{
				"functionResponse": map[string]any{
					"name":     m.ToolName,
					"response": map[string]any{"result": m.Text},
				},
			})
			// A functionResponse holds JSON, not image bytes, so a tool's
			// images ride as inlineData parts in the same user turn beside it.
			for _, img := range m.Images {
				pendingResults = append(pendingResults, map[string]any{
					"inlineData": map[string]any{
						"mimeType": img.mimeOrDefault(),
						"data":     img.base64Data(),
					},
				})
			}
		default:
			flushResults()
			parts := []map[string]any{}
			if m.Text != "" {
				parts = append(parts, map[string]any{"text": m.Text})
			}
			for _, img := range m.Images {
				parts = append(parts, map[string]any{
					"inlineData": map[string]any{
						"mimeType": img.mimeOrDefault(),
						"data":     img.base64Data(),
					},
				})
			}
			if len(parts) == 0 {
				parts = append(parts, map[string]any{"text": m.Text})
			}
			out = append(out, map[string]any{"role": "user", "parts": parts})
		}
	}
	flushResults()
	return out
}

// geminiSchemaKeys are the JSON Schema fields Gemini's Schema object accepts;
// anything else in a request is rejected, so an author's schema is reduced to
// the intersection rather than failing the call over an additionalProperties
// it never enforces anyway.
var geminiSchemaKeys = map[string]bool{
	"type": true, "format": true, "description": true, "nullable": true,
	"enum": true, "items": true, "properties": true, "required": true,
	"minimum": true, "maximum": true, "minItems": true, "maxItems": true,
}

// geminiSchema reduces a JSON Schema to the subset Gemini's responseSchema
// accepts, recursively.
func geminiSchema(schema map[string]any) map[string]any {
	out := make(map[string]any, len(schema))
	for key, value := range schema {
		if !geminiSchemaKeys[key] {
			continue
		}
		switch key {
		case "items":
			if sub, ok := value.(map[string]any); ok {
				out[key] = geminiSchema(sub)
				continue
			}
		case "properties":
			if props, ok := value.(map[string]any); ok {
				reduced := make(map[string]any, len(props))
				for name, prop := range props {
					if sub, ok := prop.(map[string]any); ok {
						reduced[name] = geminiSchema(sub)
					}
				}
				out[key] = reduced
				continue
			}
		}
		out[key] = value
	}
	return out
}

// Models implements wire over GET /v1beta/models, stripping the "models/"
// prefix Gemini puts on every name so the IDs read the way requests want them.
// The listing is followed to its last page: the API answers 50 models a page
// unless asked for more, and lists its newest models last.
func (w *geminiWire) Models(ctx context.Context) ([]string, error) {
	ids := []string{}
	token := ""
	for page := 0; page < maxModelPages; page++ {
		query := "?pageSize=1000"
		if token != "" {
			query += "&pageToken=" + neturl.QueryEscape(token)
		}
		var resp struct {
			Models []struct {
				Name string `json:"name"`
			} `json:"models"`
			NextPageToken string `json:"nextPageToken"`
		}
		if err := doJSON(ctx, "GET", w.base+"/v1beta/models"+query, w.headers(), nil, &resp); err != nil {
			return nil, err
		}
		for _, m := range resp.Models {
			ids = append(ids, strings.TrimPrefix(m.Name, "models/"))
		}
		if resp.NextPageToken == "" || resp.NextPageToken == token {
			break
		}
		token = resp.NextPageToken
	}
	return ids, nil
}
