package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// limitTypeRule derives what a limit port carries, the same way sort's does.
// Property mode reads and rewrites an array on the message, so the path check
// is scoped to that case; sequence mode never touches the property field and
// falls through to the default, which is what lets TypeFrom carry the upstream
// type through.
func limitTypeRule() *flow.PortTypeRule {
	return &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{{
			Match:       map[string][]string{"mode": {"", "property"}},
			Type:        flow.PortObject,
			PayloadPath: []string{"property"},
		}},
		Default: flow.PortAny,
	}
}

// LimitNode keeps the first or last few of something and drops the rest. In
// property mode it truncates an array on the message, which needs nothing from
// the engine. In sequence mode it collects a whole group the way sort does and
// fans the survivors back out under a fresh group frame, so a join further down
// recombines exactly the ones that were kept.
//
// The fresh frame is what makes the drop safe: the group the survivors carry
// says how many there now are, so a downstream fan-in waits for that many
// rather than for the count the split announced and never completing.
type LimitNode struct{}

func (LimitNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "limit",
		Label:       "Limit",
		Folder:      flow.FolderSequence,
		Description: "Keeps the first or last few elements of an array or messages of a sequence",
		Icon:        "las la-cut",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny, TypeRule: limitTypeRule()},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in", TypeRule: limitTypeRule()},
		},
		ConfigSpec: append([]engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Limit",
				Type:    flow.FieldSelect,
				Default: "property",
				Options: []engine.Option{
					{Value: "property", Label: "The elements of an array"},
					{Value: "sequence", Label: "A sequence of messages"},
				},
				Help: "Sequence mode holds the messages of a split group until they have all " +
					"arrived, then releases the ones it keeps.",
			},
			{
				ID:          "property",
				Label:       "Array",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.entries",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "property"},
				Help:        "The array to truncate. Dot path into the message; a bare path is read from the payload.",
			},
			{
				ID:      "keep",
				Label:   "Keep",
				Type:    flow.FieldSelect,
				Default: "first",
				Options: []engine.Option{
					{Value: "first", Label: "The first"},
					{Value: "last", Label: "The last"},
				},
			},
			{
				ID:          "limit",
				Label:       "How many",
				Type:        flow.FieldString,
				Required:    true,
				Default:     "10",
				Placeholder: "10",
				Help:        "Everything beyond this many is dropped.",
			},
			{
				ID:          "count",
				Label:       "Message count",
				Type:        flow.FieldString,
				Placeholder: "10",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sequence"},
				Help: "How many messages make one sequence. Leave empty for messages from a split, " +
					"which already say how many parts there are.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout",
				Type:        flow.FieldDuration,
				Placeholder: "30s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sequence"},
				Help: "Release whatever has arrived this long after the first message. Empty waits " +
					"indefinitely for the count to be reached.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// limitConfig is the decoded limit node configuration.
type limitConfig struct {
	Mode     string `json:"mode"`
	Property string `json:"property"`
	Keep     string `json:"keep"`
	Limit    string `json:"limit"`
	Count    string `json:"count"`
	Timeout  string `json:"timeout"`
}

// keepLast reports whether the tail is kept rather than the head.
func (c limitConfig) keepLast() (bool, error) {
	switch c.Keep {
	case "", "first":
		return false, nil
	case "last":
		return true, nil
	default:
		return false, fmt.Errorf("unknown keep %q", c.Keep)
	}
}

// max reads how many to keep, which has to be a real number: an empty or zero
// limit would silently drop everything.
func (c limitConfig) max() (int, error) {
	n, err := parseGroupCount(c.Limit)
	if err != nil {
		return 0, fmt.Errorf("how many: %w", err)
	}
	if n == 0 {
		return 0, fmt.Errorf("limit node needs a number to keep")
	}
	return n, nil
}

// Execute truncates either an array on the message or the message sequence.
func (LimitNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg limitConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	switch cfg.Mode {
	case "", "property":
		return limitProperty(cfg, msg)
	case "sequence":
		return limitSequence(ctx, ec, cfg, msg)
	default:
		return nil, fmt.Errorf("unknown limit mode %q", cfg.Mode)
	}
}

// limitProperty truncates an array already on the message and writes it back.
func limitProperty(cfg limitConfig, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	last, err := cfg.keepLast()
	if err != nil {
		return nil, err
	}
	max, err := cfg.max()
	if err != nil {
		return nil, err
	}

	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := resolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("property %q is %T, not an array", property, value)
	}

	kept := make([]any, len(items))
	copy(kept, items)
	kept = limitSlice(kept, max, last)

	out := &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	if err := setProperty(out, property, kept); err != nil {
		return nil, fmt.Errorf("cannot write the truncated array to %q: %w", property, err)
	}
	return engine.One("out", out), nil
}

// limitSequence collects the message's group and, once it is done, releases
// only the messages it keeps as a fresh fan-out.
func limitSequence(ctx context.Context, ec engine.ExecContext, cfg limitConfig, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	last, err := cfg.keepLast()
	if err != nil {
		return nil, err
	}
	max, err := cfg.max()
	if err != nil {
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

	kept := limitSlice(res.parts, max, last)

	// Re-emit under a new group frame whose count is what survived, so a join
	// downstream completes on the messages that are actually coming.
	groupID := uuid.New().String()
	out := make([]*engine.Msg, 0, len(kept))
	for i, p := range kept {
		out = append(out, &engine.Msg{
			Payload: p.payload,
			Topic:   p.topic,
			Meta:    nodeutil.CloneMeta(p.meta),
			Group:   append(append([]engine.GroupFrame{}, res.rest...), engine.GroupFrame{ID: groupID, Index: i, Count: len(kept)}),
		})
	}
	return map[string][]*engine.Msg{"out": out}, nil
}

// limitSlice keeps at most max entries from either end, leaving the survivors
// in their original order.
func limitSlice[T any](items []T, max int, last bool) []T {
	if len(items) <= max {
		return items
	}
	if last {
		return items[len(items)-max:]
	}
	return items[:max]
}

// Resume waits out a limit sequence stored before a restart or a redeploy.
func (n LimitNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	var cfg limitConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Mode != "sequence" {
		// The node no longer collects sequences, so the group has no owner.
		waits, _ := engine.WaitsFor(ec)
		releaseWait(waits, key, w.Token)
		return nil, nil
	}
	return resumeFanIn(ctx, ec, key, w, n.Execute)
}
