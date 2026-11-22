// Command dmx is a karakuri plugin bringing DMX lighting control to flows: a
// "dmx" connection kind speaking either sACN (E1.31) or Art-Net over UDP, a
// trigger starting a run per received frame, and an action setting channel
// levels. A connection accumulates a full 512-channel frame per universe and
// keeps transmitting it, the way DMX receivers expect, so a flow only ever
// states the channels it wants changed.
package main

import (
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"
)

func main() { sdk.Serve(dmxPlugin{}) }

// dmxPlugin registers the DMX connection kind and node types.
type dmxPlugin struct{}

// Manifest identifies the plugin.
func (dmxPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "dmx",
		Version:     "0.1.0",
		Description: "DMX lighting over sACN (E1.31) and Art-Net: a connection kind with receive trigger and channel-set nodes.",
		Author:      "James Coleman",
	}
}

// Register contributes the connection kind and the flow nodes.
func (dmxPlugin) Register(r plugin.Registrar) error {
	r.RegisterConnectionKind(KindDef())
	r.RegisterNode(InNode{})
	r.RegisterNode(OutNode{})
	return nil
}
