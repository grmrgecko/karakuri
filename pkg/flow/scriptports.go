package flow

import "encoding/json"

// NodeTypeScript is the script node, whose ports are declared by the author
// rather than fixed by the node type.
const NodeTypeScript = "script"

// ScriptErrorPort is the failure branch every script node carries regardless of
// what the author declared, so routing a script error never depends on
// remembering to add a port for it.
const ScriptErrorPort = "err"

// ScriptPort is one port an author declared on a script node.
type ScriptPort struct {
	ID    string `json:"id" description:"Port identifier used as an edge handle."`
	Label string `json:"label,omitempty" description:"Display name; defaults to the ID."`
	Type  string `json:"type,omitempty" description:"Port payload type; defaults to any."`
}

// scriptPortConfig is the decoded port declaration of a script node.
type scriptPortConfig struct {
	Inputs  []ScriptPort `json:"inputs"`
	Outputs []ScriptPort `json:"outputs"`
}

// ScriptPorts derives a script node's ports from its declaration. A script with
// nothing declared keeps the shape it always had: one input that takes
// anything, one message output, and the error branch.
func ScriptPorts(raw json.RawMessage) (inputs, outputs []PortSpec) {
	var cfg scriptPortConfig
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	return ScriptPortsFrom(cfg.Inputs, cfg.Outputs)
}

// ScriptPortsFrom derives a script node's ports from already-decoded
// declarations, for callers that hold the config as a struct.
func ScriptPortsFrom(declaredIn, declaredOut []ScriptPort) (inputs, outputs []PortSpec) {
	cfg := scriptPortConfig{Inputs: declaredIn, Outputs: declaredOut}

	inputs = scriptPortSpecs(cfg.Inputs, PortAny)
	if len(inputs) == 0 {
		inputs = []PortSpec{{ID: "in", Label: "In", Type: PortAny}}
	}

	outputs = scriptPortSpecs(cfg.Outputs, PortAny)
	if len(outputs) == 0 {
		outputs = []PortSpec{{ID: "out", Label: "Out", Type: PortAny}}
	}
	// The error branch is appended rather than declared, and an author who
	// named a port "err" simply owns it instead. Like every error port it
	// carries what the node was handed, so it reports the first input's type.
	if !hasPort(outputs, ScriptErrorPort) {
		outputs = append(outputs, PortSpec{
			ID: ScriptErrorPort, Label: "Error", Type: PortAny, Kind: PortKindError,
			TypeFrom: inputs[0].ID,
		})
	}
	return inputs, outputs
}

// scriptPortSpecs converts declared ports into specs, dropping unnamed ones and
// keeping the first of any duplicate ID so edges never point at two ports.
func scriptPortSpecs(declared []ScriptPort, defaultType string) []PortSpec {
	if len(declared) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(declared))
	specs := make([]PortSpec, 0, len(declared))
	for _, p := range declared {
		if p.ID == "" || seen[p.ID] {
			continue
		}
		seen[p.ID] = true
		label := p.Label
		if label == "" {
			label = p.ID
		}
		portType := p.Type
		if portType == "" {
			portType = defaultType
		}
		specs = append(specs, PortSpec{ID: p.ID, Label: label, Type: portType})
	}
	return specs
}

// hasPort reports whether a port ID is already present.
func hasPort(ports []PortSpec, id string) bool {
	for _, p := range ports {
		if p.ID == id {
			return true
		}
	}
	return false
}

// ScriptPortTypes lists the types an author may pick for a script port,
// answered from the registry so the editor's picker and this check cannot
// drift: a registered type opts in through its Script flag.
func ScriptPortTypes() []string {
	var out []string
	for _, def := range PortTypes() {
		if def.Script {
			out = append(out, def.Name)
		}
	}
	return out
}

// ValidScriptPortType reports whether a declared port type is one authors may
// choose.
func ValidScriptPortType(t string) bool {
	def, ok := PortTypeFor(t)
	return ok && def.Script
}

// validateScriptPorts reports port declarations that cannot work.
func validateScriptPorts(p *problems, key string, n *Node) {
	var cfg scriptPortConfig
	if len(n.Config) == 0 {
		return
	}
	if err := json.Unmarshal(n.Config, &cfg); err != nil {
		return
	}
	check := func(list []ScriptPort, side string) {
		seen := make(map[string]bool, len(list))
		for i, port := range list {
			if port.ID == "" {
				p.add(key, "script %s port %d has no ID", side, i+1)
				continue
			}
			if seen[port.ID] {
				p.add(key, "script has two %s ports named %q", side, port.ID)
			}
			seen[port.ID] = true
			if port.Type != "" && !ValidScriptPortType(port.Type) {
				p.add(key, "script %s port %q has unknown type %q", side, port.ID, port.Type)
			}
		}
	}
	check(cfg.Inputs, "input")
	check(cfg.Outputs, "output")
}
