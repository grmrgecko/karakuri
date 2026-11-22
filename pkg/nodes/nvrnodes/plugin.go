// Package nvrnodes contributes the Frigate connection kind and the nodes that
// react to a network video recorder's detections and read its images.
package nvrnodes

import (
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// nvrPlugin contributes the Frigate kind and the NVR nodes.
type nvrPlugin struct{}

// Plugin returns the NVR integration as a built-in plugin.
func Plugin() plugin.Plugin { return nvrPlugin{} }

func (nvrPlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "nvr",
		Description: "Frigate NVR: the connection kind, detection and review triggers, and camera images.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (nvrPlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})
	r.RegisterConnectionKind(frigate.KindDef())
	return nil
}

// RegisterAll registers the NVR nodes.
func RegisterAll(reg engine.NodeRegistrar) {
	reg.Register(EventTrigger{})
	reg.Register(ReviewTrigger{})
	reg.Register(ImageNode{})
}
