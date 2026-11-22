package mcpserver

import (
	"context"
	"github.com/grmrgecko/karakuri/pkg/access"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// notificationSendInput selects a configured endpoint rather than a recipient.
type notificationSendInput struct {
	ConnectionID string `json:"connection_id" jsonschema:"Configured notification service UUID."`
	Endpoint     string `json:"endpoint" jsonschema:"Allowed endpoint ID from list_notification_endpoints."`
	Subject      string `json:"subject,omitempty" jsonschema:"Notification subject."`
	Message      string `json:"message" jsonschema:"Notification body."`
	Timeout      *int   `json:"timeout,omitempty" jsonschema:"Whole seconds 1–120; defaults to 30. Failure may leave delivery uncertain."`
}

// addNotificationTools shares settings discovery and sends with REST and nodes.
func addNotificationTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_notification_services", Description: "List every notify service variant with settings fields, endpoint formats and unavailable reasons."}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"services": notification.Services()}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "list_notification_endpoints", Description: "List a configured notification service's allowed endpoint IDs and labels without credentials or targets."}, func(ctx context.Context, req *mcp.CallToolRequest, in connectionIDInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsRead); err != nil {
			return nil, nil, err
		}
		if _, err := useConnection(ctx, in.ID, access.Use); err != nil {
			return nil, nil, err
		}
		values, err := connops.NotificationEndpoints(in.ID)
		if err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"endpoints": values}, nil
	})
	mcp.AddTool(s, &mcp.Tool{Name: "send_notification", Description: "Send once through a configured notification service to one allowed endpoint ID. This is a real action; timeout/failure may leave delivery uncertain. No automatic retry."}, func(ctx context.Context, req *mcp.CallToolRequest, in notificationSendInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermConnectionsInvoke); err != nil {
			return nil, nil, err
		}
		seconds := 30
		if in.Timeout != nil {
			seconds = *in.Timeout
		}
		if err := connops.SendNotification(ctx, in.ConnectionID, in.Endpoint, in.Subject, in.Message, seconds); err != nil {
			return nil, nil, opErr(err)
		}
		return nil, map[string]any{"accepted": true}, nil
	})
}
