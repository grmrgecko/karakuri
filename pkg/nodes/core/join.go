package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// JoinNode combines several messages into one. It is the other half of split:
// the run that delivers a group's last part emits the combined message, and the
// group's frame is removed so the result continues as a single message.
//
// It also joins plain streams — messages from a trigger firing repeatedly, each
// in its own run — using a configured count or timeout to decide when a batch
// is done.
type JoinNode struct{}

func (JoinNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "join",
		Label:       "Join",
		Folder:      flow.FolderSequence,
		Description: "Combines several messages into one",
		Icon:        "las la-compress-arrows-alt",
		// Merging needs every part to be an object; the other modes take any
		// payload as-is.
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny,
				TypeRule: &flow.PortTypeRule{
					Cases: []flow.PortTypeCase{{Match: map[string][]string{"mode": {"merge"}}, Type: flow.PortObject}},
				}},
		},
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortObject,
			// Every mode but string builds a container out of the parts.
			TypeRule: &flow.PortTypeRule{
				Cases:   []flow.PortTypeCase{{Match: map[string][]string{"mode": {"string"}}, Type: flow.PortString}},
				Default: flow.PortObject,
			},
		}},
		ConfigSpec: append([]engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Combine into",
				Type:    flow.FieldSelect,
				Default: "array",
				Options: []engine.Option{
					{Value: "array", Label: "An array of payloads"},
					{Value: "object", Label: "An object keyed by a property"},
					{Value: "string", Label: "One string"},
					{Value: "merge", Label: "One merged object"},
				},
			},
			{
				ID:      "key_property",
				Label:   "Key",
				Type:    flow.FieldString,
				Default: "topic",
				ShowIf:  &flow.ShowIf{Field: "mode", Equals: "object"},
				Help:    "Where each message's key comes from: topic, or a meta.* or payload.* path.",
			},
			{
				ID:          "separator",
				Label:       "Separator",
				Type:        flow.FieldString,
				Default:     "\\n",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "string"},
				Placeholder: "\\n",
			},
			{
				ID:          "count",
				Label:       "Message count",
				Type:        flow.FieldString,
				Placeholder: "10",
				Help: "How many messages make one result. Leave empty for messages from a split, " +
					"which already say how many parts there are.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout",
				Type:        flow.FieldDuration,
				Placeholder: "30s",
				Help: "Emit whatever has arrived this long after the first message. Empty waits " +
					"indefinitely for the count to be reached, which for a split means the run " +
					"ends without a result if any part failed.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// joinConfig is the decoded join node configuration.
type joinConfig struct {
	Mode        string `json:"mode"`
	KeyProperty string `json:"key_property"`
	Separator   string `json:"separator"`
	Count       string `json:"count"`
	Timeout     string `json:"timeout"`
}

// Execute collects the message and, when its group is done, emits the combined
// result.
func (JoinNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg joinConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	count, err := parseGroupCount(cfg.Count)
	if err != nil {
		return nil, err
	}
	timeout, err := parseDurationDefault(cfg.Timeout, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout %q: %w", cfg.Timeout, err)
	}
	msg := inbound(ec, inputs)
	if err := requireCompletion(msg, count, timeout); err != nil {
		return nil, err
	}

	res, err := collect(ctx, ec, msg, count, timeout)
	if err != nil {
		return nil, err
	}
	if res == nil {
		// Still waiting for more messages, or another run will emit.
		return nil, nil
	}

	payload, err := joinPayload(cfg, res.parts)
	if err != nil {
		return nil, err
	}
	return engine.One("out", &engine.Msg{
		Payload: payload,
		Topic:   msg.Topic,
		Meta:    combinedMeta(res),
		Group:   res.rest,
	}), nil
}

// joinPayload builds the combined payload for the configured mode.
func joinPayload(cfg joinConfig, parts []groupedPart) (any, error) {
	switch cfg.Mode {
	case "", "array":
		return payloads(parts), nil

	case "object":
		keyProperty := strings.TrimSpace(cfg.KeyProperty)
		if keyProperty == "" {
			keyProperty = "topic"
		}
		out := make(map[string]any, len(parts))
		for i, p := range parts {
			key, err := partKey(p, keyProperty, i)
			if err != nil {
				return nil, err
			}
			out[key] = p.payload
		}
		return out, nil

	case "string":
		sep := unescapeSeparator(cfg.Separator)
		if sep == "" {
			sep = "\n"
		}
		pieces := make([]string, 0, len(parts))
		for _, p := range parts {
			pieces = append(pieces, stringify(p.payload))
		}
		return strings.Join(pieces, sep), nil

	case "merge":
		// Later messages win on conflicting keys, which matches the order the
		// parts were produced in.
		out := map[string]any{}
		for _, p := range parts {
			obj, ok := p.payload.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("merge needs object payloads, got %T", p.payload)
			}
			for k, v := range obj {
				out[k] = v
			}
		}
		return out, nil

	default:
		return nil, fmt.Errorf("unknown join mode %q", cfg.Mode)
	}
}

// partKey resolves the key one message contributes in object mode, falling back
// to its position when the property is missing so no message is silently lost.
func partKey(p groupedPart, property string, position int) (string, error) {
	msg := &engine.Msg{Payload: p.payload, Topic: p.topic, Meta: p.meta}
	value, found := resolveProperty(msg, property)
	if !found {
		return fmt.Sprintf("%d", position), nil
	}
	key := stringify(value)
	if key == "" {
		return fmt.Sprintf("%d", position), nil
	}
	return key, nil
}

// Resume waits out a join group stored before a restart or a redeploy.
func (n JoinNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	return resumeFanIn(ctx, ec, key, w, n.Execute)
}
