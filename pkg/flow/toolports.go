package flow

import (
	"encoding/json"
	"regexp"
	"strings"
)

// NodeTypeTool is the AI agent's graph-backed tool: it declares what the model
// sees (a name, a description, an argument list) and delegates the body to the
// canvas. A call from the model is injected on its call output, the wired
// branch runs, and the message arriving back at its return input is the
// observation handed to the model.
const NodeTypeTool = "tool"

// ToolCallPort is the output a tool call's message is injected on; its payload
// is the arguments object the model supplied.
const ToolCallPort = "call"

// ToolReturnPort is the loop-back input the branch wires its result into. A
// message arriving here resolves the pending call rather than dispatching the
// node.
const ToolReturnPort = "return"

// toolArgPortPrefix namespaces argument ports so they can never collide with
// the node's fixed output IDs.
const toolArgPortPrefix = "arg:"

// ToolArgPortID returns the output port ID that carries an argument's value.
func ToolArgPortID(argID string) string { return toolArgPortPrefix + argID }

// ToolArgID returns the argument a port carries, and whether the port is an
// argument port at all.
func ToolArgID(portID string) (string, bool) {
	if !strings.HasPrefix(portID, toolArgPortPrefix) {
		return "", false
	}
	return strings.TrimPrefix(portID, toolArgPortPrefix), true
}

// ToolArg is one argument a Tool node declares: what the model is told to
// supply, and the typed value port the branch reads it from.
type ToolArg struct {
	ID          string `json:"id" description:"Argument name the model supplies it by."`
	Label       string `json:"label,omitempty" description:"Display name; defaults to the ID."`
	Type        string `json:"type,omitempty" description:"Argument type; defaults to string."`
	Description string `json:"description,omitempty" description:"What the argument means; the model fills it in by this text."`
	Required    bool   `json:"required,omitempty" description:"Whether the model must supply it."`
}

// toolNodeConfig is the part of a Tool node's config the graph reasons about.
type toolNodeConfig struct {
	Name      string    `json:"name"`
	Arguments []ToolArg `json:"tool_args"`
	Timeout   float64   `json:"timeout"`
}

// decodeToolNode reads a Tool node's config, treating an unreadable config as
// an unconfigured node: config schema validation is what reports a malformed
// one.
func decodeToolNode(raw json.RawMessage) toolNodeConfig {
	var cfg toolNodeConfig
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &cfg)
	}
	return cfg
}

// ToolArgs returns the arguments a Tool node declares.
func ToolArgs(raw json.RawMessage) []ToolArg {
	return decodeToolNode(raw).Arguments
}

// ToolName returns the name a Tool node answers to, or "" when unset.
func ToolName(raw json.RawMessage) string {
	return decodeToolNode(raw).Name
}

// ToolArgRequired reports whether a declared argument is one the model must
// supply. Unknown arguments are not required: the port derivation already
// skipped them.
func ToolArgRequired(raw json.RawMessage, argID string) bool {
	for _, a := range ToolArgs(raw) {
		if a.ID == argID {
			return a.Required
		}
	}
	return false
}

// ToolArgPorts derives a Tool node's argument output ports from its config:
// one typed value port per declared argument. They carry the param kind — the
// marker that they supply values rather than messages — and their values exist
// only while a call runs, so the engine resolves them per call instead of
// pulling the node.
func ToolArgPorts(raw json.RawMessage) []PortSpec {
	args := ToolArgs(raw)
	if len(args) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(args))
	specs := make([]PortSpec, 0, len(args))
	for _, a := range args {
		if a.ID == "" || seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		label := a.Label
		if label == "" {
			label = a.ID
		}
		argType := a.Type
		if argType == "" {
			argType = PortString
		}
		specs = append(specs, PortSpec{
			ID:    ToolArgPortID(a.ID),
			Label: label,
			Type:  argType,
			Kind:  PortKindParam,
		})
	}
	return specs
}

// ValidToolArgType reports whether a type may be declared on a tool argument.
// The set is closed: an argument must have a JSON Schema shape the model can
// fill in, which rules out any and binary.
func ValidToolArgType(t string) bool {
	switch t {
	case PortString, PortNumber, PortInteger, PortFloat, PortBool, PortObject:
		return true
	}
	return false
}

// toolNameRE is the tool-name shape every AI provider accepts.
var toolNameRE = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)

// ValidToolName reports whether a tool name is one providers accept: 1-64
// characters of letters, digits, _ and -.
func ValidToolName(name string) bool { return toolNameRE.MatchString(name) }

// validateToolNode reports argument declarations that cannot work. The name's
// shape is checked even on drafts — a bad name is never a half-finished one —
// while an empty name is left to the required-field check.
func validateToolNode(p *problems, key string, n *Node) {
	cfg := decodeToolNode(n.Config)
	if cfg.Name != "" && !ValidToolName(cfg.Name) {
		p.add(key, "tool name %q is not usable: providers accept 1-64 characters of letters, digits, _ and -", cfg.Name)
	}
	if cfg.Timeout < 0 {
		p.add(key, "tool timeout cannot be negative")
	}
	seen := make(map[string]bool, len(cfg.Arguments))
	for i, a := range cfg.Arguments {
		if a.ID == "" {
			p.add(key, "tool argument %d has no name", i+1)
			continue
		}
		if seen[a.ID] {
			p.add(key, "tool has two arguments named %q", a.ID)
		}
		seen[a.ID] = true
		if a.Type != "" && !ValidToolArgType(a.Type) {
			p.add(key, "tool argument %q has unknown type %q", a.ID, a.Type)
		}
	}
}

// validateToolWiring checks what a deployed Tool node needs beyond its own
// config: a unique name per graph (the name is what the agent offers the
// model, so two nodes sharing one would collide in the tool table), a wired
// call output (the branch that runs), and a wired return input (without one a
// call could only ever time out).
func validateToolWiring(p *problems, loc string, g *Graph, msgSources map[string]portRef) {
	callWired := make(map[string]bool)
	for i := range g.Edges {
		e := &g.Edges[i]
		if !e.IsValue() && e.SourceHandle == ToolCallPort {
			callWired[e.Source] = true
		}
	}
	names := make(map[string]string)
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeTypeTool {
			continue
		}
		key := loc + "node:" + n.ID
		if name := ToolName(n.Config); name != "" {
			if first, ok := names[name]; ok {
				p.add(key, "tool name %q is already used by node %q", name, first)
			} else {
				names[name] = n.ID
			}
		}
		if !callWired[n.ID] {
			p.add(key, "the call output is not wired, so the tool has no branch to run")
		}
		if _, ok := msgSources[n.ID+"/"+ToolReturnPort]; !ok {
			p.add(key, "nothing is wired into the return input, so a call could only time out")
		}
	}
}
