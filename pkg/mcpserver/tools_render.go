package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/grmrgecko/karakuri/pkg/dashops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/render"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addRenderTools exposes the same headless renderer used by dashboard exports.
func addRenderTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "render_dashboard", Description: "Photograph a dashboard with the server's headless browser and return an image. Defaults to the editable draft, requiring edit access. Use view=deployed for the published layout. Set diagnostics=true to include widget bounds, overflow, browser errors, and failed requests. It never interacts with controls."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Diagnostics bool              `json:"diagnostics,omitempty" jsonschema:"Include browser errors, failed requests, widget bounds and overflow from the same capture; default false."`
			ID          string            `json:"id" jsonschema:"Dashboard UUID or slug."`
			View        string            `json:"view,omitempty" jsonschema:"draft (default) or deployed."`
			Width       int               `json:"width,omitempty" jsonschema:"Viewport width in pixels, 100 to 2048; default 1280."`
			Height      int               `json:"height,omitempty" jsonschema:"Viewport height in pixels, 100 to 2048; default 800."`
			Variables   map[string]string `json:"variables,omitempty" jsonschema:"Dashboard variable values by name, each an option value or label; unset variables use the dashboard-wide value."`
		}) (*mcp.CallToolResult, any, error) {
			uid, err := userID(ctx)
			if err != nil {
				return nil, nil, err
			}
			d, err := dashops.ResolveFor(ctx, callerID(ctx), in.ID)
			if err != nil {
				return nil, nil, opErr(err)
			}
			if in.View == "" {
				in.View = "draft"
			}
			if in.View != "draft" && in.View != "deployed" {
				return nil, nil, apperrors.ErrValidation.WithMessage("view must be draft or deployed")
			}
			if !dashops.CanView(ctx, uid, d) || (in.View == "draft" && !dashops.CanEdit(ctx, uid, d)) {
				return nil, nil, apperrors.ErrForbidden.WithMessage("dashboard access denied")
			}
			if render.R == nil {
				return nil, nil, apperrors.ErrServiceUnavailable.WithMessage("dashboard rendering is unavailable")
			}
			if in.Width == 0 {
				in.Width = 1280
			}
			if in.Height == 0 {
				in.Height = 800
			}
			if in.Width < 100 || in.Width > 2048 || in.Height < 100 || in.Height > 2048 {
				return nil, nil, apperrors.ErrValidation.WithMessage("preview dimensions must be between 100 and 2048 pixels")
			}
			variables, err := dashops.ResolveVariableValues(d, in.View == "draft", in.Variables)
			if err != nil {
				return nil, nil, opErr(err)
			}
			var job *models.RenderJob
			if in.Diagnostics {
				job, err = render.R.CreateInspection(ctx, d.ID, in.Width, in.Height, in.View == "draft", variables)
			} else {
				job, err = render.R.CreatePreview(ctx, d.ID, in.Width, in.Height, in.View == "draft", variables)
			}
			if err != nil && job == nil {
				return nil, nil, err
			}
			var diagnostics any
			if job.Diagnostics != "" {
				if decodeErr := json.Unmarshal([]byte(job.Diagnostics), &diagnostics); decodeErr != nil {
					return nil, nil, decodeErr
				}
			}
			if err != nil {
				raw, encodeErr := json.Marshal(map[string]any{"status": job.Status, "error": "dashboard capture did not complete", "diagnostics": diagnostics})
				if encodeErr != nil {
					return nil, nil, encodeErr
				}
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: string(raw)}}}, nil, nil
			}

			data, err := os.ReadFile(job.ResultPath)
			if err != nil {
				return nil, nil, fmt.Errorf("read dashboard preview: %w", err)
			}
			content := []mcp.Content{
				&mcp.TextContent{Text: fmt.Sprintf("%s dashboard preview at %dx%d", in.View, in.Width, in.Height)},
				&mcp.ImageContent{Data: data, MIMEType: "image/jpeg"},
			}
			if in.Diagnostics {
				raw, err := json.Marshal(diagnostics)
				if err != nil {
					return nil, nil, err
				}
				content = append(content, &mcp.TextContent{Text: string(raw)})
			}
			return &mcp.CallToolResult{Content: content}, nil, nil
		})
}
