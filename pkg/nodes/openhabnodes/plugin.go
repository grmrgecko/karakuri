// Package openhabnodes contributes the openHAB connection kind and the nodes
// that trigger on item changes, send commands, and read items.
package openhabnodes

import (
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/openhab"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// openhabPlugin contributes the openHAB kind and nodes.
type openhabPlugin struct{}

// Plugin returns the openHAB integration as a built-in plugin.
func Plugin() plugin.Plugin { return openhabPlugin{} }

func (openhabPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "openhab",
		Description: "openHAB over its REST API: the connection kind, item triggers, commands, and item reads.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (openhabPlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})
	r.RegisterConnectionKind(openhab.KindDef())
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "openhab-item", Base: flow.FieldString,
		Description: "Picks openHAB items by name, completing against the form's connection and grouped by location; " +
			"several are comma-separated.",
	})
	return nil
}

// RegisterAll registers the openHAB nodes.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(ItemChangedNode{})
	reg.Register(SendCommandNode{})
	reg.Register(GetItemNode{})
}
