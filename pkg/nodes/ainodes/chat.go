package ainodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
)

// ChatTurn adapts a configured AI node to a continuing, streamed test conversation.
// Response is the preceding completed envelope; it never becomes authored text.
type ChatTurn struct {
	engine.NodeType
	Response map[string]any
}

// Execute renders authored input against In and replays completed turns as data.
func (n ChatTurn) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	var cfg agentConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.AttachImage {
		return nil, fmt.Errorf("chat supplies text; turn off attach binary payload as image to test this node")
	}
	system, err := ec.Interpolate(cfg.System, msg)
	if err != nil {
		return nil, fmt.Errorf("rendering system prompt: %w", err)
	}
	var turn []transcriptEntry
	switch cfg.InputMode {
	case agentInputTranscript:
		turn, err = configuredTranscript(ec, msg, cfg.Transcript)
	case agentInputDirect:
		text, ok := msg.Payload.(string)
		if !ok {
			return nil, fmt.Errorf("chat requires a string input")
		}
		turn = []transcriptEntry{{Role: "user", Text: text}}
	default:
		var text string
		text, err = ec.Interpolate(cfg.Prompt, msg)
		if err == nil && strings.TrimSpace(text) == "" {
			err = fmt.Errorf("node has an empty prompt; configure a prompt or use Input mode")
		}
		turn = []transcriptEntry{{Role: "user", Text: text}}
	}
	if err != nil {
		return nil, err
	}
	_, extras, err := transcriptMessages(turn)
	if err != nil {
		return nil, err
	}
	for _, extra := range extras {
		if system != "" {
			system += "\n\n"
		}
		system += extra
	}
	var history []transcriptEntry
	if n.Response != nil {
		history, _, err = payloadConversation(n.Response)
		if err != nil {
			return nil, err
		}
	}
	envelope := make(map[string]any, len(n.Response)+1)
	for key, value := range n.Response {
		envelope[key] = value
	}
	envelope["transcript"] = append(history, turn...)
	config := map[string]any{}
	if err := ec.Config(&config); err != nil {
		return nil, err
	}
	config["input_mode"], config["stream"], config["system"] = agentInputDirect, true, system
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	input := *msg
	input.Payload = envelope
	return n.NodeType.Execute(ctx, &chatContext{ExecContext: ec, config: raw, original: msg, system: system},
		map[string]*engine.Msg{"in": &input})
}

type chatContext struct {
	engine.ExecContext
	config   json.RawMessage
	original *engine.Msg
	system   string
}

// Config retains pulled handles while selecting replay mode for this turn.
func (c *chatContext) Config(dst any) error { return json.Unmarshal(c.config, dst) }

// Interpolate keeps In expressions tied to the typed message and system text rendered once.
func (c *chatContext) Interpolate(text string, _ *engine.Msg) (string, error) {
	if text == c.system {
		return text, nil
	}
	return c.ExecContext.Interpolate(text, c.original)
}
