package mcpserver

import (
	"context"
	"github.com/grmrgecko/karakuri/pkg/access"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/mqttrelay"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type connectionIDInput struct {
	ID string `json:"id" jsonschema:"Connection UUID."`
}

type listAIModelsInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"AI provider connection UUID."`
	Refresh      bool   `json:"refresh,omitempty" jsonschema:"Bypass the cached catalog and list from the provider now."`
}

type saveConnectionInput struct {
	ID      string         `json:"id,omitempty" jsonschema:"Connection UUID to update; omit to create a new connection."`
	Name    string         `json:"name,omitempty" jsonschema:"Display name; required when creating."`
	Kind    string         `json:"kind,omitempty" jsonschema:"Driver kind from list_connection_kinds; required when creating, fixed after."`
	Config  map[string]any `json:"config,omitempty" jsonschema:"Driver configuration. On update, a secret field holding the masked sentinel keeps the stored value."`
	Enabled *bool          `json:"enabled,omitempty" jsonschema:"Whether the connection runs; a create defaults to true."`
}

type getHAStatesInput struct {
	ConnectionID      string   `json:"connection_id" jsonschema:"Home Assistant connection UUID."`
	EntityIDs         []string `json:"entity_ids,omitempty" jsonschema:"Exact entity IDs to return; omit for all."`
	Filter            string   `json:"filter,omitempty" jsonschema:"Case-insensitive substring matched against entity ID and friendly name."`
	IncludeAttributes bool     `json:"include_attributes,omitempty" jsonschema:"Also return each entity's attributes; the list is much larger with them."`
	Area              string   `json:"area,omitempty" jsonschema:"Only entities in this area, by ID or name; an entity with no area of its own sits in its device's."`
	Floor             string   `json:"floor,omitempty" jsonschema:"Only entities on this floor, by ID or name."`
	Device            string   `json:"device,omitempty" jsonschema:"Only entities of this device, by ID or name."`
	Label             string   `json:"label,omitempty" jsonschema:"Only entities with this label (their own or their device's), by ID or name."`
}

type getHARegistryInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"Home Assistant connection UUID."`
}

// nameOrID reports whether a filter names an ID or a name, ignoring case.
// An empty filter matches everything.
func nameOrID(filter, id, name string) bool {
	if filter == "" {
		return true
	}
	return strings.EqualFold(filter, id) || strings.EqualFold(filter, name)
}

type callHAServiceInput struct {
	ConnectionID string         `json:"connection_id" jsonschema:"Home Assistant connection UUID."`
	Domain       string         `json:"domain" jsonschema:"Service domain, e.g. light."`
	Service      string         `json:"service" jsonschema:"Service name, e.g. turn_on."`
	ServiceData  map[string]any `json:"service_data,omitempty" jsonschema:"Service call data."`
	Target       map[string]any `json:"target,omitempty" jsonschema:"Service call target, e.g. {entity_id: light.kitchen}."`
}

type publishMQTTInput struct {
	ConnectionID string         `json:"connection_id" jsonschema:"MQTT connection UUID."`
	Topic        string         `json:"topic" jsonschema:"Topic to publish on."`
	Payload      string         `json:"payload,omitempty" jsonschema:"Message body, published as UTF-8 text. Omit when sending a template."`
	Template     string         `json:"template,omitempty" jsonschema:"Body template with {{ }} expressions, rendered against vars."`
	Format       string         `json:"template_format,omitempty" jsonschema:"How the template is read: text, json, or script."`
	Vars         map[string]any `json:"vars,omitempty" jsonschema:"Values the template's expressions read."`
	QoS          int            `json:"qos,omitempty" jsonschema:"Delivery quality of service, 0 to 2."`
	Retain       bool           `json:"retain,omitempty" jsonschema:"Whether the broker retains the message for later subscribers."`
}

// addConnectionTools registers the connection, Home Assistant, and MQTT tools.
func addConnectionTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_connections",
		Description: "List connections with masked configuration and live driver status.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		items, err := connops.ListFor(ctx, callerID(ctx))
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"connections": items}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_connection_kinds",
		Description: "List the registered connection kinds with their config field schemas.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"kinds": connops.Kinds()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_connection",
		Description: "Create a connection (no id) or patch one (with id), hot-applying the driver change and rearming dependent flows.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveConnectionInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		if in.ID == "" {
			if strings.TrimSpace(in.Name) == "" || strings.TrimSpace(in.Kind) == "" {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("creating a connection needs name and kind"))
			}
			c, err := connops.Create(ctx, callerID(ctx), in.Name, in.Kind, in.Config, in.Enabled == nil || *in.Enabled)
			if err != nil {
				return nil, nil, opErr(err)
			}
			item, err := connops.ItemFor(ctx, callerID(ctx), c)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, item, nil
		}
		c, err := useConnection(ctx, in.ID, access.Edit)
		if err != nil {
			return nil, nil, err
		}
		if in.Config != nil {
			if err := connops.CheckConfigConnections(ctx, callerID(ctx), c.Kind, in.Config); err != nil {
				return nil, nil, opErr(err)
			}
		}
		patch := connops.Patch{Enabled: in.Enabled}
		if in.Name != "" {
			patch.Name = &in.Name
		}
		if in.Config != nil {
			patch.Config = &in.Config
		}
		if err := connops.Update(ctx, c, patch); err != nil {
			return nil, nil, opErr(err)
		}
		flowops.RearmDependents(c.ID)
		item, err := connops.ItemFor(ctx, callerID(ctx), c)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, item, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_connection",
		Description: "Delete a connection. Refused while any flow graph references it.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in connectionIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		c, err := useConnection(ctx, in.ID, access.Admin)
		if err != nil {
			return nil, nil, err
		}
		if err := connops.Delete(ctx, c); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"deleted": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "test_connection",
		Description: "Run the kind's connectivity test with a throwaway client against the stored configuration.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in connectionIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsWrite); err != nil {
			return nil, nil, err
		}
		c, err := useConnection(ctx, in.ID, access.Edit)
		if err != nil {
			return nil, nil, err
		}
		result, err := connops.Test(ctx, c)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, result, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_ha_states",
		Description: "Read entity states from a running Home Assistant connection with each entity's area, floor, device, and labels, optionally filtered by ID, substring, area, floor, device, or label.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getHAStatesInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		driver, err := connops.HADriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		states, err := driver.StatesCached(ctx)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("Home Assistant is not connected: " + err.Error()))
		}

		wanted := map[string]bool{}
		for _, id := range in.EntityIDs {
			wanted[id] = true
		}
		filter := strings.ToLower(strings.TrimSpace(in.Filter))
		reg := driver.Registry()
		labelNames := map[string]string{}
		for _, l := range reg.Labels {
			labelNames[l.ID] = l.Name
		}

		items := make([]map[string]any, 0, len(states))
		for _, st := range states {
			if len(wanted) > 0 && !wanted[st.EntityID] {
				continue
			}
			info := reg.Describe(st.EntityID)
			if !nameOrID(strings.TrimSpace(in.Area), info.AreaID, info.Area) ||
				!nameOrID(strings.TrimSpace(in.Floor), info.FloorID, info.Floor) ||
				!nameOrID(strings.TrimSpace(in.Device), info.DeviceID, info.Device) {
				continue
			}
			if label := strings.TrimSpace(in.Label); label != "" {
				found := false
				for _, id := range info.Labels {
					if nameOrID(label, id, labelNames[id]) {
						found = true
					}
				}
				if !found {
					continue
				}
			}
			name, _ := st.Attributes["friendly_name"].(string)
			if filter != "" && !strings.Contains(strings.ToLower(st.EntityID+" "+name), filter) {
				continue
			}
			item := map[string]any{"entity_id": st.EntityID, "state": st.State}
			if name != "" {
				item["friendly_name"] = name
			}
			if st.LastChanged != "" {
				item["last_changed"] = st.LastChanged
			}
			if info.Area != "" {
				item["area"] = info.Area
			}
			if info.Floor != "" {
				item["floor"] = info.Floor
			}
			if info.Device != "" {
				item["device"] = info.Device
			}
			if len(info.Labels) > 0 {
				item["labels"] = info.Labels
			}
			if in.IncludeAttributes {
				item["attributes"] = st.Attributes
			}
			items = append(items, item)
		}
		sort.Slice(items, func(i, j int) bool {
			return items[i]["entity_id"].(string) < items[j]["entity_id"].(string)
		})
		return nil, map[string]any{"states": items}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_ha_registry",
		Description: "List a running Home Assistant connection's floors, areas, devices, and labels, and whether each registry " +
			"could be read. Use the IDs as area_id, floor_id, device_id, and label_id targets.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getHARegistryInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		driver, err := connops.HADriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		reg := driver.Registry()
		return nil, map[string]any{"floors": reg.Floors, "areas": reg.Areas, "devices": reg.Devices,
			"labels": reg.Labels, "status": reg.Status}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "call_ha_service",
		Description: "Invoke a Home Assistant service, e.g. light.turn_on with a target entity.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in callHAServiceInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsInvoke); err != nil {
			return nil, nil, err
		}
		driver, err := connops.HADriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		result, err := driver.CallService(ctx, in.Domain, in.Service, in.ServiceData, in.Target)
		if err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("service call failed: " + err.Error()))
		}
		return nil, map[string]any{"result": result}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "publish_mqtt",
		Description: "Publish one message through a running MQTT connection: a literal payload, or a text/json/script template rendered against vars.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in publishMQTTInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsInvoke); err != nil {
			return nil, nil, err
		}
		driver, err := connops.MQTTDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		topic := strings.TrimSpace(in.Topic)
		if topic == "" {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("topic is required"))
		}
		if in.QoS < 0 || in.QoS > 2 {
			return nil, nil, opErr(apperrors.ErrValidation.WithMessage("qos must be 0, 1, or 2"))
		}
		msg, err := mqttrelay.RenderPublishBody(ctx, mqttrelay.PublishSpec{
			Payload:  in.Payload,
			Template: in.Template,
			Format:   in.Format,
			Vars:     in.Vars,
		})
		if err != nil {
			return nil, nil, opErr(err)
		}
		// A script may route its own message; anything else goes where the
		// caller said.
		if chosen := strings.TrimSpace(msg.Topic); chosen != "" {
			topic = chosen
		}
		if err := driver.Publish(ctx, topic, byte(in.QoS), in.Retain, []byte(msg.Payload)); err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage(err.Error()))
		}
		return nil, map[string]any{"published": true, "topic": topic}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_ai_models",
		Description: "List the model identifiers an AI provider connection serves, for the ai-model node's model field.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listAIModelsInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		driver, err := connops.AIDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		var models []string
		if in.Refresh {
			models, err = driver.RefreshModels(ctx)
		} else {
			models, err = driver.Models(ctx)
		}
		if err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("listing models: " + err.Error()))
		}
		return nil, map[string]any{"models": models}, nil
	})
}
