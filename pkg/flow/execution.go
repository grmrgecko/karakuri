package flow

// ExecutionGraph returns the part of a graph that can run from entryID. It
// includes pure value sources pulled by reachable nodes, but does not follow
// message outputs from a value that is only pulled, so a draft left unwired on
// the canvas is neither validated nor scheduled. Two kinds of node are kept
// without being reachable forward from the entry.
//
// A pulled Tool node's call branch. The agent reaches the tool only over the
// value edge into its tools input, yet the call output is the branch the
// engine runs when the model calls the tool, so a pulled tool's call edges are
// followed.
//
// The nodes upstream of a start node. A run that begins partway down the graph
// never dispatches them, but the edges into the start node are what name the
// inputs a seed has to fill, and the nodes settle as skipped so the editor
// draws them that way. They are carried inert: present and validated, with
// their message outputs followed no further than nodes already kept.
//
// Wiring that flattening splices out (a reroute, a set/get pair, a bypassed
// node) has no value of its own: what it forwards comes in over its message
// input, or from the set publishing a get's name. Pulling or carrying such a
// node keeps that source too, or the splice would leave the consumer unbound.
func ExecutionGraph(g *Graph, entryID string) *Graph {
	tools := make(map[string]bool)
	wiring := make(map[string]bool)
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type == NodeTypeTool {
			tools[n.ID] = true
		}
		if IsLinkNode(n.Type) || n.Bypass {
			wiring[n.ID] = true
		}
	}
	sets := LinkSetIndex(g)
	running := map[string]bool{entryID: true}
	pulled := make(map[string]bool)
	inert := upstream(g, entryID, sets)

	changed := true
	for changed {
		changed = false
		for i := range g.Edges {
			edge := &g.Edges[i]
			if edge.IsValue() {
				if (running[edge.Target] || pulled[edge.Target] || inert[edge.Target]) && !pulled[edge.Source] {
					pulled[edge.Source] = true
					changed = true
				}
				continue
			}
			if pulled[edge.Target] && wiring[edge.Target] && !pulled[edge.Source] {
				pulled[edge.Source] = true
				changed = true
			}
			called := pulled[edge.Source] && tools[edge.Source] && edge.SourceHandle == ToolCallPort
			if (running[edge.Source] || called) && !running[edge.Target] {
				running[edge.Target] = true
				changed = true
			}
		}

		for i := range g.Nodes {
			get := &g.Nodes[i]
			if get.Type != NodeTypeLinkGet || !pulled[get.ID] {
				continue
			}
			if set, ok := sets[LinkName(get)]; ok && !pulled[set] {
				pulled[set] = true
				changed = true
			}
		}
		for i := range g.Nodes {
			set := &g.Nodes[i]
			if set.Type != NodeTypeLinkSet || !running[set.ID] {
				continue
			}
			name := LinkName(set)
			for j := range g.Nodes {
				get := &g.Nodes[j]
				if get.Type == NodeTypeLinkGet && LinkName(get) == name && !running[get.ID] {
					running[get.ID] = true
					changed = true
				}
			}
		}
	}

	active := func(id string) bool { return running[id] || pulled[id] || inert[id] }
	out := &Graph{Viewport: g.Viewport, Subgraphs: g.Subgraphs}
	for i := range g.Nodes {
		if active(g.Nodes[i].ID) {
			out.Nodes = append(out.Nodes, g.Nodes[i])
		}
	}
	for i := range g.Edges {
		if active(g.Edges[i].Source) && active(g.Edges[i].Target) {
			out.Edges = append(out.Edges, g.Edges[i])
		}
	}
	return out
}

// upstream returns the nodes whose message output reaches entryID through any
// number of hops, crossing a get to the set publishing its name. A run started
// at entryID leaves every one of them un-run.
func upstream(g *Graph, entryID string, sets map[string]string) map[string]bool {
	gets := make(map[string]string)
	for i := range g.Nodes {
		if g.Nodes[i].Type == NodeTypeLinkGet {
			if set, ok := sets[LinkName(&g.Nodes[i])]; ok {
				gets[g.Nodes[i].ID] = set
			}
		}
	}
	seen := make(map[string]bool)
	queue := []string{entryID}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if set, ok := gets[id]; ok && !seen[set] {
			seen[set] = true
			queue = append(queue, set)
		}
		for i := range g.Edges {
			edge := &g.Edges[i]
			if edge.IsValue() || edge.Target != id || seen[edge.Source] {
				continue
			}
			seen[edge.Source] = true
			queue = append(queue, edge.Source)
		}
	}
	delete(seen, entryID)
	return seen
}
