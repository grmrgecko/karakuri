package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"gitlab.com/gomidi/midi/v2"
	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// FolderMIDI is the palette folder both nodes live in.
const FolderMIDI = "MIDI"

// connectRetryDelay is how long an armed midi-in trigger waits before
// retrying when its connection is not running yet.
const connectRetryDelay = 5 * time.Second

// conn is the driver surface the MIDI nodes need, satisfied by *Driver. Nodes
// resolve it through ExecContext.Connection like every connection-backed node.
type conn interface {
	Send(msg midi.Message) error
	Subscribe(handler func(midi.Message)) (func(), error)
}

// resolveConn resolves a node's configured connection ID to the MIDI driver.
func resolveConn(ec engine.ExecContext, id string) (conn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(conn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not a MIDI connection", id)
	}
	return c, nil
}

// typeFilterOptions is the message type select shared by the trigger.
var typeFilterOptions = []engine.Option{
	{Value: "", Label: "Any message"},
	{Value: typeNoteOn, Label: "Note on"},
	{Value: typeNoteOff, Label: "Note off"},
	{Value: typeControlChange, Label: "Control change"},
	{Value: typeProgramChange, Label: "Program change"},
	{Value: typePitchBend, Label: "Pitch bend"},
	{Value: typeAfterTouch, Label: "Aftertouch"},
	{Value: typePolyAfterTouch, Label: "Poly aftertouch"},
	{Value: typeSysEx, Label: "SysEx"},
}

// InNode is the MIDI trigger: while its flow is deployed and enabled it
// listens on the connection's input port and starts a run for every received
// message passing its filters.
type InNode struct{}

func (InNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "midi-in",
		Label:          "MIDI In",
		Category:       flow.CategoryTrigger,
		Folder:         FolderMIDI,
		Description:    "Starts a run for every received MIDI message matching its filters",
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
				Help:           "MIDI connection to receive with. It must have an input port.",
			},
			{
				ID:      "message_type",
				Label:   "Message type",
				Type:    flow.FieldSelect,
				Default: "",
				Options: typeFilterOptions,
				Help:    "Only messages of this type trigger. Realtime and clock traffic never does.",
			},
			{
				ID:          "channel",
				Label:       "Channel",
				Type:        flow.FieldNumber,
				Placeholder: "any",
				Help:        "Only messages on this channel (1 to 16) trigger. Leave empty for every channel.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with instead of waiting for " +
					"a real message, e.g. {\"type\": \"note_on\", \"channel\": 1, \"note\": 60, " +
					"\"velocity\": 100}. No hardware is involved.",
			},
		},
	}
}

// inConfig is the decoded midi-in node configuration.
type inConfig struct {
	Connection  string `json:"connection"`
	MessageType string `json:"message_type"`
	Channel     int    `json:"channel"`
}

// Execute passes the run's initial message through: runs seeded by the armed
// trigger carry the received MIDI message, test runs carry test_params.
func (InNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

// Start implements engine.TriggerType: it subscribes to the connection's
// input port and emits a message per matching delivery until the context is
// canceled. When the connection is not running yet it retries on the
// injectable clock.
func (InNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg inConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if cfg.Channel < 0 || cfg.Channel > 16 {
		return fmt.Errorf("midi channel filter must be between 1 and 16")
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
		log.Debug("midi-in waiting for connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil
		}
	}

	unsubscribe, err := c.Subscribe(func(msg midi.Message) {
		payload := decodeMessage(msg)
		if payload == nil {
			return
		}
		if cfg.MessageType != "" && payload["type"] != cfg.MessageType {
			return
		}
		if cfg.Channel != 0 && payload["channel"] != cfg.Channel {
			return
		}
		emit(&engine.Msg{
			Payload: payload,
			Topic:   payload["type"].(string),
			Meta: map[string]any{
				"connection_id": cfg.Connection,
			},
		})
	})
	if err != nil {
		return fmt.Errorf("midi subscribe failed: %w", err)
	}
	log.Info("midi-in trigger armed",
		zap.String("type", cfg.MessageType), zap.Int("channel", cfg.Channel))

	<-ctx.Done()
	unsubscribe()
	return nil
}

// OutNode is the MIDI send action.
type OutNode struct{}

func (OutNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "midi-out",
		Label:          "MIDI Out",
		Folder:         FolderMIDI,
		Description:    "Sends MIDI messages to the connection's output port",
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
				Help:           "MIDI connection to send with. It must have an output port.",
			},
			{
				ID:      "message_source",
				Label:   "Message source",
				Type:    flow.FieldSelect,
				Default: "input",
				Options: []engine.Option{
					{Value: "input", Label: "Input message payload"},
					{Value: "static", Label: "Static message"},
				},
				Help: "Where the message comes from. The payload shape is the one midi-in emits " +
					"({\"type\": \"note_on\", \"channel\": 1, \"note\": 60, \"velocity\": 100}); " +
					"a list sends each message in order.",
			},
			{
				ID:     "message",
				Label:  "Message",
				Type:   flow.FieldJSON,
				ShowIf: &engine.ShowIf{Field: "message_source", Equals: "static"},
				Help: "JSON message (or list of messages) sent when the source is static. " +
					"{{ }} expressions are interpolated textually before sending.",
			},
		},
	}
}

// outConfig is the decoded midi-out node configuration.
type outConfig struct {
	Connection    string          `json:"connection"`
	MessageSource string          `json:"message_source"`
	Message       json.RawMessage `json:"message"`
}

// Execute sends the messages and passes the input message through.
func (OutNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg outConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
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

	// Build the messages from the configured source.
	value := msg.Payload
	if cfg.MessageSource == "static" {
		value = nil
		if raw := strings.TrimSpace(string(cfg.Message)); raw != "" && raw != "null" {
			rendered, err := ec.Interpolate(raw, msg)
			if err != nil {
				return nil, fmt.Errorf("invalid message template: %w", err)
			}
			if err := json.Unmarshal([]byte(rendered), &value); err != nil {
				return nil, fmt.Errorf("static message is not valid JSON: %w", err)
			}
		}
	}
	messages, err := encodeMessages(value)
	if err != nil {
		return nil, err
	}

	for _, m := range messages {
		if err := c.Send(m); err != nil {
			return nil, err
		}
	}
	return engine.One("out", msg), nil
}
