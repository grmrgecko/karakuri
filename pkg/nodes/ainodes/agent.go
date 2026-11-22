package ainodes

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/ai"
)

// defaultMaxIterations bounds the tool loop when the author leaves the field
// empty: enough for real multi-step work, small enough that a model stuck
// calling the same tool fails the node instead of spending the account.
const defaultMaxIterations = 10

// maxToolResultBytes caps one tool result on its way into the conversation.
// A tool that returns a megabyte would spend the whole context window on one
// observation; the cap keeps the loop alive and tells the model it happened.
const maxToolResultBytes = 64 << 10

// maxTraceResultBytes caps a result's copy in the run-history trace, which
// exists to show what happened, not to store payloads twice.
const maxTraceResultBytes = 2 << 10

const (
	agentInputPrompt     = "prompt"
	agentInputTranscript = "transcript"
	agentInputDirect     = "input"
)

// AgentNode runs a templated prompt through a model with tools attached: the
// model is asked, every tool call it makes is executed and answered, and the
// loop repeats until the model gives a final answer or the iteration cap
// trips. With no tools wired it is a plain one-shot prompt node — the loop
// simply ends on the first reply.
type AgentNode struct{}

// Meta describes the ai-agent node type.
func (AgentNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ai-agent",
		Label:       "AI Agent",
		Folder:      flow.FolderAI,
		Description: "Sends a prompt or transcript to a model, running any tools it calls until it answers",
		Icon:        "las la-robot",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{
				ID: "out", Label: "Out", Type: flow.PortString,
				TypeRule: &flow.PortTypeRule{
					Cases: []flow.PortTypeCase{
						{Match: map[string][]string{"response_format": {"json"}}, Type: flow.PortObject},
					},
					Default: flow.PortString,
				},
			},
			{
				ID: "transcript", Label: "Response", Type: flow.PortObject,
				TypeRule: &flow.PortTypeRule{
					Cases:   []flow.PortTypeCase{{Match: map[string][]string{"stream": {"true"}}, Type: PortAIStream}},
					Default: flow.PortObject,
				},
			},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "model", Label: "Model", Type: flow.FieldJSON,
				Port: PortAIModel, Required: true,
			},
			{
				ID: "tools", Label: "Tools", Type: flow.FieldJSON,
				Port: PortAITool, Multi: true,
			},
			{
				ID: "memory", Label: "Memory", Type: flow.FieldJSON,
				Port: PortAIMemory,
			},
			{
				ID: "images", Label: "Images", Type: flow.FieldJSON,
				Port: flow.PortImage, Multi: true,
			},
			{
				ID: "input_mode", Label: "Input", Type: flow.FieldSelect,
				Default: agentInputPrompt, NoParam: true,
				Options: []flow.Option{
					{Value: agentInputPrompt, Label: "Prompt"},
					{Value: agentInputTranscript, Label: "Transcript"},
					{Value: agentInputDirect, Label: "Input"},
				},
				Help: "Prompt and Transcript configure the request on the node. Input reads In directly as a string prompt or an object transcript.",
			},
			{
				ID: "transcript", Label: "Transcript", Type: flow.FieldJSON, Control: "transcript",
				ShowIf:  &flow.ShowIf{Field: "input_mode", Equals: agentInputTranscript},
				NoParam: true, HideLabel: true,
			},
			{
				ID: "prompt", Label: "Prompt", Type: flow.FieldText, Required: true,
				ShowIf:  &flow.ShowIf{Field: "input_mode", Equals: agentInputPrompt},
				NoParam: true, HideLabel: true,
			},
			{
				ID: "system", Label: "System prompt", Type: flow.FieldText,
				Help: "Instructions that frame every request. Supports {{ }} expression segments.",
			},
			{
				ID: "attach_image", Label: "Attach binary payload as image", Type: flow.FieldBoolean,
				Help: "Send a binary payload — a screenshot, a camera frame — to the model as an image beside the prompt. The payload must declare its image type (screenshot does; otherwise set meta.mime).",
			},
			{
				ID: "response_format", Label: "Response format", Type: flow.FieldSelect,
				Default: "text",
				Options: []flow.Option{
					{Value: "text", Label: "Text"},
					{Value: "json", Label: "JSON"},
				},
				Help: "JSON asks the model for a JSON object and emits it decoded; a reply that is not valid JSON fails onto the error branch.",
			},
			{
				ID: "stream", Label: "Stream", Type: flow.FieldBoolean,
				Help: "Emit response events as the provider generates them. The Response output becomes AI Stream.",
			},
			{
				ID: "response_schema", Label: "Response schema", Type: flow.FieldJSON,
				ShowIf: &flow.ShowIf{Field: "response_format", Equals: "json"},
				Help:   "JSON Schema the reply must match. Enforced by the provider where possible and validated here regardless; a reply that does not match fails onto the error branch.",
			},
			{
				ID: "max_iterations", Label: "Max iterations", Type: flow.FieldNumber,
				Default: defaultMaxIterations,
				Help:    "Model calls allowed before the node fails, bounding a loop that never settles on an answer.",
			},
		},
	}
}

// agentConfig is the decoded ai-agent node configuration. Model and Tools
// arrive over the node's port fields.
type agentConfig struct {
	Model          modelHandle       `json:"model"`
	Tools          []toolSet         `json:"tools"`
	Memory         memoryHandle      `json:"memory"`
	Images         []flow.ImageValue `json:"images"`
	InputMode      string            `json:"input_mode"`
	Prompt         string            `json:"prompt"`
	System         string            `json:"system"`
	Transcript     []transcriptEntry `json:"transcript"`
	AttachImage    bool              `json:"attach_image"`
	ResponseFormat string            `json:"response_format"`
	ResponseSchema map[string]any    `json:"response_schema"`
	Stream         bool              `json:"stream"`
	MaxIterations  float64           `json:"max_iterations"`
}

// transcriptEntry mirrors the Transcript output's entry shape, so what one
// agent emits another accepts. Entries from the incoming payload remain data
// so model-written expressions are never evaluated.
type transcriptEntry struct {
	Role             string                     `json:"role"`
	Text             string                     `json:"text"`
	ToolCalls        []transcriptCall           `json:"tool_calls,omitempty"`
	ToolCallID       string                     `json:"tool_call_id,omitempty"`
	ToolName         string                     `json:"tool_name,omitempty"`
	ReasoningSummary string                     `json:"reasoning_summary,omitempty"`
	OpaqueState      map[string]json.RawMessage `json:"opaque_state,omitempty"`
}

// transcriptCall is one tool call on a replayed assistant entry.
type transcriptCall struct {
	ID   string         `json:"id"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

// transcriptMessages builds the replayed conversation: system entries come
// back as system-prompt additions, everything else as messages in entry
// order. "ai" is accepted beside "assistant", matching the editor's label.
func transcriptMessages(entries []transcriptEntry) ([]ai.Message, []string, error) {
	var history []ai.Message
	var systemExtra []string
	for i, entry := range entries {
		switch strings.ToLower(strings.TrimSpace(entry.Role)) {
		case "system":
			if strings.TrimSpace(entry.Text) != "" {
				systemExtra = append(systemExtra, entry.Text)
			}
		case "user":
			history = append(history, ai.Message{Role: ai.RoleUser, Text: entry.Text})
		case "assistant", "ai":
			m := ai.Message{
				Role: ai.RoleAssistant, Text: entry.Text,
				ReasoningSummary: entry.ReasoningSummary, OpaqueState: entry.OpaqueState,
			}
			for _, call := range entry.ToolCalls {
				m.ToolCalls = append(m.ToolCalls, ai.ToolCall{ID: call.ID, Name: call.Name, Args: call.Args})
			}
			history = append(history, m)
		case "tool":
			history = append(history, ai.Message{
				Role: ai.RoleTool, Text: entry.Text,
				ToolCallID: entry.ToolCallID, ToolName: entry.ToolName,
			})
		default:
			return nil, nil, fmt.Errorf("transcript entry %d has unknown role %q; the roles are system, user, ai, tool", i+1, entry.Role)
		}
	}
	return history, systemExtra, nil
}

// configuredTranscript renders authored rows against the incoming message.
// Runtime transcript rows remain data so model output cannot run expressions.
func configuredTranscript(ec engine.ExecContext, msg *engine.Msg, entries []transcriptEntry) ([]transcriptEntry, error) {
	rendered := make([]transcriptEntry, len(entries))
	copy(rendered, entries)
	for i := range rendered {
		text, err := ec.Interpolate(rendered[i].Text, msg)
		if err != nil {
			return nil, fmt.Errorf("rendering transcript entry %d: %w", i+1, err)
		}
		rendered[i].Text = text
	}
	return rendered, nil
}

type conversationContinuation struct {
	SessionID string
	CacheKey  string
}

// payloadConversation accepts a conversation envelope directly or nested
// under HA Assist's transcript property.
func payloadConversation(payload any) ([]transcriptEntry, conversationContinuation, error) {
	value := payload
	var continuation conversationContinuation
	for depth := 0; depth < 3; depth++ {
		object, ok := value.(map[string]any)
		if !ok {
			break
		}
		if sessionID, ok := object["session_id"].(string); ok && sessionID != "" {
			continuation.SessionID = sessionID
		}
		if cacheKey, ok := object["cache_key"].(string); ok && cacheKey != "" {
			continuation.CacheKey = cacheKey
		}
		nested, present := object["transcript"]
		if !present {
			return nil, conversationContinuation{}, fmt.Errorf("conversation input object has no transcript property")
		}
		value = nested
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, conversationContinuation{}, fmt.Errorf("encoding conversation input: %w", err)
	}
	var entries []transcriptEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, conversationContinuation{}, fmt.Errorf("conversation input must contain role and text entries: %w", err)
	}
	return entries, continuation, nil
}

func payloadTranscript(payload any) ([]transcriptEntry, error) {
	entries, _, err := payloadConversation(payload)
	return entries, err
}

func messageCacheKey(msg *engine.Msg) string {
	if msg == nil || msg.Meta == nil {
		return ""
	}
	assist, _ := msg.Meta["ha_assist"].(map[string]any)
	key, _ := assist["conversation_id"].(string)
	return providerCacheKey(key)
}

func providerCacheKey(key string) string {
	if len(key) <= 64 {
		return key
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(key)))
}

func latestUserMessage(messages []ai.Message) (int, string) {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == ai.RoleUser && strings.TrimSpace(messages[i].Text) != "" {
			return i, messages[i].Text
		}
	}
	return -1, ""
}

// traceEntry records one executed tool call for the output metadata.
type traceEntry struct {
	Tool   string         `json:"tool"`
	Args   map[string]any `json:"args"`
	Result string         `json:"result"`
	Images int            `json:"images,omitempty"`
	Error  bool           `json:"error,omitempty"`
}

// Execute runs the agent loop.
func (AgentNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg agentConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}

	// Resolve the model and render the system prompt against the message.
	if cfg.Model.Connection == "" || cfg.Model.Model == "" {
		return nil, fmt.Errorf("ai-agent node has no model wired")
	}
	if (len(cfg.Tools) > 0 || cfg.Memory.Store != "") && !cfg.Model.SupportsTools {
		return nil, fmt.Errorf("model %q does not support tools; disconnect tools and memory or choose another model", cfg.Model.Model)
	}
	conn, err := resolveChatConn(ec, cfg.Model.Connection)
	if err != nil {
		return nil, err
	}
	system, err := ec.Interpolate(cfg.System, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering system prompt: %w", err)
	}

	// Resolve the pulled memory handle: which store, and which conversation
	// this run belongs to.
	var mem *memoryContext
	if cfg.Memory.Store != "" {
		if mem, err = prepareMemory(ec, cfg.Memory, msg); err != nil {
			return nil, err
		}
	}

	// Collect the wired tool sets into one table. Names must be unique across
	// every set: the model addresses a tool by name alone, so a collision
	// would route its calls arbitrarily.
	table := map[string]toolSpec{}
	var defs []ai.ToolDef
	for _, set := range cfg.Tools {
		for _, tool := range set.Tools {
			if _, dup := table[tool.Name]; dup {
				return nil, fmt.Errorf("two wired tools are named %q; set a prefix on one of their nodes", tool.Name)
			}
			table[tool.Name] = tool
			schema := tool.Schema
			if len(schema) == 0 {
				// Providers require an object schema even for a no-argument
				// tool.
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			defs = append(defs, ai.ToolDef{Name: tool.Name, Description: tool.Description, Schema: schema})
		}
	}

	// The memory's own tools join the table last, so a collision names the
	// wired tool as the one to move.
	if mem != nil {
		for _, tool := range mem.toolSpecs() {
			if _, dup := table[tool.Name]; dup {
				return nil, fmt.Errorf("a wired tool is named %q, which the memory reserves; rename it or turn the memory's tools off", tool.Name)
			}
			table[tool.Name] = tool
			defs = append(defs, ai.ToolDef{Name: tool.Name, Description: tool.Description, Schema: tool.Schema})
		}
	}

	maxIterations := int(cfg.MaxIterations)
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}

	// The prompt turn carries the payload as an image when asked to, then
	// every wired image handle in wire order — the prompt refers to them by
	// position. The author opted in each, so a payload that is not usable
	// bytes is an error rather than a silently text-only request.
	var promptImages []ai.Image
	if cfg.AttachImage {
		img, isBinary, err := payloadImage(msg)
		if err != nil {
			return nil, err
		}
		if !isBinary {
			return nil, fmt.Errorf("attach image is on but the payload is not binary")
		}
		promptImages = []ai.Image{img}
	}
	for i, value := range cfg.Images {
		img, err := imageFromValue(value)
		if err != nil {
			return nil, fmt.Errorf("images wire %d: %w", i+1, err)
		}
		promptImages = append(promptImages, img)
	}

	// Build the request. Prompt and Transcript are authored on the node. Input
	// reads a string as one user turn or an object as conversation history.
	var request []ai.Message
	continuation := conversationContinuation{CacheKey: messageCacheKey(msg)}
	switch cfg.InputMode {
	case agentInputTranscript:
		configured, err := configuredTranscript(ec, msg, cfg.Transcript)
		if err != nil {
			return nil, err
		}
		var systemExtra []string
		request, systemExtra, err = transcriptMessages(configured)
		if err != nil {
			return nil, err
		}
		for _, extra := range systemExtra {
			if system != "" {
				system += "\n\n"
			}
			system += extra
		}
	case agentInputDirect:
		if prompt, ok := msg.Payload.(string); ok {
			if strings.TrimSpace(prompt) == "" {
				return nil, fmt.Errorf("ai-agent node has an empty input prompt")
			}
			request = []ai.Message{{Role: ai.RoleUser, Text: prompt}}
		} else {
			entries, resumed, err := payloadConversation(msg.Payload)
			if err != nil {
				return nil, err
			}
			if resumed.SessionID != "" {
				continuation.SessionID = resumed.SessionID
			}
			if resumed.CacheKey != "" {
				continuation.CacheKey = providerCacheKey(resumed.CacheKey)
			}
			request, _, err = transcriptMessages(entries)
			if err != nil {
				return nil, err
			}
		}
	default:
		prompt, err := ec.Interpolate(cfg.Prompt, msg)
		if err != nil {
			return nil, fmt.Errorf("rendering prompt: %w", err)
		}
		if strings.TrimSpace(prompt) == "" {
			return nil, fmt.Errorf("ai-agent node has an empty prompt")
		}
		request = []ai.Message{{Role: ai.RoleUser, Text: prompt}}
	}
	_, prompt := latestUserMessage(request)
	if prompt == "" {
		return nil, fmt.Errorf("ai-agent input has no user message")
	}

	// Memory frames the request: recalled context lands in the system prompt,
	// and stored turns replay before the request.
	var messages []ai.Message
	if mem != nil {
		if block := mem.recallBlock(ctx, prompt); block != "" {
			if system != "" {
				system += "\n\n"
			}
			system += block
		}
		messages = append(messages, mem.transcript()...)
		if continuation.CacheKey == "" && mem.session != "" {
			continuation.CacheKey = providerCacheKey(mem.session)
		}
	}
	messages = append(messages, request...)
	if len(promptImages) > 0 {
		index, _ := latestUserMessage(messages)
		if index < 0 {
			return nil, fmt.Errorf("ai-agent transcript has no user message for attached images")
		}
		messages[index].Images = append(messages[index].Images, promptImages...)
	}

	// The loop: ask, run what the model asked for, answer, repeat. Tool
	// failures go back to the model as observations rather than failing the
	// node — recovering from a failed call is what an agent is for — while
	// provider errors fail the node onto its error branch.
	var trace []traceEntry
	var usage ai.Usage
	var final string
	var finalReasoningSummary string
	var finalOpaqueState map[string]json.RawMessage
	var structured any
	var providerData []json.RawMessage
	var providerHistory []map[string]any
	responseStream := newResponseStream(ec, msg, cfg.Model.Model)
	// Connections without Karakuri tools (Hermes) keep the conversation in
	// their own session, so local history is never compacted for them.
	compactor := ai.NewCompactor(conn, ai.CompactionSettings{
		Window:   cfg.Model.ContextWindow,
		Buffer:   cfg.Model.CompactionBuf,
		Disabled: cfg.Model.CompactionOff || !cfg.Model.SupportsTools,
	})
	var compactions []ai.Compaction
	answered := false
	for iter := 0; iter < maxIterations; iter++ {
		req := ai.ChatRequest{
			System:         system,
			Messages:       messages,
			Tools:          defs,
			ReadTranscript: ec.Wired("transcript"),
			JSONOutput:     cfg.ResponseFormat == "json",
			JSONSchema:     cfg.ResponseSchema,
			SessionID:      continuation.SessionID,
			CacheKey:       continuation.CacheKey,
		}
		applyModel(&req, cfg.Model)
		send := func(r ai.ChatRequest) (*ai.ChatResponse, error) {
			return chatWithStream(ctx, conn, r, cfg.Stream, responseStream.emit)
		}
		var resp *ai.ChatResponse
		var err error
		if continuation.SessionID != "" {
			resp, err = send(req)
		} else {
			resp, err = compactor.Send(ctx, &req, send, func(c ai.Compaction) error {
				compactions = append(compactions, c)
				addUsage(&usage, c.Usage)
				if cfg.Stream {
					responseStream.compacted(c)
				}
				return nil
			})
		}
		if err != nil {
			return nil, err
		}
		messages = req.Messages
		if resp.SessionID != "" {
			continuation.SessionID = resp.SessionID
		}
		if len(resp.ToolCalls) == 0 {
			if cfg.ResponseFormat == "json" {
				var repairTurns []ai.Message
				var callUsage ai.Usage
				var callResponses []json.RawMessage
				var callHistory []map[string]any
				var repairedSessionID string
				structured, final, finalReasoningSummary, finalOpaqueState, repairedSessionID, repairTurns, callUsage, callResponses, callHistory, err = repairStructured(ctx, conn, req, resp)
				if repairedSessionID != "" {
					continuation.SessionID = repairedSessionID
				}
				addUsage(&usage, callUsage)
				providerData = append(providerData, callResponses...)
				if len(callHistory) > 0 {
					providerHistory = callHistory
				}
				messages = append(messages, repairTurns...)
				if err != nil {
					return nil, fmt.Errorf("model reply could not be repaired: %w", err)
				}
			} else {
				final = resp.Text
				finalReasoningSummary = resp.ReasoningSummary
				finalOpaqueState = resp.OpaqueState
				addUsage(&usage, resp.Usage)
				providerData = append(providerData, providerResponses(resp)...)
				if history := providerTranscript(resp); len(history) > 0 {
					providerHistory = history
				}
			}
			answered = true
			break
		}

		addUsage(&usage, resp.Usage)
		providerData = append(providerData, providerResponses(resp)...)
		if history := providerTranscript(resp); len(history) > 0 {
			providerHistory = history
		}
		messages = append(messages, ai.Message{Role: ai.RoleAssistant, Text: resp.Text, ToolCalls: resp.ToolCalls, ReasoningSummary: resp.ReasoningSummary, OpaqueState: resp.OpaqueState})
		for _, call := range resp.ToolCalls {
			result, images, isErr := runTool(ctx, ec, table, call)
			if len(result) > maxToolResultBytes {
				result = wholeRunes(result, maxToolResultBytes) + "\n[truncated]"
			}
			// A tool that returned only images still owes the model words,
			// or a bare screenshot arrives as an empty observation.
			if result == "" && len(images) > 0 {
				result = fmt.Sprintf("[%d image(s) returned]", len(images))
			}
			trace = append(trace, traceEntry{
				Tool: call.Name, Args: call.Args, Result: truncate(result, maxTraceResultBytes),
				Images: len(images), Error: isErr,
			})
			messages = append(messages, ai.Message{
				Role: ai.RoleTool, ToolCallID: call.ID, ToolName: call.Name, Text: result, Images: images,
			})
		}
	}
	if !answered {
		return nil, fmt.Errorf("model gave no final answer within %d iterations", maxIterations)
	}

	// Build the output message: the answer as the payload, the accounting in
	// the metadata.
	var payload any = final
	if cfg.ResponseFormat == "json" {
		payload = structured
	}
	meta := nodeutil.CloneMeta(msg.Meta)
	meta["model"] = cfg.Model.Model
	meta["usage"] = jsonShaped(usage)
	if len(trace) > 0 {
		meta["tool_calls"] = jsonShaped(trace)
	}
	if len(compactions) > 0 {
		records := make([]any, len(compactions))
		for i, c := range compactions {
			records[i] = map[string]any{"reason": c.Reason, "before_tokens": c.BeforeTokens, "after_tokens": c.AfterTokens}
		}
		meta["compactions"] = records
	}
	if mem != nil {
		mem.save(ec, prompt, final)
		memMeta := map[string]any{"store": mem.store.Name, "recalled": mem.recalled}
		if mem.session != "" {
			memMeta["session"] = mem.session
		}
		meta["memory"] = memMeta
	}
	outputs := engine.One("out", &engine.Msg{Payload: payload, Topic: msg.Topic, Meta: meta, Group: msg.Group})
	if ec.Wired("transcript") {
		response := transcriptPayload(
			messages, final, finalReasoningSummary, finalOpaqueState,
			providerData, providerHistory, continuation, compactions,
		)
		if cfg.Stream {
			response = responseStream.completed(payload, response)
		}
		outputs["transcript"] = []*engine.Msg{{
			Payload: response,
			Topic:   msg.Topic, Meta: nodeutil.CloneMeta(meta), Group: msg.Group,
		}}
	}
	return outputs, nil
}

// transcriptPayload renders the reusable conversation as it stood when the
// model answered: replayed turns, the prompt, and each tool round, plus the
// final answer, which the loop never appended. The system prompt stays node
// configuration and is not carried into the next request. Images are
// reduced to a count so a screenshot-heavy run does not land its bytes in the
// payload and run history; entries are maps so expressions and scripts read
// them like any other object payload.
func transcriptPayload(messages []ai.Message, final, finalReasoningSummary string, finalOpaqueState map[string]json.RawMessage, responses []json.RawMessage, providerHistory []map[string]any, continuation conversationContinuation, compactions []ai.Compaction) map[string]any {
	entries := make([]map[string]any, 0, len(messages)+1)
	for _, m := range messages {
		entry := map[string]any{"role": m.Role, "text": m.Text}
		if len(m.ToolCalls) > 0 {
			calls := make([]map[string]any, len(m.ToolCalls))
			for i, c := range m.ToolCalls {
				calls[i] = map[string]any{"id": c.ID, "name": c.Name, "args": c.Args}
			}
			entry["tool_calls"] = calls
		}
		if m.ToolCallID != "" || m.ToolName != "" {
			entry["tool_call_id"] = m.ToolCallID
			entry["tool_name"] = m.ToolName
		}
		if len(m.Images) > 0 {
			entry["images"] = len(m.Images)
		}
		if m.ReasoningSummary != "" {
			entry["reasoning_summary"] = m.ReasoningSummary
		}
		if len(m.OpaqueState) > 0 {
			entry["opaque_state"] = m.OpaqueState
		}
		entries = append(entries, entry)
	}
	finalEntry := map[string]any{"role": ai.RoleAssistant, "text": final}
	if finalReasoningSummary != "" {
		finalEntry["reasoning_summary"] = finalReasoningSummary
	}
	if len(finalOpaqueState) > 0 {
		finalEntry["opaque_state"] = finalOpaqueState
	}
	transcript := append(entries, finalEntry)
	if len(providerHistory) > 0 {
		transcript = providerHistory
	}
	envelope := map[string]any{
		"transcript": transcript,
		"responses":  responses,
	}
	if continuation.SessionID != "" {
		envelope["session_id"] = continuation.SessionID
	}
	if continuation.CacheKey != "" {
		envelope["cache_key"] = continuation.CacheKey
	}
	if len(compactions) > 0 {
		envelope["compactions"] = compactions
	}
	return envelope
}

// runTool executes one tool call and renders the observation the model gets:
// the text rendering and any images the tool returned. Every failure is text
// rather than an error: a wrong tool name, a broken endpoint, and a tool-level
// failure are all things the model can react to.
func runTool(ctx context.Context, ec engine.ExecContext, table map[string]toolSpec, call ai.ToolCall) (string, []ai.Image, bool) {
	spec, ok := table[call.Name]
	if !ok {
		return fmt.Sprintf("error: no tool named %q is available", call.Name), nil, true
	}
	value, images, err := dispatch(ctx, ec, spec.Invoke, call.Args)
	if err != nil {
		return "error: " + err.Error(), nil, true
	}
	return stringifyResult(value), images, false
}

// truncate caps a string, marking the cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return wholeRunes(s, n) + "…"
}

// wholeRunes returns the longest prefix of s within n bytes that does not end
// inside a character. A byte-count cut through multi-byte text leaves half a
// character, which JSON encoding then replaces.
func wholeRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
