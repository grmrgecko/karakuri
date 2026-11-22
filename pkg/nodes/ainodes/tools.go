package ainodes

import (
	"context"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/mcpconn"
)

// MCPToolsNode is the ai-tool pure source over an MCP server: it lists the
// connection's tools, filters them, and hands the set to the agent. One node
// contributes the server's whole catalog, which is the point of MCP — the
// server describes its own tools, so nothing here is written by hand.
type MCPToolsNode struct{}

// Meta describes the mcp-tools node type.
func (MCPToolsNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:           "mcp-tools",
		Label:          "MCP Tools",
		Folder:         flow.FolderAI,
		Description:    "Offers an MCP server's tools to an AI agent, with an optional include or exclude filter",
		Icon:           "las la-toolbox",
		ConnectionKind: mcpconn.Kind,
		Retryable:      true,
		Pure:           true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Tools", Type: PortAITool},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "connection", Label: "Connection", Type: flow.FieldConnection,
				Required: true, ConnectionKind: mcpconn.Kind,
				Help: "MCP server connection to list tools from.",
			},
			{
				ID: "include", Label: "Include tools", Type: flow.FieldJSON, Control: "list",
				Help: "Only offer these tool names. Empty offers everything not excluded.",
			},
			{
				ID: "exclude", Label: "Exclude tools", Type: flow.FieldJSON, Control: "list",
				Help: "Never offer these tool names.",
			},
			{
				ID: "prefix", Label: "Name prefix", Type: flow.FieldString,
				Placeholder: "github_",
				Help:        "Prepended to every tool name, for wiring two servers whose names collide.",
			},
		},
	}
}

// mcpToolsConfig is the decoded mcp-tools node configuration.
type mcpToolsConfig struct {
	Connection string   `json:"connection"`
	Include    []string `json:"include"`
	Exclude    []string `json:"exclude"`
	Prefix     string   `json:"prefix"`
}

// invalidToolNameErr names the shape every provider enforces on a tool name.
func invalidToolNameErr(name string) error {
	return fmt.Errorf("tool name %q is not usable: providers accept 1-64 characters of letters, digits, _ and -", name)
}

// Execute lists the server's tools and emits the filtered set.
func (MCPToolsNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg mcpToolsConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	conn, err := resolveMCPConn(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	tools, err := conn.Tools(ctx)
	if err != nil {
		return nil, err
	}

	include := map[string]bool{}
	for _, name := range cfg.Include {
		include[name] = true
	}
	exclude := map[string]bool{}
	for _, name := range cfg.Exclude {
		exclude[name] = true
	}
	prefix := strings.TrimSpace(cfg.Prefix)

	set := toolSet{Tools: []toolSpec{}}
	for _, tool := range tools {
		if exclude[tool.Name] || (len(include) > 0 && !include[tool.Name]) {
			continue
		}
		name := prefix + tool.Name
		if !flow.ValidToolName(name) {
			return nil, invalidToolNameErr(name)
		}
		set.Tools = append(set.Tools, toolSpec{
			Name:        name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
			Invoke:      invokeSpec{Kind: invokeMCP, Connection: cfg.Connection, Tool: tool.Name},
		})
	}
	return engine.One("out", &engine.Msg{Payload: set, Topic: cfg.Prefix}), nil
}
