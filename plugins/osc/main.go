// Command osc is a karakuri plugin bringing Open Sound Control to flows: an
// "osc" connection kind describing a UDP peer, a trigger starting a run per
// received message, and an action sending one. OSC is plain UDP with no
// session, so the driver is a bound listen socket plus a send target shared
// by every node on the connection, the MQTT shape without the broker.
package main

import (
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

func main() { sdk.Serve(oscPlugin{}) }

// oscPlugin registers the OSC connection kind and node types.
type oscPlugin struct{}

// Manifest identifies the plugin.
func (oscPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "osc",
		Version:     "0.1.0",
		Description: "Open Sound Control: an OSC connection kind with a receive trigger and a send node.",
		Author:      "James Coleman",
	}
}

// Register contributes the connection kind and the flow nodes.
func (oscPlugin) Register(r plugin.Registrar) error {
	r.RegisterConnectionKind(KindDef())
	r.RegisterNode(InNode{})
	r.RegisterNode(OutNode{})
	return nil
}
