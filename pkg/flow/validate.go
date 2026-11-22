package flow

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	apperrors "github.com/grmrgecko/karakuri/pkg/errors"
)

// MetaRegistry provides node type metadata for graph validation.
// Implemented by the engine node registry.
type MetaRegistry interface {
	MetaFor(nodeType string) (NodeMeta, bool)
}

// SubflowResolver resolves a shared subflow reference to the graph that
// defines its interface (the deployed version). Implemented by the API layer
// over the database.
type SubflowResolver interface {
	ResolveShared(id uuid.UUID) (*Graph, error)
}

// SelfIdentifier is an optional interface a SubflowResolver may implement to
// report the ID of the flow whose graph is being validated, so direct and
// indirect self-references are caught as cycles before deploy.
type SelfIdentifier interface {
	SelfFlowID() uuid.UUID
}

// problems accumulates validation failures keyed by graph location.
// Multiple failures at the same location are joined into one detail entry.
type problems struct {
	details map[string]string
}

// add records a validation failure at the given location key.
func (p *problems) add(key, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if existing, ok := p.details[key]; ok {
		msg = existing + "; " + msg
	}
	p.details[key] = msg
}

// Validate checks a graph against registered node metadata and returns a
// validation AppError with a detail entry per problem location, or nil when
// the graph is valid. Shared subflow references are not resolved; use
// ValidateFull where a resolver is available.
func Validate(g *Graph, reg MetaRegistry) error {
	return ValidateFull(g, reg, nil, false)
}

// ValidateFull validates a graph including its local subgraphs and subflow
// references. A nil resolver skips shared subflow resolution (port checks on
// shared references and shared cycle detection are then best-effort only).
// isSubflowDoc marks the graph as a shared subflow definition, allowing
// pseudo-port nodes at the top level.
func ValidateFull(g *Graph, reg MetaRegistry, resolver SubflowResolver, isSubflowDoc bool) error {
	return validateFull(g, reg, resolver, isSubflowDoc, false)
}

// ValidateDraft validates like ValidateFull but tolerates missing or empty
// required config fields, so drafts can be saved while nodes are still
// unconfigured (e.g. flows imported with blanked connection references). It
// also tolerates unknown node types, so disabling the plugin that provides a
// node does not lock its flows out of editing. Deploy and test runs use the
// strict ValidateFull.
func ValidateDraft(g *Graph, reg MetaRegistry, resolver SubflowResolver, isSubflowDoc bool) error {
	return validateFull(g, reg, resolver, isSubflowDoc, true)
}

// validateFull runs full validation; lenient relaxes required-field checks.
func validateFull(g *Graph, reg MetaRegistry, resolver SubflowResolver, isSubflowDoc, lenient bool) error {
	v := &validator{
		reg:      reg,
		resolver: resolver,
		root:     g,
		lenient:  lenient,
		p:        &problems{details: make(map[string]string)},
		shared:   make(map[uuid.UUID]*Graph),
	}

	// Validate the top graph and every local subgraph like a top graph.
	v.validateGraph(g, "", isSubflowDoc)
	for id := range g.Subgraphs {
		sub := g.Subgraphs[id]
		v.validateGraph(&sub, "subgraph:"+id+"/", true)
	}

	// Detect circular subflow references across local and shared graphs.
	v.checkCycles()

	if len(v.p.details) == 0 {
		return nil
	}
	return apperrors.ErrValidation.
		WithMessage(fmt.Sprintf("graph validation failed: %d problem(s)", len(v.p.details))).
		WithDetails(v.p.details)
}

// nodePorts is one placed node's resolved ports plus the bits of its type
// metadata the edge checks need. The ports themselves come from NodePorts, the
// same resolver the engine and the editor use.
type nodePorts struct {
	PortSet
	category string
	// pure records whether the node type may be pulled to supply a value.
	pure bool
}

// validator carries shared state across the top graph and its subgraphs.
type validator struct {
	reg      MetaRegistry
	resolver SubflowResolver
	root     *Graph
	// lenient allows required config fields to be missing or empty, used for
	// draft saves where nodes may still be unconfigured.
	lenient bool
	p       *problems
	// shared caches resolved shared subflow graphs by flow ID.
	shared map[uuid.UUID]*Graph
}

// resolveShared resolves and caches a shared subflow graph.
func (v *validator) resolveShared(id uuid.UUID) (*Graph, error) {
	if g, ok := v.shared[id]; ok {
		if g == nil {
			return nil, fmt.Errorf("shared subflow %s could not be resolved", id)
		}
		return g, nil
	}
	g, err := v.resolver.ResolveShared(id)
	if err != nil {
		v.shared[id] = nil
		return nil, err
	}
	v.shared[id] = g
	return g, nil
}

// validateGraph checks one graph's nodes and edges. loc prefixes problem keys
// ("" for the top graph, "subgraph:<id>/" for local subgraphs); portsAllowed
// permits subflow pseudo-port nodes.
func (v *validator) validateGraph(g *Graph, loc string, portsAllowed bool) {
	p := v.p

	// Check nodes: duplicate IDs, known types, refs, and config schema.
	ports := make(map[string]*nodePorts, len(g.Nodes))
	nodes := make(map[string]*Node, len(g.Nodes))
	portIDs := make(map[string]string) // direction+port_id → first declaring node.
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID == "" {
			p.add(fmt.Sprintf("%snodes[%d]", loc, i), "node has no ID")
			continue
		}
		key := loc + "node:" + n.ID
		if _, ok := nodes[n.ID]; ok {
			p.add(key, "duplicate node ID %q", n.ID)
			continue
		}
		nodes[n.ID] = n

		meta, ok := v.reg.MetaFor(n.Type)
		if !ok {
			// A draft tolerates a type the registry cannot resolve: disabling
			// the plugin that provides a node must not lock its flows out of
			// editing. The node contributes no ports, so its edges skip their
			// checks below; deploy still rejects it.
			if !v.lenient {
				p.add(key, "unknown node type %q", n.Type)
			}
			continue
		}
		// Resolve the node's ports once, through the same function the engine
		// and the editor use. A subflow's ports live in the graph it
		// references, so that graph is resolved first and handed in.
		var sub *Graph
		if n.Type == NodeTypeSubflow {
			sub = v.resolveSubflowGraph(n, key)
		}
		np := &nodePorts{PortSet: NodePorts(meta, n, sub), category: meta.Category, pure: meta.Pure}
		validateNodeParams(p, key, meta, n)
		ports[n.ID] = np
		validateConfig(p, key, n, meta, v.lenient)
		if n.Retry != nil {
			if !meta.Retryable {
				p.add(key, "node type %q does not accept a retry policy", n.Type)
			}
			if err := n.Retry.Validate(); err != nil {
				p.add(key, "retry policy: %s", err)
			}
		}

		// Pseudo-port nodes are only valid inside subflow graphs, and each
		// port ID may only be declared once per direction.
		if meta.Category == CategoryPort {
			if !portsAllowed {
				p.add(key, "subflow port nodes are only allowed inside subflow graphs")
			}
			cfg := DecodePortNode(n)
			if cfg.PortID != "" {
				dir := "in"
				if n.Type == NodeTypeSubflowOutput {
					dir = "out"
				}
				dupKey := dir + ":" + cfg.PortID
				if first, ok := portIDs[dupKey]; ok {
					p.add(key, "duplicate subflow port ID %q (already declared by node %q)", cfg.PortID, first)
				} else {
					portIDs[dupKey] = n.ID
				}
			}
			// A value parameter's default is read as its own output type, and
			// that type has to satisfy the declared port type — a string
			// parameter whose default parses to an object would emit what the
			// port promises to reject.
			if !v.lenient && n.Type == NodeTypeSubflowInput &&
				IsValuePortType(cfg.PortType) && cfg.OutputFormat != "" {
				if def := PayloadPortType(cfg.OutputFormat); !Compatible(def, cfg.PortType) {
					p.add(key, "default value is read as %s, which a %s parameter cannot accept", def, cfg.PortType)
				}
			}
		}

		// Config shapes the FieldSpec path cannot check: the rule lists and the
		// script port declaration are structured editors with their own rules.
		switch n.Type {
		case NodeTypeBrowserPrompt:
			validateBrowserPrompt(p, key, n, v.lenient)
		case NodeTypeFaceplate:
			validateFaceplate(p, key, n, v.lenient)
		case NodeTypeSwitch:
			validateSwitchRules(p, key, n, v.lenient)
		case NodeTypeScript:
			validateScriptPorts(p, key, n)
		case NodeTypeChange:
			validateChangeRules(p, key, n, v.lenient)
		case NodeTypeTool:
			validateToolNode(p, key, n)
		}
	}

	// Index each input port's message-path source before the edge checks: the
	// link rules below need to know what feeds a node without re-scanning the
	// edge list per question.
	msgSources := make(map[string]portRef, len(g.Edges))
	for i := range g.Edges {
		e := &g.Edges[i]
		if e.IsValue() {
			continue
		}
		msgSources[e.Target+"/"+e.TargetHandle] = portRef{node: e.Source, port: e.SourceHandle}
	}

	// A field exposed as a parameter is supplied only by its wire: the editor
	// disables the field while it is exposed, so a port nothing feeds would
	// leave the field with no value at all. Port fields differ — they are
	// permanent sockets rather than a choice the author made, so only a
	// required one has to be wired. Drafts are left alone while wiring is in
	// progress.
	if !v.lenient {
		valueTargets := make(map[string]bool)
		for i := range g.Edges {
			e := &g.Edges[i]
			if e.IsValue() {
				valueTargets[e.Target+"/"+e.TargetHandle] = true
			}
		}
		for i := range g.Nodes {
			n := &g.Nodes[i]
			if nodes[n.ID] != n {
				continue
			}
			np, ok := ports[n.ID]
			if !ok || !np.Known {
				continue
			}
			meta, hasMeta := v.reg.MetaFor(n.Type)
			for _, in := range np.Inputs {
				fieldID, isParam := ParamFieldID(in.ID)
				if !isParam || valueTargets[n.ID+"/"+in.ID] {
					continue
				}
				if hasMeta {
					if field, ok := fieldByID(meta.ConfigSpec, fieldID); ok && field.Port != "" {
						if field.Required {
							p.add(loc+"node:"+n.ID,
								"input port %q needs a value edge wired to it", fieldID)
						}
						continue
					}
				}
				p.add(loc+"node:"+n.ID,
					"config field %q is exposed as an input but nothing is wired to it", fieldID)
			}
		}
	}

	// Link routing: set/get pairs are level-scoped wiring, and a hole in it is
	// silent at runtime — a get with no set simply never fires — so it is
	// caught here instead. Drafts are left alone: a pair being renamed or a set
	// being rewired is routinely half-done.
	linkSets := LinkSetIndex(g)
	if !v.lenient {
		validateLinks(p, loc, g, linkSets, msgSources)
		validateWidgetTriggers(p, loc, g)
		validateToolWiring(p, loc, g, msgSources)
	}

	// Resolve the outputs that carry their input's payload onward. A node that
	// only routes or delays a message cannot say what it emits on its own, so
	// it is answered from the graph before any edge is checked against it.
	sets := make(map[string]PortSet, len(ports))
	for id, np := range ports {
		sets[id] = np.PortSet
	}
	PropagateTypes(g, sets)
	for id, np := range ports {
		np.PortSet = sets[id]
	}

	// Check edges: endpoint existence, port existence, type compatibility,
	// and the single-source rule for input ports.
	inputSeen := make(map[string]string, len(g.Edges))
	edgeSeen := make(map[string]bool, len(g.Edges))
	for i := range g.Edges {
		e := &g.Edges[i]
		key := loc + "edge:" + e.ID
		if e.ID == "" {
			key = fmt.Sprintf("%sedges[%d]", loc, i)
		} else if edgeSeen[e.ID] {
			p.add(key, "duplicate edge ID %q", e.ID)
		}
		edgeSeen[e.ID] = true

		// Resolve endpoints.
		srcNode, srcOK := nodes[e.Source]
		if !srcOK {
			p.add(key, "source node %q does not exist", e.Source)
		}
		tgtNode, tgtOK := nodes[e.Target]
		if !tgtOK {
			p.add(key, "target node %q does not exist", e.Target)
		}

		// Annotation nodes may not participate in edges.
		if srcOK {
			if np, ok := ports[srcNode.ID]; ok && np.category == CategoryAnnotation {
				p.add(key, "annotation node %q may not have edges", e.Source)
				srcOK = false
			}
		}
		if tgtOK {
			if np, ok := ports[tgtNode.ID]; ok && np.category == CategoryAnnotation {
				p.add(key, "annotation node %q may not have edges", e.Target)
				tgtOK = false
			}
		}

		// Resolve ports and check type compatibility. Nodes with unresolved
		// dynamic ports skip per-port checks.
		var srcPort, tgtPort PortSpec
		if srcOK {
			if np, ok := ports[srcNode.ID]; ok && np.Known {
				if srcPort, ok = np.Output(e.SourceHandle); !ok {
					p.add(key, "source node %q has no output port %q", e.Source, e.SourceHandle)
					srcOK = false
				}
			} else {
				srcOK = false
			}
		}
		if tgtOK {
			if np, ok := ports[tgtNode.ID]; ok && np.Known {
				if tgtPort, ok = np.Input(e.TargetHandle); !ok {
					p.add(key, "target node %q has no input port %q", e.Target, e.TargetHandle)
					tgtOK = false
				}
			} else {
				tgtOK = false
			}
		}
		// A value edge is asked to supply a config field of a declared type, so
		// an undeclared source is a real problem there. A message edge only
		// carries whatever the run produces, and "any" on it means the payload
		// is not knowable until then.
		if srcOK && tgtOK {
			ok := MessageCompatible(srcPort.Type, tgtPort.Type)
			if e.IsValue() {
				ok = Compatible(srcPort.Type, tgtPort.Type)
			}
			if !ok {
				p.add(key, "port type %q is not compatible with %q", srcPort.Type, tgtPort.Type)
			}
		}

		// A value edge binds a parameter; a message edge drives the flow. The
		// two must agree with the port they land on, or a value would be
		// delivered as a message or a parameter would never be pulled.
		if tgtOK {
			isParam := tgtPort.Kind == PortKindParam
			switch {
			case e.IsValue() && !isParam:
				p.add(key, "value edge must target a parameter port, not %q", e.TargetHandle)
			case !e.IsValue() && isParam:
				p.add(key, "parameter port %q needs a value edge, not a message edge", e.TargetHandle)
			}
		}
		// The source side has kinds of its own: a tool's argument port supplies
		// a value that exists only while a call runs, and its call output
		// delivers the call message — each works one way and not the other.
		if srcOK {
			switch {
			case srcPort.Kind == PortKindParam && !e.IsValue():
				p.add(key, "argument port %q supplies a value and needs a value edge, not a message edge", e.SourceHandle)
			case e.IsValue() && srcNode.Type == NodeTypeTool && e.SourceHandle == ToolCallPort:
				p.add(key, "the call output delivers the call message and cannot supply a value")
			}
		}
		// Pulling a value runs the source node on demand, so only a node
		// without side effects can supply one. Link and bypassed nodes are
		// wiring rather than sources, so the check lands on the node the
		// chain actually pulls from once they are spliced away.
		if e.IsValue() && srcOK {
			effID := effectiveValueSource(nodes, ports, msgSources, linkSets,
				portRef{node: srcNode.ID, port: e.SourceHandle})
			if effNode, ok := nodes[effID]; ok {
				if np, ok := ports[effID]; ok && np.Known && !np.pure {
					p.add(key, "node %q cannot supply a value: %s has side effects and only runs on the message path",
						effID, effNode.Type)
				}
			}
		}

		// Single-source rule: only one edge may feed an input port. A multi
		// parameter port is the exception — accepting several wires is what it
		// is for, and the pulled values arrive as an array in edge order.
		if e.Target != "" && e.TargetHandle != "" && !(tgtOK && tgtPort.Multi) {
			inputKey := e.Target + "/" + e.TargetHandle
			if firstEdge, ok := inputSeen[inputKey]; ok {
				p.add(key, "input port %q on node %q already has an edge (%s)", e.TargetHandle, e.Target, firstEdge)
			} else {
				inputSeen[inputKey] = e.ID
			}
		}
	}
}

// validateLinks checks one level's set/get wiring: every published name is
// declared once, every get subscribes to a name that exists, and every set has
// something wired into it — an unwired set means its gets never fire, which is
// exactly the kind of silent hole this project exists to reject.
func validateLinks(p *problems, loc string, g *Graph, linkSets map[string]string, msgSources map[string]portRef) {
	seen := make(map[string]string, len(linkSets))
	for i := range g.Nodes {
		n := &g.Nodes[i]
		key := loc + "node:" + n.ID
		switch n.Type {
		case NodeTypeLinkSet:
			name := LinkName(n)
			if name == "" {
				continue // The required-field check already reports this.
			}
			if first, ok := seen[name]; ok {
				p.add(key, "link name %q is already published by node %q", name, first)
			} else {
				seen[name] = n.ID
			}
			if _, wired := msgSources[n.ID+"/in"]; !wired {
				p.add(key, "set node has nothing wired into it, so get %q never fires", name)
			}
		case NodeTypeLinkGet:
			name := LinkName(n)
			if name == "" {
				continue
			}
			if _, ok := linkSets[name]; !ok {
				p.add(key, "no set node publishes link name %q", name)
			}
		}
	}
}

// validateWidgetTriggers checks that no two widget triggers in a graph answer
// to the same name. A dashboard widget stores the name it presses, so a name
// declared twice would leave the press pointing at either node — the deploy is
// rejected rather than resolved arbitrarily. Drafts are left alone: a trigger
// being renamed routinely collides with itself mid-edit.
func validateWidgetTriggers(p *problems, loc string, g *Graph) {
	seen := make(map[string]string)
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeTypeWidgetTrigger {
			continue
		}
		name := WidgetTriggerName(n)
		if name == "" {
			continue // The required-field check already reports this.
		}
		key := loc + "node:" + n.ID
		if first, ok := seen[name]; ok {
			p.add(key, "widget trigger name %q is already used by node %q", name, first)
			continue
		}
		seen[name] = n.ID
	}
}

// maxLinkChase bounds how far a value source is followed through link nodes,
// so a wiring cycle degrades to "unknown source" rather than looping.
const maxLinkChase = 64

// portRef names one output port of one node, the upstream end of an edge.
type portRef struct {
	node string
	port string
}

// effectiveValueSource follows wiring upstream to the node a pulled value
// actually comes from once the splice has run: a reroute or set steps to
// whatever feeds its input, a get steps to the set publishing its name, and a
// bypassed node steps through the input its output was paired with. It
// returns the first real source node, or "" when the chain dead-ends in
// unwired or unresolved wiring — which other checks report on their own terms.
func effectiveValueSource(nodes map[string]*Node, ports map[string]*nodePorts, msgSources map[string]portRef, linkSets map[string]string, start portRef) string {
	cur := start
	for depth := 0; depth < maxLinkChase; depth++ {
		n, ok := nodes[cur.node]
		if !ok {
			return ""
		}
		switch {
		case n.Type == NodeTypeReroute || n.Type == NodeTypeLinkSet:
			src, wired := msgSources[cur.node+"/in"]
			if !wired {
				return ""
			}
			cur = src
		case n.Type == NodeTypeLinkGet:
			setID, ok := linkSets[LinkName(n)]
			if !ok {
				return ""
			}
			cur = portRef{node: setID, port: "out"}
		case n.Bypass:
			np, ok := ports[cur.node]
			if !ok || !np.Known {
				return ""
			}
			// A flag with no message input to forward means the node runs
			// normally, so it is its own source.
			if !Bypassable(n.Type, np.PortSet) {
				return cur.node
			}
			in, ok := BypassPairs(np.PortSet)[cur.port]
			if !ok {
				return ""
			}
			src, wired := msgSources[cur.node+"/"+in]
			if !wired {
				return ""
			}
			cur = src
		default:
			return cur.node
		}
	}
	return ""
}

// ValueSources returns the IDs of the nodes one graph level pulls values
// from. A value edge that leaves a reroute, a get, or a bypassed node is
// followed upstream to the node the engine pulls once that wiring is spliced
// out. root is the document g belongs to, which holds the local subgraphs a
// bypassed subflow node takes its ports from.
func ValueSources(g, root *Graph, metas []NodeMeta) map[string]bool {
	byType := make(map[string]NodeMeta, len(metas))
	for _, meta := range metas {
		byType[meta.Type] = meta
	}
	nodes := make(map[string]*Node, len(g.Nodes))
	ports := make(map[string]*nodePorts)
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if _, dup := nodes[n.ID]; dup {
			continue
		}
		nodes[n.ID] = n
		meta, ok := byType[n.Type]
		if !ok || !n.Bypass {
			continue
		}
		var sub *Graph
		if n.Type == NodeTypeSubflow && root != nil {
			if local, ok := root.Subgraphs[n.SubgraphID]; ok {
				sub = &local
			}
		}
		ports[n.ID] = &nodePorts{PortSet: NodePorts(meta, n, sub)}
	}
	msgSources := make(map[string]portRef, len(g.Edges))
	for i := range g.Edges {
		e := &g.Edges[i]
		if !e.IsValue() {
			msgSources[e.Target+"/"+e.TargetHandle] = portRef{node: e.Source, port: e.SourceHandle}
		}
	}
	linkSets := LinkSetIndex(g)
	sources := make(map[string]bool)
	for i := range g.Edges {
		e := &g.Edges[i]
		if !e.IsValue() {
			continue
		}
		if id := effectiveValueSource(nodes, ports, msgSources, linkSets, portRef{node: e.Source, port: e.SourceHandle}); id != "" {
			sources[id] = true
		}
	}
	return sources
}

// resolveSubflowGraph checks a subflow node's reference and returns the graph
// its ports are derived from, or nil when the reference does not resolve —
// which NodePorts turns into an unknown port set, suppressing per-port edge
// checks for that node. Local subgraph references resolve against the root
// document's subgraphs map; shared references resolve through the resolver
// when present.
func (v *validator) resolveSubflowGraph(n *Node, key string) *Graph {
	hasShared := n.SubflowID != nil
	hasLocal := n.SubgraphID != ""
	switch {
	case hasShared && hasLocal:
		v.p.add(key, "subflow node must reference either subflow_id or subgraph_id, not both")
		return nil
	case !hasShared && !hasLocal:
		v.p.add(key, "subflow node references neither subflow_id nor subgraph_id")
		return nil
	case hasLocal:
		sub, ok := v.root.Subgraphs[n.SubgraphID]
		if !ok {
			v.p.add(key, "subgraph %q does not exist in this flow", n.SubgraphID)
			return nil
		}
		return &sub
	default:
		// A missing resolver is not a problem to report: shared references are
		// simply not checked where none was supplied.
		if v.resolver == nil {
			return nil
		}
		shared, err := v.resolveShared(*n.SubflowID)
		if err != nil {
			v.p.add(key, "shared subflow %s: %v", n.SubflowID, err)
			return nil
		}
		return shared
	}
}

// checkCycles walks subflow references depth-first across local subgraphs and
// shared subflows, reporting circular references and excessive nesting. The
// flow's own ID (when the resolver reports it) seeds the stack so a shared
// self-reference is caught.
func (v *validator) checkCycles() {
	stack := map[string]bool{}
	if si, ok := v.resolver.(SelfIdentifier); ok {
		stack["shared:"+si.SelfFlowID().String()] = true
	}
	v.walkRefs(v.root, v.root.Subgraphs, "doc", "", stack, 0)
}

// walkRefs recurses through the subflow references of one graph. docKey
// namespaces local subgraph IDs per containing document; loc prefixes problem
// keys for nodes of the top document.
func (v *validator) walkRefs(g *Graph, subs map[string]Graph, docKey, loc string, stack map[string]bool, depth int) {
	if depth > MaxSubflowDepth {
		v.p.add("graph", "subflow nesting exceeds the maximum depth of %d", MaxSubflowDepth)
		return
	}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeTypeSubflow {
			continue
		}
		key := loc + "node:" + n.ID
		switch {
		case n.SubgraphID != "" && n.SubflowID == nil:
			refKey := docKey + "|local:" + n.SubgraphID
			if stack[refKey] {
				v.p.add(key, "circular subflow reference: subgraph %q includes itself", n.SubgraphID)
				continue
			}
			sub, ok := subs[n.SubgraphID]
			if !ok {
				continue
			}
			stack[refKey] = true
			v.walkRefs(&sub, subs, docKey, loc, stack, depth+1)
			delete(stack, refKey)
		case n.SubflowID != nil && n.SubgraphID == "":
			if v.resolver == nil {
				continue
			}
			refKey := "shared:" + n.SubflowID.String()
			if stack[refKey] {
				v.p.add(key, "circular subflow reference: shared subflow %s includes itself", n.SubflowID)
				continue
			}
			shared, err := v.resolveShared(*n.SubflowID)
			if err != nil {
				continue
			}
			stack[refKey] = true
			v.walkRefs(shared, shared.Subgraphs, refKey, "", stack, depth+1)
			delete(stack, refKey)
		}
	}
}

// validateSwitchRules checks a switch node's routing rules: known operators,
// a value where the operator needs one, and unique rule IDs so two rules can
// never claim the same output port. Lenient (draft) validation only rejects
// structurally broken rules, since a rule being written is routinely
// incomplete. Legacy configs without rules are checked by their own fields
// through the ordinary FieldSpec path.
func validateSwitchRules(p *problems, key string, n *Node, lenient bool) {
	cfg := DecodeSwitchConfig(n.Config)
	if cfg.IsLegacy() {
		return
	}
	if len(cfg.Rules) == 0 {
		if !lenient {
			p.add(key, "switch node has no routing rules")
		}
		return
	}

	seen := make(map[string]bool, len(cfg.Rules))
	for i, r := range cfg.Rules {
		portID := SwitchRulePortID(r, i)
		if seen[portID] {
			p.add(key, "switch rule %d reuses rule ID %q", i+1, portID)
		}
		seen[portID] = true

		if !switchOperators[r.Operator] {
			p.add(key, "switch rule %d has unknown operator %q", i+1, r.Operator)
			continue
		}
		if !lenient && SwitchOperatorNeedsValue(r.Operator) && strings.TrimSpace(r.Value) == "" {
			p.add(key, "switch rule %d (%s) has no value", i+1, r.Operator)
		}
	}
}

// validateChangeRules checks a change node's rewrite rules: a known operation,
// a target property, and whatever else the operation needs. Lenient (draft)
// validation only rejects structurally broken rules, since a rule being
// written is routinely incomplete.
func validateChangeRules(p *problems, key string, n *Node, lenient bool) {
	cfg := DecodeChangeConfig(n.Config)
	if len(cfg.Rules) == 0 {
		if !lenient {
			p.add(key, "change node has no rules")
		}
		return
	}

	for i, r := range cfg.Rules {
		op := ChangeRuleOp(r)
		if !changeOps[op] {
			p.add(key, "change rule %d has unknown operation %q", i+1, op)
			continue
		}
		if !lenient && strings.TrimSpace(r.Target) == "" {
			p.add(key, "change rule %d (%s) has no target property", i+1, op)
		}

		switch op {
		case ChangeOpSet:
			validateChangeValueSource(p, key, i, r, lenient)
		case ChangeOpChange:
			validateChangeValueSource(p, key, i, r, lenient)
			if !changeSearchTypes[ChangeRuleSearchType(r)] {
				p.add(key, "change rule %d has unknown search type %q", i+1, r.SearchType)
				break
			}
			if strings.TrimSpace(r.Search) == "" {
				if !lenient {
					p.add(key, "change rule %d has nothing to search for", i+1)
				}
				break
			}
			// Catch a broken pattern at deploy rather than mid-run. A draft is
			// left alone: a pattern being typed is routinely half-written.
			// Patterns carrying {{ }} are only known once interpolated.
			if !lenient && ChangeRuleSearchType(r) == ChangeSearchRegex && !strings.Contains(r.Search, "{{") {
				if _, err := regexp.Compile(r.Search); err != nil {
					p.add(key, "change rule %d has an invalid regular expression: %v", i+1, err)
				}
			}
		case ChangeOpMove, ChangeOpCopy:
			source, _ := r.Value.(string)
			if strings.TrimSpace(source) == "" && !lenient {
				p.add(key, "change rule %d (%s) has no source property", i+1, op)
			}
		}
	}
}

// validateChangeValueSource checks the value interpretation of a rule that
// writes a value, and that the sources needing a string operand have one.
func validateChangeValueSource(p *problems, key string, index int, r ChangeRule, lenient bool) {
	from := ChangeRuleFrom(r)
	if !changeValueSources[from] {
		p.add(key, "change rule %d has unknown value source %q", index+1, from)
		return
	}
	if lenient {
		return
	}
	// Expressions and property reads are meaningless when empty; a literal of
	// any other kind may legitimately be an empty string.
	switch from {
	case ChangeFromExpression, ChangeFromProperty:
		operand, _ := r.Value.(string)
		if ChangeRuleOp(r) == ChangeOpChange {
			operand = r.Replace
		}
		if strings.TrimSpace(operand) == "" {
			p.add(key, "change rule %d (%s) has no %s", index+1, from, changeOperandName(from))
		}
	}
}

// changeOperandName names what an expression or property source is missing,
// for the validation message.
func changeOperandName(from string) string {
	if from == ChangeFromExpression {
		return "expression"
	}
	return "source property"
}

// validateConfig checks a node's config values against its FieldSpec schema:
// required fields present, select values within options, and numbers numeric.
// With lenient set, missing or empty required fields are tolerated so drafts
// can be saved before every node is configured.
func validateConfig(p *problems, key string, n *Node, meta NodeMeta, lenient bool) {
	// Decode config into a generic map. Empty config is treated as no values.
	values := map[string]any{}
	if len(n.Config) > 0 {
		if err := json.Unmarshal(n.Config, &values); err != nil {
			p.add(key, "config is not a valid JSON object")
			return
		}
	}

	// wired marks fields exposed as parameter ports. A wired field is supplied
	// by its value edge, so the stored literal is dead and skips every check.
	wired := make(map[string]bool, len(n.Params))
	for _, id := range n.Params {
		wired[id] = true
	}

	for _, field := range meta.ConfigSpec {
		// A port field holds another node's pulled output, never a typed-in
		// value; whether it is wired is checked against the edges instead.
		if field.Port != "" {
			continue
		}
		if wired[field.ID] && FieldBindable(field.Type) {
			continue
		}
		value, present := values[field.ID]

		// Skip validation for fields hidden by an unmet show_if condition.
		if !FieldShown(meta.ConfigSpec, field, values) {
			continue
		}

		// Required fields must be present with a non-empty value; empty
		// strings count as unset and skip the per-type checks below.
		if !present || value == nil {
			if field.Required && !lenient {
				p.add(key, "required config field %q is missing", field.ID)
			}
			continue
		}
		if s, ok := value.(string); ok && s == "" {
			if field.Required && !lenient {
				p.add(key, "required config field %q is empty", field.ID)
			}
			continue
		}

		// Per-type value checks.
		switch field.Type {
		case FieldAttachments:
			raw, err := json.Marshal(value)
			var attachments map[string]Attachment
			if err != nil || json.Unmarshal(raw, &attachments) != nil || attachments == nil {
				p.add(key, "config field %q must be named attachments", field.ID)
				continue
			}
			for _, problem := range ValidateAttachments(attachments) {
				p.add(key, "config field %q %s", field.ID, problem)
			}
		case FieldMarkdown:
			if _, ok := value.(string); !ok {
				p.add(key, "config field %q must be Markdown text", field.ID)
			}
		case FieldNumber:
			if _, ok := value.(float64); !ok {
				p.add(key, "config field %q must be a number", field.ID)
			}
		case FieldBoolean:
			if _, ok := value.(bool); !ok {
				p.add(key, "config field %q must be a boolean", field.ID)
			}
		case FieldScript:
			if _, ok := value.(string); !ok {
				p.add(key, "config field %q must be a string of source code", field.ID)
			}
		case FieldSelect:
			s, ok := value.(string)
			if !ok {
				p.add(key, "config field %q must be a string", field.ID)
				continue
			}
			if !optionAllowed(field.Options, s) {
				p.add(key, "config field %q value %q is not a valid option", field.ID, s)
			}
		case FieldImages:
			// Checked on draft saves as well as deploys: an image the editor
			// would place in an img src is not a half-finished setting, it is
			// a value that must never reach the page.
			for _, problem := range ValidateImages(value) {
				p.add(key, "config field %q %s", field.ID, problem)
			}
		}
	}
}

// ValidateNodeConfig applies the catalog's config-field validation without a
// graph or node execution. Callers remain responsible for rejecting unknown
// fields and for validating permanent parameter-port values.
func ValidateNodeConfig(meta NodeMeta, raw json.RawMessage) error {
	p := &problems{details: make(map[string]string)}
	validateConfig(p, "config", &Node{Type: meta.Type, Config: raw}, meta, false)

	// Direct invocation promises primitive option types in its generated SDK.
	// Keep those checks out of ordinary graph validation, whose manual value
	// source intentionally accepts structured text and JSON values.
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err == nil {
		for _, field := range meta.ConfigSpec {
			if field.Port != "" {
				continue
			}
			value, present := values[field.ID]
			if !present || value == nil || !FieldShown(meta.ConfigSpec, field, values) {
				continue
			}
			switch field.Type {
			case FieldString, FieldConnection, FieldEntity, FieldHATarget, FieldExpression,
				FieldDuration, FieldCron, FieldText, FieldTimezone:
				if _, ok := value.(string); !ok {
					p.add("config", "config field %q must be a string", field.ID)
				}
			}
		}
	}
	if len(p.details) == 0 {
		return nil
	}
	return fmt.Errorf("%s", p.details["config"])
}

// fieldByID returns the config field with the given ID, or false when the
// node type does not declare one.
func fieldByID(spec []FieldSpec, id string) (FieldSpec, bool) {
	for _, f := range spec {
		if f.ID == id {
			return f, true
		}
	}
	return FieldSpec{}, false
}

// fieldDefault returns the default value of the config field with the given
// ID, or nil when the field is unknown or has no default.
func fieldDefault(spec []FieldSpec, id string) any {
	for _, f := range spec {
		if f.ID == id {
			return f.Default
		}
	}
	return nil
}

// optionAllowed reports whether a value appears in a select field's options.
func optionAllowed(options []Option, value string) bool {
	for _, o := range options {
		if o.Value == value {
			return true
		}
	}
	return false
}

// looseEquals compares JSON-decoded values with numeric tolerance so that
// show_if conditions written as ints match decoded float64 values.
// FieldShown reports whether a field's show_if condition is met against the
// given values, falling back to the controlling field's declared default when
// it is unset — so configs saved before a mode field existed evaluate the way
// the editor renders them. Required-field checks skip hidden fields, in graph
// validation and connection config validation alike.
func FieldShown(spec []FieldSpec, field FieldSpec, values map[string]any) bool {
	if field.ShowIf == nil {
		return true
	}
	controlling, ok := values[field.ShowIf.Field]
	if !ok {
		controlling = fieldDefault(spec, field.ShowIf.Field)
	}
	return showIfMatches(controlling, field.ShowIf.Equals)
}

// showIfMatches evaluates a ShowIf expectation: a list matches any of its
// values, "*" matches any non-empty value (the PortTypeRule convention), and
// anything else compares directly. A controlling string holding commas is
// read as the tokens control's list, matching when any one token does, so a
// field can hang off one entry of a token set. Lists arrive as []string from
// Go specs and []any from JSON, so elements are walked reflectively.
func showIfMatches(controlling, equals any) bool {
	rv := reflect.ValueOf(equals)
	if rv.Kind() == reflect.Slice {
		for i := 0; i < rv.Len(); i++ {
			if showIfMatches(controlling, rv.Index(i).Interface()) {
				return true
			}
		}
		return false
	}
	if equals == "*" {
		return configString(controlling) != ""
	}
	if s, ok := controlling.(string); ok && strings.Contains(s, ",") {
		for _, token := range strings.Split(s, ",") {
			if looseEquals(strings.TrimSpace(token), equals) {
				return true
			}
		}
		return false
	}
	return looseEquals(controlling, equals)
}

func looseEquals(a, b any) bool {
	if a == b {
		return true
	}
	af, aOK := toFloat(a)
	bf, bOK := toFloat(b)
	return aOK && bOK && af == bf
}

// toFloat converts numeric values of common Go types to float64.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	}
	return 0, false
}

// Validate rejects retry settings that can overflow waits or loop without a bound.
func (p RetryPolicy) Validate() error {
	if p.MaxAttempts < 1 || p.MaxAttempts > 1000 {
		return fmt.Errorf("max attempts must be between 1 and 1000")
	}
	maxMS := int64(math.MaxInt64) / int64(time.Millisecond)
	if p.DelayMs < 0 || int64(p.DelayMs) > maxMS || p.MaxDelayMs < 0 || int64(p.MaxDelayMs) > maxMS {
		return fmt.Errorf("delays must be nonnegative and fit within a duration")
	}
	if math.IsNaN(p.Backoff) || math.IsInf(p.Backoff, 0) || p.Backoff < 0 || (p.Backoff > 0 && p.Backoff < 1) {
		return fmt.Errorf("backoff must be zero or a finite number at least one")
	}
	if math.IsNaN(p.Jitter) || math.IsInf(p.Jitter, 0) || p.Jitter < 0 || p.Jitter > 1 {
		return fmt.Errorf("jitter must be a finite fraction between zero and one")
	}
	return nil
}
