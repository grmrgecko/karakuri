package flow

import "encoding/json"

// Dependency kind constants for external references extracted from a graph.
const (
	DepConnection = "connection"
	DepSubflow    = "subflow"
)

// Dep is one external reference a graph depends on: a connection used by a
// node's config or a shared subflow referenced by a subflow node.
type Dep struct {
	Kind  string `json:"kind" description:"Dependency kind: connection or subflow."`
	RefID string `json:"ref_id" description:"Referenced resource identifier."`
}

// Dependencies extracts deduplicated connection and subflow references from a
// graph, including nodes inside local subgraphs. Connection references are
// found via config fields of type "connection" in the node's metadata.
func Dependencies(g *Graph, reg MetaRegistry) []Dep {
	seen := make(map[Dep]bool)
	var deps []Dep

	add := func(kind, refID string) {
		if refID == "" {
			return
		}
		d := Dep{Kind: kind, RefID: refID}
		if !seen[d] {
			seen[d] = true
			deps = append(deps, d)
		}
	}

	var walk func(g *Graph)
	walk = func(g *Graph) {
		for i := range g.Nodes {
			n := &g.Nodes[i]

			// Shared subflow references.
			if n.SubflowID != nil {
				add(DepSubflow, n.SubflowID.String())
			}

			// Connection references from config fields of type "connection".
			meta, ok := reg.MetaFor(n.Type)
			if !ok || len(n.Config) == 0 {
				continue
			}
			var values map[string]any
			if err := json.Unmarshal(n.Config, &values); err != nil {
				continue
			}
			for _, field := range meta.ConfigSpec {
				if field.Type != FieldConnection {
					continue
				}
				if s, ok := values[field.ID].(string); ok {
					add(DepConnection, s)
				}
			}
		}
		for _, sub := range g.Subgraphs {
			walk(&sub)
		}
	}
	walk(g)

	return deps
}
