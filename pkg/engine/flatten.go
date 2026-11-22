package engine

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// SharedSubflowResolver resolves a shared subflow flow ID to the graph that
// should execute for it (the deployed version). Wired from the database at
// serve time; a resolver error fails run creation with a clear message.
type SharedSubflowResolver func(id uuid.UUID) (*flow.Graph, error)

// FlattenGraph expands every subflow node in a graph into its child nodes,
// producing a plain graph the run scheduler executes unchanged.
//
// Flatten semantics: child node IDs are namespaced as
// "<subflowNodeID>/<childID>" (recursively for nested subflows). The
// subflow-input/subflow-output pseudo-nodes never execute — they are spliced
// out entirely: a parent edge into subflow port X becomes direct edges onto
// every child node that port X's pseudo-input feeds, and the child edge into
// pseudo-output Y becomes direct edges onto every parent target of port Y.
// Chains of pseudo-nodes (a passthrough subflow, or subflows wired
// back-to-back) splice transitively. Validation rejects cycles, but nesting
// depth is still capped defensively.
//
// The link nodes (reroute, link-set, link-get) are spliced the same way: they
// are wiring rather than steps, so each one is replaced by the direct edges it
// stands for. A set/get pair splices through a synthesized edge from the set to
// every get publishing the same name, scoped to the graph level declaring them.
//
// A bypassed node is spliced too, but port-aware: each of its outputs forwards
// the input flow.BypassPairs matched it with, and an output no input can
// honestly feed goes dark. The registry supplies the port metadata the pairing
// needs; nil falls back to the process default.
func FlattenGraph(g *flow.Graph, resolver SharedSubflowResolver, reg flow.MetaRegistry) (*flow.Graph, error) {
	if !needsFlatten(g) {
		return g, nil
	}
	if reg == nil {
		reg = Default()
	}
	f := &flattener{
		resolver: resolver,
		reg:      reg,
		out:      &flow.Graph{Viewport: g.Viewport},
		pseudo:   make(map[string]bool),
		defaults: make(map[string]bool),
		boundary: make(map[string]string),
		pairing:  make(map[string]map[string]string),
	}
	if err := f.expand(g, g.Subgraphs, "", 0); err != nil {
		return nil, err
	}
	f.splice()
	return f.out, nil
}

// needsFlatten reports whether the top-level graph places any subflow, link,
// or bypassed nodes. Subgraphs only execute through subflow nodes, so orphaned
// subgraph entries alone never require flattening.
func needsFlatten(g *flow.Graph) bool {
	for i := range g.Nodes {
		if g.Nodes[i].Type == flow.NodeTypeSubflow || flow.IsLinkNode(g.Nodes[i].Type) || g.Nodes[i].Bypass {
			return true
		}
	}
	return false
}

// flattener accumulates the expanded graph plus the pseudo-node bookkeeping
// needed to splice subflow boundaries.
type flattener struct {
	resolver SharedSubflowResolver
	// reg supplies node metadata for bypass port pairing.
	reg flow.MetaRegistry
	// defaults marks value-typed input pseudo-nodes kept in the graph to supply
	// a parameter default. They are removed only if the parent wired the port.
	defaults map[string]bool
	out      *flow.Graph
	// pseudo marks namespaced pseudo-node IDs pending splice removal.
	pseudo map[string]bool
	// boundary maps "<namespacedSubflowNodeID>|<dir>|<portID>" to the
	// namespaced pseudo-node that declares the port.
	boundary map[string]string
	// pairing constrains a pseudo-node's splice to matched ports: output
	// handle → input handle, from flow.BypassPairs. A pseudo without an entry
	// splices every in × out pair, which is the link-node behavior.
	pairing map[string]map[string]string
}

// expand walks one graph document, appending namespaced nodes and edges to
// the output. subs is the subgraph environment local subgraph references
// resolve against: the root document's map for the top graph and local
// subgraphs, a shared flow's own map inside that flow.
func (f *flattener) expand(g *flow.Graph, subs map[string]flow.Graph, prefix string, depth int) error {
	if depth > flow.MaxSubflowDepth {
		return fmt.Errorf("subflow nesting exceeds the maximum depth of %d", flow.MaxSubflowDepth)
	}

	// First pass: classify nodes so edge rewriting knows subflow endpoints.
	types := make(map[string]string, len(g.Nodes))
	for i := range g.Nodes {
		types[g.Nodes[i].ID] = g.Nodes[i].Type
	}

	// Link names are scoped to this level: a set inside a subflow is invisible
	// to the graph placing it, so the index is built fresh per document.
	linkSets := flow.LinkSetIndex(g)

	// bypassed marks this level's nodes spliced out by their bypass flag, so
	// the edge rewriting below leaves their edges pointed at them rather than
	// redirecting a bypassed subflow's edges onto boundary ports that were
	// never expanded.
	bypassed := make(map[string]bool)

	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Bypass {
			handled, err := f.bypassNode(n, prefix, subs)
			if err != nil {
				return err
			}
			if handled {
				bypassed[n.ID] = true
				continue
			}
		}
		switch n.Type {
		case flow.NodeTypeSubflow:
			child, childSubs, err := f.resolveChild(n, subs)
			if err != nil {
				return fmt.Errorf("subflow node %q: %w", prefix+n.ID, err)
			}
			if n.SubflowID != nil {
				child, childSubs = withFaceplateValues(child, childSubs, n.Config)
			}
			if err := f.expand(child, childSubs, prefix+n.ID+"/", depth+1); err != nil {
				return err
			}
		case flow.NodeTypeReroute, flow.NodeTypeLinkSet:
			// Pure wiring: spliced into the direct edges it stands for.
			f.pseudo[prefix+n.ID] = true
		case flow.NodeTypeLinkGet:
			// A get is wiring too, with its input side synthesized: the edge
			// from the set publishing the same name is what the splice resolves
			// into direct connections.
			id := prefix + n.ID
			f.pseudo[id] = true
			setID, ok := linkSets[flow.LinkName(n)]
			if !ok {
				return fmt.Errorf("get node %q: no set node publishes link name %q", id, flow.LinkName(n))
			}
			f.out.Edges = append(f.out.Edges, flow.Edge{
				ID:           "link/" + id,
				Source:       prefix + setID,
				SourceHandle: "out",
				Target:       id,
				TargetHandle: "in",
			})
		case flow.NodeTypeSubflowInput, flow.NodeTypeSubflowOutput:
			// A pseudo-node at the very top level has no owning subflow node;
			// validation forbids it, so just drop it.
			if prefix == "" {
				continue
			}
			cfg := flow.DecodePortNode(n)
			id := prefix + n.ID
			f.pseudo[id] = true
			// A value-typed input doubles as a value node holding the port's
			// default. It is kept in the graph so an unwired parameter still
			// has something to pull; splice() drops it once the parent wires
			// the parameter, because then the parent's source supplies it.
			if n.Type == flow.NodeTypeSubflowInput && flow.IsValuePortType(cfg.PortType) {
				cp := *n
				cp.ID = id
				f.out.Nodes = append(f.out.Nodes, cp)
				f.defaults[id] = true
			}
			owner := strings.TrimSuffix(prefix, "/")
			dir := "in"
			if n.Type == flow.NodeTypeSubflowOutput {
				dir = "out"
			}
			f.boundary[owner+"|"+dir+"|"+cfg.PortID] = id
		default:
			cp := *n
			cp.ID = prefix + n.ID
			f.out.Nodes = append(f.out.Nodes, cp)
		}
	}

	// Rewrite edges: subflow endpoints redirect onto the boundary pseudo-node
	// of the referenced port; everything else is namespaced verbatim. A
	// bypassed subflow was not expanded, so its edges keep their handles and
	// the splice pairing resolves them.
	for i := range g.Edges {
		e := g.Edges[i]
		e.ID = prefix + e.ID
		if types[e.Source] == flow.NodeTypeSubflow && !bypassed[e.Source] {
			id, ok := f.boundary[prefix+e.Source+"|out|"+e.SourceHandle]
			if !ok {
				return fmt.Errorf("subflow node %q has no output port %q", prefix+e.Source, e.SourceHandle)
			}
			e.Source = id
			e.SourceHandle = "out"
		} else {
			e.Source = prefix + e.Source
		}
		if types[e.Target] == flow.NodeTypeSubflow && !bypassed[e.Target] {
			id, ok := f.boundary[prefix+e.Target+"|in|"+e.TargetHandle]
			if !ok {
				return fmt.Errorf("subflow node %q has no input port %q", prefix+e.Target, e.TargetHandle)
			}
			e.Target = id
			e.TargetHandle = "in"
		} else {
			e.Target = prefix + e.Target
		}
		f.out.Edges = append(f.out.Edges, e)
	}
	return nil
}

// bypassNode splices a bypassed node out of the graph, pairing its outputs
// with the inputs they forward. It reports false — leaving the node to run
// normally — when the flag means nothing here: a link node is already wiring,
// a pseudo-port belongs to the subflow machinery, and a node with no message
// input has nothing to forward.
func (f *flattener) bypassNode(n *flow.Node, prefix string, subs map[string]flow.Graph) (bool, error) {
	if flow.IsLinkNode(n.Type) || n.Type == flow.NodeTypeSubflowInput || n.Type == flow.NodeTypeSubflowOutput {
		return false, nil
	}

	// Resolve the node's ports: a subflow's come from the graph it references,
	// which is deliberately not expanded — bypassing it skips its whole body.
	var set flow.PortSet
	if n.Type == flow.NodeTypeSubflow {
		child, _, err := f.resolveChild(n, subs)
		if err != nil {
			return false, fmt.Errorf("bypassed subflow node %q: %w", prefix+n.ID, err)
		}
		ins, outs := flow.SubflowPorts(child)
		set = flow.PortSet{Inputs: ins, Outputs: outs, Known: true}
	} else {
		meta, ok := f.reg.MetaFor(n.Type)
		if !ok {
			return false, fmt.Errorf("bypassed node %q has unknown type %q", prefix+n.ID, n.Type)
		}
		set = flow.NodePorts(meta, n, nil)
	}
	if !flow.Bypassable(n.Type, set) {
		return false, nil
	}

	id := prefix + n.ID
	f.pseudo[id] = true
	f.pairing[id] = flow.BypassPairs(set)
	return true, nil
}

// resolveChild resolves a subflow node's referenced graph and the subgraph
// environment its own local references use: local subgraphs share the owning
// document's environment; a shared flow brings its own subgraphs map.
func (f *flattener) resolveChild(n *flow.Node, subs map[string]flow.Graph) (*flow.Graph, map[string]flow.Graph, error) {
	switch {
	case n.SubgraphID != "" && n.SubflowID == nil:
		sub, ok := subs[n.SubgraphID]
		if !ok {
			return nil, nil, fmt.Errorf("subgraph %q does not exist in this flow", n.SubgraphID)
		}
		return &sub, subs, nil
	case n.SubflowID != nil && n.SubgraphID == "":
		if f.resolver == nil {
			return nil, nil, fmt.Errorf("shared subflow %s cannot be resolved: no resolver configured", n.SubflowID)
		}
		shared, err := f.resolver(*n.SubflowID)
		if err != nil {
			return nil, nil, err
		}
		return shared, shared.Subgraphs, nil
	default:
		return nil, nil, fmt.Errorf("subflow node must reference exactly one of subflow_id or subgraph_id")
	}
}

// splice removes every pseudo-node, replacing its incoming and outgoing edges
// with the cross product of direct edges. Removing one pseudo-node at a time
// resolves chains of pseudo-nodes transitively.
func (f *flattener) splice() {
	for len(f.pseudo) > 0 {
		// Value defaults go last. Whether one stays depends on what is wired
		// into it, and that is only known once the wiring feeding it has been
		// spliced: a bypassed node's dark output drops the edge it would
		// otherwise count as a source.
		var id string
		for p := range f.pseudo {
			id = p
			if !f.defaults[p] {
				break
			}
		}
		delete(f.pseudo, id)

		var ins []flow.Edge
		for _, e := range f.out.Edges {
			if e.Target == id {
				ins = append(ins, e)
			}
		}
		// A value default with nothing wired into it stays: it is the node the
		// consumer pulls. Leave its edges untouched.
		if f.defaults[id] && len(ins) == 0 {
			continue
		}
		// A bypassed node splices port-aware: an output forwards only the
		// input it was paired with, so an unmatched output goes dark and a
		// parameter binding is dropped rather than routed onward. Pseudo-nodes
		// without a pairing (subflow ports, link nodes) have one input and one
		// output side and splice every combination.
		//
		// Spliced edges take the place of the outgoing edge they replace, so a
		// multi port keeps its sources in the order its own edges list them
		// whichever pseudo-node is spliced first.
		pairs, paired := f.pairing[id]
		rest := make([]flow.Edge, 0, len(f.out.Edges))
		for _, oe := range f.out.Edges {
			if oe.Target == id {
				continue
			}
			if oe.Source != id {
				rest = append(rest, oe)
				continue
			}
			for _, ie := range ins {
				if paired {
					in, ok := pairs[oe.SourceHandle]
					if !ok || in != ie.TargetHandle {
						continue
					}
				}
				rest = append(rest, flow.Edge{
					ID:           ie.ID + ">" + oe.ID,
					Source:       ie.Source,
					SourceHandle: ie.SourceHandle,
					Target:       oe.Target,
					TargetHandle: oe.TargetHandle,
					// The spliced edge means whatever the consumer's own edge
					// meant: a parameter binding stays a parameter binding
					// across the boundary, rather than becoming a message.
					Kind: oe.Kind,
				})
			}
		}
		f.out.Edges = rest
		// The node is being spliced out, so drop the default copy with it.
		if f.defaults[id] {
			delete(f.defaults, id)
			kept := f.out.Nodes[:0]
			for _, n := range f.out.Nodes {
				if n.ID != id {
					kept = append(kept, n)
				}
			}
			f.out.Nodes = kept
		}
	}
}

// withFaceplateValues returns a shared subflow's graph with one calling
// node's faceplate values written over the subflow's own. A value that is
// unset, names a control the subflow no longer has, or no longer fits its
// control is left out, so the subflow's own value stands. The resolved graph
// may be shared through a cache, so only copies are changed: the node slice,
// the subgraph map, and each changed config.
func withFaceplateValues(g *flow.Graph, subs map[string]flow.Graph, callerConfig json.RawMessage) (*flow.Graph, map[string]flow.Graph) {
	var cfg struct {
		Values map[string]map[string]any `json:"faceplate_values"`
	}
	if json.Unmarshal(callerConfig, &cfg) != nil || len(cfg.Values) == 0 {
		return g, subs
	}
	override := func(nodes []flow.Node, prefix string) []flow.Node {
		out := make([]flow.Node, len(nodes))
		copy(out, nodes)
		for i := range out {
			values, ok := cfg.Values[prefix+out[i].ID]
			if !ok || out[i].Type != flow.NodeTypeFaceplate {
				continue
			}
			conf := map[string]any{}
			if len(out[i].Config) > 0 && json.Unmarshal(out[i].Config, &conf) != nil {
				continue
			}
			merged := map[string]any{}
			if stored, ok := conf["values"].(map[string]any); ok {
				for k, v := range stored {
					merged[k] = v
				}
			}
			fp, err := flow.ParseFaceplate(out[i].Config)
			if err != nil {
				continue
			}
			controls := make(map[string]flow.FaceplateControl, len(fp.Controls))
			for _, c := range fp.Controls {
				controls[c.ID] = c
			}
			for k, v := range values {
				c, found := controls[k]
				if !found || v == nil || flow.CheckControlValue(c, v) != "" {
					continue
				}
				merged[k] = v
			}
			conf["values"] = merged
			if raw, err := json.Marshal(conf); err == nil {
				out[i].Config = raw
			}
		}
		return out
	}
	cp := *g
	cp.Nodes = override(g.Nodes, "")
	newSubs := make(map[string]flow.Graph, len(subs))
	for id, sub := range subs {
		sub.Nodes = override(sub.Nodes, id+"/")
		newSubs[id] = sub
	}
	cp.Subgraphs = newSubs
	return &cp, newSubs
}
