package mcpserver

import (
	"context"
	"errors"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/autoimport"
	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/entitydevices"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"
	"github.com/grmrgecko/karakuri/pkg/zigbee2mqtt"
	"strings"
	"time"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type saveMQTTDeviceInput struct {
	ID            string         `json:"id,omitempty" jsonschema:"Device UUID to replace; omit to create a new device."`
	Name          string         `json:"name" jsonschema:"Display name, unique across devices."`
	ConnectionID  string         `json:"connection_id" jsonschema:"MQTT connection UUID the device speaks on."`
	Topics        []string       `json:"topics" jsonschema:"Topic filters the device listens on, wildcards allowed."`
	StateScript   string         `json:"state_script" jsonschema:"JavaScript reading one message into the values it carries; see karakuri://guides/mqtt-device-state-script."`
	Control       string         `json:"control,omitempty" jsonschema:"Control kind the device is drawn with, from list_control_kinds."`
	Options       map[string]any `json:"options,omitempty" jsonschema:"Settings of that kind: bounds, choices, buttons, features."`
	PayloadScript string         `json:"payload_script,omitempty" jsonschema:"JavaScript turning a control change into {topic, payload}; see karakuri://guides/mqtt-device-payload-script. Without one the device takes no commands."`
	Description   string         `json:"description,omitempty" jsonschema:"What the device is."`

	AvailabilityTopic   string `json:"availability_topic,omitempty" jsonschema:"Topic carrying online and offline payloads, such as a last-will topic; empty for none."`
	AvailabilityOnline  string `json:"availability_online,omitempty" jsonschema:"Payload meaning online, default online. A JSON object whose state equals it also matches."`
	AvailabilityOffline string `json:"availability_offline,omitempty" jsonschema:"Payload meaning offline, default offline. A JSON object whose state equals it also matches."`
	ExpectEveryS        int    `json:"expect_every_s,omitempty" jsonschema:"Mark the device offline after this many seconds with no message; 0 is off, otherwise at least 60."`
	OfflineCommands     bool   `json:"offline_commands,omitempty" jsonschema:"Send commands while the device is offline, for sleeping devices that queue them."`

	StateQoS         int  `json:"state_qos,omitempty" jsonschema:"QoS of the state and availability subscriptions: 0 (default), 1, or 2."`
	CommandQoS       int  `json:"command_qos,omitempty" jsonschema:"QoS commands are published at: 0 (default), 1, or 2. A payload script may return qos for one message."`
	CommandRetain    bool `json:"command_retain,omitempty" jsonschema:"Publish commands retained. A payload script may return retain for one message."`
	StateTimeoutMS   int  `json:"state_timeout_ms,omitempty" jsonschema:"State script time limit in milliseconds, 50 to 5000; 0 or omitted is the default of 250."`
	PayloadTimeoutMS int  `json:"payload_timeout_ms,omitempty" jsonschema:"Payload script time limit in milliseconds, 100 to 10000; 0 or omitted is the default of 2000."`
}

type discoverZigbee2MQTTInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"MQTT connection Zigbee2MQTT publishes on."`
	BaseTopic    string `json:"base_topic,omitempty" jsonschema:"Zigbee2MQTT base topic, default zigbee2mqtt."`
}

type addZigbee2MQTTInput struct {
	ConnectionID  string   `json:"connection_id" jsonschema:"MQTT connection Zigbee2MQTT publishes on."`
	BaseTopic     string   `json:"base_topic,omitempty" jsonschema:"Zigbee2MQTT base topic, default zigbee2mqtt."`
	IEEEAddresses []string `json:"ieee_addresses" jsonschema:"IEEE addresses of the devices to add, from discover_zigbee2mqtt_devices."`
}

type esphomeConnInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"ESPHome connection UUID."`
}

type addESPHomeInput struct {
	ConnectionID string   `json:"connection_id" jsonschema:"ESPHome connection UUID."`
	ObjectIDs    []string `json:"object_ids" jsonschema:"Object IDs of the entities to add, from discover_entities or discover_esphome_entities."`
}

type callESPHomeActionInput struct {
	ConnectionID string         `json:"connection_id" jsonschema:"ESPHome connection UUID."`
	Action       string         `json:"action" jsonschema:"Action name from the node's api: actions:."`
	Args         map[string]any `json:"args,omitempty" jsonschema:"Arguments by name."`
}

type deviceIDInput struct {
	ID string `json:"id" jsonschema:"Device UUID."`
}

type commandMQTTDeviceInput struct {
	ID       string `json:"id" jsonschema:"Device UUID."`
	Field    string `json:"field,omitempty" jsonschema:"Attribute of the device's control kind being set; omit for the kind's primary attribute."`
	Value    any    `json:"value,omitempty" jsonschema:"The value being commanded."`
	On       *bool  `json:"on,omitempty" jsonschema:"Direction, for an attribute that has two."`
	Previous any    `json:"previous,omitempty" jsonschema:"What the value was before."`
}

type testMQTTDeviceScriptInput struct {
	Mode   string `json:"mode" jsonschema:"Which script is being tested: state or payload."`
	Script string `json:"script" jsonschema:"The script source."`
	// State mode.
	Topic     string         `json:"topic,omitempty" jsonschema:"State mode: topic the sample arrived on."`
	Payload   string         `json:"payload,omitempty" jsonschema:"State mode: sample payload."`
	State     map[string]any `json:"state,omitempty" jsonschema:"State mode: values the device already holds."`
	Retained  bool           `json:"retained,omitempty" jsonschema:"State mode: test the sample as a message the broker delivered from its retained store."`
	TimeoutMS int            `json:"timeout_ms,omitempty" jsonschema:"Time limit in milliseconds to test under, as the device would set it; 0 is the default for the mode."`
	// Payload mode.
	Field    string         `json:"field,omitempty" jsonschema:"Payload mode: which value is being set."`
	Value    any            `json:"value,omitempty" jsonschema:"Payload mode: the value being commanded."`
	On       *bool          `json:"on,omitempty" jsonschema:"Payload mode: direction, for a control that has two."`
	Previous any            `json:"previous,omitempty" jsonschema:"Payload mode: what the value was before."`
	Control  string         `json:"control,omitempty" jsonschema:"Payload mode: control kind to coerce the command against, as the device declares."`
	Options  map[string]any `json:"options,omitempty" jsonschema:"Payload mode: settings of that kind."`
}

// deviceDefinition converts the tool input into the relay's definition type.
func (in saveMQTTDeviceInput) definition() (mqttrelay.Definition, error) {
	connID, err := uuid.Parse(in.ConnectionID)
	if err != nil {
		return mqttrelay.Definition{}, apperrors.ErrValidation.WithMessage("invalid connection ID")
	}
	return mqttrelay.Definition{
		Name:          in.Name,
		ConnectionID:  connID,
		Topics:        in.Topics,
		StateScript:   in.StateScript,
		Control:       in.Control,
		Options:       in.Options,
		PayloadScript: in.PayloadScript,
		Description:   in.Description,

		AvailabilityTopic:   in.AvailabilityTopic,
		AvailabilityOnline:  in.AvailabilityOnline,
		AvailabilityOffline: in.AvailabilityOffline,
		ExpectEveryS:        in.ExpectEveryS,
		OfflineCommands:     in.OfflineCommands,
		StateQoS:            in.StateQoS,
		CommandQoS:          in.CommandQoS,
		CommandRetain:       in.CommandRetain,
		StateTimeoutMS:      in.StateTimeoutMS,
		PayloadTimeoutMS:    in.PayloadTimeoutMS,
	}, nil
}

// addDeviceTools registers the MQTT device tools.
func addDeviceTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_mqtt_devices",
		Description: "List every declared MQTT device with its decoded state, run status, and last-message time.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		devices, err := models.ListMQTTDevices()
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithMessage(err.Error()))
		}
		usable, err := access.ConnectionIDs(ctx, callerID(ctx), access.Use)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithError(err))
		}
		views := make([]mqttrelay.View, 0, len(devices))
		for _, d := range devices {
			if !access.Allowed(usable, d.ConnectionID) {
				continue
			}
			views = append(views, mqttrelay.BuildView(d, mqttrelay.D))
		}
		return nil, map[string]any{"devices": views}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_mqtt_device",
		Description: "Declare an MQTT device (no id) or replace one (with id), restarting its subscriptions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveMQTTDeviceInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		def, err := in.definition()
		if err != nil {
			return nil, nil, opErr(err)
		}
		id := uuid.Nil
		if in.ID != "" {
			if id, err = uuid.Parse(in.ID); err != nil {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid device id"))
			}
			existing, err := models.FindMQTTDeviceByID(id)
			if err == nil {
				err = useDeviceConnection(ctx, existing)
			}
			if err != nil {
				return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("device not found"))
			}
		}
		d, err := mqttrelay.SaveDefinition(id, def, mqttrelay.Origin{})
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, mqttrelay.BuildView(d, mqttrelay.D), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_zigbee2mqtt_devices",
		Description: "Read Zigbee2MQTT's retained device list on a connection and propose a karakuri device for each: kind, whether it takes commands, the other values it reports, and whether it is already added. Reads only; publishes nothing.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in discoverZigbee2MQTTInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		driver, err := connops.MQTTDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		base := zigbee2mqtt.NormalizeBase(in.BaseTopic)
		devices, err := zigbee2mqtt.ReadDevices(ctx, driver, base)
		if err != nil {
			return nil, nil, opErr(err)
		}
		existing, err := models.ListMQTTDevices()
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithMessage(err.Error()))
		}
		return nil, map[string]any{"base_topic": base, "devices": zigbee2mqtt.Discover(devices, existing, connID, base)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_zigbee2mqtt_devices",
		Description: "Create MQTT devices for Zigbee2MQTT devices by IEEE address, with kind, options, state and payload scripts, and availability topic generated from their exposes. Each device succeeds or fails on its own.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addZigbee2MQTTInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		if len(in.IEEEAddresses) == 0 {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("name at least one IEEE address"))
		}
		driver, err := connops.MQTTDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		result, err := zigbee2mqtt.Add(ctx, driver, connID, in.BaseTopic, in.IEEEAddresses)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, result, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "regenerate_mqtt_device",
		Description: "Rebuild the kind, options, scripts, and topics of a device added from Zigbee2MQTT from its current exposes, replacing hand edits.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deviceIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid device id"))
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("device not found"))
		}
		if err := useDeviceConnection(ctx, device); err != nil {
			return nil, nil, err
		}
		driver, err := connops.MQTTDriver(device.ConnectionID.String())
		if err != nil {
			return nil, nil, opErr(err)
		}
		saved, err := zigbee2mqtt.Regenerate(ctx, driver, id)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, mqttrelay.BuildView(saved, mqttrelay.D), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_esphome_nodes",
		Description: "List ESPHome nodes announcing themselves by mDNS on the server's network, listening for three seconds. Nodes on other subnets are added by host instead.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		browse, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		nodes, err := esphome.Browse(browse)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("searching the network: " + err.Error()))
		}
		if nodes == nil {
			nodes = []esphome.FoundNode{}
		}
		return nil, map[string]any{"nodes": nodes}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_esphome_entities",
		Description: "List a connected ESPHome node's entities with the device kind each would become, whether it takes commands, and whether it is already added.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in esphomeConnInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		driver, err := connops.ESPHomeDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		if driver.Status().State != "connected" {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("the ESPHome node is not connected: " + driver.Status().Error))
		}
		existing, err := models.ListMQTTDevices()
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithMessage(err.Error()))
		}
		return nil, map[string]any{"node": driver.Info(), "entities": entitydevices.Discover(entitydevices.ESPHome(driver), existing, connID)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_esphome_entities",
		Description: "Create a device for each listed entity of a connected ESPHome node, by object ID. Each entity succeeds or fails on its own.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addESPHomeInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		if len(in.ObjectIDs) == 0 {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("name at least one object ID"))
		}
		driver, err := connops.ESPHomeDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		result, err := entitydevices.Add(entitydevices.ESPHome(driver), connID, in.ObjectIDs)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, result, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_entities",
		Description: "List the entities of a connected ESPHome node, Frigate server (its camera switches), Home Assistant instance, or openHAB server with the device kind each would become, whether it takes commands, and whether it is already added.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in esphomeConnInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		src, err := entitydevices.SourceFor(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		if !src.Connected() {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("the connection is not connected"))
		}
		existing, err := models.ListMQTTDevices()
		if err != nil {
			return nil, nil, opErr(apperrors.ErrInternal.WithMessage(err.Error()))
		}
		return nil, map[string]any{"transport": src.Transport(), "entities": entitydevices.Discover(src, existing, connID)}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_entities",
		Description: "Create a device for each listed entity of a connected ESPHome node, Frigate server, Home Assistant instance, or openHAB server, by object ID (<camera>/<feature> for Frigate, the entity ID for Home Assistant, the item name for openHAB). Each entity succeeds or fails on its own.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addESPHomeInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		if len(in.ObjectIDs) == 0 {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("name at least one object ID"))
		}
		src, err := entitydevices.SourceFor(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		result, err := entitydevices.Add(src, connID, in.ObjectIDs)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, result, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_auto_import_status",
		Description: "Report a connection's auto import: whether it is on, its Remove after duration, how many devices it imported, which reported devices it skipped and why, and when it last imported or removed one. Auto import is turned on in the connection's config (auto_import, remove_after, and per kind discovery_prefix, zigbee2mqtt_base, import_areas, import_domains, import_locations, import_types).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in esphomeConnInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		connID, err := uuid.Parse(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid connection id"))
		}
		if autoimport.M == nil {
			return nil, autoimport.Status{Skipped: []autoimport.Skip{}}, nil
		}
		return nil, autoimport.M.Status(connID), nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "discover_mqtt_discovery_devices",
		Description: "List the Home Assistant MQTT discovery configs retained on an MQTT connection (read for three seconds under its discovery prefix) with the device kind each would become, whether it takes commands, and whether it is already added. object_id is the discovery key.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in esphomeConnInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		devices, err := autoimport.DiscoverMQTT(ctx, in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"devices": devices}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "add_mqtt_discovery_devices",
		Description: "Create a device for each listed Home Assistant MQTT discovery config on an MQTT connection, by discovery key from discover_mqtt_discovery_devices. Each succeeds or fails on its own.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in addESPHomeInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		if len(in.ObjectIDs) == 0 {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("name at least one discovery key"))
		}
		result, err := autoimport.AddMQTT(ctx, in.ConnectionID, in.ObjectIDs)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, result, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "call_esphome_action",
		Description: "Run one of an ESPHome node's user-defined actions (api: actions: in its YAML). Arguments are checked against the action's declared types first.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in callESPHomeActionInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsInvoke); err != nil {
			return nil, nil, err
		}
		driver, err := connops.ESPHomeDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		args := in.Args
		if args == nil {
			args = map[string]any{}
		}
		if err := driver.CallAction(ctx, in.Action, args); err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage(err.Error()))
		}
		return nil, map[string]any{"called": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_mqtt_device",
		Description: "Remove an MQTT device and withdraw its subscriptions.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in deviceIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid device id"))
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("device not found"))
		}
		if err := useDeviceConnection(ctx, device); err != nil {
			return nil, nil, err
		}
		if err := mqttrelay.DeleteDevice(id); err != nil {
			return nil, nil, opErr(err)
		}
		tsdbops.DeleteOwned(id)
		return nil, map[string]any{"deleted": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "command_mqtt_device",
		Description: "Send one interaction to a device. The value is coerced against its control kind first, then the device's payload script publishes it (MQTT) or its entity receives it (ESPHome).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in commandMQTTDeviceInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsInvoke); err != nil {
			return nil, nil, err
		}
		id, err := uuid.Parse(in.ID)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid device id"))
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("device not found"))
		}
		if err := useDeviceConnection(ctx, device); err != nil {
			return nil, nil, err
		}
		if mqttrelay.D == nil {
			return nil, nil, opErr(apperrors.ErrServiceUnavailable.WithMessage("device runner is not available"))
		}
		result, err := mqttrelay.D.Command(ctx, device, in.Field, in.Value, in.On, in.Previous)
		if err != nil {
			var invalid *mqttrelay.InvalidCommandError
			if errors.As(err, &invalid) {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage(err.Error()))
			}
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage(err.Error()))
		}
		if !result.Sent {
			return nil, map[string]any{"sent": false, "reason": "the payload script chose no action"}, nil
		}
		return nil, map[string]any{"sent": true, "topic": result.Topic}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_mqtt_device_script",
		Description: "Run a device's state or payload script against a sample without touching anything: no subscription, no publish, no state. A script failure is part of the result.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in testMQTTDeviceScriptInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		if err := mqttrelay.CheckTestTimeout(in.Mode, in.TimeoutMS); err != nil {
			return nil, nil, opErr(err)
		}
		result := map[string]any{}

		if in.Mode == "payload" {
			// Hold the sample to the kind the way a real press is held. A
			// refusal is a result rather than an error: the mismatch is
			// exactly what the author is here to see.
			field, value := in.Field, in.Value
			if strings.TrimSpace(in.Control) != "" {
				device := &models.MQTTDevice{Name: "test", Control: in.Control}
				device.SetOptionMap(in.Options)
				var err error
				if field, value, err = mqttrelay.ResolveCommand(device, field, value); err != nil {
					result["error"] = err.Error()
					return nil, result, nil
				}
			}
			vars := mqttrelay.CommandVars(field, value, in.On, in.Previous)
			message, output, err := mqttrelay.RunPayloadScriptWithin(ctx, in.Script, vars,
				time.Duration(in.TimeoutMS)*time.Millisecond)
			result["output"] = output
			if err != nil {
				result["error"] = err.Error()
			} else if message.Topic == "" {
				result["value"] = map[string]any{"action": "none"}
			} else {
				result["value"] = sentMessage(message)
			}
			return nil, result, nil
		}

		values, output, err := mqttrelay.TestStateScriptWith(ctx, in.Script, in.Topic, in.Payload, in.State,
			in.Retained, time.Duration(in.TimeoutMS)*time.Millisecond)
		result["output"] = output
		if err != nil {
			result["error"] = err.Error()
		} else if values == nil {
			result["value"] = map[string]any{"action": "ignored"}
		} else {
			result["value"] = values
		}
		return nil, result, nil
	})
}

// sentMessage is what a payload script test would send, with the QoS and
// retain flag the script asked for when it asked.
func sentMessage(m mqttrelay.CommandMessage) map[string]any {
	out := map[string]any{"topic": m.Topic, "payload": m.Payload}
	if m.QoS != nil {
		out["qos"] = *m.QoS
	}
	if m.Retain != nil {
		out["retain"] = *m.Retain
	}
	return out
}
