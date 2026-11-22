package mqttnodes

import (
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/mqtt"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// mqttPlugin contributes the MQTT nodes and the MQTT connection kind.
type mqttPlugin struct{}

// Plugin returns the MQTT integration as a built-in plugin.
func Plugin() plugin.Plugin { return mqttPlugin{} }

func (mqttPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "mqtt",
		Description: "MQTT triggers and actions, and the MQTT broker connection kind.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (mqttPlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})
	r.RegisterConnectionKind(mqtt.KindDef())
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "mqtt-device", Base: flow.FieldString,
		Description: "Picks an MQTT device by name, storing its ID.",
	})
	return nil
}
