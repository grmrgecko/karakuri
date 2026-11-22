// Package mqttnodes provides the MQTT trigger and action node types. Both
// resolve their connection through ExecContext.Connection and talk to the
// shared MQTT driver session, never to the broker directly.
package mqttnodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// RegisterAll registers the MQTT node types on the given registry.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(InNode{})
	reg.Register(OutNode{})
	reg.Register(DeviceGetNode{})
	reg.Register(DeviceSetNode{})
	reg.Register(DeviceChangedNode{})
	reg.Register(DeviceAvailabilityNode{})
}

// connectRetryDelay is how long an armed mqtt-in trigger waits before
// retrying when its connection is not running yet.
const connectRetryDelay = 5 * time.Second

// conn is the driver surface the MQTT nodes need. Satisfied structurally by
// *mqtt.Driver, avoiding a package dependency on the driver implementation.
type conn interface {
	Publish(ctx context.Context, topic string, qos byte, retain bool, payload []byte) error
	Subscribe(filter string, qos byte, handler func(topic string, payload []byte)) (func(), error)
}

// resolveConn resolves a node's configured connection ID to the MQTT driver.
func resolveConn(ec engine.ExecContext, id string) (conn, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	c, ok := raw.(conn)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an MQTT connection", id)
	}
	return c, nil
}

// parseQoS converts the "0"/"1"/"2" select value to an MQTT QoS byte.
func parseQoS(s string) (byte, error) {
	if s == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 2 {
		return 0, fmt.Errorf("invalid QoS %q", s)
	}
	return byte(n), nil
}

// qosOptions is the shared QoS select field option set.
var qosOptions = []engine.Option{
	{Value: "0", Label: "0 - At most once"},
	{Value: "1", Label: "1 - At least once"},
	{Value: "2", Label: "2 - Exactly once"},
}

// InNode is the MQTT trigger: while its flow is deployed and enabled it
// subscribes to a topic filter and starts a run for every received message.
type InNode struct{}

func (InNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "mqtt-in",
		Label:          "MQTT In",
		Category:       flow.CategoryTrigger,
		Folder:         flow.FolderMQTT,
		Description:    "Starts a run for every MQTT message matching a topic filter",
		Icon:           "las la-sign-in-alt",
		ConnectionKind: "mqtt",
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortObject,
			// The declared format decides what a message becomes. A payload
			// that does not match it is dropped with a warning rather than
			// emitted as something else, so the port states what a run carries.
			TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: map[string][]string{"format": {mqttFormatText}}, Type: flow.PortString},
					{Match: map[string][]string{"format": {mqttFormatBinary}}, Type: flow.PortBinary},
					// A config saved before the format setting existed said the
					// same thing with a boolean.
					{Match: map[string][]string{"format": {""}, "parse_json": {"false"}}, Type: flow.PortString},
				},
				Default: flow.PortObject,
			},
		}},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:             "connection",
				Label:          "Connection",
				Type:           flow.FieldConnection,
				Required:       true,
				ConnectionKind: "mqtt",
				Help:           "MQTT connection to subscribe with.",
			},
			{
				ID:          "topic",
				Label:       "Topic filter",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "zigbee/+/state",
				Help:        "MQTT topic filter; + and # wildcards are allowed.",
			},
			{
				ID:      "qos",
				Label:   "QoS",
				Type:    flow.FieldSelect,
				Default: "0",
				Options: qosOptions,
			},
			{
				ID:      "format",
				Label:   "Read the payload as",
				Type:    flow.FieldSelect,
				Default: mqttFormatJSON,
				Options: []engine.Option{
					{Value: mqttFormatJSON, Label: "JSON"},
					{Value: mqttFormatText, Label: "Text"},
					{Value: mqttFormatBinary, Label: "Binary"},
				},
				Help: "What the payload is read as. A message that does not match is dropped with " +
					"a warning, since a trigger has no error branch to route it to, so the output " +
					"port states what a run will actually carry.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Payload the Test Run button seeds this trigger with, read as the format " +
					"above, instead of waiting for a real message. Binary is written as base64. " +
					"No broker traffic is involved.",
			},
		},
	}
}

// inConfig is the decoded mqtt-in node configuration.
type inConfig struct {
	Connection string `json:"connection"`
	Topic      string `json:"topic"`
	QoS        string `json:"qos"`
	Format     string `json:"format"`
	// ParseJSON is how a config saved before the format setting said the same
	// thing. It is only read when no format is set.
	ParseJSON *bool `json:"parse_json"`
}

// MQTT payload formats: how a received payload becomes the trigger message.
const (
	mqttFormatJSON   = "json"
	mqttFormatText   = "text"
	mqttFormatBinary = "binary"
)

// format resolves the payload format, reading the old parse_json boolean when
// no format is configured so an existing subscription keeps behaving.
func (c inConfig) format() string {
	if f := strings.TrimSpace(c.Format); f != "" {
		return f
	}
	if c.ParseJSON != nil && !*c.ParseJSON {
		return mqttFormatText
	}
	return mqttFormatJSON
}

// Execute passes the run's initial message through: runs seeded by the armed
// trigger carry the received MQTT message, test runs carry test_params.
func (InNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	msg := inputs[engine.TriggerInput]
	if msg == nil {
		msg = &engine.Msg{}
	}
	return engine.One("out", msg), nil
}

// Start implements engine.TriggerType: it subscribes to the configured topic
// filter and emits a message per delivery until the context is canceled. When
// the connection is not running yet it retries on the injectable clock.
func (InNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg inConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	if cfg.Topic == "" {
		return fmt.Errorf("mqtt-in node has no topic filter")
	}
	qos, err := parseQoS(cfg.QoS)
	if err != nil {
		return err
	}
	log := ec.Logger()

	// Resolve the connection, retrying while it is missing or not running so
	// arming succeeds regardless of connection startup order.
	var c conn
	for {
		c, err = resolveConn(ec, cfg.Connection)
		if err == nil {
			break
		}
		log.Debug("mqtt-in waiting for connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil
		}
	}

	format := cfg.format()
	unsubscribe, err := c.Subscribe(cfg.Topic, qos, func(topic string, payload []byte) {
		// A payload that does not match the declared format is dropped: a
		// trigger has no error branch, and starting a run with something other
		// than what the port promised would break every node downstream.
		value, err := decodePayload(payload, format)
		if err != nil {
			log.Warn("mqtt-in dropped a message",
				zap.String("topic", topic), zap.Error(err))
			return
		}
		emit(&engine.Msg{
			Payload: value,
			Topic:   topic,
			Meta: map[string]any{
				"connection_id": cfg.Connection,
				"qos":           int(qos),
			},
		})
	})
	if err != nil {
		return fmt.Errorf("mqtt subscribe failed: %w", err)
	}
	log.Info("mqtt-in trigger armed", zap.String("filter", cfg.Topic))

	<-ctx.Done()
	unsubscribe()
	return nil
}

// decodePayload converts a raw MQTT payload to a message payload value in the
// declared format, reporting a payload that does not match rather than handing
// back something the port did not promise.
func decodePayload(payload []byte, format string) (any, error) {
	switch format {
	case mqttFormatText:
		return string(payload), nil
	case mqttFormatBinary:
		return payload, nil
	case mqttFormatJSON, "":
		var v any
		if err := json.Unmarshal(payload, &v); err != nil {
			return nil, fmt.Errorf("payload is not valid JSON: %w", err)
		}
		return v, nil
	default:
		return nil, fmt.Errorf("unknown payload format %q", format)
	}
}

// OutNode is the MQTT publish action.
type OutNode struct{}

func (OutNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "mqtt-out",
		Label:          "MQTT Out",
		Folder:         flow.FolderMQTT,
		Description:    "Publishes a message to an MQTT topic",
		Icon:           "las la-sign-out-alt",
		ConnectionKind: "mqtt",
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
				ConnectionKind: "mqtt",
				Help:           "MQTT connection to publish with.",
			},
			{
				ID:          "topic",
				Label:       "Topic",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "home/lights/office/set",
				Help:        "Topic to publish to. {{ }} expressions are interpolated, e.g. home/{{ payload.room }}/set.",
			},
			{
				ID:      "qos",
				Label:   "QoS",
				Type:    flow.FieldSelect,
				Default: "0",
				Options: qosOptions,
			},
			{
				ID:    "retain",
				Label: "Retain",
				Type:  flow.FieldBoolean,
				Help:  "Ask the broker to retain the message for new subscribers.",
			},
			{
				ID:      "payload_source",
				Label:   "Payload source",
				Type:    flow.FieldSelect,
				Default: "input",
				Options: []engine.Option{
					{Value: "input", Label: "Input message payload"},
					{Value: "static", Label: "Static payload"},
				},
			},
			{
				ID:     "payload",
				Label:  "Payload",
				Type:   flow.FieldJSON,
				ShowIf: &engine.ShowIf{Field: "payload_source", Equals: "static"},
				Help:   "JSON payload published when the payload source is static. {{ }} expressions are interpolated textually before publishing.",
			},
		},
	}
}

// outConfig is the decoded mqtt-out node configuration.
type outConfig struct {
	Connection    string          `json:"connection"`
	Topic         string          `json:"topic"`
	QoS           string          `json:"qos"`
	Retain        bool            `json:"retain"`
	PayloadSource string          `json:"payload_source"`
	Payload       json.RawMessage `json:"payload"`
}

// Execute publishes the payload and passes the input message through.
func (OutNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg outConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Topic == "" {
		return nil, fmt.Errorf("mqtt-out node has no topic")
	}
	qos, err := parseQoS(cfg.QoS)
	if err != nil {
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

	// Interpolate {{ }} expressions in the topic against the input message.
	topic, err := ec.Interpolate(cfg.Topic, msg)
	if err != nil {
		return nil, fmt.Errorf("invalid topic template: %w", err)
	}

	// Build the publish payload from the configured source.
	payload, err := buildPayload(ec, &cfg, msg)
	if err != nil {
		return nil, err
	}

	if err := c.Publish(ctx, topic, qos, cfg.Retain, payload); err != nil {
		return nil, err
	}
	return engine.One("out", msg), nil
}

// buildPayload renders the bytes to publish: the static JSON config with
// {{ }} segments interpolated textually, or the input message payload
// (strings sent raw, everything else as JSON).
func buildPayload(ec engine.ExecContext, cfg *outConfig, msg *engine.Msg) ([]byte, error) {
	if cfg.PayloadSource == "static" {
		if len(cfg.Payload) == 0 {
			return nil, fmt.Errorf("mqtt-out static payload is empty")
		}
		rendered, err := ec.Interpolate(string(cfg.Payload), msg)
		if err != nil {
			return nil, fmt.Errorf("invalid payload template: %w", err)
		}
		return []byte(rendered), nil
	}
	switch p := msg.Payload.(type) {
	case string:
		return []byte(p), nil
	case []byte:
		// A binary payload goes out as it came in. JSON would publish it as
		// a quoted base64 string.
		return p, nil
	}
	raw, err := json.Marshal(msg.Payload)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize message payload: %w", err)
	}
	return raw, nil
}
