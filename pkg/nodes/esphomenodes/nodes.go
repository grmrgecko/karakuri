package esphomenodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"go.uber.org/zap"
)

// connectRetryDelay is how long an armed trigger waits before looking for
// its connection again when it is not running yet.
const connectRetryDelay = 5 * time.Second

// node is the part of the ESPHome driver the nodes use.
type node interface {
	CallAction(ctx context.Context, name string, args map[string]any) error
	WatchActions(fn func(esphome.NodeAction)) func()
	Info() esphome.NodeInfo
}

var _ node = (*esphome.Driver)(nil)

// resolveNode resolves a configured connection ID to an ESPHome driver.
func resolveNode(ec engine.ExecContext, id string) (node, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	n, ok := raw.(node)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an ESPHome connection", id)
	}
	return n, nil
}

// connectionField is the ESPHome connection picker.
func connectionField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:             "connection",
		Label:          "Node",
		Type:           flow.FieldConnection,
		Required:       true,
		ConnectionKind: esphome.Kind,
		Help:           "ESPHome connection of the node.",
	}
}

// ActionNode calls one of a node's user-defined actions (api: actions: in its
// YAML).
type ActionNode struct{}

func (ActionNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "esphome-action",
		Label:       "ESPHome Action",
		Folder:      flow.FolderESPHome,
		Description: "Calls one of an ESPHome node's own actions with arguments",
		Icon:        "las la-microchip",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
		},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{
				ID: "action", Label: "Action", Type: flow.FieldString, Required: true,
				Placeholder: "beep",
				Help:        "Name of the action under api: actions: in the node's YAML.",
			},
			{
				ID: "args", Label: "Arguments", Type: flow.FieldJSON,
				Default: map[string]any{},
				Help: "Arguments by name. They are checked against the types the action declares " +
					"(bool, int, float, string, and lists of them). {{ }} expressions are evaluated first.",
			},
		},
		Retryable: true,
	}
}

// actionConfig is the decoded esphome-action configuration.
type actionConfig struct {
	Connection string          `json:"connection"`
	Action     string          `json:"action"`
	Args       json.RawMessage `json:"args"`
}

// Execute checks the arguments and calls the action; the message passes
// through.
func (ActionNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg actionConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	args := map[string]any{}
	if raw := strings.TrimSpace(string(cfg.Args)); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &args); err != nil {
			return nil, fmt.Errorf("arguments must be a JSON object: %w", err)
		}
		filled, err := fill(ec, args, msg)
		if err != nil {
			return nil, err
		}
		args = filled.(map[string]any)
	}
	n, err := resolveNode(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	if err := n.CallAction(ctx, strings.TrimSpace(cfg.Action), args); err != nil {
		return nil, err
	}
	return engine.One("out", msg), nil
}

// fill evaluates expressions inside decoded arguments. A string that is one
// whole {{ }} keeps the expression's type, so "{{ payload.count }}" is a
// number for an int argument; other strings are interpolated as text.
func fill(ec engine.ExecContext, node any, msg *engine.Msg) (any, error) {
	switch v := node.(type) {
	case string:
		if inner, ok := expr.SingleExpression(v); ok {
			return ec.Eval(inner, msg)
		}
		return ec.Interpolate(v, msg)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			filled, err := fill(ec, item, msg)
			if err != nil {
				return nil, err
			}
			out[i] = filled
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, item := range v {
			filled, err := fill(ec, item, msg)
			if err != nil {
				return nil, err
			}
			out[k] = filled
		}
		return out, nil
	}
	return node, nil
}

// NodeActionTrigger starts a run when a node calls a Home Assistant style
// action (homeassistant.action or homeassistant.event in its YAML), the way it
// would ask Home Assistant to do something.
type NodeActionTrigger struct{}

func (NodeActionTrigger) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "esphome-node-action",
		Label:             "ESPHome Node Action",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderESPHome,
		Description:       "Starts a run when an ESPHome node calls an action or fires an event",
		Icon:              "las la-bolt",
		TestWithoutParams: true,
		Outputs:           []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{
				ID: "action", Label: "Action", Type: flow.FieldString, Placeholder: "notify.phone",
				Help: "Only fire for this action or event name. Empty fires for every one.",
			},
			{
				ID: "test_params", Label: "Test parameters", Type: flow.FieldJSON,
				Help: "Payload a test run starts with.",
			},
		},
	}
}

// nodeActionConfig is the decoded esphome-node-action configuration.
type nodeActionConfig struct {
	Connection string `json:"connection"`
	Action     string `json:"action"`
}

// Execute passes the seeded message through, or a sample on a test run.
func (NodeActionTrigger) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	var cfg nodeActionConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	return engine.One("out", actionMsg(esphome.NodeAction{Action: cfg.Action, Data: map[string]any{}}, cfg.Connection, "")), nil
}

// Start watches the node's action calls until the context ends.
func (NodeActionTrigger) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg nodeActionConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	var n node
	for {
		var err error
		if n, err = resolveNode(ec, cfg.Connection); err == nil {
			break
		}
		ec.Logger().Debug("waiting for ESPHome connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil
		}
	}
	want := strings.TrimSpace(cfg.Action)
	unwatch := n.WatchActions(func(a esphome.NodeAction) {
		if want != "" && a.Action != want {
			return
		}
		emit(actionMsg(a, cfg.Connection, n.Info().Name))
	})
	defer unwatch()
	<-ctx.Done()
	return nil
}

// actionMsg renders a node action call as a message.
func actionMsg(a esphome.NodeAction, connID, nodeName string) *engine.Msg {
	return &engine.Msg{
		Payload: map[string]any{
			"action":        a.Action,
			"data":          a.Data,
			"is_event":      a.IsEvent,
			"connection_id": connID,
			"node":          nodeName,
		},
		Topic: a.Action,
		Meta:  map[string]any{"esphome_node": nodeName, "esphome_action": a.Action},
	}
}
