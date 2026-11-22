package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// FolderOSC is the palette folder both nodes live in.
const FolderOSC = "OSC"

// connectRetryDelay is how long an armed osc-in trigger waits before retrying
// when its connection is not running yet.
const connectRetryDelay = 5 * time.Second

// conn is the driver surface the OSC nodes need, satisfied by *Driver. Nodes
// resolve it through ExecContext.Connection like every connection-backed node.
type conn interface {
	Send(ctx context.Context, address string, args []any) error
	Subscribe(filter string, handler func(address string, args []any)) (func(), error)
}

// resolveConn resolves a node's configured connection ID to the OSC driver.
func resolveConn(ec engine.ExecContext, id string) (conn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(conn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an OSC connection", id)
	}
	return c, nil
}

// InNode is the OSC trigger: while its flow is deployed and enabled it
// watches the connection's listen port and starts a run for every received
// message matching its address filter.
type InNode struct{}

func (InNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "osc-in",
		Label:          "OSC In",
		Category:       flow.CategoryTrigger,
		Folder:         FolderOSC,
		Description:    "Starts a run for every received OSC message matching an address filter",
		Icon:           "las la-sign-in-alt",
		ConnectionKind: Kind,
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortObject,
		}},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:             "connection",
				Label:          "Connection",
				Type:           flow.FieldConnection,
				Required:       true,
				ConnectionKind: Kind,
				Help:           "OSC connection to receive with. It must have a listen port.",
			},
			{
				ID:          "address",
				Label:       "Address filter",
				Type:        flow.FieldString,
				Placeholder: "/mixer/channel/*/level",
				Help: "OSC address pattern received messages are matched against: ? matches one " +
					"character, * matches within one path segment, [a-z] and {foo,bar} match sets. " +
					"Left empty, every message triggers.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with instead of waiting for " +
					"a real message, e.g. {\"address\": \"/a/b\", \"args\": [1]}. No network " +
					"traffic is involved.",
			},
		},
	}
}

// inConfig is the decoded osc-in node configuration.
type inConfig struct {
	Connection string `json:"connection"`
	Address    string `json:"address"`
}

// Execute passes the run's initial message through: runs seeded by the armed
// trigger carry the received OSC message, test runs carry test_params.
func (InNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

// Start implements engine.TriggerType: it subscribes to the connection's
// listen socket and emits a message per matching delivery until the context
// is canceled. When the connection is not running yet it retries on the
// injectable clock.
func (InNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg inConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	filter := strings.TrimSpace(cfg.Address)
	if filter != "" && !strings.HasPrefix(filter, "/") {
		return fmt.Errorf("osc address filter %q does not start with /", filter)
	}
	log := ec.Logger()

	// Resolve the connection, retrying while it is missing or not running so
	// arming succeeds regardless of connection startup order.
	var c conn
	for {
		var err error
		c, err = resolveConn(ec, cfg.Connection)
		if err == nil {
			break
		}
		log.Debug("osc-in waiting for connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil
		}
	}

	unsubscribe, err := c.Subscribe(filter, func(address string, args []any) {
		emit(&engine.Msg{
			Payload: map[string]any{
				"address": address,
				"args":    args,
			},
			Topic: address,
			Meta: map[string]any{
				"connection_id": cfg.Connection,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("osc subscribe failed: %w", err)
	}
	log.Info("osc-in trigger armed", zap.String("filter", filter))

	<-ctx.Done()
	unsubscribe()
	return nil
}

// OutNode is the OSC send action.
type OutNode struct{}

func (OutNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "osc-out",
		Label:          "OSC Out",
		Folder:         FolderOSC,
		Description:    "Sends an OSC message to the connection's target",
		Icon:           "las la-sign-out-alt",
		ConnectionKind: Kind,
		Retryable:      true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:             "connection",
				Label:          "Connection",
				Type:           flow.FieldConnection,
				Required:       true,
				ConnectionKind: Kind,
				Help:           "OSC connection to send with. It must have a send target.",
			},
			{
				ID:          "address",
				Label:       "Address",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "/mixer/channel/1/level",
				Help:        "OSC address to send to. {{ }} expressions are interpolated, e.g. /light/{{ payload.channel }}/dim.",
			},
			{
				ID:      "args_source",
				Label:   "Arguments source",
				Type:    flow.FieldSelect,
				Default: "input",
				Options: []engine.Option{
					{Value: "input", Label: "Input message payload"},
					{Value: "static", Label: "Static arguments"},
				},
				Help: "Where the message's arguments come from. A list payload sends one argument " +
					"per element, any other value sends a single argument, and null sends none.",
			},
			{
				ID:     "args",
				Label:  "Arguments",
				Type:   flow.FieldJSON,
				ShowIf: &engine.ShowIf{Field: "args_source", Equals: "static"},
				Help: "JSON arguments sent when the source is static, usually a list. {{ }} " +
					"expressions are interpolated textually before sending. Left empty, the " +
					"message goes address-only.",
			},
			{
				ID:      "number_format",
				Label:   "Numbers as",
				Type:    flow.FieldSelect,
				Default: numberAuto,
				Options: []engine.Option{
					{Value: numberAuto, Label: "Auto (whole numbers as int32, else float32)"},
					{Value: numberFloat, Label: "Float (32-bit)"},
					{Value: numberDouble, Label: "Double (64-bit)"},
					{Value: numberInt, Label: "Integer (32-bit)"},
				},
				Help: "OSC argument type numbers are sent as. Receivers match on the type tag, " +
					"so a device expecting floats will ignore an integer; pick the type the " +
					"receiver documents.",
			},
		},
	}
}

// outConfig is the decoded osc-out node configuration.
type outConfig struct {
	Connection   string          `json:"connection"`
	Address      string          `json:"address"`
	ArgsSource   string          `json:"args_source"`
	Args         json.RawMessage `json:"args"`
	NumberFormat string          `json:"number_format"`
}

// Execute sends the message and passes the input message through.
func (OutNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg outConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Address == "" {
		return nil, fmt.Errorf("osc-out node has no address")
	}
	c, err := resolveConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}

	// Pick any connected input as the message to forward.
	var msg *engine.Msg
	for _, m := range inputs {
		if m != nil {
			msg = m
			break
		}
	}
	if msg == nil {
		msg = &engine.Msg{}
	}

	// Interpolate {{ }} expressions in the address against the input message.
	address, err := ec.Interpolate(cfg.Address, msg)
	if err != nil {
		return nil, fmt.Errorf("invalid address template: %w", err)
	}
	if !strings.HasPrefix(address, "/") {
		return nil, fmt.Errorf("osc address %q does not start with /", address)
	}

	// Build the arguments from the configured source.
	args, err := buildArgs(ec, &cfg, msg)
	if err != nil {
		return nil, err
	}

	if err := c.Send(ctx, address, args); err != nil {
		return nil, err
	}
	return engine.One("out", msg), nil
}

// buildArgs renders the arguments to send: the static JSON config with {{ }}
// segments interpolated textually, or the input message payload.
func buildArgs(ec engine.ExecContext, cfg *outConfig, msg *engine.Msg) ([]any, error) {
	value := msg.Payload
	if cfg.ArgsSource == "static" {
		value = nil
		if raw := strings.TrimSpace(string(cfg.Args)); raw != "" && raw != "null" {
			rendered, err := ec.Interpolate(raw, msg)
			if err != nil {
				return nil, fmt.Errorf("invalid arguments template: %w", err)
			}
			if err := json.Unmarshal([]byte(rendered), &value); err != nil {
				return nil, fmt.Errorf("static arguments are not valid JSON: %w", err)
			}
		}
	}
	return toOSCArgs(value, cfg.NumberFormat)
}
