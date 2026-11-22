package ainodes

import (
	"context"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// ToolNode is the graph-backed tool: it declares what the model sees — a
// name, a description, an argument list — and delegates the body to the
// canvas. When the model calls the tool, the engine injects the arguments on
// the call output, the wired branch runs, and the message arriving back at
// the return input is the observation. Each argument is also a typed value
// output port, resolved per call, so branch nodes bind config fields to the
// model's arguments the way they bind to anything else.
type ToolNode struct{}

// Meta describes the tool node type.
func (ToolNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeTool,
		Label:       "Tool",
		Folder:      flow.FolderAI,
		Description: "Defines a tool for an AI agent, backed by the branch of nodes wired to it",
		Icon:        "las la-tools",
		// Pure because Execute only emits the tool definition; the call and
		// return traffic is engine-managed and never dispatches this node.
		Pure:         true,
		DynamicPorts: true,
		Inputs: []engine.PortSpec{
			// Optional so the loop-back wire never gates the node: a return
			// message resolves the pending call instead of scheduling it.
			{ID: flow.ToolReturnPort, Label: "Return", Type: flow.PortAny, Kind: flow.PortKindReturn, Optional: true},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Tool", Type: PortAITool},
			{ID: flow.ToolCallPort, Label: "Call", Type: flow.PortObject},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID: "name", Label: "Tool name", Type: flow.FieldString, Required: true,
				Placeholder: "lookup_weather",
				Help:        "Name the model calls the tool by: 1-64 characters of letters, digits, _ and -.",
			},
			{
				ID: "description", Label: "Description", Type: flow.FieldText, Required: true,
				Help: "What the tool does and when to use it. The model decides by this text alone.",
			},
			{
				ID: "tool_args", Label: "Arguments", Type: flow.FieldToolArgs,
				Help: "Arguments the model supplies. Each becomes a typed output port the branch reads over a value edge; the call message's payload carries them all.",
			},
			{
				ID: "timeout", Label: "Timeout (seconds)", Type: flow.FieldNumber, Default: 60,
				Help: "Seconds one call may run before the model is told it timed out.",
			},
		},
	}
}

// toolNodeConfig is the decoded tool node configuration.
type toolNodeConfig struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Arguments   []flow.ToolArg `json:"tool_args"`
	Timeout     float64        `json:"timeout"`
}

// Execute emits the tool definition. The branch runs only when the model
// calls the tool, driven by the agent through the engine.
func (ToolNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg toolNodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(cfg.Name)
	if !flow.ValidToolName(name) {
		return nil, invalidToolNameErr(name)
	}
	set := toolSet{Tools: []toolSpec{{
		Name:        name,
		Description: cfg.Description,
		Schema:      toolArgSchema(cfg.Arguments),
		Invoke:      invokeSpec{Kind: invokeBranch, Node: ec.NodeID(), TimeoutSec: cfg.Timeout},
	}}}
	return engine.One("out", &engine.Msg{Payload: set, Topic: name}), nil
}

// toolArgSchema builds the JSON Schema the provider is handed from the
// declared argument list. Port types map onto JSON Schema types; number and
// float both land on "number" because JSON knows no narrower shape.
func toolArgSchema(args []flow.ToolArg) map[string]any {
	properties := map[string]any{}
	var required []string
	seen := map[string]bool{}
	for _, a := range args {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		prop := map[string]any{"type": jsonSchemaType(a.Type)}
		if a.Description != "" {
			prop["description"] = a.Description
		}
		properties[a.ID] = prop
		if a.Required {
			required = append(required, a.ID)
		}
	}
	schema := map[string]any{"type": "object", "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// jsonSchemaType maps a tool argument's port type to its JSON Schema name.
func jsonSchemaType(portType string) string {
	switch portType {
	case flow.PortNumber, flow.PortFloat:
		return "number"
	case flow.PortInteger:
		return "integer"
	case flow.PortBool:
		return "boolean"
	case flow.PortObject:
		return "object"
	default:
		return "string"
	}
}
