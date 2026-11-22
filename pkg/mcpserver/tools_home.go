package mcpserver

import (
	"context"
	"time"

	"github.com/grmrgecko/karakuri/pkg/homeops"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addHomeTools registers the overview tools. Like the REST routes they need
// only a signed-in caller; each source is gated by its own permission.
func addHomeTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "get_attention", Description: "Answer whether anything is broken: connections in error, unstable, or stuck connecting; offline agents; flow failures of the last 24 hours grouped by flow and error, with the newest failed run; failed or overdue backups; and failing history delivery. Covers only the sources you may read, named in checked; unchecked names sources that could not be read, so an empty list is all clear only for checked sources."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			uid, err := userID(ctx)
			if err != nil {
				return nil, nil, err
			}
			return nil, homeops.Attention(ctx, uid, time.Now()), nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_upcoming", Description: "List what runs next: the next firing of each schedule trigger in enabled, deployed flows, and pending wait deadlines, soonest first, within a window of hours. Waits carry the node_id and key cancel_flow_wait takes."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Hours int `json:"hours,omitempty" jsonschema:"Hours ahead to look, 1 to 24; default 12."`
		}) (*mcp.CallToolResult, any, error) {
			uid, err := userID(ctx)
			if err != nil {
				return nil, nil, err
			}
			window := homeops.DefaultUpcomingWindow
			if in.Hours > 0 {
				window = min(time.Duration(in.Hours)*time.Hour, homeops.MaxUpcomingWindow)
			}
			items, err := homeops.Upcoming(ctx, uid, time.Now(), window)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"items": items}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "list_unfinished_work", Description: "List flows and dashboards you may edit whose draft differs from the deployed version or that were never deployed, newest change first, up to 10 of each."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			uid, err := userID(ctx)
			if err != nil {
				return nil, nil, err
			}
			res, err := homeops.Unfinished(ctx, uid)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, res, nil
		})
}
