package core

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// BatchNode regroups a stream of messages into batches. Messages arrive one run
// at a time from a repeating trigger; the batch node holds them until the batch
// is full or its window closes, then releases them together as a fan-out with a
// fresh group frame. Wiring a join after it turns each batch into one message;
// leaving it unwired runs the downstream nodes once per message but in bursts.
type BatchNode struct{}

func (BatchNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "batch",
		Label:       "Batch",
		Folder:      flow.FolderSequence,
		Description: "Groups a stream of messages into batches",
		Icon:        "las la-layer-group",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
		},
		ConfigSpec: append([]engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Release a batch",
				Type:    flow.FieldSelect,
				Default: "count",
				Options: []engine.Option{
					{Value: "count", Label: "Once it holds this many messages"},
					{Value: "interval", Label: "Every interval"},
				},
			},
			{
				ID:          "count",
				Label:       "Batch size",
				Type:        flow.FieldString,
				Default:     "10",
				Placeholder: "10",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "count"},
			},
			{
				ID:          "interval",
				Label:       "Interval",
				Type:        flow.FieldDuration,
				Default:     "1m",
				Placeholder: "1m",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "interval"},
				Help: "Measured from the first message of a batch. Its run stays open for " +
					"the window, which does not count toward the run timeout.",
			},
			{
				ID:          "max_count",
				Label:       "Maximum size",
				Type:        flow.FieldString,
				Placeholder: "100",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "interval"},
				Help:        "Release early once the batch reaches this many messages. Empty means no cap.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// batchConfig is the decoded batch node configuration.
type batchConfig struct {
	Mode     string `json:"mode"`
	Count    string `json:"count"`
	Interval string `json:"interval"`
	MaxCount string `json:"max_count"`
}

// Execute holds the message until its batch is ready, then releases the whole
// batch as one message per member.
func (BatchNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg batchConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	// Resolve what closes a batch: a size, a window, or both.
	var count int
	var window time.Duration
	switch cfg.Mode {
	case "", "count":
		n, err := parseGroupCount(cfg.Count)
		if err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, fmt.Errorf("batch node needs a batch size")
		}
		count = n

	case "interval":
		d, err := parseDurationDefault(cfg.Interval, 0)
		if err != nil {
			return nil, fmt.Errorf("invalid interval %q: %w", cfg.Interval, err)
		}
		if d <= 0 {
			return nil, fmt.Errorf("batch node needs an interval")
		}
		window = d
		// A maximum size lets a busy interval release early instead of growing
		// without bound.
		max, err := parseGroupCount(cfg.MaxCount)
		if err != nil {
			return nil, err
		}
		count = max

	default:
		return nil, fmt.Errorf("unknown batch mode %q", cfg.Mode)
	}

	res, err := collect(ctx, ec, msg, count, window)
	if err != nil {
		return nil, err
	}
	if res == nil {
		// The batch is still filling, or another run is waiting out the window.
		return nil, nil
	}

	// Release the batch as its own fan-out group so a join downstream can
	// recombine exactly these messages.
	groupID := uuid.New().String()
	out := make([]*engine.Msg, 0, len(res.parts))
	for i, p := range res.parts {
		member := &engine.Msg{
			Payload: p.payload,
			Topic:   p.topic,
			Meta:    nodeutil.CloneMeta(p.meta),
			Group:   append(append([]engine.GroupFrame{}, res.rest...), engine.GroupFrame{ID: groupID, Index: i, Count: len(res.parts)}),
		}
		out = append(out, member)
	}
	return map[string][]*engine.Msg{"out": out}, nil
}

// Resume waits out a batch group stored before a restart or a redeploy.
func (n BatchNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	return resumeFanIn(ctx, ec, key, w, n.Execute)
}
