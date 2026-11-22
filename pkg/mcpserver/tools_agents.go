package mcpserver

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/agentops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func addAgentTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{Name: "list_agents", Description: "List enrolled hosts with enabled state, live connection health, last seen time, and capability counts. Does not connect to or execute on hosts."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermAgentsRead); err != nil {
				return nil, nil, err
			}
			agents, err := models.ListAgents()
			if err != nil {
				return nil, nil, err
			}
			allowed, err := access.AgentIDs(ctx, callerID(ctx), access.Use)
			if err != nil {
				return nil, nil, err
			}
			items := []map[string]any{}
			for _, a := range agents {
				if !access.Allowed(allowed, a.ID) {
					continue
				}
				items = append(items, map[string]any{"agent": agentops.DescribeFor(ctx, callerID(ctx), a), "capability_count": len(agentops.Capabilities(a))})
			}
			return nil, map[string]any{"agents": items}, nil
		})
	mcp.AddTool(s, &mcp.Tool{Name: "get_agent_status", Description: "Inspect one enrolled host and its registered node type IDs. Use get_node_type for config schemas. No commands are executed."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID string `json:"id" jsonschema:"Agent UUID."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermAgentsRead); err != nil {
				return nil, nil, err
			}
			a, err := access.Agent(ctx, callerID(ctx), in.ID, access.Use)
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, map[string]any{"agent": agentops.DescribeFor(ctx, callerID(ctx), a), "node_types": agentops.Capabilities(a)}, nil
		})
}
