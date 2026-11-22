package mcpserver

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// connectionArgumentAccess refuses a tool call whose connection_id argument
// names a connection the caller cannot use, before the tool runs. Most tools
// that reach a connection take it under that name, so one check covers them
// and the editor chat, which calls the same tools. Tools that name a
// connection another way check it themselves. A value that is not a UUID,
// such as query_timeseries' energy, is left to the tool.
func connectionArgumentAccess(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		if method != "tools/call" {
			return next(ctx, method, req)
		}
		params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
		if !ok || len(params.Arguments) == 0 {
			return next(ctx, method, req)
		}
		var args struct {
			ConnectionID string `json:"connection_id"`
		}
		if json.Unmarshal(params.Arguments, &args) != nil || args.ConnectionID == "" {
			return next(ctx, method, req)
		}
		if _, err := uuid.Parse(args.ConnectionID); err != nil {
			return next(ctx, method, req)
		}
		if _, err := access.Connection(ctx, callerID(ctx), args.ConnectionID, access.Use); err != nil {
			return toolError(opErr(err)), nil
		}
		return next(ctx, method, req)
	}
}

// toolError reports a refused call as a failed tool result, which a client
// shows the model, rather than a protocol error.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// useConnection authorizes the caller on a connection named other than by a
// connection_id argument.
func useConnection(ctx context.Context, id string, level string) (*models.Connection, error) {
	c, err := access.Connection(ctx, callerID(ctx), id, level)
	if err != nil {
		return nil, opErr(err)
	}
	return c, nil
}

// useDeviceConnection authorizes the caller on the connection a device speaks
// on, answering as if the device does not exist when they cannot use it.
func useDeviceConnection(ctx context.Context, device *models.MQTTDevice) error {
	if !access.CanUseConnection(ctx, callerID(ctx), device.ConnectionID.String()) {
		return opErr(apperrors.ErrNotFound.WithMessage("device not found"))
	}
	return nil
}

// referenceAccess refuses a reference to a connection, device, entity, or
// agent the caller cannot use. Other kinds, such as controls and flows, have
// their own rules and pass.
func referenceAccess(ctx context.Context, kind, ref, owner string) error {
	uid := callerID(ctx)
	switch kind {
	case "connection":
		if !access.CanUseConnection(ctx, uid, ref) {
			return opErr(apperrors.ErrNotFound.WithMessage("connection not found"))
		}
	case "ha_entity":
		if !access.CanUseConnection(ctx, uid, owner) {
			return opErr(apperrors.ErrNotFound.WithMessage("connection not found"))
		}
	case "mqtt_device":
		id, err := uuid.Parse(ref)
		if err != nil {
			return opErr(apperrors.ErrNotFound.WithMessage("device not found"))
		}
		device, err := models.FindMQTTDeviceByID(id)
		if err != nil {
			return opErr(apperrors.ErrNotFound.WithMessage("device not found"))
		}
		return useDeviceConnection(ctx, device)
	case "agent":
		if !access.CanUseAgent(ctx, uid, ref) {
			return opErr(apperrors.ErrNotFound.WithMessage("agent not found"))
		}
	}
	return nil
}
