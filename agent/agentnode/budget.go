package agentnode

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// budgetGrace is added on top of a node's resolved budget so a node that
// handles its own deadline answers first: a script reports its exit and a
// dialog reports timed_out rather than both being cut off by the wrapper at
// the same instant.
const budgetGrace = 10 * time.Second

// budgetNode bounds Execute with the same budget resolution the exec nodes
// apply themselves. Most agent nodes end up blocked in a session tool or a
// kernel call when the desktop cannot answer — osascript waiting on a consent
// dialog, an ACPI ioctl that never returns — and without a deadline that hang
// holds the run forever. Nodes with their own timeout config keep it: the
// wrapper reads the same field and fires a grace period later.
type budgetNode struct {
	nodeapi.NodeType
	opts Options
}

// budgetTrigger keeps a trigger's Start reachable through the wrapper. Start
// is not bounded: the stream context is the trigger's lifetime, and the host
// cancels it to disarm.
type budgetTrigger struct {
	budgetNode
	trigger nodeapi.TriggerType
}

// WithBudget wraps a node type so one execution cannot outlive its budget.
func WithBudget(nt nodeapi.NodeType, opts Options) nodeapi.NodeType {
	b := budgetNode{NodeType: nt, opts: opts}
	if tr, ok := nt.(nodeapi.TriggerType); ok {
		return budgetTrigger{budgetNode: b, trigger: tr}
	}
	return b
}

// result carries a completed Execute back from its goroutine.
type result struct {
	outputs map[string][]*nodeapi.Msg
	err     error
}

func (n budgetNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	// Duration strings and numeric seconds share the same wrapper deadline.
	// Confirmation adds its own budget after the service control action.
	var cfg struct {
		Timeout             json.RawMessage `json:"timeout"`
		Confirm             bool            `json:"confirm"`
		ConfirmationTimeout string          `json:"confirmation_timeout"`
		TerminationGrace    string          `json:"termination_grace"`
	}
	_ = ec.Config(&cfg)

	// A node that omits its timeout waits for the default its timeout field
	// declares, so the wrapper has to budget for that wait as well.
	meta := n.NodeType.Meta()
	budget := ResolveTimeout(0, n.opts.ExecTimeout)
	if configured, ok := timeoutSetting(cfg.Timeout); ok {
		budget = configured
	} else if declared, ok := declaredTimeout(meta); ok {
		budget = declared
	}
	if cfg.Confirm {
		confirmation := 30 * time.Second
		if cfg.ConfirmationTimeout != "" {
			if parsed, err := time.ParseDuration(cfg.ConfirmationTimeout); err == nil && parsed > 0 && parsed <= 24*time.Hour {
				confirmation = parsed
			}
		}
		if budget <= time.Duration(math.MaxInt64-budgetGrace)-confirmation {
			budget += confirmation
		}
	}
	switch meta.Type {
	case "command", "script":
		if grace, err := TerminationGrace(cfg.TerminationGrace); err == nil && budget <= time.Duration(math.MaxInt64-budgetGrace)-grace {
			budget += grace
		}
	}
	budget += budgetGrace
	deadline, cancel := context.WithTimeout(ctx, budget)
	defer cancel()

	// Run in a goroutine and race it against the deadline. A context passed
	// through is not enough on its own: battery.GetAll and other readings block
	// in a syscall that takes no context, so a node stuck there would ignore a
	// cancelled ctx and hold the run forever. On timeout the goroutine is left
	// to finish on its own — a blocked syscall cannot be interrupted — but the
	// run gets its answer and moves on.
	done := make(chan result, 1)
	go func() {
		outputs, err := n.NodeType.Execute(deadline, ec, inputs)
		done <- result{outputs, err}
	}()

	select {
	case r := <-done:
		return r.outputs, r.err
	case <-deadline.Done():
		// The parent cancelling the run is not a budget overrun: report it as
		// the cancellation it is rather than a timeout the node never hit.
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("node gave no answer within %s; the host may be blocked on hardware or waiting on something only a person at its screen can settle", budget)
	}
}

// timeoutSetting reads a timeout written as numeric seconds or as a duration
// string, the two forms node configurations use. It reports false for a
// missing, non-positive, or unrepresentable value.
func timeoutSetting(raw json.RawMessage) (time.Duration, bool) {
	limit := time.Duration(math.MaxInt64 - budgetGrace)
	var seconds float64
	if json.Unmarshal(raw, &seconds) == nil {
		if seconds > 0 && seconds <= limit.Seconds() {
			return time.Duration(seconds * float64(time.Second)), true
		}
		return 0, false
	}
	var text string
	if json.Unmarshal(raw, &text) != nil {
		return 0, false
	}
	duration, err := time.ParseDuration(text)
	if err != nil || duration <= 0 || duration > limit {
		return 0, false
	}
	return duration, true
}

// declaredTimeout returns the default a node declares on its timeout field.
func declaredTimeout(meta nodeapi.NodeMeta) (time.Duration, bool) {
	for _, field := range meta.ConfigSpec {
		if field.ID != "timeout" || field.Default == nil {
			continue
		}
		raw, err := json.Marshal(field.Default)
		if err != nil {
			return 0, false
		}
		return timeoutSetting(raw)
	}
	return 0, false
}

func (t budgetTrigger) Start(ctx context.Context, ec nodeapi.ExecContext, emit func(*nodeapi.Msg)) error {
	return t.trigger.Start(ctx, ec, emit)
}
