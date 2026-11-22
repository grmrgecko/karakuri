package core

import (
	"context"
	"fmt"
	"time"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// ConditionWaitNode observes fresh values until its condition holds or times out.
type ConditionWaitNode struct{}

func (ConditionWaitNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type: "condition-wait", Label: "Condition wait", Folder: flow.FolderTime, Icon: "las la-hourglass-half",
		Description:     "Waits for a fresh value to satisfy a condition, optionally for a stable duration",
		InvocationState: flow.InvocationStateStateful,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
			{ID: "check", Label: "Check", Type: flow.PortAny, Kind: flow.PortKindSignal, Optional: true},
		},
		Outputs: []engine.PortSpec{{ID: "matched", Label: "Matched", Type: flow.PortAny, TypeFrom: "in"}, {ID: "timeout", Label: "Timeout", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()},
		ConfigSpec: append([]engine.FieldSpec{
			{ID: "value", Label: "Observed value", Type: flow.FieldJSON, Port: flow.PortAny, Help: "Optional pure source read afresh at every observation. Without a wire, the condition reads the incoming payload and live variables."},
			{ID: "condition", Label: "Condition", Type: flow.FieldExpression, Required: true, Default: "payload == true", Help: "Boolean expression against the observed value as payload. Flow and global variables are read afresh."},
			{ID: "stable_for", Label: "Held for", Type: flow.FieldDuration, Placeholder: "5m", Help: "Require matching observations for this duration. Empty matches immediately. Changes between observations cannot be detected."},
			{ID: "timeout", Label: "Timeout", Type: flow.FieldDuration, Required: true, Default: "30s"},
			{ID: "poll_interval", Label: "Poll interval", Type: flow.FieldDuration, Default: "1s", Help: "Polling continues when Check is connected. A message on Check requests a fresh observation for every active wait on this node."},
		}, engine.MissedDeadlineFields()...),
	}
}

func (ConditionWaitNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	store := ec.NodeStore()
	if inputs["check"] != nil {
		store.Update("condition_waiters", func(current any, _ bool) any {
			waiters, _ := current.(map[chan struct{}]struct{})
			for wake := range waiters {
				select {
				case wake <- struct{}{}:
				default:
				}
			}
			return current
		})
		if inputs["in"] == nil {
			return nil, ctx.Err()
		}
	}
	var cfg conditionWaitConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Condition == "" {
		return nil, fmt.Errorf("condition wait needs a boolean condition")
	}
	timeout, err := parseDurationDefault(cfg.Timeout, 30*time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout: %w", err)
	}
	interval, err := parseDurationDefault(cfg.PollInterval, time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid poll interval: %w", err)
	}
	stable, err := parseDurationDefault(cfg.StableFor, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid stable duration: %w", err)
	}
	if stable > timeout {
		return nil, fmt.Errorf("held for %s is longer than the %s timeout", stable, timeout)
	}
	msg := inbound(ec, inputs)
	started := ec.Clock().Now()
	waits, key, mine, err := armExecWait(ec, started.Add(timeout), conditionWaitState{Msg: storeMsg(msg), Started: started})
	if err != nil {
		return nil, err
	}
	return conditionObserve(ctx, ec, cfg, interval, stable, waits, key, mine, msg, started)
}

// conditionWaitConfig is the decoded condition wait configuration.
type conditionWaitConfig struct {
	Value        any    `json:"value"`
	Condition    string `json:"condition"`
	StableFor    string `json:"stable_for"`
	Timeout      string `json:"timeout"`
	PollInterval string `json:"poll_interval"`
}

// conditionWaitState is a stored condition wait: the message it holds and
// when the wait began, for the reported wait duration.
type conditionWaitState struct {
	Msg     *storedMsg `json:"msg"`
	Started time.Time  `json:"started"`
}

// Resume continues a condition wait stored before a restart or a redeploy. It
// keeps the original deadline and observes afresh; a hold restarts, because
// stability while the server was down cannot be observed.
func (ConditionWaitNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	var cfg conditionWaitConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	interval, err := parseDurationDefault(cfg.PollInterval, time.Second)
	if err != nil {
		return nil, fmt.Errorf("invalid poll interval: %w", err)
	}
	stable, err := parseDurationDefault(cfg.StableFor, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid stable duration: %w", err)
	}
	var state conditionWaitState
	if err := decodeWaitState(w, &state); err != nil {
		return nil, err
	}
	waits, _ := engine.WaitsFor(ec)
	return conditionObserve(ctx, ec, cfg, interval, stable, waits, key, w, state.Msg.msg(), state.Started)
}

// conditionObserve observes until the condition holds or the wait's deadline
// passes, then claims the wait and emits. A deadline already past emits
// timeout without observing.
func conditionObserve(ctx context.Context, ec engine.ExecContext, cfg conditionWaitConfig, interval, stable time.Duration, waits engine.WaitStore, key string, mine engine.Wait, msg *engine.Msg, started time.Time) (map[string][]*engine.Msg, error) {
	if cfg.Condition == "" {
		return nil, fmt.Errorf("condition wait needs a boolean condition")
	}
	store := ec.NodeStore()
	wake := make(chan struct{}, 1)
	store.Update("condition_waiters", func(current any, _ bool) any {
		waiters, _ := current.(map[chan struct{}]struct{})
		if waiters == nil {
			waiters = make(map[chan struct{}]struct{})
		}
		waiters[wake] = struct{}{}
		return waiters
	})
	defer store.Update("condition_waiters", func(current any, _ bool) any {
		waiters, _ := current.(map[chan struct{}]struct{})
		delete(waiters, wake)
		if len(waiters) == 0 {
			return nil
		}
		return waiters
	})
	var last any
	matched := false
	remaining := mine.Deadline.Sub(ec.Clock().Now())
	if remaining > 0 && stable > remaining {
		// A resumed wait restarts its hold, and a hold longer than the time
		// left cannot finish before the original deadline, so the wait can
		// only time out there.
		if err := ec.Park(ctx, remaining); err != nil {
			if !engine.Suspended(ctx) {
				releaseWait(waits, key, mine.Token)
			}
			return nil, err
		}
	} else if remaining > 0 {
		var err error
		last, matched, err = nodeutil.WaitConditionWithSignal(ctx, ec, remaining, interval, stable, wake, func(ctx context.Context) (any, bool, error) {
			value := msg.Payload
			if cfg.Value != nil {
				value = cfg.Value
			}
			if refresher, ok := ec.(engine.ParameterRefresher); ok {
				refreshed, bound, err := refresher.RefreshParameter(ctx, "value")
				if err != nil {
					return nil, false, err
				}
				if bound {
					value = refreshed
				}
			}
			observation := &engine.Msg{Payload: value, Topic: msg.Topic, Meta: nodeutil.CloneMeta(msg.Meta)}
			result, err := ec.Eval(cfg.Condition, observation)
			if err != nil {
				return nil, false, fmt.Errorf("evaluating condition: %w", err)
			}
			match, ok := result.(bool)
			if !ok {
				return nil, false, fmt.Errorf("condition returned %T; expected boolean", result)
			}
			return value, match, nil
		})
		if err != nil {
			if !engine.Suspended(ctx) {
				releaseWait(waits, key, mine.Token)
			}
			return nil, err
		}
	}
	if _, ok, err := waits.Claim(key, mine.Token); err != nil || !ok {
		if err != nil {
			return nil, fmt.Errorf("condition wait could not be claimed: %w", err)
		}
		return nil, nil
	}
	out := passThrough(msg)
	out.Meta["condition_value"] = last
	out.Meta["condition_wait_ms"] = ec.Clock().Now().Sub(started).Milliseconds()
	if matched {
		return engine.One("matched", out), nil
	}
	return engine.One("timeout", out), nil
}
