package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"go.uber.org/zap"
)

// execCtx is the engine's ExecContext implementation, scoped to one node
// instance within one run.
type execCtx struct {
	run  *Run
	node *runNode
	inst *nodeInstance
	slot *workSlot
	// params holds parameter values pulled for this execution, overlaid onto
	// the node's stored config when it decodes.
	params map[string]any
	// valueEval marks an on-demand pure evaluation. Synthetic calls inherit it
	// so a pure parent cannot reach an action, park, or invoke a tool through a
	// child execution context.
	valueEval bool
	// directEmit records mid-execution messages on the synthetic instance
	// instead of handing them to the graph scheduler.
	directEmit bool
	// scriptParentID retains the originating graph node through transitive
	// synthetic calls so durable identity never depends on a history UUID.
	scriptParentID string

	callMu      sync.Mutex
	callCount   int
	callStarted bool
}

// Config decodes the node's config JSON into the given struct, with any
// parameters pulled over value edges overlaid on top. A node therefore reads a
// wired field exactly as it reads one that was typed in.
func (c *execCtx) Config(out any) error {
	cfg, err := mergeParams(c.node.cfg, c.params)
	if err != nil {
		return err
	}
	if err := c.run.checkConfigConnections(c.node.meta, cfg); err != nil {
		return err
	}
	if len(cfg) == 0 {
		return nil
	}
	if err := json.Unmarshal(cfg, out); err != nil {
		return fmt.Errorf("invalid node config: %w", err)
	}
	return nil
}

// Logger returns a logger annotated with run and node identity.
func (c *execCtx) Logger() *zap.Logger {
	return c.run.eng.log.With(
		zap.String("run_id", c.run.id.String()),
		zap.String("flow_id", c.run.flowID.String()),
		zap.String("node_id", c.node.id),
		zap.String("node_type", c.node.meta.Type),
	)
}

// Clock returns the engine's injectable clock.
func (c *execCtx) Clock() Clock { return c.run.eng.clock }

// evalEnv builds the expression environment for a message within this run:
// payload/topic/meta/msg plus `nodes`, the last outputs of completed nodes,
// and the three variable scopes. Every map is built per evaluation so it
// reflects run progress and writes other runs have made.
func (c *execCtx) evalEnv(msg *Msg) map[string]any {
	if msg == nil {
		msg = &Msg{}
	}
	return expr.BuildEnv(expr.Env{
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    msg.Meta,
		Nodes:   c.run.outputsEnv(),
		Vars:    c.run.varsEnv(),
		Flow:    c.run.eng.vars.snapshot(ScopeFlow, c.run.flowID),
		Global:  c.run.eng.vars.snapshot(ScopeGlobal, c.run.flowID),
	})
}

// Eval evaluates an expression against the given message and the run's
// cross-node outputs.
func (c *execCtx) Eval(src string, msg *Msg) (any, error) {
	return expr.Eval(src, c.evalEnv(msg))
}

// Interpolate replaces {{ expr }} segments using the run environment.
func (c *execCtx) Interpolate(s string, msg *Msg) (string, error) {
	return expr.Interpolate(s, c.evalEnv(msg))
}

// NodeID returns the graph node ID being executed.
func (c *execCtx) NodeID() string { return c.node.id }

// FlowID returns the executing flow's identity, including shared-subflow calls.
func (c *execCtx) FlowID() string { return c.run.flowID.String() }

// Ports returns the node's resolved signature, computed once when the run was
// built.
func (c *execCtx) Ports() flow.PortSet { return c.node.ports }

// Wired reports whether any edge leaves the named output port in this run's
// flattened graph.
func (c *execCtx) Wired(port string) bool { return c.node.wired[port] }

// Debug emits a debug event for the node: a run event row and a WebSocket
// message on the run topic.
func (c *execCtx) Debug(payload any, note string) {
	c.run.eng.emitDebug(c.run, c.node.id, payload, note)
}

// GetState reads a value from the run-scoped key/value store.
func (c *execCtx) GetState(key string) (any, bool) {
	c.run.mu.Lock()
	defer c.run.mu.Unlock()
	v, ok := c.run.state[key]
	return v, ok
}

// SetState writes a value to the run-scoped key/value store.
func (c *execCtx) SetState(key string, value any) {
	c.run.mu.Lock()
	defer c.run.mu.Unlock()
	c.run.state[key] = value
}

// GetVar reads a run variable.
func (c *execCtx) GetVar(key string) (any, bool) { return c.run.getVar(key) }

// SetVar writes a run variable.
func (c *execCtx) SetVar(key string, value any) { c.run.setVar(key, value) }

// Vars snapshots the run's variables.
func (c *execCtx) Vars() map[string]any { return c.run.varsEnv() }

// GetScoped reads a variable from the run, flow, or global scope.
func (c *execCtx) GetScoped(scope, key string) (any, bool) {
	switch scope {
	case ScopeRun:
		return c.run.getVar(key)
	case ScopeFlow, ScopeGlobal:
		return c.run.eng.vars.get(scope, c.run.flowID, key)
	default:
		return nil, false
	}
}

// SetScoped writes a variable in the run, flow, or global scope.
func (c *execCtx) SetScoped(scope, key string, value any) error {
	switch scope {
	case ScopeRun:
		c.run.setVar(key, value)
		return nil
	case ScopeFlow, ScopeGlobal:
		c.run.eng.vars.set(scope, c.run.flowID, key, value)
		return nil
	default:
		return fmt.Errorf("cannot write to scope %q", scope)
	}
}

// ScopedVars snapshots one scope's variables.
func (c *execCtx) ScopedVars(scope string) map[string]any {
	switch scope {
	case ScopeRun:
		return c.run.varsEnv()
	case ScopeFlow, ScopeGlobal:
		return c.run.eng.vars.snapshot(scope, c.run.flowID)
	default:
		return map[string]any{}
	}
}

// storageNodeID returns the existing graph identity or a synthetic call's
// separate stable store identity.
func (c *execCtx) storageNodeID() string {
	if c.node.storeID != "" {
		return c.node.storeID
	}
	return c.node.id
}

// NodeStore returns this node's flow-scoped store, shared by every run of the
// flow.
func (c *execCtx) NodeStore() NodeStore {
	return c.run.eng.nodeStore(c.run.flowID, c.storageNodeID())
}

// DurableStore returns this node's persistent store, which survives redeploys
// and restarts.
func (c *execCtx) DurableStore() NodeStore {
	return c.run.eng.durableNodeStore(c.run.flowID, c.storageNodeID())
}

// WaitStore returns this node's pending-wait store. Waits of deployed runs
// are stored and survive restarts and redeploys; tests, previews, value
// evaluations, and script-hosted calls keep theirs in memory.
func (c *execCtx) WaitStore() WaitStore {
	if !c.DurableWaits() {
		return nodeapi.MemWaitsIn(c.NodeStore())
	}
	return &runWaitStore{
		nw:    c.run.eng.waits.get(c.run.flowID, c.storageNodeID(), c.node.meta.Type),
		runID: c.run.id,
	}
}

// DurableWaits reports whether this execution's waits are stored.
func (c *execCtx) DurableWaits() bool {
	return c.run.durableWaits && !c.valueEval && !c.directEmit && c.scriptParentID == ""
}

// ExecutionID identifies this node execution: the run, the node, and the
// instance's message group.
func (c *execCtx) ExecutionID() string {
	key := ""
	if c.inst != nil {
		key = c.inst.key
	}
	return c.run.id.String() + "/" + c.node.id + "/" + key
}

// Park suspends the node for d, releasing its worker slot and pausing the run
// timeout while it waits.
func (c *execCtx) Park(ctx context.Context, d time.Duration) error {
	if c.valueEval {
		return fmt.Errorf("pure value evaluation cannot park")
	}
	return c.run.park(ctx, d, c.slot)
}

// ParkUntil suspends execution until the timer, signal or cancellation arrives.
func (c *execCtx) ParkUntil(ctx context.Context, d time.Duration, signal <-chan struct{}) error {
	if c.valueEval {
		return fmt.Errorf("pure value evaluation cannot park")
	}
	return c.run.parkUntil(ctx, d, signal, c.slot)
}

// Emit delivers a message downstream immediately, before Execute returns.
// Synthetic calls only record it for their own result and history.
func (c *execCtx) Emit(port string, msg *Msg) {
	if c.inst != nil && c.inst.resume != nil {
		c.run.stampResumedMsg(msg)
	}
	if c.directEmit {
		if msg != nil {
			c.inst.record(port, msg)
		}
		return
	}
	c.run.emit(c.inst, port, msg)
}

// CallTool runs one graph-backed tool call: the arguments are injected on the
// named tool node's call output, the wired branch runs, and the message that
// arrives back at the tool node's return input is the result. It blocks
// without holding a worker slot, the way Park does.
func (c *execCtx) CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*Msg, error) {
	if c.valueEval || c.inst == nil || c.slot == nil {
		return nil, fmt.Errorf("tool calls cannot run while a node is evaluated for a value")
	}
	return c.run.callTool(ctx, c.inst, c.slot, toolNodeID, args, timeout)
}

// Connection resolves a configured connection ID to a live driver handle via
// the engine's installed resolver.
func (c *execCtx) Connection(id string) (any, error) {
	if err := c.run.checkConnection(id); err != nil {
		return nil, err
	}
	return c.run.eng.resolveConnection(id)
}

// RefreshParameter reads a new pure observation without changing Config's snapshot.
func (c *execCtx) RefreshParameter(ctx context.Context, field string) (any, bool, error) {
	sources := c.node.params[field]
	if len(sources) == 0 {
		return nil, false, nil
	}
	var group []GroupFrame
	if c.inst != nil {
		group = callGroup(c.inst)
	}
	cache := newPullState()
	values := make([]any, 0, len(sources))
	for _, src := range sources {
		value, err := c.run.pullWithCache(ctx, src, []string{c.node.id}, group, cache, true)
		if err != nil {
			return nil, true, fmt.Errorf("refreshing parameter %q: %w", field, err)
		}
		values = append(values, value)
	}
	if port, ok := c.node.ports.Input(flow.ParamPortID(field)); ok && port.Multi {
		return values, true, nil
	}
	return values[0], true, nil
}

// RunID returns the run's identity.
func (c *execCtx) RunID() string { return c.run.id.String() }

// FlowName returns the executing flow's name.
func (c *execCtx) FlowName() string { return c.run.flowName }

// ControlChain returns how many control handoffs between flows led to this
// run and the names of each, read from the trigger message a Control Changed
// trigger started it with. Any other start is depth 0 with no chain.
func (c *execCtx) ControlChain() (int, []string) {
	if c.run.initial == nil || c.run.initial.Meta == nil {
		return 0, nil
	}
	meta := c.run.initial.Meta
	depth := 0
	switch d := meta["control_depth"].(type) {
	case int:
		depth = d
	case float64:
		depth = int(d)
	}
	var chain []string
	switch v := meta["control_chain"].(type) {
	case []string:
		chain = append(chain, v...)
	case []any:
		for _, s := range v {
			if str, ok := s.(string); ok {
				chain = append(chain, str)
			}
		}
	}
	return depth, chain
}
