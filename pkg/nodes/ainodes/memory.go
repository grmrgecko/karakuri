package ainodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
	"github.com/grmrgecko/karakuri/pkg/memories"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// memoryHandle is the ai-memory payload: which store to remember into, which
// session the conversation lives under, and how much of the bank rides each
// request. The session is a template the agent renders against its own
// incoming message — the memory node is a pure source and never sees one.
type memoryHandle struct {
	Store       string `json:"store"`
	Session     string `json:"session"`
	Window      int    `json:"window"`
	Recall      int    `json:"recall"`
	RecallTurns bool   `json:"recall_turns"`
	Rerank      string `json:"rerank,omitempty"`
	Tools       bool   `json:"tools"`
	Save        bool   `json:"save"`
}

// Reserved tool names the memory handle contributes to the agent.
const (
	memoryRememberTool = "remember"
	memoryRecallTool   = "recall"
)

// MemoryNode is the ai-memory pure source: it names a memory store and the
// conversation settings, and emits that as a handle the agent pulls over its
// memory port. A node rather than fields on the agent for the same reason the
// model is: one configured memory feeds any number of agents, which is what
// makes two flows share a conversation.
type MemoryNode struct{}

// Meta describes the ai-memory node type.
func (MemoryNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ai-memory",
		Label:       "AI Memory",
		Folder:      flow.FolderAI,
		Description: "Names a memory store and session, supplying the agent's memory input",
		Icon:        "las la-book",
		Pure:        true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Memory", Type: PortAIMemory},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "store", Label: "Memory store", Type: flow.FieldString,
				Control: "memory-store", Required: true,
				Help: "Memory store to remember into, defined under Settings → Memory.",
			},
			{
				ID: "session", Label: "Session", Type: flow.FieldString,
				Help: "Conversation key, rendered against the agent's incoming message. Empty is one shared conversation for the store.",
			},
			{
				ID: "window", Label: "Transcript window", Type: flow.FieldNumber, Default: 20,
				Help: "Past conversation turns replayed to the model each run. 0 replays nothing.",
			},
			{
				ID: "recall", Label: "Recalled memories", Type: flow.FieldNumber, Default: 5,
				Help: "Most relevant memories injected into the system prompt each run, scored against the rendered prompt. 0 disables.",
			},
			{
				ID: "recall_turns", Label: "Recall from conversations", Type: flow.FieldBoolean,
				Help: "Let recall search past conversation turns beside saved facts.",
			},
			{
				ID: "rerank", Label: "Diversify results", Type: flow.FieldSelect,
				Options: []flow.Option{
					{Value: "", Label: "Off"},
					{Value: memories.RerankMMR, Label: "MMR reranking"},
				},
				Help: "MMR reranking suppresses near-duplicate memories so recall includes more distinct context.",
			},
			{
				ID: "tools", Label: "Offer remember and recall tools", Type: flow.FieldBoolean, Default: true,
				Help: "Give the model remember and recall tools so it can save and search memories itself.",
			},
			{
				ID: "save", Label: "Save conversations", Type: flow.FieldBoolean, Default: true,
				Help: "Append each prompt and answer to the session's transcript.",
			},
		},
	}
}

// memoryNodeConfig is the decoded ai-memory node configuration. Every field
// whose default is not zero is a pointer, so a config that never stored it
// still gets the default while an explicit zero still means off.
type memoryNodeConfig struct {
	Store       string   `json:"store"`
	Session     string   `json:"session"`
	Window      *float64 `json:"window"`
	Recall      *float64 `json:"recall"`
	RecallTurns bool     `json:"recall_turns"`
	Rerank      string   `json:"rerank"`
	Tools       *bool    `json:"tools"`
	Save        *bool    `json:"save"`
}

// Execute resolves the store and emits the handle. Resolution runs here so a
// deleted store fails at the node whose config names it.
func (MemoryNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg memoryNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	snap, err := resolveMemoryStore(cfg.Store)
	if err != nil {
		return nil, err
	}
	defaultTrue := func(v *bool) bool { return v == nil || *v }
	defaultNum := func(v *float64, d int) int {
		if v == nil {
			return d
		}
		return int(*v)
	}
	return engine.One("out", &engine.Msg{
		Payload: memoryHandle{
			Store:       snap.ID.String(),
			Session:     cfg.Session,
			Window:      defaultNum(cfg.Window, 20),
			Recall:      defaultNum(cfg.Recall, 5),
			RecallTurns: cfg.RecallTurns,
			Rerank:      cfg.Rerank,
			Tools:       defaultTrue(cfg.Tools),
			Save:        defaultTrue(cfg.Save),
		},
		Topic: snap.Name,
	}), nil
}

// resolveMemoryStore looks up a configured store reference and reports a
// usable error when it is missing.
func resolveMemoryStore(ref string) (memories.StoreSnapshot, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return memories.StoreSnapshot{}, fmt.Errorf("no memory store selected")
	}
	snap, ok := memories.S.ResolveStore(ref)
	if !ok {
		return memories.StoreSnapshot{}, fmt.Errorf("memory store %q not found", ref)
	}
	return snap, nil
}

// memoryContext is a handle resolved against one agent run: the store, the
// rendered session key, and what the handle asked to happen around the loop.
type memoryContext struct {
	store    memories.StoreSnapshot
	session  string
	handle   memoryHandle
	recalled int
}

// prepareMemory resolves the pulled handle for this run.
func prepareMemory(ec engine.ExecContext, handle memoryHandle, msg *engine.Msg) (*memoryContext, error) {
	snap, err := resolveMemoryStore(handle.Store)
	if err != nil {
		return nil, err
	}
	session, err := ec.Interpolate(handle.Session, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering memory session: %w", err)
	}
	return &memoryContext{store: snap, session: strings.TrimSpace(session), handle: handle}, nil
}

// transcript returns the session's replayed turns as conversation messages.
// The window counts turns, so it can cut the oldest exchange in half; the
// replay then starts at the next user turn, because providers reject a
// conversation that opens with the assistant.
func (m *memoryContext) transcript() []ai.Message {
	if m.handle.Window <= 0 {
		return nil
	}
	turns := memories.S.Transcript(m.store.ID, m.session, m.handle.Window)
	out := make([]ai.Message, 0, len(turns))
	for _, turn := range turns {
		if turn.Role != ai.RoleUser && turn.Role != ai.RoleAssistant {
			continue
		}
		if len(out) == 0 && turn.Role != ai.RoleUser {
			continue
		}
		out = append(out, ai.Message{Role: turn.Role, Text: turn.Text})
	}
	return out
}

// recallBlock scores the bank against the rendered prompt and renders the
// system-prompt addendum carrying what surfaced, dated so the model can weigh
// old context for what it is. Empty when recall is off or nothing scored.
func (m *memoryContext) recallBlock(ctx context.Context, prompt string) string {
	if m.handle.Recall <= 0 {
		return ""
	}
	results := memories.S.Recall(ctx, m.store.ID, prompt, memories.RecallOpts{
		Session:      m.session,
		Limit:        m.handle.Recall,
		IncludeTurns: m.handle.RecallTurns,
		Rerank:       m.handle.Rerank,
	})
	m.recalled = len(results)
	if len(results) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("Relevant memories from earlier conversations and saved facts:")
	for _, r := range results {
		b.WriteString("\n- [")
		b.WriteString(r.CreatedAt.Format("2006-01-02"))
		if r.Kind == models.MemoryKindTurn && r.Role != "" {
			b.WriteString(", ")
			b.WriteString(r.Role)
			b.WriteString(" said")
		}
		b.WriteString("] ")
		b.WriteString(r.Text)
	}
	return b.String()
}

// toolSpecs returns the remember and recall tools the handle offers the
// model, bound to this run's store and session.
func (m *memoryContext) toolSpecs() []toolSpec {
	if !m.handle.Tools {
		return nil
	}
	storeID := m.store.ID.String()
	return []toolSpec{
		{
			Name: memoryRememberTool,
			Description: "Save a lasting memory to the \"" + m.store.Name + "\" memory store so future " +
				"conversations can recall it. Use for durable facts, preferences, decisions, and outcomes " +
				"worth keeping — not for transient conversation state.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"text":       map[string]any{"type": "string", "description": "The fact to remember, phrased to stand alone."},
					"importance": map[string]any{"type": "number", "description": "How important it is, 0 to 1. Default 0.5."},
					"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "Optional tags for recall."},
					"session_only": map[string]any{"type": "boolean",
						"description": "True to scope the memory to this conversation instead of the whole store."},
				},
				"required": []string{"text"},
			},
			Invoke: invokeSpec{Kind: invokeRemember, Store: storeID, Session: m.session},
		},
		{
			Name: memoryRecallTool,
			Description: "Search the \"" + m.store.Name + "\" memory store for memories relevant to a query. " +
				"Returns the best matches with relevance scores. Use it when earlier context might exist " +
				"beyond what was already provided.",
			Schema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "What to search for."},
					"limit": map[string]any{"type": "integer", "description": "Most results to return. Default 5."},
				},
				"required": []string{"query"},
			},
			Invoke: invokeSpec{Kind: invokeRecall, Store: storeID, Session: m.session,
				RecallTurns: m.handle.RecallTurns, Rerank: m.handle.Rerank},
		},
	}
}

// save appends the exchange to the session's transcript. A storage failure is
// logged rather than failing the node: the answer exists, and losing it over
// bookkeeping would be the worse trade.
func (m *memoryContext) save(ec engine.ExecContext, prompt, answer string) {
	if !m.handle.Save {
		return
	}
	if err := memories.S.AppendExchange(m.store.ID, m.session, prompt, answer); err != nil {
		ec.Logger().Error("saving conversation to memory failed", zap.Error(err))
	}
}

// dispatchMemory executes the remember and recall tool calls.
func dispatchMemory(ctx context.Context, inv invokeSpec, args map[string]any) (any, error) {
	storeID, err := uuid.Parse(inv.Store)
	if err != nil {
		return nil, fmt.Errorf("memory tool has an invalid store reference")
	}
	switch inv.Kind {
	case invokeRemember:
		text, _ := args["text"].(string)
		params := memories.MemoryParams{Text: text}
		if imp, ok := args["importance"].(float64); ok {
			params.Importance = imp
		}
		if raw, ok := args["tags"].([]any); ok {
			for _, t := range raw {
				if s, ok := t.(string); ok {
					params.Tags = append(params.Tags, s)
				}
			}
		}
		if scoped, _ := args["session_only"].(bool); scoped {
			params.Session = inv.Session
		}
		if _, err := memories.S.Remember(storeID, params); err != nil {
			return nil, err
		}
		return "Remembered.", nil

	case invokeRecall:
		query, _ := args["query"].(string)
		if strings.TrimSpace(query) == "" {
			return nil, fmt.Errorf("recall needs a query")
		}
		limit := 0
		if n, ok := args["limit"].(float64); ok {
			limit = int(n)
		}
		results := memories.S.Recall(ctx, storeID, query, memories.RecallOpts{
			Session: inv.Session, Limit: limit, IncludeTurns: inv.RecallTurns, Rerank: inv.Rerank,
		})
		if len(results) == 0 {
			return "No matching memories.", nil
		}
		out := make([]map[string]any, 0, len(results))
		for _, r := range results {
			entry := map[string]any{
				"text":  r.Text,
				"score": r.Score,
				"date":  r.CreatedAt.Format("2006-01-02"),
			}
			if len(r.Tags) > 0 {
				entry["tags"] = r.Tags
			}
			out = append(out, entry)
		}
		return out, nil

	default:
		return nil, fmt.Errorf("unknown memory tool kind %q", inv.Kind)
	}
}

// MemoryRememberNode stores one memory from the flow, passing the message
// through — the counterpart of the agent's remember tool for flows that know
// what is worth keeping without asking a model.
type MemoryRememberNode struct{}

// Meta describes the memory-remember node type.
func (MemoryRememberNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "memory-remember",
		Label:       "Remember",
		Folder:      flow.FolderAI,
		Description: "Stores a memory in a memory store and passes the message through",
		Icon:        "las la-save",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "store", Label: "Memory store", Type: flow.FieldString,
				Control: "memory-store", Required: true,
				Help: "Memory store to remember into.",
			},
			{
				ID: "text", Label: "Text", Type: flow.FieldText, Required: true,
				Placeholder: "{{ payload }}",
				Help:        "What to remember. Supports {{ }} expression segments.",
			},
			{
				ID: "session", Label: "Session", Type: flow.FieldString,
				Help: "Scope the memory to one conversation. Supports {{ }} segments; empty is global to the store.",
			},
			{
				ID: "importance", Label: "Importance", Type: flow.FieldNumber,
				Help: "Recall weight, 0 to 1. Empty uses the default.",
			},
			{
				ID: "tags", Label: "Tags", Type: flow.FieldJSON, Control: "list",
				Help: "Tags matched alongside the text in recall.",
			},
			{
				ID: "ttl_hours", Label: "Expires after (hours)", Type: flow.FieldNumber,
				Help: "Drop the memory after this many hours. Empty keeps it forever.",
			},
		},
	}
}

// memoryRememberConfig is the decoded memory-remember node configuration.
type memoryRememberConfig struct {
	Store      string   `json:"store"`
	Text       string   `json:"text"`
	Session    string   `json:"session"`
	Importance float64  `json:"importance"`
	Tags       []string `json:"tags"`
	TTLHours   float64  `json:"ttl_hours"`
}

// Execute stores the rendered memory.
func (MemoryRememberNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg memoryRememberConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	snap, err := resolveMemoryStore(cfg.Store)
	if err != nil {
		return nil, err
	}
	text, err := ec.Interpolate(cfg.Text, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering text: %w", err)
	}
	session, err := ec.Interpolate(cfg.Session, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering session: %w", err)
	}
	params := memories.MemoryParams{
		Text:       text,
		Session:    strings.TrimSpace(session),
		Importance: cfg.Importance,
		Tags:       cfg.Tags,
	}
	if cfg.TTLHours > 0 {
		at := ec.Clock().Now().Add(time.Duration(cfg.TTLHours * float64(time.Hour)))
		params.ExpiresAt = &at
	}
	item, err := memories.S.Remember(snap.ID, params)
	if err != nil {
		return nil, err
	}
	meta := nodeutil.CloneMeta(msg.Meta)
	meta["memory_id"] = item.ID.String()
	return engine.One("out", &engine.Msg{Payload: msg.Payload, Topic: msg.Topic, Meta: meta, Group: msg.Group}), nil
}

// MemoryRecallNode searches a memory store from the flow, no model involved —
// the counterpart of mcp-call for memories. The results become the payload,
// so a flow can route on whether anything was remembered.
type MemoryRecallNode struct{}

// Meta describes the memory-recall node type.
func (MemoryRecallNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "memory-recall",
		Label:       "Recall",
		Folder:      flow.FolderAI,
		Description: "Searches a memory store and emits the matching memories",
		Icon:        "las la-search",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "store", Label: "Memory store", Type: flow.FieldString,
				Control: "memory-store", Required: true,
				Help: "Memory store to search.",
			},
			{
				ID: "query", Label: "Query", Type: flow.FieldText, Required: true,
				Placeholder: "{{ payload }}",
				Help:        "What to search for. Supports {{ }} expression segments.",
			},
			{
				ID: "session", Label: "Session", Type: flow.FieldString,
				Help: "Include memories scoped to this conversation. Supports {{ }} segments.",
			},
			{
				ID: "limit", Label: "Limit", Type: flow.FieldNumber, Default: 5,
				Help: "Most results to return.",
			},
			{
				ID: "include_turns", Label: "Search conversations", Type: flow.FieldBoolean,
				Help: "Search past conversation turns beside saved facts.",
			},
			{
				ID: "rerank", Label: "Diversify results", Type: flow.FieldSelect,
				Options: []flow.Option{
					{Value: "", Label: "Off"},
					{Value: memories.RerankMMR, Label: "MMR reranking"},
				},
				Help: "MMR reranking suppresses near-duplicate memories so recall includes more distinct context.",
			},
		},
	}
}

// memoryRecallConfig is the decoded memory-recall node configuration.
type memoryRecallConfig struct {
	Store        string  `json:"store"`
	Query        string  `json:"query"`
	Session      string  `json:"session"`
	Limit        float64 `json:"limit"`
	IncludeTurns bool    `json:"include_turns"`
	Rerank       string  `json:"rerank"`
}

// Execute runs the recall and emits the results.
func (MemoryRecallNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg memoryRecallConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	snap, err := resolveMemoryStore(cfg.Store)
	if err != nil {
		return nil, err
	}
	query, err := ec.Interpolate(cfg.Query, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering query: %w", err)
	}
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("memory-recall node has an empty query")
	}
	session, err := ec.Interpolate(cfg.Session, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering session: %w", err)
	}
	results := memories.S.Recall(ctx, snap.ID, query, memories.RecallOpts{
		Session:      strings.TrimSpace(session),
		Limit:        int(cfg.Limit),
		IncludeTurns: cfg.IncludeTurns,
		Rerank:       cfg.Rerank,
	})

	payload := make([]any, 0, len(results))
	for _, r := range results {
		entry := map[string]any{
			"text":       r.Text,
			"score":      r.Score,
			"kind":       r.Kind,
			"importance": r.Importance,
			// A payload holds JSON values. A time value compares and renders
			// differently from the text a rerun reads back from history.
			"created_at": r.CreatedAt.Format(time.RFC3339Nano),
		}
		if len(r.Tags) > 0 {
			tags := make([]any, len(r.Tags))
			for i, tag := range r.Tags {
				tags[i] = tag
			}
			entry["tags"] = tags
		}
		if r.Session != "" {
			entry["session"] = r.Session
		}
		payload = append(payload, entry)
	}
	meta := nodeutil.CloneMeta(msg.Meta)
	meta["memory_store"] = snap.Name
	return engine.One("out", &engine.Msg{Payload: payload, Topic: msg.Topic, Meta: meta, Group: msg.Group}), nil
}
