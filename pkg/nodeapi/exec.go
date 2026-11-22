package nodeapi

import (
	"context"
	"encoding/json"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// TriggerInput is the synthetic input port trigger nodes are seeded on. It is
// never a declared port, so it never gates readiness or accepts edges; the run
// loop places the run's initial message there.
const TriggerInput = "__trigger__"

// GroupFrame is one frame of a message's fan-out stack. Nodes that fan out
// (split, batch) push a frame naming the group, the message's position in it,
// and how many messages the group holds; nodes that fan in (join, batch,
// aggregate) pop it. The stack lets the engine schedule one node instance per
// message and lets fan-in nodes recognize which messages belong together.
type GroupFrame struct {
	// ID identifies the group; unique per fan-out.
	ID string `json:"id"`
	// Index is the message's zero-based position within the group.
	Index int `json:"index"`
	// Count is the group's total message count, or 0 when the producer emits
	// an open-ended stream and the group has no known end.
	Count int `json:"count"`
}

// Msg is one message flowing between nodes.
type Msg struct {
	ID      string         `json:"id"`
	Payload any            `json:"payload"`
	Topic   string         `json:"topic,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
	// Group is the fan-out stack, outermost frame first. Empty for messages
	// that are not part of a fan-out.
	Group []GroupFrame `json:"group,omitempty"`
	// Continuation marks a message a node produced for itself rather than one
	// received from upstream. Nodes that re-enter themselves use it to tell a
	// fresh input apart from their own deferred work.
	Continuation bool `json:"continuation,omitempty"`
}

// PushGroup returns a copy of the message's group stack with a frame appended.
// Callers building fan-out messages must not mutate a shared slice, so the
// result is always a fresh allocation.
func (m *Msg) PushGroup(id string, index, count int) []GroupFrame {
	out := make([]GroupFrame, 0, len(m.Group)+1)
	out = append(out, m.Group...)
	return append(out, GroupFrame{ID: id, Index: index, Count: count})
}

// PopGroup returns the message's innermost group frame and the stack with that
// frame removed. ok is false when the message is not part of a fan-out.
func (m *Msg) PopGroup() (frame GroupFrame, rest []GroupFrame, ok bool) {
	if len(m.Group) == 0 {
		return GroupFrame{}, nil, false
	}
	last := len(m.Group) - 1
	rest = make([]GroupFrame, last)
	copy(rest, m.Group[:last])
	return m.Group[last], rest, true
}

// NodeStore is a node's key/value store, scoped to one node of one flow and
// shared by every run of that flow. It is what nodes that reason across
// messages (filter, trigger, rate-limited delay, join, batch, aggregate) keep
// their state in. It comes in two lifetimes: the store from
// ExecContext.NodeStore lives in memory only and is dropped when the flow is
// redeployed, disabled, or the process restarts; the one from
// ExecContext.DurableStore is persisted and survives all three.
//
// Concurrent runs of the same flow reach the same store, so read-modify-write
// sequences must go through Update rather than Get followed by Set.
type NodeStore interface {
	// Get returns the value stored under key.
	Get(key string) (any, bool)
	// Set stores a value under key.
	Set(key string, value any)
	// Delete removes a key.
	Delete(key string)
	// Update applies fn to the current value under the store's lock and stores
	// the result, returning it. A nil result deletes the key. fn must not call
	// back into the store.
	Update(key string, fn func(cur any, ok bool) any) any
}

// ExecContext is a node's window into the engine during execution. Nodes
// touch the world only through this interface.
type ExecContext interface {
	// Config decodes the node's config JSON into the given struct.
	Config(out any) error
	// Logger returns a logger scoped to the current node execution.
	Logger() *zap.Logger
	// Clock returns the injectable clock for all time operations.
	Clock() Clock
	// Eval evaluates an expression against the given message. Within a run
	// the environment also exposes `nodes`, the last emitted output of every
	// node completed so far.
	Eval(expr string, msg *Msg) (any, error)
	// Interpolate replaces {{ expr }} segments in s using the same
	// environment as Eval.
	Interpolate(s string, msg *Msg) (string, error)
	// NodeID returns the graph node ID being executed.
	NodeID() string
	// Ports returns the node's resolved signature: the ports the graph sees,
	// which for the types that declare their own (script, switch, manual) is
	// not the static type metadata. A node reads its main input through
	// PrimaryInput rather than taking whatever an input map happens to yield.
	Ports() flow.PortSet
	// Wired reports whether at least one edge — message or value — leaves the
	// named output port in the running graph. A node may use it to skip
	// building an output nothing consumes; it must never change what a wired
	// port carries. An armed trigger answers from the deployed graph it was
	// armed against.
	Wired(port string) bool
	// Debug emits a debug event (run event + WebSocket message) for the node.
	Debug(payload any, note string)
	// GetState reads a value from the run-scoped key/value store.
	GetState(key string) (any, bool)
	// SetState writes a value to the run-scoped key/value store.
	SetState(key string, value any)
	// GetVar reads a run variable. Variables differ from run state in being
	// author-facing: they are exposed to expressions as `vars` and to scripts
	// as their `vars` object, and the reserved response_* names shape the
	// reply to a synchronous webhook.
	GetVar(key string) (any, bool)
	// SetVar writes a run variable. A nil value clears it.
	SetVar(key string, value any)
	// Vars snapshots every run variable. The result is a copy, so mutating it
	// does not write back; use SetVar for that.
	Vars() map[string]any
	// GetScoped reads a variable from the run, flow, or global scope. The
	// message scope is not reachable here: the node holds its own message and
	// reads properties from it directly.
	GetScoped(scope, key string) (any, bool)
	// SetScoped writes a variable in the run, flow, or global scope. A nil
	// value clears it. Flow and global writes are visible to other runs
	// immediately and are persisted asynchronously.
	SetScoped(scope, key string, value any) error
	// ScopedVars snapshots one scope's variables as a copy.
	ScopedVars(scope string) map[string]any
	// NodeStore returns this node's flow-scoped store, which outlives the run.
	NodeStore() NodeStore
	// DurableStore returns this node's persistent store: the same contract as
	// NodeStore, but entries survive a redeploy and a restart and are removed
	// only when the flow is deleted. Values must be JSON-encodable; every
	// write is normalized through a JSON round trip, so reads always return
	// JSON shapes (map[string]any, []any, float64, string, bool) — encode a
	// time.Time as a string before storing it. State that references a live
	// run (a pending wait, a message being held) belongs in NodeStore instead:
	// persisting it would outlive the run it points at.
	DurableStore() NodeStore
	// Park suspends the node for d, returning early with the context's error
	// if it is canceled first. Parked nodes hold no worker slot and their
	// parked time does not count toward the run timeout, so a node may park
	// far longer than a run would otherwise be allowed to live.
	Park(ctx context.Context, d time.Duration) error
	// Emit delivers a message downstream immediately instead of waiting for
	// Execute to return. Unlike returned outputs it is not rolled back when a
	// later attempt of the same node fails, so only nodes that must emit
	// before they finish (trigger) should use it.
	Emit(port string, msg *Msg)
	// CallTool runs one graph-backed tool call: args are injected on the named
	// tool node's call output, the wired branch runs, and the message arriving
	// back at its return input is the result. It blocks without holding a
	// worker slot, and its wait does not count toward the run timeout. Only
	// message-path executions may call it; a node being evaluated for a value
	// cannot.
	CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*Msg, error)
	// Connection resolves a configured connection by ID.
	Connection(id string) (any, error)
}

// SignalParker suspends execution until a timer, signal or cancellation arrives.
// It releases the worker slot while waiting and reacquires it before returning
// to a live execution. A nil signal waits only for the timer or cancellation.
type SignalParker interface {
	ParkUntil(ctx context.Context, d time.Duration, signal <-chan struct{}) error
}

// ParameterRefresher obtains a new observation without replacing run snapshots.
// This capability is provided by the server execution context for polling nodes.
type ParameterRefresher interface {
	// RefreshParameter evaluates one bound pure dependency chain with a new cache.
	// An unbound field returns bound=false; normal Config values remain unchanged.
	RefreshParameter(ctx context.Context, field string) (value any, bound bool, err error)
}

// FlowIdentity supplies the server-owned identity for flow-scoped resources.
// It is optional because remote node contexts do not own server storage.
type FlowIdentity interface {
	FlowID() string
}

// NodeType is a registered node implementation: metadata plus execution.
type NodeType interface {
	// Meta returns the node type's metadata.
	Meta() NodeMeta
	// Execute runs the node with the given input messages and returns output
	// messages keyed by output port ID. A nil, empty, or absent entry for an
	// output port means that branch is not taken. Returning more than one
	// message on a port fans out: the downstream subgraph runs once per
	// message, so fan-out nodes must stamp a group frame on each message
	// (see Msg.PushGroup) for a fan-in node to recombine them later.
	Execute(ctx context.Context, ec ExecContext, inputs map[string]*Msg) (map[string][]*Msg, error)
}

// TriggerType is a node type that can start runs on its own while a flow is
// deployed and enabled. Start blocks until ctx is canceled, calling emit for
// every message that should begin a new run. Manual triggers implement it as
// a no-op since they only fire through the test/run API.
type TriggerType interface {
	NodeType
	Start(ctx context.Context, ec ExecContext, emit func(*Msg)) error
}

// NextFirer is implemented by trigger types that fire at times known in
// advance, so the overview can say what runs next without arming anything.
// NextFire returns the first firing strictly after after for the given
// armed config, or false when there is none, such as a sun event that does
// not occur at the configured latitude.
type NextFirer interface {
	NextFire(config json.RawMessage, after time.Time) (time.Time, bool, error)
}

// One returns the single-message output map for a port, the shape almost every
// node emits. A nil message yields an untaken branch.
func One(port string, msg *Msg) map[string][]*Msg {
	if msg == nil {
		return nil
	}
	return map[string][]*Msg{port: {msg}}
}
