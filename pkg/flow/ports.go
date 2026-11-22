package flow

import "encoding/json"

const NodeTypeWebhook = "webhook"

// Port resolution is the one place that answers "what ports does this placed
// node have?". A node type's static metadata is only the starting point: the
// types marked DynamicPorts derive their ports from their own config, and any
// node may expose config fields as value input ports. Validation, the engine,
// and the editor all have to agree on the answer, so they all come here rather
// than each carrying a copy of the rules.

// ErrorPort is the failure branch a node type declares to route failed
// executions somewhere. It carries whatever the node was handed, so it reports
// the type of its own input rather than declaring one; the error itself rides
// in the message metadata. Every node type that has one names its input "in".
func ErrorPort() PortSpec {
	return PortSpec{ID: "error", Label: "Error", Type: PortAny, Kind: PortKindError, TypeFrom: "in"}
}

// PortSet is one placed node's resolved ports.
type PortSet struct {
	Inputs  []PortSpec
	Outputs []PortSpec
	// Known is false when the node's ports could not be determined, which
	// today means a subflow node whose referenced graph did not resolve.
	// Callers skip per-port checks rather than reporting ports that do not
	// exist.
	Known bool
}

// Input returns the input port with the given ID, or false when absent.
func (s PortSet) Input(id string) (PortSpec, bool) { return findPort(s.Inputs, id) }

// Output returns the output port with the given ID, or false when absent.
func (s PortSet) Output(id string) (PortSpec, bool) { return findPort(s.Outputs, id) }

// PrimaryInput returns the port a node reads when it wants "the message":
// the first declared input that is neither a parameter, return, signal, nor optional.
// A node whose only message inputs are optional falls back to the first of
// those, and a node with no ordinary message inputs returns false.
func (s PortSet) PrimaryInput() (PortSpec, bool) {
	for _, p := range s.Inputs {
		if p.Kind == PortKindParam || p.Kind == PortKindReturn || p.Kind == PortKindSignal || p.Optional {
			continue
		}
		return p, true
	}
	for _, p := range s.Inputs {
		if p.Kind != PortKindParam && p.Kind != PortKindReturn && p.Kind != PortKindSignal {
			return p, true
		}
	}
	return PortSpec{}, false
}

// ErrorOutput returns the node's failure branch, the port a failed execution
// is routed to when an edge is wired to it. Only the kind is consulted: a
// script author who declares an output named "err" owns that port outright,
// and the node then has no failure branch at all.
func (s PortSet) ErrorOutput() (PortSpec, bool) {
	for _, p := range s.Outputs {
		if p.Kind == PortKindError {
			return p, true
		}
	}
	return PortSpec{}, false
}

func findPort(ports []PortSpec, id string) (PortSpec, bool) {
	for _, p := range ports {
		if p.ID == id {
			return p, true
		}
	}
	return PortSpec{}, false
}

// NodePorts resolves one placed node's ports. Ordinary types use their static
// metadata; the DynamicPorts types derive theirs from config. Parameter ports
// are appended last in every case, so a value socket always sits below the
// node's own inputs.
//
// A subflow node's ports come from the graph it references, which this package
// cannot reach on its own: the caller resolves it and passes it as sub. A nil
// sub for a subflow node yields an unknown port set. Every other type ignores
// sub, and the engine passes nil because it only ever sees flattened graphs.
func NodePorts(meta NodeMeta, n *Node, sub *Graph) PortSet {
	set := PortSet{Inputs: meta.Inputs, Outputs: meta.Outputs, Known: true}

	switch n.Type {
	case NodeTypeWebhook:
		var cfg struct {
			ResponseMode string `json:"response_mode"`
		}
		_ = json.Unmarshal(n.Config, &cfg)
		if cfg.ResponseMode != "sync" {
			set.Inputs = withoutPortKind(set.Inputs, PortKindReturn)
		}
	case NodeTypeSwitch:
		set.Outputs = SwitchPorts(n.Config)
	case NodeTypeBrowserPrompt:
		set.Outputs = BrowserPromptPorts(n.Config)
	case NodeTypeFaceplate:
		set.Outputs = FaceplatePorts(n.Config)
	case NodeTypeChange:
		set.Outputs = ChangePorts(n.Config)
	case NodeTypeScript:
		set.Inputs, set.Outputs = ScriptPorts(n.Config)
	case NodeTypeAIClassify:
		// Category ports are prepended to the static outputs (other, error)
		// so the routes sit above the fallbacks on the card; the copy exists
		// because the static slice is shared by every placed node.
		if cats := ClassifyPorts(n.Config); len(cats) > 0 {
			set.Outputs = append(append([]PortSpec{}, cats...), set.Outputs...)
		}
	case NodeTypeTool:
		// Argument ports are appended to the static outputs rather than
		// replacing them: the tool and call ports belong to the node type
		// (whose metadata this package never redeclares), and the append
		// copies because the static slice is shared by every placed node.
		if args := ToolArgPorts(n.Config); len(args) > 0 {
			set.Outputs = append(append([]PortSpec{}, set.Outputs...), args...)
		}
	case NodeTypeSubflow:
		if sub == nil {
			return PortSet{Known: false}
		}
		set.Inputs, set.Outputs = SubflowPorts(sub)
	}

	// Ports whose payload depends on the node's settings resolve their type
	// here, so a file read as binary and one read as text advertise what they
	// each actually carry.
	set.Inputs = resolvePortTypes(set.Inputs, n.Config)
	set.Outputs = resolvePortTypes(set.Outputs, n.Config)

	// Config fields the node exposed as value inputs become extra input ports.
	// They are appended to a copy: the static slice belongs to the node type
	// and is shared by every placed node of that type.
	if params := ParamPorts(meta.ConfigSpec, n.Params, n.Config); len(params) > 0 {
		set.Inputs = append(append([]PortSpec{}, set.Inputs...), params...)
	}
	return set
}

func withoutPortKind(ports []PortSpec, kind string) []PortSpec {
	out := make([]PortSpec, 0, len(ports))
	for _, port := range ports {
		if port.Kind != kind {
			out = append(out, port)
		}
	}
	return out
}
