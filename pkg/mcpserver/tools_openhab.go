package mcpserver

import (
	"context"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type listOpenHABItemsInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"openHAB connection UUID."`
	Location     string `json:"location,omitempty" jsonschema:"Only items in this location or anywhere inside it, by location group name or label, such as Kitchen or a floor."`
	Type         string `json:"type,omitempty" jsonschema:"Only items of this type, such as Switch or Number (dimensioned numbers match too)."`
	Filter       string `json:"filter,omitempty" jsonschema:"Case-insensitive text matched against item name and label."`
}

type sendOpenHABCommandInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"openHAB connection UUID."`
	Item         string `json:"item" jsonschema:"Item or group name; a group passes the command to its members."`
	Command      string `json:"command" jsonschema:"The command, as openHAB takes it: ON, OFF, a percent, h,s,b for a colour, UP, DOWN, STOP, a number, or text."`
}

func addOpenHABTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name: "list_openhab_items",
		Description: "List a running openHAB connection's items with type, state, location, and floor, ordered by floor and " +
			"location, optionally filtered by location, type, or text.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listOpenHABItemsInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		driver, err := connops.OpenHABDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		if !driver.Connected() {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage("openHAB is not connected: " + driver.Status().Error))
		}
		catalog := driver.Items()
		location := strings.TrimSpace(in.Location)
		if location != "" {
			// A location is named by its group or its label.
			for name, it := range catalog {
				if strings.EqualFold(it.DisplayLabel(), location) && it.Semantics != nil &&
					strings.HasPrefix(it.Semantics.Value, "Location") {
					location = name
				}
			}
		}
		filter := strings.ToLower(strings.TrimSpace(in.Filter))
		items := []any{}
		for _, v := range catalog.Views() {
			if location != "" && !catalog.InLocation(v.Name, location) {
				continue
			}
			if in.Type != "" && !strings.EqualFold(strings.SplitN(v.Type, ":", 2)[0], in.Type) && !strings.EqualFold(v.Type, in.Type) {
				continue
			}
			if filter != "" && !strings.Contains(strings.ToLower(v.Name+" "+v.Label), filter) {
				continue
			}
			items = append(items, v)
		}
		return nil, map[string]any{"items": items}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "send_openhab_command",
		Description: "Send a command to an openHAB item or group. openHAB's refusal comes back with its reason.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in sendOpenHABCommandInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsInvoke); err != nil {
			return nil, nil, err
		}
		driver, err := connops.OpenHABDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		if err := driver.SendCommand(ctx, strings.TrimSpace(in.Item), in.Command); err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage(err.Error()))
		}
		return nil, map[string]any{"sent": true, "item": in.Item, "command": in.Command}, nil
	})
}
