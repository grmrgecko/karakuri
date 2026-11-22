package flow

import "encoding/json"

// Link nodes are routing constructs, not processing steps: a reroute is a
// movable elbow in a wire, and a set/get pair carries a value across the canvas
// under a name instead of a drawn edge.
// None of them exist at runtime — the engine splices them out when flattening a
// graph, replacing them with the direct edges they stand for — so everything
// that reasons about them (type propagation, validation, the editor) treats
// them as wiring rather than as nodes that run.
const (
	// NodeTypeReroute is a passthrough elbow: one input, one output, no config.
	NodeTypeReroute = "reroute"
	// NodeTypeLinkSet publishes its input under a name for link-get nodes in the
	// same graph level, and passes it through its own output as well.
	NodeTypeLinkSet = "link-set"
	// NodeTypeLinkGet emits whatever the link-set with the same name receives.
	NodeTypeLinkGet = "link-get"
)

// linkNameConfig is the shared config shape of the set/get pair.
type linkNameConfig struct {
	Name string `json:"name"`
}

// LinkName returns the name a set publishes or a get subscribes to, or "" when
// unset.
func LinkName(n *Node) string {
	var cfg linkNameConfig
	if len(n.Config) > 0 {
		_ = json.Unmarshal(n.Config, &cfg)
	}
	return cfg.Name
}

// IsLinkNode reports whether the node type is spliced out of the graph before
// execution rather than run.
func IsLinkNode(nodeType string) bool {
	return nodeType == NodeTypeReroute || nodeType == NodeTypeLinkSet || nodeType == NodeTypeLinkGet
}

// LinkSetIndex maps each published link name to the node ID of the link-set
// declaring it, within one graph level. Names are level-scoped: a set inside a
// subflow is invisible to the graph placing that subflow. A name declared twice
// keeps its first declaration; validation reports the duplicate.
func LinkSetIndex(g *Graph) map[string]string {
	var sets map[string]string
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeTypeLinkSet {
			continue
		}
		name := LinkName(n)
		if name == "" {
			continue
		}
		if sets == nil {
			sets = make(map[string]string)
		}
		if _, ok := sets[name]; !ok {
			sets[name] = n.ID
		}
	}
	return sets
}
