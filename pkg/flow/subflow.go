package flow

import (
	"encoding/json"
	"sort"
)

// Node type name constants for the subflow machinery. Defined here so graph
// validation and engine flattening agree without importing node packages.
const (
	// NodeTypeSubflow is the instance node that embeds another graph: either a
	// local subgraph (subgraph_id) or a shared subflow flow (subflow_id).
	NodeTypeSubflow = "subflow"
	// NodeTypeSubflowInput is the pseudo-node that declares one input port of
	// a subflow graph.
	NodeTypeSubflowInput = "subflow-input"
	// NodeTypeSubflowOutput is the pseudo-node that declares one output port
	// of a subflow graph.
	NodeTypeSubflowOutput = "subflow-output"
)

// MaxSubflowDepth caps subflow nesting for both validation and engine
// flattening.
const MaxSubflowDepth = 8

// PortNodeConfig is the decoded config of a subflow-input or subflow-output
// pseudo-node: the port it declares on the enclosing subflow.
type PortNodeConfig struct {
	PortID   string `json:"port_id"`
	Label    string `json:"label"`
	PortType string `json:"port_type"`
	// Payload and OutputFormat hold a value port's default, read exactly the
	// way the value node reads its own: text plus the type it is read as. It
	// is what the subflow uses when the parent leaves the parameter unwired.
	Payload      json.RawMessage `json:"payload,omitempty"`
	OutputFormat string          `json:"output_format,omitempty"`
}

// IsValuePortType reports whether a port type carries a value rather than a
// message. A subflow input declared with one becomes a parameter on the
// enclosing subflow node instead of a message input. Answered from the type
// registry, so a plugin type opts in through its definition.
func IsValuePortType(t string) bool {
	def, ok := PortTypeFor(t)
	return ok && def.Value
}

// DecodePortNode decodes a pseudo-port node's config, applying defaults for
// label (the port ID) and port type (message).
func DecodePortNode(n *Node) PortNodeConfig {
	var cfg PortNodeConfig
	if len(n.Config) > 0 {
		_ = json.Unmarshal(n.Config, &cfg)
	}
	if cfg.PortType == "" {
		cfg.PortType = PortAny
	}
	if cfg.Label == "" {
		cfg.Label = cfg.PortID
	}
	return cfg
}

// SubflowPorts derives the input and output port specs a subflow node exposes
// from a subflow graph's pseudo-port nodes. Ports are ordered by the
// pseudo-node's canvas Y position, then node ID, for a stable layout.
// Pseudo-nodes without a port_id are skipped; validation reports them.
func SubflowPorts(g *Graph) (inputs, outputs []PortSpec) {
	type entry struct {
		y    float64
		id   string
		spec PortSpec
	}
	var ins, outs []entry
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeTypeSubflowInput && n.Type != NodeTypeSubflowOutput {
			continue
		}
		cfg := DecodePortNode(n)
		if cfg.PortID == "" {
			continue
		}
		spec := PortSpec{ID: cfg.PortID, Label: cfg.Label, Type: cfg.PortType}
		// A value-typed input is a parameter: it is pulled when the subflow
		// runs rather than delivered, so it never gates readiness.
		if n.Type == NodeTypeSubflowInput && IsValuePortType(cfg.PortType) {
			spec.Kind = PortKindParam
			spec.Optional = true
		}
		e := entry{y: n.Position.Y, id: n.ID, spec: spec}
		if n.Type == NodeTypeSubflowInput {
			ins = append(ins, e)
		} else {
			outs = append(outs, e)
		}
	}
	order := func(list []entry) []PortSpec {
		sort.Slice(list, func(i, j int) bool {
			if list[i].y != list[j].y {
				return list[i].y < list[j].y
			}
			return list[i].id < list[j].id
		})
		specs := make([]PortSpec, 0, len(list))
		for _, e := range list {
			specs = append(specs, e.spec)
		}
		return specs
	}
	return order(ins), order(outs)
}
