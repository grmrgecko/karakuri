// Command example is a karakuri external plugin demonstrating the SDK. It
// contributes an interval trigger and a counting transform, exercising the
// paths a real plugin lives on: trigger emit streaming, the host's injectable
// clock, node execution with inputs and outputs, and flow-scoped node store
// state — all over the process boundary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

func main() { sdk.Serve(examplePlugin{}) }

// examplePlugin implements the same plugin.Plugin interface the built-ins do;
// sdk.Serve runs it behind the wire protocol instead of in-process.
type examplePlugin struct{}

func (examplePlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "example",
		Version:     "0.1.0",
		Description: "Example external plugin: an interval trigger and a counting transform.",
		Author:      "Karakuri",
	}
}

func (examplePlugin) Register(r plugin.Registrar) error {
	r.RegisterNode(IntervalNode{})
	r.RegisterNode(CounterNode{})
	r.RegisterNode(SkipNode{})
	r.RegisterConnectionKind(integrations.ConnectionKind{
		Kind: "example-probe", Label: "Example probe",
		ConfigSpec: []flow.FieldSpec{{ID: "name", Label: "Name", Type: flow.FieldString}},
		// Probe reports what it found, which the host shows beside a
		// connection test's result.
		Probe: func(_ context.Context, _ uuid.UUID, cfg []byte) (string, error) {
			var c struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(cfg, &c); err != nil {
				return "", err
			}
			if c.Name == "" {
				return "nothing to probe", errors.New("name is required")
			}
			return "found " + c.Name, nil
		},
	})
	return nil
}

// SkipNode is a trigger that records one skipped event and then waits to be
// disarmed, exercising the host's skipped-event callback.
type SkipNode struct{}

func (SkipNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "example-skip", Label: "Skip", Category: flow.CategoryTrigger, Folder: "Example",
		Description: "Records one skipped event when armed",
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
	}
}

// Execute passes the run's seed message through, like every trigger.
func (SkipNode) Execute(_ context.Context, _ engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", inputs[engine.TriggerInput]), nil
}

// Start records one skipped event, then waits to be disarmed.
func (SkipNode) Start(ctx context.Context, ec engine.ExecContext, _ func(*engine.Msg)) error {
	if rec, ok := ec.(engine.TriggerRecorder); ok {
		rec.Skipped(&engine.Msg{Payload: map[string]any{"event": "standup"}}, "missed; dropped")
	}
	<-ctx.Done()
	return nil
}

// IntervalNode starts a run on a fixed period. It waits on the host's clock,
// so a fake clock in tests drives it like any built-in trigger.
type IntervalNode struct{}

func (IntervalNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "example-interval",
		Label:             "Interval",
		Category:          flow.CategoryTrigger,
		Folder:            "Example",
		Description:       "Starts a run every fixed number of seconds",
		Icon:              "las la-stopwatch",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "interval_s", Label: "Interval (seconds)", Type: flow.FieldNumber,
				Default: 60, Help: "How often a run starts.",
			},
		},
	}
}

type intervalConfig struct {
	IntervalS float64 `json:"interval_s"`
}

// Execute passes the run's seed message through, like every trigger.
func (IntervalNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{Payload: map[string]any{"tick": 0}}
	}
	return engine.One("out", msg), nil
}

// Start emits a tick per interval until disarmed.
func (IntervalNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg intervalConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if cfg.IntervalS <= 0 {
		cfg.IntervalS = 60
	}
	interval := time.Duration(cfg.IntervalS * float64(time.Second))
	tick := 0
	for {
		select {
		case <-ec.Clock().After(interval):
			tick++
			emit(&engine.Msg{
				Payload: map[string]any{"tick": tick},
				Topic:   "interval",
			})
		case <-ctx.Done():
			return nil
		}
	}
}

// CounterNode counts the messages that pass through it, keeping the count in
// the flow-scoped node store so it survives across runs.
type CounterNode struct{}

func (CounterNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "example-counter",
		Label:       "Counter",
		Folder:      "Example",
		Description: "Counts messages through it, keeping the count across runs",
		Icon:        "las la-sort-numeric-up",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
	}
}

func (CounterNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	in := inputs["in"]
	if in == nil {
		in = &engine.Msg{}
	}
	// The state is a map changed in place, the way real plugins keep sets
	// of records, so the host-side test can see the update commit by
	// compare-and-swap rather than by the fallback write.
	state := ec.NodeStore().Update("count", func(cur any, ok bool) any {
		m, _ := cur.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		n, _ := m["n"].(float64)
		m["n"] = n + 1
		return m
	})
	m, _ := state.(map[string]any)
	n, ok := m["n"].(float64)
	if !ok {
		return nil, fmt.Errorf("counter state is not a number")
	}
	return engine.One("out", &engine.Msg{
		// The wired flags echo what the request's wired_outputs carried, so
		// the host-side test can see the per-port answer that crossed the wire.
		Payload: map[string]any{
			"count": n, "value": in.Payload,
			"wired_out": ec.Wired("out"), "wired_aux": ec.Wired("aux"),
		},
		Topic: in.Topic,
		Meta:  in.Meta,
	}), nil
}
