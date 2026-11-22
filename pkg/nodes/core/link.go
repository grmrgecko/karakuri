package core

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// The routing nodes are wiring, not processing steps: a reroute is a movable
// elbow in an edge, and a set/get pair carries a connection across the canvas
// under a name instead of a drawn wire. None of them execute — FlattenGraph
// splices each one into the direct
// edges it stands for before a run is built — so the Execute methods here are
// defensive passthroughs for the case of a graph that somehow escaped
// flattening. They are Pure for the same reason: a value edge may run through
// them, and the purity that matters is the spliced source's, which validation
// checks by chasing through the chain.

// RerouteNode is a passthrough elbow used to shape how edges run across the
// canvas. The editor draws it as a bare dot rather than a card.
type RerouteNode struct{}

func (RerouteNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeReroute,
		Label:       "Reroute",
		Folder:      flow.FolderValues,
		Description: "Passes its input through unchanged; a movable elbow for organizing edges",
		Icon:        "las la-circle",
		Pure:        true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
		},
	}
}

func (RerouteNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", inbound(ec, inputs)), nil
}

// LinkSetNode publishes its input under a name that link-get nodes in the same
// graph level emit. It is a dead end on the canvas: the
// only way onward is a Get subscribing to the same name.
type LinkSetNode struct{}

func (LinkSetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeLinkSet,
		Label:       "Set",
		Folder:      flow.FolderValues,
		Description: "Publishes its input under a name for Get nodes, in place of a drawn wire",
		Icon:        "las la-sign-in-alt",
		Pure:        true,
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "name",
				Label:       "Name",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "value",
				Help:        "Every Get node with this name receives whatever arrives here. Names are scoped to this graph level.",
			},
		},
	}
}

func (LinkSetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	// A set has no output port; in an unflattened graph the message ends here.
	return nil, nil
}

// LinkGetNode emits whatever the link-set publishing the same name receives.
// It has no input port of its own: its "in" side is the named link, which type
// propagation resolves as a virtual source (see flow.PropagateTypes).
type LinkGetNode struct{}

func (LinkGetNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeLinkGet,
		Label:       "Get",
		Folder:      flow.FolderValues,
		Description: "Emits whatever the Set node with the same name receives",
		Icon:        "las la-sign-out-alt",
		Pure:        true,
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
		},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "name",
				Label:       "Name",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "value",
				Help:        "Name of the Set node to receive from, in this graph level.",
			},
		},
	}
}

func (LinkGetNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	return engine.One("out", inbound(ec, inputs)), nil
}
