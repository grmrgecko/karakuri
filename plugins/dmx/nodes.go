package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// FolderDMX is the palette folder both nodes live in.
const FolderDMX = "DMX"

// connectRetryDelay is how long an armed dmx-in trigger waits before retrying
// when its connection is not running yet.
const connectRetryDelay = 5 * time.Second

// conn is the driver surface the DMX nodes need, satisfied by *Driver. Nodes
// resolve it through ExecContext.Connection like every connection-backed node.
type conn interface {
	SetChannels(universe int, values map[int]byte) error
	Subscribe(universe int, handler func(ReceiveEvent)) (func(), error)
}

// resolveConn resolves a node's configured connection ID to the DMX driver.
func resolveConn(ec engine.ExecContext, id string) (conn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(conn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not a DMX connection", id)
	}
	return c, nil
}

// InNode is the DMX trigger: while its flow is deployed and enabled it
// watches a universe and starts a run for every received frame whose levels
// moved. Keepalive re-transmissions of an unchanged frame are dropped by the
// driver, so a run means something on the wire actually changed.
type InNode struct{}

func (InNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "dmx-in",
		Label:          "DMX In",
		Category:       flow.CategoryTrigger,
		Folder:         FolderDMX,
		Description:    "Starts a run when a received DMX frame changes a universe's levels",
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
				Help:           "DMX connection to receive with.",
			},
			{
				ID:          "universe",
				Label:       "Universe",
				Type:        flow.FieldNumber,
				Required:    true,
				Placeholder: "1",
				Help:        "Universe to watch. sACN universes are 1 to 63999; an Art-Net port-address is 0 to 32767.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with instead of waiting for " +
					"a real frame, e.g. {\"universe\": 1, \"changed\": {\"1\": 255}}. No network " +
					"traffic is involved.",
			},
		},
	}
}

// inConfig is the decoded dmx-in node configuration.
type inConfig struct {
	Connection string `json:"connection"`
	Universe   int    `json:"universe"`
}

// Execute passes the run's initial message through: runs seeded by the armed
// trigger carry the received frame, test runs carry test_params.
func (InNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

// Start implements engine.TriggerType: it subscribes to the configured
// universe and emits a message per changed frame until the context is
// canceled. When the connection is not running yet it retries on the
// injectable clock.
func (InNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg inConfig
	if err := ec.Config(&cfg); err != nil {
		return err
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
		log.Debug("dmx-in waiting for connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil
		}
	}

	unsubscribe, err := c.Subscribe(cfg.Universe, func(evt ReceiveEvent) {
		emit(&engine.Msg{
			Payload: channelsPayload(evt),
			Topic:   strconv.Itoa(evt.Universe),
			Meta: map[string]any{
				"connection_id": cfg.Connection,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("dmx subscribe failed: %w", err)
	}
	log.Info("dmx-in trigger armed", zap.Int("universe", cfg.Universe))

	<-ctx.Done()
	unsubscribe()
	return nil
}

// OutNode is the DMX channel-set action.
type OutNode struct{}

func (OutNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "dmx-out",
		Label:          "DMX Out",
		Folder:         FolderDMX,
		Description:    "Sets DMX channel levels on a universe",
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
				Help:           "DMX connection to send with.",
			},
			{
				ID:          "universe",
				Label:       "Universe",
				Type:        flow.FieldNumber,
				Required:    true,
				Placeholder: "1",
				Help:        "Universe the channels live on. Levels merge into the universe's frame, so channels this node does not name keep their levels.",
			},
			{
				ID:      "channels_source",
				Label:   "Channels source",
				Type:    flow.FieldSelect,
				Default: "input",
				Options: []engine.Option{
					{Value: "input", Label: "Input message payload"},
					{Value: "static", Label: "Static channels"},
				},
				Help: "Where the levels come from. An object maps channels to levels ({\"1\": 255}), " +
					"a list fills consecutive channels from the start channel, and a single number " +
					"or boolean sets the start channel alone.",
			},
			{
				ID:     "channels",
				Label:  "Channels",
				Type:   flow.FieldJSON,
				ShowIf: &engine.ShowIf{Field: "channels_source", Equals: "static"},
				Help: "JSON levels sent when the source is static, in any of the payload shapes " +
					"above. {{ }} expressions are interpolated textually before sending.",
			},
			{
				ID:          "start_channel",
				Label:       "Start channel",
				Type:        flow.FieldNumber,
				Default:     1,
				Placeholder: "1",
				Help:        "Channel a list payload starts filling at, and the one a single value sets.",
			},
		},
	}
}

// outConfig is the decoded dmx-out node configuration.
type outConfig struct {
	Connection     string          `json:"connection"`
	Universe       int             `json:"universe"`
	ChannelsSource string          `json:"channels_source"`
	Channels       json.RawMessage `json:"channels"`
	StartChannel   int             `json:"start_channel"`
}

// Execute merges the levels into the universe's frame, transmits it, and
// passes the input message through.
func (OutNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg outConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.StartChannel == 0 {
		cfg.StartChannel = 1
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

	// Build the levels from the configured source.
	value := msg.Payload
	if cfg.ChannelsSource == "static" {
		value = nil
		if raw := strings.TrimSpace(string(cfg.Channels)); raw != "" && raw != "null" {
			rendered, err := ec.Interpolate(raw, msg)
			if err != nil {
				return nil, fmt.Errorf("invalid channels template: %w", err)
			}
			if err := json.Unmarshal([]byte(rendered), &value); err != nil {
				return nil, fmt.Errorf("static channels are not valid JSON: %w", err)
			}
		}
	}
	values, err := channelValues(value, cfg.StartChannel)
	if err != nil {
		return nil, err
	}

	if err := c.SetChannels(cfg.Universe, values); err != nil {
		return nil, err
	}
	return engine.One("out", msg), nil
}
