package mcpserver

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/connops"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type nvrConnInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"Frigate connection UUID."`
}

type nvrEventsInput struct {
	ConnectionID string   `json:"connection_id" jsonschema:"Frigate connection UUID."`
	Cameras      []string `json:"cameras,omitempty" jsonschema:"Only these cameras."`
	Labels       []string `json:"labels,omitempty" jsonschema:"Only these labels, such as person or car."`
	Zones        []string `json:"zones,omitempty" jsonschema:"Only events in these zones."`
	Limit        int      `json:"limit,omitempty" jsonschema:"Events to return, 1 to 100, default 20."`
}

type nvrSnapshotInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"Frigate connection UUID."`
	Camera       string `json:"camera" jsonschema:"Camera name, from list_nvr_cameras."`
	Height       int    `json:"height,omitempty" jsonschema:"Scale to this many pixels high; 0 keeps the camera's size."`
	BBox         bool   `json:"bbox,omitempty" jsonschema:"Draw boxes around tracked objects."`
}

// addNVRTools registers the NVR tools.
func addNVRTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_nvr_cameras",
		Description: "List a Frigate connection's cameras with their zones, tracked objects, switches, and whether each is receiving frames.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nvrConnInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		recorder, err := connops.NVRDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"cameras": recorder.Cameras()}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_nvr_events",
		Description: "List a Frigate connection's recent events, newest first, with label, zones, score, times, and relay paths for each event's snapshot, thumbnail, and clip.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nvrEventsInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		for _, c := range in.Cameras {
			if !frigate.CameraNameRe.MatchString(c) {
				return nil, nil, opErr(apperrors.ErrValidation.WithMessage("invalid camera name " + c))
			}
		}
		recorder, err := connops.NVRDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		limit := in.Limit
		if limit < 1 || limit > nvr.MaxEvents {
			limit = 20
		}
		events, err := recorder.Events(ctx, nvr.EventQuery{Cameras: in.Cameras, Labels: in.Labels, Zones: in.Zones, Limit: limit})
		if err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage(err.Error()))
		}
		return nil, map[string]any{"events": events}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_nvr_snapshot",
		Description: "Return a camera's latest frame from a Frigate connection as an image.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in nvrSnapshotInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		recorder, err := connops.NVRDriver(in.ConnectionID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		resp, err := recorder.Snapshot(ctx, in.Camera, nvr.ImageOptions{Height: in.Height, BBox: in.BBox})
		if err != nil {
			return nil, nil, opErr(apperrors.ErrBadGateway.WithMessage(err.Error()))
		}
		defer resp.Body.Close()
		data, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, nil, fmt.Errorf("reading the snapshot: %w", err)
		}
		mime := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(mime, "image/") {
			mime = "image/jpeg"
		}
		return &mcp.CallToolResult{Content: []mcp.Content{
			&mcp.TextContent{Text: fmt.Sprintf("latest frame of camera %s", in.Camera)},
			&mcp.ImageContent{Data: data, MIMEType: mime},
		}}, nil, nil
	})
}
