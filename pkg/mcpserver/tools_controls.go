package mcpserver

import (
	"context"
	"github.com/grmrgecko/karakuri/pkg/tsdbops"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/controls"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/events"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type saveControlInput struct {
	ID          string         `json:"id,omitempty" jsonschema:"Control UUID to update; omit to create a new control."`
	Name        string         `json:"name" jsonschema:"Display name."`
	Kind        string         `json:"kind" jsonschema:"Control kind, from list_control_kinds."`
	Icon        string         `json:"icon,omitempty" jsonschema:"Line Awesome icon name."`
	Options     map[string]any `json:"options,omitempty" jsonschema:"Kind-specific settings: bounds, choices, buttons, features."`
	AllowPublic bool           `json:"allow_public,omitempty" jsonschema:"Whether anonymous viewers of a public dashboard may read and set it."`
}

type controlIDInput struct {
	ID string `json:"id" jsonschema:"Control UUID."`
}

type setControlInput struct {
	ID        string `json:"id" jsonschema:"Control UUID."`
	Attribute string `json:"attribute,omitempty" jsonschema:"Which attribute of the control's kind to set; omit for the kind's primary attribute."`
	Value     any    `json:"value" jsonschema:"The value to write; refused when the kind cannot hold it."`
}

// parseControlID reads a control UUID argument.
func parseControlID(id string) (uuid.UUID, error) {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return uuid.Nil, apperrors.ErrValidation.WithMessage("invalid control ID")
	}
	return parsed, nil
}

// addControlTools registers the control tools.
func addControlTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_control_kinds",
		Description: "List the control kinds with their settings schemas, value shapes, and addressable attributes.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		// The kind catalog describes the software rather than the house, so
		// like the REST endpoint it needs only a signed-in user.
		if _, err := userID(ctx); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"kinds": controls.Kinds()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_controls",
		Description: "List every control with its current value.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermControlsRead); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"controls": controls.S.List()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "save_control",
		Description: "Create a control (no id) or rewrite an existing one's definition (with id).",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in saveControlInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermControlsWrite); err != nil {
			return nil, nil, err
		}
		if in.ID == "" {
			snap, err := controls.S.Create(in.Name, in.Kind, in.Icon, in.Options, in.AllowPublic)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, snap, nil
		}
		id, err := parseControlID(in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		snap, err := controls.S.Update(id, in.Name, in.Kind, in.Icon, in.Options, in.AllowPublic)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, snap, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "delete_control",
		Description: "Remove a control. Dashboards and flows referencing it lose their binding.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in controlIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermControlsWrite); err != nil {
			return nil, nil, err
		}
		id, err := parseControlID(in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		// The store deletes an unknown ID without complaint, so the tool
		// answers not found itself, as the REST route does.
		if _, ok := controls.S.Get(id); !ok {
			return nil, nil, opErr(apperrors.ErrNotFound.WithMessage("control not found"))
		}
		if err := controls.S.Delete(id); err != nil {
			return nil, nil, opErr(err)
		}
		tsdbops.DeleteOwned(id)
		return nil, map[string]any{"deleted": true}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "set_control",
		Description: "Write a control's value, or one attribute of a composite kind. A composite merges: setting brightness leaves color alone.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in setControlInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermControlsSet); err != nil {
			return nil, nil, err
		}
		id, err := parseControlID(in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		w := controls.By(controls.SourceMCP)
		w.Actor = events.ActorFrom(ctx)
		snap, err := controls.S.SetAttribute(id, in.Attribute, in.Value, w)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, snap, nil
	})
}
