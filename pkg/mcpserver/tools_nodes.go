package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/flowops"
	"github.com/grmrgecko/karakuri/pkg/models"
	perm "github.com/grmrgecko/karakuri/pkg/permissions"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type flowNodesInput struct {
	ID         string `json:"id" jsonschema:"Flow UUID."`
	View       string `json:"view,omitempty" jsonschema:"draft (default) or deployed."`
	SubgraphID string `json:"subgraph_id,omitempty" jsonschema:"Local subgraph key from the root graph; omit for the root graph."`
	NodeID     string `json:"node_id,omitempty" jsonschema:"Filter to a placed node ID. Required by get_flow_node; list_flow_edges returns its incident edges."`
}

type placedNodeSummary struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
	Bypass    bool   `json:"bypass,omitempty"`
}

type placedNodeInfo struct {
	Node       flow.Node       `json:"node"`
	Available  bool            `json:"available"`
	Inputs     []flow.PortSpec `json:"inputs"`
	Outputs    []flow.PortSpec `json:"outputs"`
	PortsKnown bool            `json:"ports_known"`
	PortError  string          `json:"port_error,omitempty"`
	Incoming   []flow.Edge     `json:"incoming_edges"`
	Outgoing   []flow.Edge     `json:"outgoing_edges"`
	Metadata   *flow.NodeMeta  `json:"metadata,omitempty"`
}

func addNodeTools(s *mcp.Server) {
	for _, spec := range []struct{ name, description string }{
		{"list_flow_nodes", "List compact placed-node summaries: ID, type, label, availability and bypass state. Includes draft revision and local subgraph keys. Use get_flow_node for configuration, resolved ports and metadata, or list_flow_edges for wiring."},
		{"get_flow_node", "Inspect one placed node: saved configuration, config schema, resolved dynamic ports, retry settings and incoming/outgoing message and value edges. Requires node_id."},
		{"list_flow_edges", "List graph edges with source/target node IDs and port handles, preserving edge order. Optionally filter to a node's incident edges. An omitted kind means a message edge; value edges provide configuration parameters."},
	} {
		mcp.AddTool(s, &mcp.Tool{Name: spec.name, Description: spec.description},
			func(ctx context.Context, req *mcp.CallToolRequest, in flowNodesInput) (*mcp.CallToolResult, any, error) {
				if err := requirePermission(ctx, perm.PermFlowsRead); err != nil {
					return nil, nil, err
				}
				if spec.name == "get_flow_node" && in.NodeID == "" {
					return nil, nil, fmt.Errorf("node_id is required")
				}
				f, err := loadFlow(ctx, in.ID, models.FlowAccessView)
				if err != nil {
					return nil, nil, err
				}
				version, root, graph, err := flowops.NodeGraph(f, in.View, in.SubgraphID)
				if err != nil {
					return nil, nil, opErr(err)
				}
				if in.View == "" {
					in.View = "draft"
				}
				subgraphs := make([]string, 0, len(root.Subgraphs))
				for id := range root.Subgraphs {
					subgraphs = append(subgraphs, id)
				}
				sort.Strings(subgraphs)
				result := map[string]any{"view": in.View, "version": version.Version, "subgraph_id": in.SubgraphID, "subgraph_ids": subgraphs}
				if in.View == "draft" {
					// Use the same loaded version so the revision describes these nodes.
					result["draft_revision"] = flowops.DraftRevision(version.Graph)
				}
				found := in.NodeID == ""
				for _, node := range graph.Nodes {
					found = found || node.ID == in.NodeID
				}
				if !found {
					return nil, nil, fmt.Errorf("node %q not found", in.NodeID)
				}
				if spec.name == "list_flow_edges" {
					edges := []flow.Edge{}
					for _, edge := range graph.Edges {
						if in.NodeID == "" || edge.Source == in.NodeID || edge.Target == in.NodeID {
							edges = append(edges, edge)
						}
					}
					result["edges"] = edges
					return nil, result, nil
				}

				if spec.name == "list_flow_nodes" {
					nodes := []placedNodeSummary{}
					for _, node := range graph.Nodes {
						if in.NodeID != "" && node.ID != in.NodeID {
							continue
						}
						meta, available := engine.Default().MetaFor(node.Type)
						var config struct {
							Label string `json:"_label"`
						}
						_ = json.Unmarshal(node.Config, &config)
						label := config.Label
						if label == "" {
							label = meta.Label
						}
						if label == "" {
							label = node.Type
						}
						nodes = append(nodes, placedNodeSummary{ID: node.ID, Type: node.Type, Label: label, Available: available, Bypass: node.Bypass})
					}
					result["nodes"] = nodes
					return nil, result, nil
				}
				ports := map[string]flow.PortSet{}
				portErrors := map[string]string{}
				for _, node := range graph.Nodes {
					meta, ok := engine.Default().MetaFor(node.Type)
					if !ok {
						continue
					}
					var sub *flow.Graph
					if node.Type == flow.NodeTypeSubflow {
						if node.SubflowID != nil {
							sub, err = engine.ResolveSharedSubflowFromDB(*node.SubflowID)
							if err != nil {
								portErrors[node.ID] = err.Error()
							}
						} else if local, exists := root.Subgraphs[node.SubgraphID]; exists {
							sub = &local
						}
					}
					ports[node.ID] = flow.NodePorts(meta, &node, sub)
				}
				flow.PropagateTypes(graph, ports)
				nodes := []placedNodeInfo{}
				for _, node := range graph.Nodes {
					if in.NodeID != "" && node.ID != in.NodeID {
						continue
					}
					meta, available := engine.Default().MetaFor(node.Type)
					info := placedNodeInfo{Node: node, Available: available, Inputs: append([]flow.PortSpec{}, ports[node.ID].Inputs...),
						Outputs: append([]flow.PortSpec{}, ports[node.ID].Outputs...), PortsKnown: ports[node.ID].Known,
						PortError: portErrors[node.ID], Incoming: []flow.Edge{}, Outgoing: []flow.Edge{}}
					for _, edge := range graph.Edges {
						if edge.Source == node.ID {
							info.Outgoing = append(info.Outgoing, edge)
						}
						if edge.Target == node.ID {
							info.Incoming = append(info.Incoming, edge)
						}
					}
					if available {
						info.Metadata = &meta
					}
					nodes = append(nodes, info)
				}
				result["node"] = nodes[0]
				return nil, result, nil
			})
	}
	mcp.AddTool(s, &mcp.Tool{Name: "test_flow_node", Description: "Execute one draft node in isolation with explicit input messages. Does not execute upstream, downstream, value-source nodes or the flow's error handler. Real actions still occur in the selected node. Config overrides are temporary; every wired value parameter requires an override. Routing, subflow, tool, annotation and bypassed nodes require run_flow_test instead. Returns run_id; inspect get_flow_run with include_events=true for outputs and errors."},
		func(ctx context.Context, req *mcp.CallToolRequest, in struct {
			ID              string                             `json:"id" jsonschema:"Flow UUID."`
			NodeID          string                             `json:"node_id" jsonschema:"Placed node ID to execute."`
			SubgraphID      string                             `json:"subgraph_id,omitempty" jsonschema:"Local subgraph key from the root graph."`
			Inputs          map[string]flowops.NodeTestMessage `json:"inputs,omitempty" jsonschema:"Messages keyed by input port ID, e.g. {\"in\":{\"payload\":42}}. Triggers and input-free value sources use __trigger__ or their configured test payload if omitted."`
			ConfigOverrides map[string]any                     `json:"config_overrides,omitempty" jsonschema:"Temporary top-level config fields; each supplied field replaces the saved value. Does not save the draft."`
		}) (*mcp.CallToolResult, any, error) {
			if err := requirePermission(ctx, perm.PermFlowsWrite); err != nil {
				return nil, nil, err
			}
			f, err := loadFlow(ctx, in.ID, models.FlowAccessEdit)
			if err != nil {
				return nil, nil, err
			}
			result, err := flowops.TestNode(f, flowops.NodeTestOptions{RunAs: callerID(ctx), NodeID: in.NodeID, SubgraphID: in.SubgraphID, Inputs: in.Inputs, ConfigOverrides: in.ConfigOverrides})
			if err != nil {
				return nil, nil, opErr(err)
			}
			return nil, result, nil
		})
}
