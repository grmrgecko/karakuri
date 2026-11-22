package flowops

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// NodeGraph loads a draft or deployment and selects a local subgraph by its root key.
func NodeGraph(f *models.Flow, view, subgraphID string) (*models.FlowVersion, *flow.Graph, *flow.Graph, error) {
	var version *models.FlowVersion
	var err error
	switch view {
	case "", "draft":
		version, err = LoadDraft(f)
	case "deployed":
		if f.DeployedVersionID == nil {
			return nil, nil, nil, fmt.Errorf("flow has no deployed version")
		}
		version, err = models.FindFlowVersionByID(*f.DeployedVersionID)
	default:
		return nil, nil, nil, fmt.Errorf("view must be draft or deployed")
	}
	if err != nil {
		return nil, nil, nil, err
	}
	root, err := ParseGraph(version.Graph)
	if err != nil {
		return nil, nil, nil, err
	}
	g := root
	if subgraphID != "" {
		sub, ok := root.Subgraphs[subgraphID]
		if !ok {
			return nil, nil, nil, fmt.Errorf("subgraph %q not found", subgraphID)
		}
		g = &sub
	}
	return version, root, g, nil
}

// NodeTestMessage supplies one input without executing its upstream producer.
type NodeTestMessage struct {
	Payload any            `json:"payload" jsonschema:"Input payload; binary ports accept base64 text."`
	Topic   string         `json:"topic,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// NodeTestOptions selects one draft node and temporary execution inputs.
type NodeTestOptions struct {
	// RunAs is the user the test acts as, whose connection and agent access
	// the node uses.
	RunAs           uuid.UUID
	NodeID          string
	SubgraphID      string
	Inputs          map[string]NodeTestMessage
	ConfigOverrides map[string]any
}

// NodeTestResult identifies the run and the node ID used in its history.
type NodeTestResult struct {
	RunID  uuid.UUID `json:"run_id"`
	NodeID string    `json:"node_id"`
}

// TestNode executes one draft node through the engine without traversing edges.
// Overrides exist only in this run. Error flows are excluded from isolated tests.
func TestNode(f *models.Flow, opts NodeTestOptions) (*NodeTestResult, error) {
	if err := RequireEngine(); err != nil {
		return nil, err
	}
	version, _, g, err := NodeGraph(f, "draft", opts.SubgraphID)
	if err != nil {
		return nil, err
	}
	var node *flow.Node
	for _, n := range g.Nodes {
		if n.ID == opts.NodeID {
			node = &n
			break
		}
	}
	if node == nil {
		return nil, fmt.Errorf("node %q not found", opts.NodeID)
	}
	config := map[string]any{}
	if len(node.Config) > 0 {
		if err := json.Unmarshal(node.Config, &config); err != nil {
			return nil, fmt.Errorf("node config: %w", err)
		}
	}
	if config == nil {
		config = map[string]any{}
	}
	for key, value := range opts.ConfigOverrides {
		config[key] = value
	}
	meta, ok := engine.Default().MetaFor(node.Type)
	if !ok {
		return nil, fmt.Errorf("unknown node type %q", node.Type)
	}
	ports := flow.NodePorts(meta, node, nil)
	signalOnly := len(opts.Inputs) > 0
	for id := range opts.Inputs {
		port, exists := ports.Input(id)
		if !exists || port.Kind != flow.PortKindSignal {
			signalOnly = false
			break
		}
	}
	for _, edge := range g.Edges {
		if signalOnly || edge.Target != node.ID || !edge.IsValue() {
			continue
		}
		field, _ := flow.ParamFieldID(edge.TargetHandle)
		if _, supplied := opts.ConfigOverrides[field]; !supplied {
			return nil, fmt.Errorf("provide config_overrides.%s to replace value edge %q for this isolated test", field, edge.ID)
		}
	}
	node.Config, err = json.Marshal(config)
	if err != nil {
		return nil, err
	}
	// No wired parameter may exempt a required field from strict validation.
	node.Params = nil
	if opts.SubgraphID != "" {
		// Local definitions have no caller instance. Keep their test state apart
		// from root nodes and the runtime state of subflow call sites.
		node.ID = "@subgraph/" + opts.SubgraphID + "/" + node.ID
	}
	options, err := prepareNodeTest(node, opts.Inputs)
	if err != nil {
		return nil, err
	}
	options.FlowID, options.FlowName, options.FlowSlug, options.FlowVersionID = f.ID, f.Name, f.Slug, version.ID
	options.RunAs = opts.RunAs
	handle, err := eng.RunFlow(*options)
	if err != nil {
		return nil, err
	}
	return &NodeTestResult{RunID: handle.ID, NodeID: node.ID}, nil
}

// prepareNodeTest validates one executable node and resolves its input envelopes.
func prepareNodeTest(node *flow.Node, messages map[string]NodeTestMessage) (*engine.RunOptions, error) {
	var err error
	meta, ok := engine.Default().MetaFor(node.Type)
	if !ok {
		return nil, fmt.Errorf("unknown node type %q", node.Type)
	}
	if node.Bypass || meta.Category == flow.CategoryAnnotation || meta.Category == flow.CategoryPort ||
		node.Type == flow.NodeTypeSubflow || node.Type == flow.NodeTypeTool || flow.IsLinkNode(node.Type) {
		return nil, fmt.Errorf("node %q requires graph routing or is disabled; use run_flow_test for its branch", node.ID)
	}
	graph := &flow.Graph{Nodes: []flow.Node{*node}, Edges: []flow.Edge{}}
	// An isolated test supplies port fields through literal configuration.
	validationMeta := meta
	validationMeta.ConfigSpec = append([]flow.FieldSpec(nil), meta.ConfigSpec...)
	for i := range validationMeta.ConfigSpec {
		validationMeta.ConfigSpec[i].Port = ""
	}
	if err := flow.ValidateFull(graph, nodeTestMetadata{validationMeta}, nil, false); err != nil {
		return nil, err
	}
	if err := validateScripts(graph); err != nil {
		return nil, err
	}
	ports := flow.NodePorts(meta, node, nil)
	seedAsTrigger := meta.IsTrigger() || meta.Pure
	if meta.Pure && !meta.IsTrigger() {
		for _, port := range ports.Inputs {
			if port.Kind != flow.PortKindParam {
				seedAsTrigger = false
			}
		}
	}
	optionalOnly := len(messages) > 0
	inputs := make(map[string]*engine.Msg, len(messages))
	for port, input := range messages {
		spec, exists := ports.Input(port)
		if seedAsTrigger {
			if port != engine.TriggerInput {
				return nil, fmt.Errorf("trigger inputs must use port %q", engine.TriggerInput)
			}
		} else if !exists || spec.Kind == flow.PortKindParam || spec.Kind == flow.PortKindReturn {
			return nil, fmt.Errorf("%q is not a message input; inspect get_node_type with the same config for ports", port)
		}
		optionalOnly = optionalOnly && spec.Optional
		if seedAsTrigger && len(ports.Outputs) > 0 {
			spec = ports.Outputs[0]
		}
		payload := input.Payload
		if spec.Type == flow.PortBinary {
			encoded, ok := payload.(string)
			if !ok {
				return nil, fmt.Errorf("input %q requires base64 text", port)
			}
			payload, err = base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				return nil, fmt.Errorf("input %q: %w", port, err)
			}
		}
		inputs[port] = &engine.Msg{ID: uuid.New().String(), Payload: payload, Topic: input.Topic, Meta: input.Meta}
	}
	if !seedAsTrigger {
		for _, port := range ports.Inputs {
			if !optionalOnly && !port.Optional && port.Kind != flow.PortKindParam && port.Kind != flow.PortKindReturn && inputs[port.ID] == nil {
				return nil, fmt.Errorf("provide inputs.%s for this isolated test", port.ID)
			}
		}
	} else if inputs[engine.TriggerInput] == nil {
		if meta.IsTrigger() || node.Type == flow.NodeTypeManual {
			inputs[engine.TriggerInput], err = buildInitialMsg(node, TestOptions{})
		} else {
			inputs[engine.TriggerInput] = &engine.Msg{}
		}
		if err != nil {
			return nil, err
		}
	}
	options := engine.RunOptions{Graph: graph, TriggerNodeID: node.ID}
	if seedAsTrigger {
		options.InitialMsg = inputs[engine.TriggerInput]
	} else {
		options.StartNodeID = node.ID
		options.SeedInputs = inputs
	}
	return &options, nil
}

type nodeTestMetadata struct{ meta flow.NodeMeta }

func (r nodeTestMetadata) MetaFor(typ string) (flow.NodeMeta, bool) {
	return r.meta, r.meta.Type == typ
}

// TestNodeType executes a configured type without creating a document or history row.
func TestNodeType(ctx context.Context, runAs uuid.UUID, nodeType string, config map[string]any, inputs map[string]NodeTestMessage, timeout time.Duration) (*engine.NodeTestResult, error) {
	if err := RequireEngine(); err != nil {
		return nil, err
	}
	if timeout <= 0 || timeout > 2*time.Minute {
		return nil, fmt.Errorf("timeout must be between 1 and 120 seconds")
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	options, err := prepareNodeTest(&flow.Node{ID: "test", Type: nodeType, Config: raw}, inputs)
	if err != nil {
		return nil, err
	}
	options.RunAs = runAs
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return eng.TestNode(ctx, *options)
}
