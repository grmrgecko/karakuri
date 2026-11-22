package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/grmrgecko/karakuri/pkg/access"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// nodeTypeSummary is the compact catalog row list_node_types returns. The full
// metadata with config schemas is far too large for one response, so the list
// carries what an author picks by and get_node_type answers the rest.
type nodeTypeSummary struct {
	Type        string `json:"type"`
	Label       string `json:"label"`
	Folder      string `json:"folder,omitempty"`
	Group       string `json:"group,omitempty"`
	Category    string `json:"category,omitempty"`
	Trigger     bool   `json:"trigger,omitempty"`
	Pure        bool   `json:"pure,omitempty"`
	Description string `json:"description,omitempty"`
}

type listNodeTypesInput struct {
	Filter string `json:"filter,omitempty" jsonschema:"Case-insensitive substring matched against type, label, folder, and description."`
}

type getNodeTypeInput struct {
	Config map[string]any `json:"config,omitempty" jsonschema:"Optional proposed configuration to resolve dynamic input and output ports without creating a flow."`
	Type   string         `json:"type" jsonschema:"Node type identifier, as list_node_types reports it."`
}

// addCatalogTools registers the node and port type catalog tools.
func addCatalogTools(s *mcp.Server) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_node_types",
		Description: "List the registered node types as compact summaries. Use get_node_type for a type's config schema and ports.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in listNodeTypesInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		filter := strings.ToLower(strings.TrimSpace(in.Filter))
		summaries := []nodeTypeSummary{}
		for _, meta := range access.VisibleNodeTypes(ctx, callerID(ctx), engine.Default().List()) {
			if filter != "" {
				haystack := strings.ToLower(meta.Type + " " + meta.Label + " " + meta.Folder + " " + meta.Description)
				if !strings.Contains(haystack, filter) {
					continue
				}
			}
			summaries = append(summaries, nodeTypeSummary{
				Type:        meta.Type,
				Label:       meta.Label,
				Folder:      meta.Folder,
				Group:       meta.Group,
				Category:    meta.Category,
				Trigger:     meta.IsTrigger(),
				Pure:        meta.Pure,
				Description: meta.Description,
			})
		}
		return nil, map[string]any{"node_types": summaries}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_node_type",
		Description: "Get one node type's full metadata: config field schema, ports, port type rules, and retry defaults. Optionally supply config to see resolved ports for that configuration; no flow is required.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in getNodeTypeInput) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		meta, ok := engine.Default().MetaFor(in.Type)
		if !ok || !access.NodeTypeVisible(ctx, callerID(ctx), in.Type) {
			return nil, nil, errors.New("unknown node type " + in.Type)
		}
		raw, err := json.Marshal(in.Config)
		if err != nil {
			return nil, nil, err
		}
		ports := flow.NodePorts(meta, &flow.Node{Type: in.Type, Config: raw}, nil)
		return nil, struct {
			flow.NodeMeta
			ResolvedInputs  []flow.PortSpec `json:"resolved_inputs"`
			ResolvedOutputs []flow.PortSpec `json:"resolved_outputs"`
			PortsKnown      bool            `json:"ports_known"`
		}{meta, append([]flow.PortSpec{}, ports.Inputs...), append([]flow.PortSpec{}, ports.Outputs...), ports.Known}, nil
	})

	mcp.AddTool(s, &mcp.Tool{Name: "test_node", Description: "Execute a registered node type directly without creating or selecting a flow. Supply type, config, and input messages keyed by resolved port ID. Returns outputs, node/debug events, status and errors directly; no saved run_id. Uses real configured connections and may perform real actions. Node stores and variables are temporary and start empty. Routing, subflow and graph-tool nodes require a flow. Trigger tests execute a sample once, without arming listeners."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			Type           string                             `json:"type" jsonschema:"Registered node type from list_node_types."`
			Config         map[string]any                     `json:"config,omitempty" jsonschema:"Complete configuration for this test; inspect get_node_type for fields and resolved ports."`
			Inputs         map[string]flowops.NodeTestMessage `json:"inputs,omitempty" jsonschema:"Input envelopes keyed by port ID, e.g. {\"in\":{\"payload\":42}}. Triggers and input-free values use __trigger__ or configured test payloads."`
			TimeoutSeconds int                                `json:"timeout_seconds,omitempty" jsonschema:"Wall-clock timeout including parked waits, default 30, range 1-120 seconds."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
				return nil, nil, err
			}
			if in.TimeoutSeconds == 0 {
				in.TimeoutSeconds = 30
			}
			if in.TimeoutSeconds < 1 || in.TimeoutSeconds > 120 {
				return nil, nil, errors.New("timeout_seconds must be between 1 and 120")
			}
			result, err := flowops.TestNodeType(ctx, callerID(ctx), in.Type, in.Config, in.Inputs, time.Duration(in.TimeoutSeconds)*time.Second)
			return nil, result, opErr(err)
		})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_port_types",
		Description: "List the registered payload port types in registration order, with widening and value-parameter rules.",
	}, func(ctx context.Context, req *mcp.CallToolRequest, in struct{}) (*mcp.CallToolResult, any, error) {
		if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
			return nil, nil, err
		}
		return nil, map[string]any{"port_types": flow.PortTypes()}, nil
	})
}
