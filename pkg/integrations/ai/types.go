package ai

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// The chat vocabulary every provider wire maps onto. One request and response
// shape serves OpenAI-compatible servers, Anthropic, and Gemini, so the agent
// node builds a conversation once and the wire owns the provider's encoding —
// including tool calls, which is the part the three APIs spell differently.

// Message is one turn of a conversation. Role is "user", "assistant", or
// "tool"; an assistant turn may carry the tool calls the model requested, and
// a tool turn answers one of them.
type Message struct {
	Role string
	Text string
	// ToolCalls holds the calls an assistant turn requested.
	ToolCalls []ToolCall
	// ToolCallID and ToolName identify which call a tool turn answers.
	// Anthropic correlates by ID, Gemini by name; both are carried so every
	// wire finds what it needs.
	ToolCallID string
	ToolName   string
	// Images are visual parts attached to this turn — a screenshot a tool
	// returned, or an image on a user turn. Each wire places them where its
	// API allows: Anthropic inside the tool_result block, Gemini as inlineData
	// parts beside the functionResponse, and chat-completions in a following
	// user turn, since that API has no image slot on a tool message.
	Images []Image
	// ReasoningSummary is provider-disclosed reasoning text for display. It is
	// not authenticated replay state and wires never send it back to providers.
	ReasoningSummary string
	// OpaqueState carries provider output that must be replayed unchanged on a
	// later turn. Wires only read their own key.
	OpaqueState map[string]json.RawMessage
}

// Image is one visual part: raw bytes and their MIME type. The wires base64
// the bytes for their own encodings, so callers hand over the decoded image.
type Image struct {
	MIME string
	Data []byte
}

// mimeOrDefault returns the image's MIME type, falling back to PNG when a tool
// left it unset — the common screenshot format, and a value the providers
// require.
func (im Image) mimeOrDefault() string {
	if im.MIME == "" {
		return "image/png"
	}
	return im.MIME
}

// base64Data renders the image bytes as base64, the payload every provider
// encoding carries.
func (im Image) base64Data() string {
	return base64.StdEncoding.EncodeToString(im.Data)
}

// dataURL renders the image as an RFC 2397 data: URL, the form the
// chat-completions image_url field expects.
func (im Image) dataURL() string {
	return "data:" + im.mimeOrDefault() + ";base64," + im.base64Data()
}

// Message role constants.
const (
	RoleUser      = "user"
	RoleAssistant = "assistant"
	RoleTool      = "tool"
)

// ToolCall is one tool invocation a model requested. Args is the decoded
// arguments object; an ID is synthesized for providers that do not send one.
type ToolCall struct {
	ID   string
	Name string
	Args map[string]any
}

// ToolDef describes one tool offered to the model: a name, what it does, and
// a JSON Schema for its arguments object.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any
}

// ChatRequest is one model invocation.
type ChatRequest struct {
	Model    string
	System   string
	Messages []Message
	Tools    []ToolDef
	// SessionID resumes a provider-managed conversation when its wire supports
	// one. Hermes carries this through X-Hermes-Session-Id.
	SessionID string
	// CacheKey groups related requests for providers with a prompt-cache key.
	CacheKey string
	// ReadTranscript asks providers with a server-side agent session to return
	// its persisted message history. Other providers ignore it.
	ReadTranscript bool
	// Temperature is optional; nil leaves the provider's default in force,
	// which matters because some models reject the parameter outright.
	Temperature *float64
	// MaxTokens caps the response; zero leaves it to the provider, except
	// Anthropic, where the API requires a value and the wire supplies one.
	MaxTokens int
	// ReasoningEffort uses the provider's named reasoning depth when supported.
	ReasoningEffort string
	// ReasoningBudget is a provider token budget. Wires without a budget field
	// leave it unset rather than translating it to a different limit.
	ReasoningBudget int
	// JSONOutput asks for a JSON object response where the provider has a
	// switch for it (OpenAI's response_format, Gemini's response MIME type
	// when no tools are attached). Elsewhere it is prompt discipline.
	JSONOutput bool
	// JSONSchema constrains the JSON response to a schema. Read only when
	// JSONOutput is set: OpenAI enforces it through the json_schema response
	// format and Gemini through responseSchema; where a wire has no native
	// switch — Anthropic, or Gemini with tools attached — the schema is
	// stated in the system prompt instead, and the caller still validates
	// the reply.
	JSONSchema map[string]any
}

// schemaInstruction is the system-prompt statement of a response schema, for
// the wires with no native enforcement switch.
func schemaInstruction(system string, schema map[string]any) string {
	raw, err := json.Marshal(schema)
	if err != nil {
		return system
	}
	instruction := "Respond with a single JSON object that validates against this JSON Schema, and nothing else:\n" + string(raw)
	if system == "" {
		return instruction
	}
	return system + "\n\n" + instruction
}

// Usage is the token accounting a response reports.
type Usage struct {
	InputTokens           int `json:"input_tokens"`
	OutputTokens          int `json:"output_tokens"`
	CachedInputTokens     int `json:"cached_input_tokens,omitempty"`
	CacheWriteInputTokens int `json:"cache_write_input_tokens,omitempty"`
}

// ChatResponse is what one model invocation produced: text, tool calls the
// model wants run, or both.
type ChatResponse struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string
	Usage      Usage
	// PromptTokens is the whole prompt the provider counted, cached tokens
	// included, whatever the wire's usage fields exclude. The driver fills it.
	PromptTokens     int
	ReasoningSummary string
	OpaqueState      map[string]json.RawMessage
	// SessionID is the provider-managed conversation identifier to send on the
	// next turn. Empty means the wire is stateless.
	SessionID string
	// ProviderResponse is the complete successful response received from the
	// provider. JSON APIs retain the response object; streaming APIs retain the
	// ordered event array.
	ProviderResponse json.RawMessage
	// ProviderSupplementalResponses retains successful follow-up responses made
	// for the same invocation, such as Hermes' session message history.
	ProviderSupplementalResponses []json.RawMessage
	// ProviderTranscript is a provider's persisted conversation, including
	// server-side tool calls and results that are absent from the completion.
	ProviderTranscript []map[string]any
}

const (
	StreamEventContentDelta = "content.delta"
	StreamEventToolProgress = "tool.progress"
	// StreamEventContextCompacted reports that history was compacted before
	// the next model request.
	StreamEventContextCompacted = "context.compacted"
)

// StreamEvent is one visible update from a provider stream.
type StreamEvent struct {
	Type       string
	Delta      string
	ToolCallID string
	ToolName   string
	Label      string
	Status     string
}

func joinReasoningSummary(parts []string) string {
	nonempty := parts[:0]
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			nonempty = append(nonempty, part)
		}
	}
	return strings.Join(nonempty, "\n\n")
}

const maxOpaqueStateBytes = 256 << 10

func boundedOpaque(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || len(raw) > maxOpaqueStateBytes || !json.Valid(raw) {
		return nil
	}
	return append(json.RawMessage(nil), raw...)
}

// wire is one provider's HTTP encoding of the chat vocabulary.
type wire interface {
	// Chat runs one model invocation.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)
	// Models lists the model IDs the endpoint serves, which doubles as the
	// connection probe: it is the cheapest authenticated call every provider
	// has.
	Models(ctx context.Context) ([]string, error)
}

// streamingWire emits visible text as the provider sends it and returns the
// same completed response Chat would return.
type streamingWire interface {
	ChatStream(ctx context.Context, req ChatRequest, emit func(string) error) (*ChatResponse, error)
}

type eventStreamingWire interface {
	ChatEventStream(ctx context.Context, req ChatRequest, emit func(StreamEvent) error) (*ChatResponse, error)
}
