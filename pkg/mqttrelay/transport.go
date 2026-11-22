package mqttrelay

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// Device transports.
const (
	TransportMQTT    = "mqtt"
	TransportESPHome = "esphome"
	TransportFrigate = "frigate"
	// TransportHomeAssistant devices are Home Assistant entities, read and
	// commanded through the connection's shared domain mapping.
	TransportHomeAssistant = "homeassistant"
	// TransportOpenHAB devices are openHAB items, read and commanded through
	// the connection's item-type mapping.
	TransportOpenHAB = "openhab"
)

// IsEntityTransport reports whether a transport's devices are entities a
// connection holds rather than MQTT topics.
func IsEntityTransport(transport string) bool {
	return transport == TransportESPHome || transport == TransportFrigate ||
		transport == TransportHomeAssistant || transport == TransportOpenHAB
}

// transportLabel names a transport in messages.
func transportLabel(transport string) string {
	switch transport {
	case TransportESPHome:
		return "ESPHome"
	case TransportFrigate:
		return "Frigate"
	case TransportHomeAssistant:
		return "Home Assistant"
	case TransportOpenHAB:
		return "openHAB"
	}
	return "MQTT"
}

// EntityTransport is a connection whose devices are entities it holds rather
// than MQTT topics: an ESPHome node or a Frigate server. It hands each
// entity's values as the device's kind holds them and sends one attribute at
// a time.
type EntityTransport interface {
	WatchEntity(objectID string, fn func(values map[string]any)) (func(), error)
	CommandEntity(ctx context.Context, objectID, field string, value any) error
}

// EntityAvailability is an entity transport that also says when one entity
// cannot answer although the connection is up, such as a Frigate camera
// receiving no frames.
type EntityAvailability interface {
	WatchEntityAvailability(objectID string, fn func(available bool)) (func(), error)
}

// EntityWritability is an entity transport that knows whether one entity
// takes commands when its type alone does not say, such as an openHAB item
// its server marks read only.
type EntityWritability interface {
	// EntityWritable reports whether the entity takes commands. known is
	// false while the connection does not hold the entity.
	EntityWritable(objectID string) (writable, known bool)
}

// writableEntityTypes are the ESPHome and Frigate entity types that take
// commands. Home Assistant domains answer from the shared mapping instead.
var writableEntityTypes = map[string]bool{
	"switch": true, "light": true, "cover": true, "fan": true, "lock": true, "climate": true,
	"number": true, "select": true, "text": true, "button": true, "mediaplayer": true,
}

// Commandable reports whether a device takes commands: an MQTT device with a
// payload script, or an entity of a writable type.
func Commandable(d *models.MQTTDevice) bool {
	if d.Transport == TransportHomeAssistant {
		return homeassistant.Writable(d.EntityType)
	}
	if d.Transport == TransportOpenHAB {
		return openhab.Writable(d.EntityType)
	}
	if IsEntityTransport(d.Transport) {
		return writableEntityTypes[d.EntityType]
	}
	return strings.TrimSpace(d.PayloadScript) != ""
}

// Commandable is Commandable held to what the device's connection says about
// its entity. A connection that is not running, or does not hold the entity,
// leaves the answer to the entity's type.
func (d *Devices) Commandable(device *models.MQTTDevice) bool {
	if !Commandable(device) {
		return false
	}
	if !IsEntityTransport(device.Transport) || d.resolve == nil {
		return true
	}
	driver, ok := d.resolve(device.ConnectionID)
	if !ok {
		return true
	}
	if w, ok := driver.(EntityWritability); ok {
		if writable, known := w.EntityWritable(device.EntityKey); known {
			return writable
		}
	}
	return true
}

// ErrNoCommands is the refusal of a command to a device that takes none.
var ErrNoCommands = errors.New("takes no commands")

// InvalidCommandError is a command refused before anything was sent: a value
// the kind cannot hold, a device that takes no commands, or a payload script
// that failed.
type InvalidCommandError struct{ Err error }

func (e *InvalidCommandError) Error() string { return e.Err.Error() }
func (e *InvalidCommandError) Unwrap() error { return e.Err }

// CommandResult is what a command did. Topic is the MQTT topic published to,
// empty when the payload script chose to send nothing or the device is not
// an MQTT device.
type CommandResult struct {
	Field string
	Value any
	Topic string
	// Sent is false when an MQTT payload script decided the change means
	// nothing.
	Sent bool
	// QoS and Retain are what an MQTT command was published with.
	QoS    byte
	Retain bool
}

// Command checks one interaction against the device's kind and sends it over
// the device's transport: the payload script and a publish for MQTT, the
// connection for ESPHome and Frigate. Nothing about the device's state changes here; the
// device reports it.
func (d *Devices) Command(ctx context.Context, device *models.MQTTDevice, field string, value any, on *bool, previous any) (CommandResult, error) {
	if !d.Commandable(device) {
		return CommandResult{}, &InvalidCommandError{fmt.Errorf("device %q %w", device.Name, ErrNoCommands)}
	}
	field, coerced, err := ResolveCommand(device, field, value)
	if err != nil {
		return CommandResult{}, &InvalidCommandError{err}
	}
	if state, ok := d.State(device.ID); ok {
		if err := controls.CheckAgainstValue(device.Control, state, field, coerced); err != nil {
			return CommandResult{}, &InvalidCommandError{err}
		}
	}
	if err := d.refuseOffline(device); err != nil {
		return CommandResult{}, err
	}
	result := CommandResult{Field: field, Value: coerced}

	if IsEntityTransport(device.Transport) {
		label := transportLabel(device.Transport)
		driver, ok := d.resolve(device.ConnectionID)
		if !ok {
			return result, fmt.Errorf("the %s connection of device %q is not running", label, device.Name)
		}
		transport, ok := driver.(EntityTransport)
		if !ok {
			return result, fmt.Errorf("device %q points at a connection that is not %s", device.Name, label)
		}
		if err := transport.CommandEntity(ctx, device.EntityKey, field, coerced); err != nil {
			return result, err
		}
		result.Sent = true
		recordCommand(ctx, device, field, coerced, nil)
		return result, nil
	}

	message, _, err := RunPayloadScriptWithin(ctx, device.PayloadScript, CommandVars(field, coerced, on, previous),
		device.PayloadTimeout())
	if err != nil {
		return result, &InvalidCommandError{fmt.Errorf("payload script of device %q failed: %w", device.Name, err)}
	}
	if message.Topic == "" {
		return result, nil
	}
	if err := d.PublishCommand(ctx, device, message); err != nil {
		return result, err
	}
	result.Topic, result.Sent = message.Topic, true
	result.QoS, result.Retain = DeliveryOf(device, message)
	recordCommand(ctx, device, field, coerced, map[string]any{"qos": result.QoS, "retain": result.Retain})
	return result, nil
}

// recordCommand records a sent command in the event log with whoever sent
// it, which the context carries from the request or the flow, and delivery
// details such as the QoS and retain flag an MQTT command was published with.
func recordCommand(ctx context.Context, device *models.MQTTDevice, field string, value any, delivery map[string]any) {
	details := map[string]any{"field": field, "value": value, "command": true}
	for k, v := range delivery {
		details[k] = v
	}
	events.Record(events.Entry{
		Category: models.EventDevice,
		Subject:  events.Subject{Kind: "device", ID: device.ID.String(), Name: device.Name},
		Message:  fmt.Sprintf("%s: %s set to %s", device.Name, field, events.FormatValue(value)),
		Details:  details,
		Actor:    events.ActorFrom(ctx),
	})
}
