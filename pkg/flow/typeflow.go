package flow

// Type propagation. A node's own metadata can only say so much: a delay, a
// switch, or a filter hands on exactly what it was given, so the honest answer
// for its output is not "any" but "whatever is upstream". Resolving that needs
// the graph, which NodePorts deliberately does not have — it answers for one
// placed node — so it happens here, once, over a whole graph.
//
// The single-source rule makes this unambiguous: an input port accepts at most
// one edge, so a port that carries its input onward has exactly one type to
// carry.

// maxTypeFlowDepth bounds how far a type is chased through pass-through nodes.
// A graph may contain a cycle, and a chain long enough to hit this is past the
// point where a resolved type tells an author anything useful.
const maxTypeFlowDepth = 64

// PropagateTypes resolves the outputs that carry their input's payload onward,
// across a whole graph. Ports resolves each node's own ports (from NodePorts);
// this fills in the ones that could only be answered by looking upstream.
//
// It is applied to the map in place, since callers hold it while checking every
// edge and expect one consistent answer per port.
func PropagateTypes(g *Graph, ports map[string]PortSet) {
	if len(ports) == 0 {
		return
	}

	// Index each input port's single source. Value edges are skipped: they
	// carry a config value on demand, not the message this is tracing.
	sources := make(map[string]edgeEnd, len(g.Edges))
	for _, e := range g.Edges {
		if e.IsValue() {
			continue
		}
		sources[portKey(e.Target, e.TargetHandle)] = edgeEnd{node: e.Source, port: e.SourceHandle}
	}

	// A link-get has no wired input: its upstream is whatever feeds the link-set
	// publishing the same name. The set is a dead end with no output port, so
	// the get's virtual source is the set's own source directly, which is what
	// lets the get's output (declared TypeFrom "in") carry that type across the
	// canvas without a drawn edge. The editor mirrors this in EditorShell's
	// linkVirtualEdges.
	if sets := LinkSetIndex(g); sets != nil {
		for i := range g.Nodes {
			n := &g.Nodes[i]
			if n.Type != NodeTypeLinkGet {
				continue
			}
			if setID, ok := sets[LinkName(n)]; ok {
				if src, wired := sources[portKey(setID, "in")]; wired {
					sources[portKey(n.ID, "in")] = src
				}
			}
		}
	}

	resolver := typeFlow{ports: ports, sources: sources, resolved: map[string]string{}}
	for nodeID, set := range ports {
		if !set.Known {
			continue
		}
		var updated []PortSpec
		for i, p := range set.Outputs {
			carried := resolver.outputType(nodeID, p, 0)
			if carried == p.Type {
				continue
			}
			if updated == nil {
				updated = append([]PortSpec{}, set.Outputs...)
			}
			updated[i].Type = carried
		}
		if updated != nil {
			set.Outputs = updated
			ports[nodeID] = set
		}
	}
}

// edgeEnd is the output port on the far side of an edge.
type edgeEnd struct {
	node string
	port string
}

// typeFlow resolves carried types, memoizing per output port so a node feeding
// several branches is only chased once.
type typeFlow struct {
	ports    map[string]PortSet
	sources  map[string]edgeEnd
	resolved map[string]string
}

// outputType returns what an output port carries, following TypeFrom upstream.
// A port that declares its own type keeps it; only an undeclared one asks what
// it was handed.
func (t typeFlow) outputType(nodeID string, p PortSpec, depth int) string {
	if p.TypeFrom == "" || p.Type != PortAny || depth >= maxTypeFlowDepth {
		return p.Type
	}
	key := portKey(nodeID, p.ID)
	if cached, ok := t.resolved[key]; ok {
		return cached
	}
	// Mark the port before recursing so a cycle resolves to "any" rather than
	// running until the depth cap.
	t.resolved[key] = PortAny

	src, wired := t.sources[portKey(nodeID, p.TypeFrom)]
	if !wired {
		return PortAny
	}
	upstream, ok := t.ports[src.node]
	if !ok || !upstream.Known {
		return PortAny
	}
	spec, ok := upstream.Output(src.port)
	if !ok {
		return PortAny
	}
	carried := t.outputType(src.node, spec, depth+1)
	t.resolved[key] = carried
	return carried
}

// portKey identifies one port of one node.
func portKey(nodeID, portID string) string { return nodeID + "/" + portID }
