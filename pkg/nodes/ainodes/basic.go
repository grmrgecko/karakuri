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

// BasicChainNode sends one prompt or transcript to a model without adding
// Karakuri tools or memory.
type BasicChainNode struct{}

// Meta describes the ai-basic-chain node type.
func (BasicChainNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "ai-basic-chain",
		Label:       "AI Basic Chain",
		Folder:      flow.FolderAI,
		Description: "Sends a prompt or transcript to a model without tools or memory",
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
				Help: "Send a binary payload to the model as an image beside the prompt. The payload must declare its image type.",
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
				Help:   "JSON Schema the reply must match. Enforced by the provider where possible and validated here regardless.",
			},
		},
	}
}

type basicChainConfig struct {
	Model          modelHandle       `json:"model"`
	Images         []flow.ImageValue `json:"images"`
	InputMode      string            `json:"input_mode"`
	Prompt         string            `json:"prompt"`
	System         string            `json:"system"`
	Transcript     []transcriptEntry `json:"transcript"`
	AttachImage    bool              `json:"attach_image"`
	ResponseFormat string            `json:"response_format"`
	ResponseSchema map[string]any    `json:"response_schema"`
	Stream         bool              `json:"stream"`
}

// Execute runs the prompt through the configured model without a tool loop.
func (BasicChainNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg basicChainConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	if cfg.Model.Connection == "" || cfg.Model.Model == "" {
		return nil, fmt.Errorf("ai-basic-chain node has no model wired")
	}
	conn, err := resolveChatConn(ec, cfg.Model.Connection)
	if err != nil {
		return nil, err
	}
	system, err := ec.Interpolate(cfg.System, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering system prompt: %w", err)
	}

	var messages []ai.Message
	continuation := conversationContinuation{CacheKey: messageCacheKey(msg)}
	switch cfg.InputMode {
	case agentInputTranscript:
		configured, err := configuredTranscript(ec, msg, cfg.Transcript)
		if err != nil {
			return nil, err
		}
		var systemExtra []string
		messages, systemExtra, err = transcriptMessages(configured)
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
				return nil, fmt.Errorf("ai-basic-chain node has an empty input prompt")
			}
			messages = []ai.Message{{Role: ai.RoleUser, Text: prompt}}
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
			messages, _, err = transcriptMessages(entries)
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
			return nil, fmt.Errorf("ai-basic-chain node has an empty prompt")
		}
		messages = []ai.Message{{Role: ai.RoleUser, Text: prompt}}
	}
	index, _ := latestUserMessage(messages)
	if index < 0 {
		return nil, fmt.Errorf("ai-basic-chain input has no user message")
	}

	if cfg.AttachImage {
		image, isBinary, err := payloadImage(msg)
		if err != nil {
			return nil, err
		}
		if !isBinary {
			return nil, fmt.Errorf("attach image is on but the payload is not binary")
		}
		messages[index].Images = append(messages[index].Images, image)
	}
	for i, value := range cfg.Images {
		image, err := imageFromValue(value)
		if err != nil {
			return nil, fmt.Errorf("images wire %d: %w", i+1, err)
		}
		messages[index].Images = append(messages[index].Images, image)
	}

	req := ai.ChatRequest{
		System: system, Messages: messages,
		ReadTranscript: ec.Wired("transcript"),
		JSONOutput:     cfg.ResponseFormat == "json", JSONSchema: cfg.ResponseSchema,
		SessionID: continuation.SessionID, CacheKey: continuation.CacheKey,
	}
	applyModel(&req, cfg.Model)
	responseStream := newResponseStream(ec, msg, cfg.Model.Model)
	resp, err := chatWithStream(ctx, conn, req, cfg.Stream, responseStream.emit)
	if err != nil {
		return nil, err
	}
	if resp.SessionID != "" {
		continuation.SessionID = resp.SessionID
	}
	if len(resp.ToolCalls) > 0 {
		return nil, fmt.Errorf("model returned tool calls to an AI Basic Chain node; use AI Agent for Karakuri tools")
	}

	var payload any = resp.Text
	final := resp.Text
	reasoningSummary := resp.ReasoningSummary
	finalOpaqueState := resp.OpaqueState
	usage := resp.Usage
	providerData := providerResponses(resp)
	providerHistory := providerTranscript(resp)
	if cfg.ResponseFormat == "json" {
		var repairTurns []ai.Message
		var repairedSessionID string
		payload, final, reasoningSummary, finalOpaqueState, repairedSessionID, repairTurns, usage, providerData, providerHistory, err = repairStructured(ctx, conn, req, resp)
		if repairedSessionID != "" {
			continuation.SessionID = repairedSessionID
		}
		messages = append(messages, repairTurns...)
		if err != nil {
			return nil, fmt.Errorf("model reply could not be repaired: %w", err)
		}
	}

	meta := nodeutil.CloneMeta(msg.Meta)
	meta["model"] = cfg.Model.Model
	meta["usage"] = jsonShaped(usage)
	outputs := engine.One("out", &engine.Msg{Payload: payload, Topic: msg.Topic, Meta: meta, Group: msg.Group})
	if ec.Wired("transcript") {
		response := transcriptPayload(
			messages, final, reasoningSummary, finalOpaqueState,
			providerData, providerHistory, continuation, nil,
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
