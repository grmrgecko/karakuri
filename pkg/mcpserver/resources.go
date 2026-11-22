package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/docs"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// addResources registers every guide as a karakuri://guides resource, served
// as markdown rather than the rendered HTML the help pages get: an agent reads
// source. The flow-authoring guide is the one the server's instructions point
// at before an agent writes a graph.
func addResources(s *mcp.Server) {
	guides := docs.Guides()
	names := make([]string, 0, len(guides))
	for _, g := range guides {
		names = append(names, g.ID)
	}
	mcp.AddTool(s, &mcp.Tool{Name: "read_guide", Description: "Read an embedded guide by name: " + strings.Join(names, ", ") + "."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Name string `json:"name" jsonschema:"Guide name."`
		}) (*mcp.CallToolResult, any, error) {
			if _, err := userID(ctx); err != nil {
				return nil, nil, err
			}
			markdown, ok := docs.Markdown(in.Name)
			if !ok {
				return nil, nil, fmt.Errorf("unknown guide %q", in.Name)
			}
			return nil, map[string]any{"name": in.Name, "markdown": markdown}, nil
		})
	for _, g := range guides {
		id := g.ID
		s.AddResource(&mcp.Resource{
			URI:         "karakuri://guides/" + id,
			Name:        id,
			Description: g.Title + ": " + g.Summary,
			MIMEType:    "text/markdown",
		}, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			markdown, _ := docs.Markdown(id)
			return &mcp.ReadResourceResult{
				Contents: []*mcp.ResourceContents{{
					URI:      req.Params.URI,
					MIMEType: "text/markdown",
					Text:     markdown,
				}},
			}, nil
		})
	}
}
