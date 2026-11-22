package mqttrelay

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/controls"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
)

// Commanding a device is three steps shared by the HTTP command endpoint and
// the device-set node: hold the interaction to what the device's control kind
// accepts, run the device's own payload script, and publish what it returned.
// The steps live here rather than in the API package so a flow presses a
// device exactly the way a dashboard row does.

// CommandTimeout bounds a payload script unless the device sets its own
// limit. A press waits on it, so the budget is the one a person will sit
// through rather than the script node's thirty seconds: a formatter that needs
// longer is not formatting.
const CommandTimeout = models.DefaultPayloadTimeout

// CommandPreamble binds the command variables to the names a payload script
// reads. It shares the script's first line rather than taking one of its own,
// so a reported error line is the line the author wrote. `field` names which
// of a device's values is being set, which is what lets one device serve rows
// that set different things.
const CommandPreamble = `const {field, value, on, previous} = payload; `

// CommandMessage is what a payload script asked to send: a payload, and the
// topic it goes to. An empty topic is the null action — nothing is published
// and nothing is wrong.
type CommandMessage struct {
	Topic   string
	Payload string
	// QoS and Retain are what the script asked for this one message; nil
	// leaves the device's command QoS and retain flag.
	QoS    *byte
	Retain *bool
}

// ResolveCommand resolves which attribute an interaction names and holds the
// value to what that attribute accepts. A device with no kind declared is let
// through untouched: it predates the catalog, and refusing its commands would
// break a dashboard rather than fix a device. A kind that is named but not in
// the catalog is a broken definition, not a legacy one, and is refused.
func ResolveCommand(device *models.MQTTDevice, field string, value any) (string, any, error) {
	def, ok := controls.FindKind(device.Control)
	if !ok {
		if strings.TrimSpace(device.Control) != "" {
			return "", nil, fmt.Errorf("device %q declares unknown control kind %q", device.Name, device.Control)
		}
		return field, value, nil
	}
	opts := device.OptionMap()
	if strings.TrimSpace(field) == "" {
		field = def.PrimaryAttribute().ID
	}
	coerced, err := controls.CoerceAttribute(def.Name, opts, field, value)
	if err != nil {
		return "", nil, err
	}
	return field, coerced, nil
}

// CommandVars builds the variables a payload script reads for one interaction.
// It is shared by the command endpoint, the device-set node, and the script
// test so every path hands the script the same shape. `on` is present only
// when the caller gave a direction or the value itself is a boolean; anything
// else would be a lie.
func CommandVars(field string, value any, on *bool, previous any) map[string]any {
	vars := map[string]any{"field": field, "value": value, "previous": previous}
	switch {
	case on != nil:
		vars["on"] = *on
	default:
		if b, ok := value.(bool); ok {
			vars["on"] = b
		}
	}
	return vars
}

// RunPayloadScript turns one control change into a message. The script names
// the topic itself, and naming none is how it says the change means nothing:
// the caller publishes nothing rather than treating it as a failure.
func RunPayloadScript(ctx context.Context, source string, vars map[string]any) (CommandMessage, error) {
	msg, _, err := RunPayloadScriptWithOutput(ctx, source, vars)
	return msg, err
}

// RunPayloadScriptWithOutput runs a payload script and also returns its
// console lines, for the editor's Test button: a debugging tool needs what the
// script printed as much as what it returned.
func RunPayloadScriptWithOutput(ctx context.Context, source string, vars map[string]any) (CommandMessage, []string, error) {
	return RunPayloadScriptWithin(ctx, source, vars, CommandTimeout)
}

// RunPayloadScriptWithin runs a payload script under a time limit, the
// device's own when it sets one.
func RunPayloadScriptWithin(ctx context.Context, source string, vars map[string]any, limit time.Duration) (CommandMessage, []string, error) {
	if limit <= 0 {
		limit = CommandTimeout
	}
	// The JavaScript runtime stops a script when its context ends, so the
	// limit is a deadline on the context as well as the declared budget.
	ctx, cancel := context.WithTimeout(ctx, limit)
	defer cancel()
	res, err := sandbox.JSRuntime{}.Run(ctx, sandbox.Request{
		Language: sandbox.LangJavaScript,
		Source:   CommandPreamble + source,
		Payload:  vars,
		Network:  sandbox.NetworkNone,
		Limits:   sandbox.Limits{Timeout: limit}.Normalize(),
	})
	if err != nil {
		return CommandMessage{}, nil, err
	}
	msg, err := ReadPayloadResult(res.Value)
	return msg, res.Output, err
}

// ReadPayloadResult reads what a payload script returned.
func ReadPayloadResult(value any) (CommandMessage, error) {
	switch v := value.(type) {
	case nil:
		// Returning nothing is a null action, the same answer as returning no
		// topic; both mean there is nothing to send.
		return CommandMessage{}, nil
	case map[string]any:
		out := CommandMessage{}
		if topic, ok := v["topic"].(string); ok {
			out.Topic = strings.TrimSpace(topic)
		}
		if _, present := v["payload"]; !present && out.Topic == "" {
			// An empty object names nowhere to send and nothing to send,
			// which is the null action. Generated Zigbee2MQTT scripts end in
			// it for the fields they have no case for.
			return out, nil
		}
		if q, present := v["qos"]; present && q != nil {
			n, ok := q.(float64)
			if !ok || (n != 0 && n != 1 && n != 2) {
				return CommandMessage{}, fmt.Errorf("payload script returned qos %v; it must be 0, 1, or 2", q)
			}
			qos := byte(n)
			out.QoS = &qos
		}
		if r, present := v["retain"]; present && r != nil {
			retain, ok := r.(bool)
			if !ok {
				return CommandMessage{}, fmt.Errorf("payload script returned retain %v; it must be true or false", r)
			}
			out.Retain = &retain
		}
		raw, present := v["payload"]
		if !present {
			return CommandMessage{}, fmt.Errorf("payload script returned no payload")
		}
		if text, ok := raw.(string); ok {
			out.Payload = text
			return out, nil
		}
		// An object payload is the short way to build a document for a JSON
		// device, so it is encoded rather than refused.
		encoded, err := json.Marshal(raw)
		if err != nil {
			return CommandMessage{}, fmt.Errorf("payload is not encodable: %w", err)
		}
		out.Payload = string(encoded)
		return out, nil
	default:
		return CommandMessage{}, fmt.Errorf("payload script returned %T, want {topic, payload}", value)
	}
}

// OfflineError is the refusal of a command to a device that reports offline
// and does not accept commands while offline.
type OfflineError struct {
	Device string
	Since  time.Time
}

// Error names the device, since when, and how to send anyway.
func (e *OfflineError) Error() string {
	return fmt.Sprintf("device %q is offline since %s; allow commands while offline on the device to send anyway",
		e.Device, e.Since.UTC().Format(time.RFC3339))
}

// PublishCommand publishes a built command on the device's connection. A
// device that reports offline refuses it unless it allows commands while
// offline; unknown is not refused, since it mostly means the broker is down
// and the publish fails on its own with a clearer reason.
func (d *Devices) PublishCommand(ctx context.Context, device *models.MQTTDevice, msg CommandMessage) error {
	if err := d.refuseOffline(device); err != nil {
		return err
	}
	driver, ok := d.resolve(device.ConnectionID)
	if !ok {
		return fmt.Errorf("connection for device %q is not running", device.Name)
	}
	conn, ok := driver.(*mqtt.Driver)
	if !ok {
		return fmt.Errorf("device %q points at a connection that is not MQTT", device.Name)
	}
	qos, retain := DeliveryOf(device, msg)
	return conn.Publish(ctx, msg.Topic, qos, retain, []byte(msg.Payload))
}

// DeliveryOf is the QoS and retain flag a command is published with: what the
// payload script asked for, else the device's settings.
func DeliveryOf(device *models.MQTTDevice, msg CommandMessage) (byte, bool) {
	qos, retain := byte(device.CommandQoS), device.CommandRetain
	if msg.QoS != nil {
		qos = *msg.QoS
	}
	if msg.Retain != nil {
		retain = *msg.Retain
	}
	return qos, retain
}

// refuseOffline refuses a command to an offline device that does not take
// commands while offline.
func (d *Devices) refuseOffline(device *models.MQTTDevice) error {
	if device.OfflineCommands {
		return nil
	}
	if availability, at, running := d.Availability(device.ID); running && availability == AvailabilityOffline {
		return &OfflineError{Device: device.Name, Since: at}
	}
	return nil
}
