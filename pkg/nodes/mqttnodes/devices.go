package mqttnodes

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
)

// MQTT devices are declared once — topics, decoder, control kind, payload
// script — and these nodes are how flows reach them. Without them a flow had
// to re-subscribe the raw topics and re-implement both scripts; with them a
// flow reads, commands, and triggers on the same accumulated state a
// dashboard row shows, through the same coercion and the same payload script
// a row's press goes through.

// deviceField is the shared picker naming which device a node acts on.
func deviceField(help string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:       "device",
		Label:    "Device",
		Type:     flow.FieldString,
		Control:  "mqtt-device",
		Required: true,
		Help:     help,
	}
}

// deviceValueField names one reported value of a device. The keys come from
// what the device's own state script returns, so it is a free string rather
// than a pick from a catalog.
func deviceValueField(help string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "field",
		Label:       "Value",
		Type:        flow.FieldString,
		Placeholder: "brightness",
		Help:        help,
	}
}

// resolveDevice looks a device up by ID or, failing that, by name — the same
// bargain the control nodes make: graphs store IDs so a rename cannot break
// them, but a name is what an operator types.
func resolveDevice(ref string) (*models.MQTTDevice, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("no device selected")
	}
	if id, err := uuid.Parse(ref); err == nil {
		if m, err := models.FindMQTTDeviceByID(id); err == nil {
			return m, nil
		}
	}
	all, err := models.ListMQTTDevices()
	if err != nil {
		return nil, err
	}
	for _, m := range all {
		if strings.EqualFold(m.Name, ref) {
			return m, nil
		}
	}
	return nil, fmt.Errorf("MQTT device %q not found", ref)
}

// runningDevices returns the device runner, which exists exactly when a
// server is serving.
func runningDevices() (*mqttrelay.Devices, error) {
	if mqttrelay.D == nil {
		return nil, fmt.Errorf("the MQTT device runner is not running")
	}
	return mqttrelay.D, nil
}

// deviceMeta is the metadata every device node stamps on what it emits.
func deviceMeta(m *models.MQTTDevice) map[string]any {
	return map[string]any{
		"device_id":   m.ID.String(),
		"device_name": m.Name,
		"device_kind": m.Control,
	}
}

// DeviceGetNode reads a device's accumulated state. It is an output-only pure
// source like control-get: no input port and no failure branch, so it runs
// when a value edge pulls it and a failure surfaces at the node that asked.
type DeviceGetNode struct{}

func (DeviceGetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "mqtt-device-get",
		Label:             "Device Value",
		Folder:            flow.FolderDevices,
		Description:       "Reads a device's accumulated state, MQTT or ESPHome, pulled when a consumer needs it",
		Icon:              "las la-microchip",
		Pure:              true,
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Value", Type: flow.PortAny,
			// The whole state is an object of whatever the decoder returns; a
			// single named value could be anything, so the port only narrows
			// when nothing is named.
			TypeRule: &flow.PortTypeRule{
				Cases:   []flow.PortTypeCase{{Match: map[string][]string{"field": {""}}, Type: flow.PortObject}},
				Default: flow.PortAny,
			},
		}},
		ConfigSpec: []engine.FieldSpec{
			deviceField("Device to read. Its state becomes the payload, and its name the topic."),
			deviceValueField("Which reported value to read. Empty reads the whole state object. " +
				"The keys are whatever the device's state script returns."),
		},
	}
}

// deviceGetConfig is the decoded mqtt-device-get node configuration.
type deviceGetConfig struct {
	Device string `json:"device"`
	Field  string `json:"field"`
}

// Execute emits the device's current state. A value the device has not
// reported yet reads as nothing rather than failing: it is a value that has
// not arrived, not a binding that is wrong.
func (DeviceGetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg deviceGetConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	m, err := resolveDevice(cfg.Device)
	if err != nil {
		return nil, err
	}
	runner, err := runningDevices()
	if err != nil {
		return nil, err
	}
	status, running := runner.Status(m.ID)
	if !running {
		return nil, fmt.Errorf("device %q is not running; is its connection enabled?", m.Name)
	}

	meta := deviceMeta(m)
	// A value restored from before a restart is labelled as such, so a flow
	// can decide whether an unconfirmed reading is good enough.
	meta["stale"] = status.Stale
	meta["availability"] = status.Availability
	if !status.ReportedAt.IsZero() {
		meta["reported_at"] = status.ReportedAt.UTC().Format(time.RFC3339Nano)
	}
	var payload any = status.State
	if field := strings.TrimSpace(cfg.Field); field != "" {
		payload = status.State[field]
		meta["device_field"] = field
	}
	return engine.One("out", &engine.Msg{Payload: payload, Topic: m.Name, Meta: meta}), nil
}

// DeviceSetNode commands a device: the same coercion against its control kind
// and the same payload script a dashboard row's press goes through, so a flow
// and a wall panel press the same way.
type DeviceSetNode struct{}

func (DeviceSetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "mqtt-device-set",
		Label:       "Command Device",
		Folder:      flow.FolderDevices,
		Description: "Commands a device: through its payload script for MQTT, its entity for ESPHome",
		Icon:        "las la-paper-plane",
		Retryable:   true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []engine.FieldSpec{
			deviceField("Device to command. It must have a payload script."),
			{
				ID:          "field",
				Label:       "Attribute",
				Type:        flow.FieldString,
				Placeholder: "on",
				Help: "Attribute of the device's control kind being set: \"on\" for a switch, " +
					"\"brightness\" for a light, \"position\" for a cover. Empty means the kind's " +
					"primary attribute.",
			},
			{
				ID:      "source",
				Label:   "Value from",
				Type:    flow.FieldSelect,
				Default: "message",
				Options: []engine.Option{
					{Value: "message", Label: "Message property"},
					{Value: "value", Label: "Fixed value"},
				},
			},
			{
				ID:          "property",
				Label:       "Property",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload",
				ShowIf:      &flow.ShowIf{Field: "source", Equals: "message"},
				Help:        "Dot path the value is read from, such as payload.brightness.",
			},
			{
				ID:          "value",
				Label:       "Value",
				Type:        flow.FieldString,
				Placeholder: "on",
				ShowIf:      &flow.ShowIf{Field: "source", Equals: "value"},
				Help:        "Value to send. {{ }} expressions are evaluated against the message first.",
			},
		},
	}
}

// deviceSetConfig is the decoded mqtt-device-set node configuration.
type deviceSetConfig struct {
	Device   string `json:"device"`
	Field    string `json:"field"`
	Source   string `json:"source"`
	Property string `json:"property"`
	Value    string `json:"value"`
}

// Execute builds and publishes the command, passing the message through. The
// value is checked against the device's kind before the script runs, so a
// slider outside its bounds never reaches the broker — exactly the dashboard
// command path.
func (DeviceSetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg deviceSetConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	m, err := resolveDevice(cfg.Device)
	if err != nil {
		return nil, err
	}
	if !mqttrelay.Commandable(m) {
		return nil, fmt.Errorf("device %q takes no commands", m.Name)
	}
	runner, err := runningDevices()
	if err != nil {
		return nil, err
	}

	// Pick any connected input as the message to forward.
	var msg *engine.Msg
	for _, in := range inputs {
		if in != nil {
			msg = in
			break
		}
	}
	if msg == nil {
		msg = &engine.Msg{}
	}

	// Resolve the value to send.
	var value any
	if cfg.Source == "value" {
		rendered, err := ec.Interpolate(cfg.Value, msg)
		if err != nil {
			return nil, err
		}
		value = rendered
	} else {
		property := strings.TrimSpace(cfg.Property)
		if property == "" {
			property = "payload"
		}
		found := false
		value, found = resolveMsgPath(msg, property)
		if !found {
			return nil, fmt.Errorf("message has no property %q to send", property)
		}
	}

	// Hold it to the kind, then send it over the device's transport, with
	// what the device last reported for that value as `previous`, which the
	// HTTP path can only take on the browser's word.
	field, _, err := mqttrelay.ResolveCommand(m, cfg.Field, value)
	if err != nil {
		return nil, fmt.Errorf("cannot command device %q: %w", m.Name, err)
	}
	var previous any
	if status, running := runner.Status(m.ID); running {
		previous = status.State[field]
	}
	result, err := runner.Command(ctx, m, field, value, nil, previous)
	if err != nil {
		return nil, err
	}

	// The input is shared with every other node wired to the same output, so
	// the metadata goes on a copy: two workers writing into msg.Meta at once is
	// a fatal concurrent map write.
	out := &engine.Msg{ID: msg.ID, Payload: msg.Payload, Topic: msg.Topic, Meta: nodeutil.CloneMeta(msg.Meta), Group: msg.Group}
	for k, v := range deviceMeta(m) {
		out.Meta[k] = v
	}
	out.Meta["device_field"] = result.Field
	out.Meta["device_value"] = result.Value
	// An empty topic is the payload script saying this change means nothing,
	// or an ESPHome device, which has none; the message passes through either
	// way.
	out.Meta["device_topic"] = result.Topic
	return engine.One("out", out), nil
}

// resolveMsgPath walks a dot path into a message, mirroring the property
// resolution the core nodes use: the first segment selects payload, topic, or
// meta, and a bare path resolves against the payload.
func resolveMsgPath(msg *engine.Msg, path string) (any, bool) {
	parts := strings.Split(path, ".")
	var cur any
	switch parts[0] {
	case "topic":
		cur = msg.Topic
		parts = parts[1:]
	case "meta":
		m := make(map[string]any, len(msg.Meta))
		for k, v := range msg.Meta {
			m[k] = v
		}
		cur = m
		parts = parts[1:]
	case "payload":
		cur = msg.Payload
		parts = parts[1:]
	default:
		cur = msg.Payload
	}
	for _, part := range parts {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = obj[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// DeviceChangedNode starts a run when a device's decoder merges new values —
// the trigger half of what previously required re-subscribing the raw topics
// with an MQTT In node and re-implementing the state script.
type DeviceChangedNode struct{}

func (DeviceChangedNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "mqtt-device-changed",
		Label:             "Device Changed",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderDevices,
		Description:       "Starts a run when a device, MQTT or ESPHome, reports new values",
		Icon:              "las la-satellite-dish",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			deviceField("Device to watch."),
			deviceValueField("Fire only when a message carried this value. Empty fires on every " +
				"decoded message."),
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help:  "Sample payload a test run starts with.",
			},
		},
	}
}

// deviceChangedConfig is the decoded mqtt-device-changed node configuration.
type deviceChangedConfig struct {
	Device string `json:"device"`
	Field  string `json:"field"`
}

// Execute passes the trigger's seeded message through, fabricating a sample
// when a test run started with none.
func (DeviceChangedNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg deviceChangedConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	m, err := resolveDevice(cfg.Device)
	if err != nil {
		return nil, err
	}
	sample := deviceChangeMsg(mqttrelay.DeviceChange{
		DeviceID: m.ID,
		Name:     m.Name,
		Kind:     m.Control,
		Values:   map[string]any{},
		State:    map[string]any{},
	}, strings.TrimSpace(cfg.Field))
	return engine.One("out", sample), nil
}

// Start watches the device runner and emits a run per decoded message.
func (DeviceChangedNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg deviceChangedConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	m, err := resolveDevice(cfg.Device)
	if err != nil {
		return err
	}
	runner, err := runningDevices()
	if err != nil {
		return err
	}

	field := strings.TrimSpace(cfg.Field)
	unwatch := runner.Watch(func(c mqttrelay.DeviceChange) {
		if c.DeviceID != m.ID {
			return
		}
		if field != "" {
			if _, carried := c.Values[field]; !carried {
				return
			}
		}
		emit(deviceChangeMsg(c, field))
	})
	defer unwatch()

	<-ctx.Done()
	return nil
}

// deviceChangeMsg renders a device change as the message the trigger emits.
// The topic is the MQTT topic that spoke, which a wildcard device needs to say
// which of its things it heard.
func deviceChangeMsg(c mqttrelay.DeviceChange, field string) *engine.Msg {
	payload := map[string]any{
		"device_id": c.DeviceID.String(),
		"name":      c.Name,
		"kind":      c.Kind,
		"topic":     c.Topic,
		"values":    c.Values,
		"state":     c.State,
	}
	meta := map[string]any{
		"device_id":   c.DeviceID.String(),
		"device_name": c.Name,
		"device_kind": c.Kind,
	}
	if mqttrelay.D != nil {
		if availability, _, ok := mqttrelay.D.Availability(c.DeviceID); ok {
			meta["availability"] = availability
		}
	}
	if field != "" {
		payload["field"] = field
		payload["value"] = c.Values[field]
		meta["device_field"] = field
	}
	return &engine.Msg{Payload: payload, Topic: c.Topic, Meta: meta}
}

// DeviceAvailabilityNode starts a run when a device goes online, offline, or
// unknown.
type DeviceAvailabilityNode struct{}

func (DeviceAvailabilityNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "mqtt-device-availability",
		Label:             "Device Availability",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderDevices,
		Description:       "Starts a run when a device goes offline, comes back online, or becomes unknown",
		Icon:              "las la-plug",
		TestWithoutParams: true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "device",
				Label:   "Device",
				Type:    flow.FieldString,
				Control: "mqtt-device",
				Help:    "Device to watch. Empty watches every device.",
			},
			{
				ID:    "to",
				Label: "To",
				Type:  flow.FieldSelect,
				Options: []engine.Option{
					{Value: "", Label: "Any change"},
					{Value: mqttrelay.AvailabilityOffline, Label: "Offline"},
					{Value: mqttrelay.AvailabilityOnline, Label: "Online"},
					{Value: mqttrelay.AvailabilityUnknown, Label: "Unknown"},
				},
				Help: "Only fire when the device becomes this. Unknown is the broker being down or a device " +
					"whose availability topic has not spoken since it came back.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help:  "Sample payload a test run starts with.",
			},
		},
	}
}

// deviceAvailabilityConfig is the decoded mqtt-device-availability configuration.
type deviceAvailabilityConfig struct {
	Device string `json:"device"`
	To     string `json:"to"`
}

// Execute passes the trigger's seeded message through, or reports the
// device's current availability on a test run that started with none.
func (DeviceAvailabilityNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	var cfg deviceAvailabilityConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	change := mqttrelay.AvailabilityChange{Current: mqttrelay.AvailabilityUnknown, At: ec.Clock().Now()}
	if strings.TrimSpace(cfg.Device) != "" {
		m, err := resolveDevice(cfg.Device)
		if err != nil {
			return nil, err
		}
		change.DeviceID, change.Name = m.ID, m.Name
		if mqttrelay.D != nil {
			if availability, at, ok := mqttrelay.D.Availability(m.ID); ok {
				change.Current, change.At = availability, at
			}
		}
	}
	return engine.One("out", availabilityMsg(change)), nil
}

// Start watches availability changes and emits a run per matching change.
func (DeviceAvailabilityNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg deviceAvailabilityConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	var only uuid.UUID
	if strings.TrimSpace(cfg.Device) != "" {
		m, err := resolveDevice(cfg.Device)
		if err != nil {
			return err
		}
		only = m.ID
	}
	runner, err := runningDevices()
	if err != nil {
		return err
	}
	to := strings.TrimSpace(cfg.To)
	unwatch := runner.WatchAvailability(func(c mqttrelay.AvailabilityChange) {
		if only != uuid.Nil && c.DeviceID != only {
			return
		}
		if to != "" && c.Current != to {
			return
		}
		emit(availabilityMsg(c))
	})
	defer unwatch()

	<-ctx.Done()
	return nil
}

// availabilityMsg renders an availability change as the message the trigger
// emits.
func availabilityMsg(c mqttrelay.AvailabilityChange) *engine.Msg {
	payload := map[string]any{
		"device_id":    c.DeviceID.String(),
		"name":         c.Name,
		"availability": c.Current,
		"previous":     c.Previous,
		"source":       c.Source,
		"at":           c.At.UTC().Format(time.RFC3339),
	}
	meta := map[string]any{
		"device_id":    c.DeviceID.String(),
		"device_name":  c.Name,
		"availability": c.Current,
	}
	return &engine.Msg{Payload: payload, Topic: c.Name, Meta: meta}
}
