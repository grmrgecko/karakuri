// Package engine runs flows: the run state machine, retry, fan-out
// scheduling, variable scopes, and the history sink that persists runs. The
// contract node types are written against lives in pkg/nodeapi, which carries
// no database dependency; the aliases below re-export it so node code, the
// plugin surface, and the API all keep spelling it engine.Msg.
package engine

import (
	"context"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Node execution contract, re-exported from pkg/nodeapi.
type (
	// PortSpec describes one input or output port of a node type.
	PortSpec = nodeapi.PortSpec
	// FieldSpec describes one config field of a node type.
	FieldSpec = nodeapi.FieldSpec
	// Option is one selectable value for a select-type config field.
	Option = nodeapi.Option
	// ShowIf conditionally shows a config field based on another field's value.
	ShowIf = nodeapi.ShowIf
	// NodeMeta is the full metadata describing a node type.
	NodeMeta = nodeapi.NodeMeta
	// GroupFrame is one frame of a message's fan-out stack.
	GroupFrame = nodeapi.GroupFrame
	// Msg is one message flowing between nodes.
	Msg = nodeapi.Msg
	// NodeStore is a node's flow-scoped key/value store.
	NodeStore = nodeapi.NodeStore
	// ExecContext is a node's window into the engine during execution.
	ExecContext = nodeapi.ExecContext
	// FlowIdentity exposes the server-owned flow identity to optional capabilities.
	FlowIdentity = nodeapi.FlowIdentity
	// ParameterRefresher supports explicit fresh server-side observations.
	ParameterRefresher = nodeapi.ParameterRefresher
	// SignalParker supports worker-free waits interrupted by control messages.
	SignalParker = nodeapi.SignalParker
	// Wait is one pending wait held by a node.
	Wait = nodeapi.Wait
	// WaitStore is a node's pending waits.
	WaitStore = nodeapi.WaitStore
	// WaitParker is implemented by execution contexts that provide a wait store.
	WaitParker = nodeapi.WaitParker
	// Resumer is implemented by node types whose stored waits can resume.
	Resumer = nodeapi.Resumer
	// TriggerRecorder records events an armed trigger skipped.
	TriggerRecorder = nodeapi.TriggerRecorder
	// ExecutionError carries a node-specific retry decision.
	ExecutionError = nodeapi.ExecutionError
	// NodeType is a registered node implementation.
	NodeType = nodeapi.NodeType
	// TriggerType is a node type that starts runs on its own.
	TriggerType = nodeapi.TriggerType
	// NextFirer is a trigger type whose firing times are known in advance.
	NextFirer = nodeapi.NextFirer
	// Clock abstracts time for the engine and nodes.
	Clock = nodeapi.Clock
	// RealClock implements Clock using the system clock.
	RealClock = nodeapi.RealClock
	// FakeClock is a deterministic Clock for tests.
	FakeClock = nodeapi.FakeClock
	// NodeRegistrar is the registration surface a node package needs.
	NodeRegistrar = nodeapi.NodeRegistrar
	// Registry holds registered node types keyed by type name.
	Registry = nodeapi.Registry
)

// TriggerInput is the synthetic input port trigger nodes are seeded on.
const TriggerInput = nodeapi.TriggerInput

// ErrSuspended is the cancellation cause for shutdown and redeploy.
var ErrSuspended = nodeapi.ErrSuspended

// Suspended reports whether ctx was cancelled because its run is suspended.
func Suspended(ctx context.Context) bool { return nodeapi.Suspended(ctx) }

// WaitsFor returns the execution context's wait store and execution ID.
func WaitsFor(ec ExecContext) (WaitStore, string) { return nodeapi.WaitsFor(ec) }

// IsDurableRun reports whether ec stores its waits, so a node can say when
// one of them could not be stored.
func IsDurableRun(ec ExecContext) bool {
	d, ok := ec.(interface{ DurableWaits() bool })
	return ok && d.DurableWaits()
}

// NewWaitToken returns a random token for a wait owner.
func NewWaitToken() string { return nodeapi.NewWaitToken() }

// NewMemWaitStore returns an empty memory-only wait store.
func NewMemWaitStore() WaitStore { return nodeapi.NewMemWaitStore() }

// One returns the single-message output map for a port.
func One(port string, msg *Msg) map[string][]*Msg { return nodeapi.One(port, msg) }

// NewRegistry returns an empty node type registry.
func NewRegistry() *Registry { return nodeapi.NewRegistry() }

// Default returns the process-wide default registry.
func Default() *Registry { return nodeapi.Default() }

// Register adds a node type to the default registry.
func Register(nt NodeType) { nodeapi.Register(nt) }

// NewFakeClock returns a FakeClock starting at the given time.
func NewFakeClock(start time.Time) *FakeClock { return nodeapi.NewFakeClock(start) }
