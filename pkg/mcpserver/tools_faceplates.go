package mcpserver

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addFaceplateTools registers the faceplate tools. Building a faceplate is
// ordinary graph editing: add a "faceplate" node with save_flow_graph or
// patch_flow_graph and wire its control outputs into the settings they drive.
func addFaceplateTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "get_flow_faceplates",
		Description: "List a flow's faceplate tabs: each tab's key, name, controls (id, label, type, default, range, options), applied values, and change script. Editors see the draft; a faceplate-level share sees the deployed version."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID string `json:"id" jsonschema:"Flow UUID."`
		}) (*mcp.CallToolResult, any, error) {
			f, err := loadFlow(ctx, in.ID, models.FlowAccessFaceplate)
			if err != nil {
				return nil, nil, err
			}
			view, err := flowops.Faceplates(ctx, callerID(ctx), f)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, view, nil
		})

	mcp.AddTool(s, &mcp.Tool{Name: "apply_flow_faceplates",
		Description: "Apply faceplate values and deploy. values maps a faceplate key (from get_flow_faceplates) to {control_id: value}; null returns a control to its default. Edit access saves the draft with the values and deploys it, draft edits included; a faceplate-level share deploys the current version with only the new values. This changes what the running flow does."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID      string                    `json:"id" jsonschema:"Flow UUID."`
			Values  map[string]map[string]any `json:"values" jsonschema:"New values by faceplate key, then control ID."`
			Comment string                    `json:"comment,omitempty" jsonschema:"Version comment."`
		}) (*mcp.CallToolResult, any, error) {
			f, err := loadFlow(ctx, in.ID, models.FlowAccessFaceplate)
			if err != nil {
				return nil, nil, err
			}
			version, err := flowops.ApplyFaceplates(ctx, callerID(ctx), f, flowops.FaceplateValues(in.Values), in.Comment)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"version": version}, nil
		})
}
