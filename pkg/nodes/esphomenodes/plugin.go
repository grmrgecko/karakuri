// Package esphomenodes contributes the ESPHome connection kind and the nodes
// that call a node's actions and react to the actions it calls.
package esphomenodes

import (
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/esphome"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// esphomePlugin contributes the ESPHome kind and nodes.
type esphomePlugin struct{}

// Plugin returns the ESPHome integration as a built-in plugin.
func Plugin() plugin.Plugin { return esphomePlugin{} }

func (esphomePlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "esphome",
		Description: "ESPHome nodes over the native API: the connection kind, node actions, and node action triggers.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (esphomePlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})
	r.RegisterConnectionKind(esphome.KindDef())
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "esphome-host", Base: flow.FieldString,
		Description: "A node address with a Find nodes button listing ESPHome nodes announcing themselves on the network.",
	})
	return nil
}

// RegisterAll registers the ESPHome nodes.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(ActionNode{})
	reg.Register(NodeActionTrigger{})
}
