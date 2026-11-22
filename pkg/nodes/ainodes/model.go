package ainodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
)

// chatConn is the slice of the AI driver the nodes need. Asserted rather than
// imported as a concrete type so the compile-time check below is the only
// coupling to the driver's shape.
type chatConn interface {
	Chat(ctx context.Context, req ai.ChatRequest) (*ai.ChatResponse, error)
}

type streamingChatConn interface {
	Stream(ctx context.Context, req ai.ChatRequest, emit func(string) error) (*ai.ChatResponse, error)
}

type eventStreamingChatConn interface {
	StreamEvents(ctx context.Context, req ai.ChatRequest, emit func(ai.StreamEvent) error) (*ai.ChatResponse, error)
}

type modelConn interface {
	chatConn
	SupportsTools() bool
}

var _ chatConn = (*ai.Driver)(nil)
var _ streamingChatConn = (*ai.Driver)(nil)
var _ eventStreamingChatConn = (*ai.Driver)(nil)

func chatWithStream(ctx context.Context, conn chatConn, req ai.ChatRequest, stream bool, emit func(ai.StreamEvent) error) (*ai.ChatResponse, error) {
	if !stream {
		return conn.Chat(ctx, req)
	}
	if streaming, ok := conn.(eventStreamingChatConn); ok {
		return streaming.StreamEvents(ctx, req, emit)
	}
	if streaming, ok := conn.(streamingChatConn); ok {
		return streaming.Stream(ctx, req, func(delta string) error {
			return emit(ai.StreamEvent{Type: ai.StreamEventContentDelta, Delta: delta})
		})
	}
	resp, err := conn.Chat(ctx, req)
	if err == nil && resp.Text != "" {
		err = emit(ai.StreamEvent{Type: ai.StreamEventContentDelta, Delta: resp.Text})
	}
	return resp, err
}

type responseStream struct {
	ec    engine.ExecContext
	msg   *engine.Msg
	model string
	index int
}

func newResponseStream(ec engine.ExecContext, msg *engine.Msg, model string) *responseStream {
	return &responseStream{ec: ec, msg: msg, model: model}
}

func (s *responseStream) emit(event ai.StreamEvent) error {
	payload := map[string]any{"type": event.Type, "index": s.index}
	switch event.Type {
	case ai.StreamEventContentDelta:
		if event.Delta == "" {
			return nil
		}
		payload["delta"] = event.Delta
	case ai.StreamEventToolProgress:
		payload["tool_call_id"] = event.ToolCallID
		payload["tool_name"] = event.ToolName
		payload["label"] = event.Label
		payload["status"] = event.Status
	default:
		return nil
	}
	meta := nodeutil.CloneMeta(s.msg.Meta)
	meta["model"] = s.model
	s.index++
	s.ec.Emit("transcript", &engine.Msg{Payload: payload, Topic: s.msg.Topic, Meta: meta, Group: s.msg.Group})
	return nil
}

// compacted emits a context.compacted event for a compaction made before the
// next model request.
func (s *responseStream) compacted(c ai.Compaction) {
	meta := nodeutil.CloneMeta(s.msg.Meta)
	meta["model"] = s.model
	payload := map[string]any{
		"type": ai.StreamEventContextCompacted, "index": s.index,
		"reason": c.Reason, "before_tokens": c.BeforeTokens, "after_tokens": c.AfterTokens,
	}
	s.index++
	s.ec.Emit("transcript", &engine.Msg{Payload: payload, Topic: s.msg.Topic, Meta: meta, Group: s.msg.Group})
}

func (s *responseStream) completed(output, response any) map[string]any {
	return map[string]any{
		"type":     "response.completed",
		"index":    s.index,
		"output":   output,
		"response": response,
	}
}

// resolveChatConn resolves a connection ID to an AI provider driver.
func resolveChatConn(ec engine.ExecContext, id string) (chatConn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(chatConn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an AI provider connection", id)
	}
	return c, nil
}

// ModelNode is the ai-model pure source: it names a provider connection, a
// model, and the sampling settings, and emits that as a handle the agent
// pulls over its model port. A separate node rather than fields on the agent
// because one configured model feeds any number of agents — and because the
// connection field can then say which provider family it takes while the
// agent stays provider-blind.
type ModelNode struct{}

// Meta describes the ai-model node type.
func (ModelNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ai-model",
		Label:       "AI Model",
		Folder:      flow.FolderAI,
		Description: "Names an AI provider connection and model, supplying the agent's model input",
		Icon:        "las la-brain",
		Pure:        true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Model", Type: PortAIModel},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "connection", Label: "Connection", Type: flow.FieldConnection,
				Required: true, ConnectionKind: ai.ConnectionKinds(),
				Help: "AI provider connection to use.",
			},
			{
				ID: "model", Label: "Model", Type: flow.FieldString, Control: "ai-model", Required: true,
				Placeholder: "gpt-5-mini",
				Help:        "Model identifier as the provider spells it. Completes from the connection's model list; anything the provider accepts may still be typed.",
			},
			{
				ID: "temperature", Label: "Temperature", Type: flow.FieldNumber,
				Help: "Sampling temperature. Empty leaves the provider's default, which matters for models that reject the setting.",
			},
			{
				ID: "max_tokens", Label: "Max tokens", Type: flow.FieldNumber,
				Help: "Response cap per model call. Empty leaves the provider's default.",
			},
			{
				ID: "reasoning_effort", Label: "Reasoning effort", Type: flow.FieldSelect,
				Options: []flow.Option{{Value: "minimal", Label: "Minimal"}, {Value: "low", Label: "Low"}, {Value: "medium", Label: "Medium"}, {Value: "high", Label: "High"}, {Value: "xhigh", Label: "Extra high"}, {Value: "max", Label: "Max"}},
				Help:    "Provider reasoning depth. Empty leaves the provider's default.",
			},
			{
				ID: "reasoning_budget", Label: "Reasoning budget", Type: flow.FieldNumber,
				Help: "Reasoning token budget for providers that support one.",
			},
			{
				ID: "context_window", Label: "Context window", Type: flow.FieldNumber,
				Placeholder: "From connection",
				Help:        "Tokens a request may hold. Empty uses the connection's setting or the window the provider reports.",
			},
			{
				ID: "auto_compaction", Label: "Automatic compaction", Type: flow.FieldBoolean, Default: true,
				Help: "Summarize older conversation into a checkpoint when a request nears the context window or the provider rejects it as too long. Off returns the provider's error instead.",
			},
			{
				ID: "compaction_buffer", Label: "Compaction buffer", Type: flow.FieldString,
				Placeholder: "15%",
				ShowIf:      &flow.ShowIf{Field: "auto_compaction", Equals: true},
				Help:        "Room kept free below the window before compacting: a percentage such as 20% or a token count. Empty keeps 15%, raised to 16,000 tokens or a quarter of the window, whichever is smaller.",
			},
		},
	}
}

// modelConfig is the decoded ai-model node configuration.
type modelConfig struct {
	Connection      string   `json:"connection"`
	Model           string   `json:"model"`
	Temperature     *float64 `json:"temperature"`
	MaxTokens       float64  `json:"max_tokens"`
	ReasoningEffort string   `json:"reasoning_effort"`
	ReasoningBudget float64  `json:"reasoning_budget"`
	ContextWindow   float64  `json:"context_window"`
	AutoCompaction  *bool    `json:"auto_compaction"`
	CompactionBuf   string   `json:"compaction_buffer"`
}

// Execute checks the connection resolves to an AI provider and emits the
// handle. The check runs here rather than at the consumer so a wrong
// connection fails at the node whose config holds it.
func (ModelNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg modelConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		return nil, fmt.Errorf("ai-model node has no model")
	}
	conn, err := resolveChatConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	modelProvider, ok := conn.(modelConn)
	if !ok {
		return nil, fmt.Errorf("connection %s does not report model capabilities", cfg.Connection)
	}
	if strings.TrimSpace(cfg.ReasoningEffort) != "" && cfg.ReasoningBudget > 0 {
		return nil, fmt.Errorf("ai-model reasoning effort and budget cannot both be set")
	}
	if cfg.ContextWindow < 0 {
		return nil, fmt.Errorf("ai-model context window must be a positive token count")
	}
	if err := ai.ValidateCompactionBuffer(cfg.CompactionBuf); err != nil {
		return nil, err
	}
	return engine.One("out", &engine.Msg{
		Payload: modelHandle{
			Connection:      cfg.Connection,
			Model:           model,
			SupportsTools:   modelProvider.SupportsTools(),
			Temperature:     cfg.Temperature,
			MaxTokens:       int(cfg.MaxTokens),
			ReasoningEffort: strings.TrimSpace(cfg.ReasoningEffort),
			ReasoningBudget: int(cfg.ReasoningBudget),
			ContextWindow:   int(cfg.ContextWindow),
			CompactionOff:   cfg.AutoCompaction != nil && !*cfg.AutoCompaction,
			CompactionBuf:   strings.TrimSpace(cfg.CompactionBuf),
		},
		Topic: model,
	}), nil
}
