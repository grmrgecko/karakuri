package core

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// TriggerNode sends one message immediately and a second one after a wait,
// unless something resets it first. This is the shape of most timed automation:
// motion turns a light on now and off once the room has been quiet for a while.
//
// The immediate message goes out through ExecContext.Emit so downstream nodes
// act on it before the wait starts; the delayed one is the node's return value.
// The pending wait lives in the node's flow-scoped store, so messages arriving
// in later runs can extend, restart, or cancel it.
type TriggerNode struct{}

func (TriggerNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:            "trigger",
		Label:           "Trigger",
		Folder:          flow.FolderLogic,
		Description:     "Sends a message now and another after a delay, unless reset",
		Icon:            "las la-stopwatch",
		InvocationState: flow.InvocationStateStateful,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
			{ID: "reset", Label: "Reset", Type: flow.PortAny, Optional: true},
		},
		// Each output carries the input onward only when its source setting says
		// "the incoming message"; every other source emits a configured JSON
		// value whose type is not knowable, so those cases assert "any" and
		// keep propagation from dressing the payload in the upstream's type.
		// Unset reads as the fixed payload, matching triggerPayloadMsg.
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in",
				TypeRule: &flow.PortTypeRule{
					Cases:   []flow.PortTypeCase{{Match: map[string][]string{"first_source": {"", "payload", "nothing"}}, Type: flow.PortAny}},
					Default: flow.PortAny,
				}},
			{ID: "expired", Label: "Expired", Type: flow.PortAny, TypeFrom: "in",
				TypeRule: &flow.PortTypeRule{
					Cases:   []flow.PortTypeCase{{Match: map[string][]string{"second_source": {"", "payload", "nothing"}}, Type: flow.PortAny}},
					Default: flow.PortAny,
				}},
		},
		ConfigSpec: append([]engine.FieldSpec{
			stateKeyField(),
			{
				ID:      "first_source",
				Label:   "Send now",
				Type:    flow.FieldSelect,
				Default: "original",
				Options: []engine.Option{
					{Value: "original", Label: "The incoming message"},
					{Value: "payload", Label: "A fixed payload"},
					{Value: "nothing", Label: "Nothing"},
				},
				Help: "Emitted on the Out port as soon as the message arrives.",
			},
			{
				ID:      "first_payload",
				Label:   "Payload to send now",
				Type:    flow.FieldJSON,
				ShowIf:  &flow.ShowIf{Field: "first_source", Equals: "payload"},
				Default: map[string]any{},
			},
			{
				ID:          "duration",
				Label:       "Then wait",
				Type:        flow.FieldDuration,
				Required:    true,
				Default:     "5m",
				Placeholder: "5m",
				Help:        "How long to wait before the Expired message. Waiting does not count toward the run timeout.",
			},
			{
				ID:      "second_source",
				Label:   "Then send",
				Type:    flow.FieldSelect,
				Default: "payload",
				Options: []engine.Option{
					{Value: "payload", Label: "A fixed payload"},
					{Value: "original", Label: "The incoming message"},
					{Value: "nothing", Label: "Nothing"},
				},
				Help: "Emitted on the Expired port once the wait completes.",
			},
			{
				ID:      "second_payload",
				Label:   "Payload to send after",
				Type:    flow.FieldJSON,
				ShowIf:  &flow.ShowIf{Field: "second_source", Equals: "payload"},
				Default: map[string]any{},
			},
			{
				ID:      "retrigger",
				Label:   "A message while waiting",
				Type:    flow.FieldSelect,
				Default: "extend",
				Options: []engine.Option{
					{Value: "extend", Label: "Extends the wait"},
					{Value: "ignore", Label: "Is ignored"},
					{Value: "restart", Label: "Starts over, sending now again"},
				},
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// triggerConfig is the decoded trigger node configuration.
type triggerConfig struct {
	Key           string          `json:"key"`
	FirstSource   string          `json:"first_source"`
	FirstPayload  json.RawMessage `json:"first_payload"`
	Duration      string          `json:"duration"`
	SecondSource  string          `json:"second_source"`
	SecondPayload json.RawMessage `json:"second_payload"`
	Retrigger     string          `json:"retrigger"`
}

// triggerArmStoreKey names the pending wait in the wait store.
const triggerArmStoreKey = "armed"

// Execute emits the immediate message, waits, then emits the delayed one.
//
// The pending wait lives in the node's wait store, shared by every run of
// the node and kept across restarts and redeploys. Its token names the run
// that owns the delayed message: a restart replaces the token, a reset
// deletes the wait, and an extension moves only the deadline, so the owner
// learns at its deadline whether it is still the one to emit.
func (TriggerNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg triggerConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	waits, _ := engine.WaitsFor(ec)
	msg := inbound(ec, inputs)
	key, err := messageStateKey(msg, cfg.Key)
	if err != nil {
		return nil, err
	}
	storeKey := keyedStoreKey(triggerArmStoreKey, key)

	// A reset cancels the pending wait. The waiting run keeps its park until
	// the deadline and then finds its claim refused, so nothing is emitted
	// even though the wait is not interrupted.
	if reset := inputs["reset"]; reset != nil && inputs["in"] == nil {
		waits.Update(storeKey, func(engine.Wait, bool) *engine.Wait { return nil })
		ec.Debug(nil, "trigger reset; the pending message was cancelled")
		return nil, nil
	}

	wait, err := parseRequiredDuration(cfg.Duration)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q: %w", cfg.Duration, err)
	}
	deadline := ec.Clock().Now().Add(wait)
	state, err := json.Marshal(storeMsg(msg))
	if err != nil {
		return nil, fmt.Errorf("trigger message cannot be stored: %w", err)
	}

	// Decide this message's role against the pending wait.
	var role string
	mine, durable := waits.Update(storeKey, func(cur engine.Wait, ok bool) *engine.Wait {
		switch {
		case !ok:
			role = "wait"
			return &engine.Wait{Deadline: deadline, State: state}
		case cfg.Retrigger == "ignore":
			role = "ignored"
			return &cur
		case cfg.Retrigger == "restart":
			// A new token supersedes the waiting run and takes over the wait.
			role = "wait"
			return &engine.Wait{Deadline: deadline, State: state}
		default:
			// Extend: push the deadline out and let the existing run keep it.
			role = "extended"
			cur.Deadline = deadline
			return &cur
		}
	})

	switch role {
	case "ignored":
		return nil, nil
	case "extended":
		// The run already waiting will emit; this message only moved the
		// deadline, so it emits nothing itself.
		return nil, nil
	}
	noteNotDurable(ec, durable)

	// Send the immediate message before parking so downstream nodes act now.
	if first := triggerPayloadMsg(cfg.FirstSource, cfg.FirstPayload, msg); first != nil {
		ec.Emit("out", first)
	}
	return triggerAwait(ctx, ec, waits, cfg, storeKey, mine, msg)
}

// Resume continues a stored trigger wait after a restart or a redeploy. The
// immediate message went out before the wait was stored, so only the delayed
// one remains.
func (TriggerNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	var cfg triggerConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	var stored storedMsg
	if err := json.Unmarshal(w.State, &stored); err != nil {
		return nil, fmt.Errorf("stored trigger message is unreadable: %w", err)
	}
	waits, _ := engine.WaitsFor(ec)
	return triggerAwait(ctx, ec, waits, cfg, key, w, stored.msg())
}

// triggerAwait parks until the wait owned by mine comes due, following
// extensions, and emits the delayed message once the claim succeeds. A reset
// or a restart by another message ends it quietly.
func triggerAwait(ctx context.Context, ec engine.ExecContext, waits engine.WaitStore, cfg triggerConfig, storeKey string, mine engine.Wait, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	_, claimed, err := parkClaim(ctx, ec, waits, storeKey, mine)
	if err != nil || !claimed {
		// Reset, or another message restarted the wait.
		return nil, err
	}
	second := triggerPayloadMsg(cfg.SecondSource, cfg.SecondPayload, msg)
	if second == nil {
		return nil, nil
	}
	return engine.One("expired", second), nil
}

// releaseWait deletes a wait, but only if token still owns it.
func releaseWait(waits engine.WaitStore, key, token string) {
	waits.Update(key, func(cur engine.Wait, ok bool) *engine.Wait {
		if !ok || cur.Token != token {
			if !ok {
				return nil
			}
			return &cur
		}
		return nil
	})
}

// triggerPayloadMsg builds one of the node's two emissions: the original
// message, a copy carrying the configured payload, or nothing at all.
func triggerPayloadMsg(source string, raw json.RawMessage, in *engine.Msg) *engine.Msg {
	switch source {
	case "nothing":
		return nil
	case "original":
		return in
	default:
		var payload any = map[string]any{}
		if len(raw) > 0 {
			// A payload that will not decode is reported as-is rather than
			// failing the node, since the wait already happened.
			if err := json.Unmarshal(raw, &payload); err != nil {
				payload = string(raw)
			}
		}
		return &engine.Msg{
			Payload: payload,
			Topic:   in.Topic,
			Meta:    nodeutil.CloneMeta(in.Meta),
			Group:   in.Group,
		}
	}
}
