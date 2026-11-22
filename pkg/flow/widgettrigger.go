package flow

import "encoding/json"

// NodeTypeWidgetTrigger is the dashboard's entry point into a flow: a trigger
// that fires when a dashboard widget presses it by name. The name — not the
// node ID — is what a widget stores, so it is the flow's public contract with
// the dashboard and has to be unique within the graph.
const NodeTypeWidgetTrigger = "widget-trigger"

// widgetTriggerConfig is the part of the node's config the graph reasons about.
type widgetTriggerConfig struct {
	Name        string `json:"name"`
	AllowPublic bool   `json:"allow_public"`
}

// WidgetTrigger is one named widget trigger a graph declares.
type WidgetTrigger struct {
	NodeID      string `json:"node_id" description:"Graph node the press starts the run from."`
	Name        string `json:"name" description:"Name a widget presses the trigger by."`
	AllowPublic bool   `json:"allow_public" description:"Whether anonymous viewers of a public dashboard may press it."`
}

// WidgetTriggerName returns the name a widget trigger answers to, or "" when
// unset.
func WidgetTriggerName(n *Node) string {
	return decodeWidgetTrigger(n).Name
}

// WidgetTriggers returns the named widget triggers a graph declares, in graph
// order. Unnamed nodes are skipped: they cannot be pressed, and the
// required-field check already reports them. The dashboard's trigger picker
// and the press endpoint both read the deployed graph through this, so what a
// dashboard can fire is exactly what the deployed flow declares.
func WidgetTriggers(g *Graph) []WidgetTrigger {
	var out []WidgetTrigger
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Type != NodeTypeWidgetTrigger {
			continue
		}
		cfg := decodeWidgetTrigger(n)
		if cfg.Name == "" {
			continue
		}
		out = append(out, WidgetTrigger{
			NodeID:      n.ID,
			Name:        cfg.Name,
			AllowPublic: cfg.AllowPublic,
		})
	}
	return out
}

// decodeWidgetTrigger reads a widget trigger node's config, treating an
// unreadable config as an unconfigured node rather than failing: config schema
// validation is what reports a malformed config.
func decodeWidgetTrigger(n *Node) widgetTriggerConfig {
	var cfg widgetTriggerConfig
	if len(n.Config) > 0 {
		_ = json.Unmarshal(n.Config, &cfg)
	}
	return cfg
}
