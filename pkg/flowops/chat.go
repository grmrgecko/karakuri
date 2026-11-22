package flowops

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodes/ainodes"
)

// NodeChatOptions selects a draft AI node and the preceding completed response.
type NodeChatOptions struct {
	// RunAs is the user the chat turn acts as; the handler sets it.
	RunAs      uuid.UUID      `json:"-"`
	SubgraphID string         `json:"subgraph_id,omitempty"`
	Prompt     string         `json:"prompt"`
	Response   map[string]any `json:"response,omitempty"`
}

// PrepareNodeChat validates the node's wired test graph before a stream starts.
func PrepareNodeChat(f *models.Flow, nodeID string, opts NodeChatOptions) (*engine.RunOptions, engine.NodeType, error) {
	if err := RequireEngine(); err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(opts.Prompt) == "" || len(opts.Prompt) > 32000 {
		return nil, nil, fmt.Errorf("message must contain between 1 and 32000 bytes")
	}
	_, root, selected, err := NodeGraph(f, "draft", opts.SubgraphID)
	if err != nil {
		return nil, nil, err
	}
	graph := &flow.Graph{Nodes: append([]flow.Node(nil), selected.Nodes...), Subgraphs: root.Subgraphs}
	var target *flow.Node
	for i := range graph.Nodes {
		if graph.Nodes[i].ID == nodeID {
			target = &graph.Nodes[i]
			break
		}
	}
	if target == nil {
		return nil, nil, fmt.Errorf("node %q not found", nodeID)
	}
	if target.Type != "ai-agent" && target.Type != "ai-basic-chain" {
		return nil, nil, fmt.Errorf("chat is available for AI Agent and AI Basic Chain nodes")
	}
	if target.Bypass {
		return nil, nil, fmt.Errorf("enable the node before testing chat")
	}
	nt, ok := engine.Default().Get(target.Type)
	if !ok {
		return nil, nil, fmt.Errorf("node type %q is unavailable", target.Type)
	}
	// A chat turn must not repeat actions through automatic node retries.
	target.Retry = &flow.RetryPolicy{MaxAttempts: 1}
	for _, edge := range selected.Edges {
		if !edge.IsValue() && (edge.Source == nodeID || edge.Target == nodeID) {
			continue
		}
		graph.Edges = append(graph.Edges, edge)
	}
	graph = flow.ExecutionGraph(graph, nodeID)
	graph, err = engine.FlattenGraph(graph, engine.ResolveSharedSubflowFromDB, engine.Default())
	if err != nil {
		return nil, nil, err
	}
	graph.Subgraphs = nil
	if err := flow.ValidateFull(graph, engine.Default(), dbSubflowResolver{selfID: f.ID}, false); err != nil {
		return nil, nil, err
	}
	if err := validateScripts(graph); err != nil {
		return nil, nil, err
	}
	return &engine.RunOptions{Graph: graph, TriggerNodeID: nodeID, StartNodeID: nodeID, RunAs: opts.RunAs,
			SeedInputs: map[string]*engine.Msg{"in": {Payload: opts.Prompt}}},
		ainodes.ChatTurn{NodeType: nt, Response: opts.Response}, nil
}

// RunNodeChat executes a prepared test without changing the flow's last run.
func RunNodeChat(ctx context.Context, opts engine.RunOptions, nt engine.NodeType, output func(string, *engine.Msg)) error {
	if err := RequireEngine(); err != nil {
		return err
	}
	return eng.PreviewNode(ctx, opts, opts.StartNodeID, nt, output)
}
