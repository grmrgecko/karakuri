// Package nodeapi is the contract a node type is written against: its
// metadata, the ExecContext it reaches the world through, the messages it
// carries, and the registry it lands in. It depends on nothing but pkg/flow,
// which is what lets a remote agent or an out-of-process plugin implement
// nodes without linking the engine, the data model, or a database driver.
// pkg/engine aliases every name here, so node code keeps saying engine.Msg.
package nodeapi

import "github.com/grmrgecko/karakuri/pkg/flow"

// Node metadata types are defined in pkg/flow so graph validation can consume
// them without importing the engine. Aliases keep the natural engine-facing
// names for node implementations and later engine code.
type (
	// PortSpec describes one input or output port of a node type.
	PortSpec = flow.PortSpec
	// FieldSpec describes one config field of a node type.
	FieldSpec = flow.FieldSpec
	// Option is one selectable value for a select-type config field.
	Option = flow.Option
	// ShowIf conditionally shows a config field based on another field's value.
	ShowIf = flow.ShowIf
	// NodeMeta is the full metadata describing a node type.
	NodeMeta = flow.NodeMeta
)
