package hanodes

import (
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/homeassistant"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// haPlugin contributes the Home Assistant nodes and connection kind.
type haPlugin struct{}

// Plugin returns the Home Assistant integration as a built-in plugin.
func Plugin() plugin.Plugin { return haPlugin{} }

func (haPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "homeassistant",
		Description: "Home Assistant triggers, actions, and the Home Assistant connection kind.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (haPlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})
	r.RegisterConnectionKind(homeassistant.KindDef())
	for _, c := range targetControls {
		r.RegisterFieldControl(c)
	}
	return nil
}

// targetControls pick Home Assistant registry entries for an ha-target field,
// completing against the form's connection.
var targetControls = []flow.FieldControlDef{
	{Name: "ha-area", Base: flow.FieldHATarget, Description: "Picks Home Assistant areas, storing their IDs comma-separated."},
	{Name: "ha-floor", Base: flow.FieldHATarget, Description: "Picks Home Assistant floors, storing their IDs comma-separated."},
	{Name: "ha-device", Base: flow.FieldHATarget, Description: "Picks Home Assistant devices, storing their IDs comma-separated."},
	{Name: "ha-label", Base: flow.FieldHATarget, Description: "Picks Home Assistant labels, storing their IDs comma-separated."},
}
