// Command midi is a karakuri plugin bringing MIDI devices into flows: a
// "midi" connection kind naming a local input and output port pair, a trigger
// starting a run per received message, and an action sending one. Ports are
// the machine's own (ALSA through rtmidi), so unlike the network protocols
// this plugin talks to hardware plugged into the host.
package main

import (
	"github.com/grmrgecko/karakuri/pkg/plugin"
	"github.com/grmrgecko/karakuri/pkg/plugin/sdk"

	// rtmidi is the MIDI backend: cgo over ALSA on Linux. Registered once for
	// the whole process and shared by every connection.
	_ "gitlab.com/gomidi/midi/v2/drivers/rtmididrv"
)

func main() { sdk.Serve(midiPlugin{}) }

// midiPlugin registers the MIDI connection kind and node types.
type midiPlugin struct{}

// Manifest identifies the plugin.
func (midiPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "midi",
		Version:     "0.1.0",
		Description: "MIDI devices on this machine: a connection kind over local ports with a receive trigger and a send node.",
		Author:      "James Coleman",
	}
}

// Register contributes the connection kind and the flow nodes.
func (midiPlugin) Register(r plugin.Registrar) error {
	r.RegisterConnectionKind(KindDef())
	r.RegisterNode(InNode{})
	r.RegisterNode(OutNode{})
	return nil
}
