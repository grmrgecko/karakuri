// Package agent assembles the node types karakuri-agent contributes to a host.
// The node groups beside it (exec, storage, desktop, gui, system) each own
// their node types, their per-platform files, and their names; this package is
// what puts them in one contribution set and applies the operator's disable
// list to it.
package agent

import (
	"fmt"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/agent/desktop"
	"github.com/grmrgecko/karakuri/agent/exec"
	"github.com/grmrgecko/karakuri/agent/gui"
	"github.com/grmrgecko/karakuri/agent/storage"
	"github.com/grmrgecko/karakuri/agent/system"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/internal/version"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// Plugin is the set of node types this agent contributes to the host. The host
// namespaces every type by the agent's id and labels it with the agent's name,
// so nothing here needs to know either.
type Plugin struct {
	opts agentnode.Options
	// sampler is the CPU reading the system metrics node takes deltas against.
	// It lives on the plugin rather than the node so the previous counters
	// survive from one pull to the next.
	sampler *system.CPUSampler
	// disabled holds the node types the operator withheld. A withheld type is
	// never registered, so the host never learns it exists and the palette
	// never offers it.
	disabled map[string]bool
	// sess is the one automation session the desktop, GUI, and system groups
	// share, and sessErr is what building it failed with. It is built once
	// here rather than per group because each session is a real resource — an
	// X connection, a portal session, and on a privileged agent a helper
	// process — and nodeTypes is called several times.
	sess    *automation.Session
	sessErr error
}

// Manifest identifies the contribution set. The host overrides the name with
// the agent's, so this is only a fallback label.
func (Plugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "agent",
		Version:     version.Version,
		Description: "Runs commands, reaches the filesystem, and drives the desktop session on the agent host.",
	}
}

// nodeTypes builds every node type this agent can contribute, disabled or not.
// The order is the order the palette lists them in.
func (p Plugin) nodeTypes() []nodeapi.NodeType {
	var types []nodeapi.NodeType
	types = append(types, exec.Nodes(p.opts)...)
	types = append(types, storage.Nodes(p.opts)...)
	types = append(types, desktop.Nodes(p.opts, p.sess, p.sessErr)...)
	types = append(types, gui.Nodes(p.sess, p.sessErr)...)
	types = append(types, system.Nodes(p.sampler, p.sess, p.sessErr, p.opts.Credentials, p.opts)...)
	// Every execution is bounded here rather than per node: a session tool or
	// kernel call that never answers would otherwise hold its run forever.
	for i, nt := range types {
		types[i] = agentnode.WithBudget(nt, p.opts)
	}
	return types
}

// Register adds the node types the operator left enabled.
func (p Plugin) Register(r plugin.Registrar) error {
	for _, nt := range p.nodeTypes() {
		if p.disabled[nt.Meta().Type] {
			continue
		}
		r.RegisterNode(nt)
	}
	return nil
}

// New builds the plugin from the run options and the operator's disable list.
// An entry naming no node type is an error rather than a warning: a typo in the
// list means a node the operator believes is off is live, and power and command
// are exactly the ones a typo would leave running.
func New(opts agentnode.Options, disable []string) (Plugin, error) {
	p := Plugin{opts: opts, sampler: system.NewCPUSampler(), disabled: make(map[string]bool, len(disable))}
	p.sess, p.sessErr = agentnode.NewSession(opts)

	known := make(map[string]bool)
	for _, nt := range p.nodeTypes() {
		known[nt.Meta().Type] = true
	}

	var unknown []string
	for _, name := range disable {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		if !known[name] {
			unknown = append(unknown, name)
			continue
		}
		p.disabled[name] = true
	}
	if len(unknown) > 0 {
		names := make([]string, 0, len(known))
		for name := range known {
			names = append(names, name)
		}
		sort.Strings(names)
		return Plugin{}, fmt.Errorf("disable names no node type: %s; this agent has %s",
			strings.Join(unknown, ", "), strings.Join(names, ", "))
	}
	return p, nil
}

// Disabled reports how many node types the operator withheld, for the caller
// deciding whether the startup log line is worth printing.
func (p Plugin) Disabled() int { return len(p.disabled) }

// EnabledNames lists the node types that will be registered, for the startup
// log line an operator checks a disable list against.
func (p Plugin) EnabledNames() []string {
	var names []string
	for _, nt := range p.nodeTypes() {
		if !p.disabled[nt.Meta().Type] {
			names = append(names, nt.Meta().Type)
		}
	}
	return names
}
