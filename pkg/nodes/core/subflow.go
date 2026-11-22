package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// SubflowNode is the instance node that embeds another graph: a local
// subgraph (subgraph_id on the placed node) or a shared subflow flow
// (subflow_id). Its ports are derived per instance from the referenced
// graph's pseudo-port nodes, so the static metadata declares none. The engine
// flattens subflow nodes away at run creation; Execute never runs.
type SubflowNode struct{}

func (SubflowNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:         flow.NodeTypeSubflow,
		Label:        "Subflow",
		Description:  "Runs a nested subflow: a local subgraph or a shared library subflow",
		Icon:         "las la-sitemap",
		DynamicPorts: true,
		ConfigSpec: []engine.FieldSpec{
			// This instance's values for the shared subflow's faceplates, by
			// faceplate key and then control ID. They are set from the
			// faceplate tabs and override the subflow's own values here only.
			{ID: "faceplate_values", Label: "Faceplate values", Type: flow.FieldJSON, Control: "hidden", NoParam: true},
		},
	}
}

// Execute is unreachable: subflow nodes are expanded into their child nodes
// before a run is created.
func (SubflowNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return nil, errors.New("subflow node was not expanded; this is an engine bug")
}

// portTypeField is the shared port_type select spec of the pseudo-port nodes.
var portTypeField = engine.FieldSpec{
	ID:      "port_type",
	Label:   "Port type",
	Type:    flow.FieldSelect,
	Default: flow.PortAny,
	Options: []engine.Option{
		{Value: flow.PortAny, Label: "Any"},
		{Value: flow.PortString, Label: "String"},
		{Value: flow.PortInteger, Label: "Integer"},
		{Value: flow.PortFloat, Label: "Float"},
		{Value: flow.PortNumber, Label: "Number"},
		{Value: flow.PortBool, Label: "Boolean"},
		{Value: flow.PortObject, Label: "Object"},
		{Value: flow.PortBinary, Label: "Binary"},
	},
	Help: "Any leaves the payload undeclared and keeps this a message input. The value types turn an input into a parameter on the subflow node: the parent wires a value into it, or the default below is used.",
}

// boundaryTypeRule advertises the declared port_type on a boundary pseudo-
// node's own port, so the inside of a subflow sees the contract its edge on
// the outside is held to: an inner edge into a typed subflow output is
// checked against the declared type, and inner nodes fed by a typed subflow
// input know what arrives. Without it the boundary reads "any" from within,
// and the outer graph trusts a declaration nothing enforced.
func boundaryTypeRule() *flow.PortTypeRule {
	types := []string{
		flow.PortString, flow.PortInteger, flow.PortFloat, flow.PortNumber,
		flow.PortBool, flow.PortObject, flow.PortBinary,
	}
	cases := make([]flow.PortTypeCase, 0, len(types))
	for _, t := range types {
		cases = append(cases, flow.PortTypeCase{Match: map[string][]string{"port_type": {t}}, Type: t})
	}
	return &flow.PortTypeRule{Cases: cases, Default: flow.PortAny}
}

// SubflowInputNode declares one input port of a subflow graph. It never
// executes: at run creation the engine splices parent edges directly onto the
// child nodes its output feeds.
type SubflowInputNode struct{}

func (SubflowInputNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeSubflowInput,
		Label:       "Subflow Input",
		Category:    flow.CategoryPort,
		Folder:      flow.FolderSubflowPorts,
		Description: "Declares an input port of the enclosing subflow",
		Icon:        "las la-sign-in-alt",
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeRule: boundaryTypeRule()},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "port_id",
				Label:       "Port ID",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "in",
				Help:        "Handle ID of the port on the subflow node.",
			},
			{
				ID:          "label",
				Label:       "Label",
				Type:        flow.FieldString,
				Placeholder: "In",
			},
			portTypeField,
			{
				ID:            "payload",
				Label:         "Default value",
				Type:          flow.FieldText,
				Placeholder:   "{}",
				LanguageField: "output_format",
				Help:          "Used when the parent leaves this parameter unwired. Only applies to the value port types.",
			},
			outputTypeField(outputJSON),
		},
		// An unwired value parameter stays in the graph and supplies its
		// default, which makes it a value node like any other: pure, pullable,
		// and driven entirely by its own config.
		Pure: true,
	}
}

// Execute supplies the port's default value. A wired parameter never reaches
// here: flattening splices the pseudo-node out and points the consumer at the
// parent's value source instead. An untyped port never reaches here either, and
// passes its input through as a safety net if it somehow does.
func (SubflowInputNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg struct {
		PortType string `json:"port_type"`
		manualConfig
	}
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if !flow.IsValuePortType(cfg.PortType) {
		return engine.One("out", inbound(ec, inputs)), nil
	}
	value, err := manualPayload(cfg.manualConfig)
	if err != nil {
		return nil, fmt.Errorf("subflow input default: %w", err)
	}
	return engine.One("out", &engine.Msg{Payload: value}), nil
}

// SubflowOutputNode declares one output port of a subflow graph. Like the
// input pseudo-node, it is spliced out at run creation.
type SubflowOutputNode struct{}

func (SubflowOutputNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeSubflowOutput,
		Label:       "Subflow Output",
		Category:    flow.CategoryPort,
		Folder:      flow.FolderSubflowPorts,
		Description: "Declares an output port of the enclosing subflow",
		Icon:        "las la-sign-out-alt",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny, TypeRule: boundaryTypeRule()},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "port_id",
				Label:       "Port ID",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "out",
				Help:        "Handle ID of the port on the subflow node.",
			},
			{
				ID:          "label",
				Label:       "Label",
				Type:        flow.FieldString,
				Placeholder: "Out",
			},
			portTypeField,
		},
	}
}

// Execute passes the input through as a safety net; flattening removes these
// nodes before execution.
func (SubflowOutputNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("in", inbound(ec, inputs)), nil
}
