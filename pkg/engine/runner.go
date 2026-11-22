package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/expr"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// Runner arms the trigger nodes of deployed, enabled flows so they can start
// runs on their own. Each armed flow holds one goroutine per trigger node
// running TriggerType.Start against the flow's deployed graph. Flows are
// rearmed on serve start, enable, and deploy, and disarmed on disable.
type Runner struct {
	eng *Engine
	log *zap.Logger

	mu    sync.Mutex
	armed map[uuid.UUID]context.CancelFunc
	// resumes holds each flow's running resume queue. A queue captures the
	// graph it was armed with, so disarm stops it before the flow is armed
	// again.
	resumes map[uuid.UUID]*resumeQueueHandle
	// armMu admits one arm or disarm at a time. Two rearms of one flow, a
	// deploy racing a connection update, would both pass the disarm and both
	// start trigger goroutines, and the first cancel would be overwritten and
	// its triggers left running.
	armMu   sync.Mutex
	baseCtx context.Context
	cancel  context.CancelFunc
	stopped bool
}

// NewRunner creates a trigger runner bound to an engine.
func NewRunner(eng *Engine, log *zap.Logger) *Runner {
	if log == nil {
		log = zap.NewNop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{
		eng:     eng,
		log:     log,
		armed:   make(map[uuid.UUID]context.CancelFunc),
		resumes: make(map[uuid.UUID]*resumeQueueHandle),
		baseCtx: ctx,
		cancel:  cancel,
	}
}

// Start arms every enabled flow that has a deployed version.
func (r *Runner) Start() error {
	flows, err := models.ListDeployedEnabledFlows()
	if err != nil {
		return err
	}
	for _, f := range flows {
		r.Rearm(f.ID)
	}
	r.log.Info("trigger runner started", zap.Int("armed_flows", len(flows)))
	return nil
}

// Stop disarms every flow.
func (r *Runner) Stop() {
	r.mu.Lock()
	r.stopped = true
	r.armed = make(map[uuid.UUID]context.CancelFunc)
	r.mu.Unlock()
	r.cancel()
}

// Disarm cancels a flow's trigger goroutines and drops the flow-scoped state
// its nodes accumulated. Safe when the flow is not armed.
func (r *Runner) Disarm(flowID uuid.UUID) {
	r.armMu.Lock()
	defer r.armMu.Unlock()
	r.disarm(flowID)
}

// disarm is Disarm for a caller already holding armMu.
func (r *Runner) disarm(flowID uuid.UUID) {
	r.mu.Lock()
	cancel := r.armed[flowID]
	delete(r.armed, flowID)
	resume := r.resumes[flowID]
	delete(r.resumes, flowID)
	r.mu.Unlock()
	// Wait for the queue to release the waits it has not started, so the
	// next restore can queue them against the new deployment.
	if resume != nil {
		resume.cancel()
		<-resume.done
	}
	r.eng.PurgeNodeState(flowID)
	if cancel != nil {
		cancel()
		r.log.Debug("flow disarmed", zap.String("flow_id", flowID.String()))
	}
}

// Rearm disarms a flow and arms it again from its current deployed version.
// Flows that are disabled or have no deployed version end up disarmed.
func (r *Runner) Rearm(flowID uuid.UUID) {
	r.armMu.Lock()
	defer r.armMu.Unlock()
	r.disarm(flowID)

	log := r.log.With(zap.String("flow_id", flowID.String()))
	f, err := models.FindFlowByID(flowID)
	if err != nil {
		log.Warn("cannot arm flow: not found", zap.Error(err))
		return
	}
	// Subflows have no triggers of their own; they only run inside a parent.
	if f.IsSubflow {
		return
	}
	if !f.Enabled || f.DeployedVersionID == nil {
		return
	}
	version, err := models.FindFlowVersionByID(*f.DeployedVersionID)
	if err != nil {
		log.Error("cannot arm flow: deployed version missing", zap.Error(err))
		return
	}
	var g flow.Graph
	if err := json.Unmarshal([]byte(version.Graph), &g); err != nil {
		log.Error("cannot arm flow: deployed graph is invalid", zap.Error(err))
		return
	}

	// Restore pending waits before any trigger can fire, so a reset that
	// arrives right after a start or a redeploy finds the wait it resets.
	r.restoreWaits(f, version, &g)

	// Collect trigger nodes that can start on their own.
	type armTarget struct {
		node    *flow.Node
		trigger TriggerType
	}
	var targets []armTarget
	for i := range g.Nodes {
		n := &g.Nodes[i]
		nt, ok := r.eng.reg.Get(n.Type)
		if !ok {
			continue
		}
		if tt, ok := nt.(TriggerType); ok {
			targets = append(targets, armTarget{node: n, trigger: tt})
		}
	}
	if len(targets) == 0 {
		return
	}

	// Register the armed flow under the lock.
	ctx, cancel := context.WithCancel(r.baseCtx)
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		cancel()
		return
	}
	r.armed[flowID] = cancel
	r.mu.Unlock()

	// Launch one goroutine per trigger node. Each arms with the access of
	// the user the deployed version acts as.
	runAs := RunAsFor(f, version)
	userCtx := access.WithRunUser(ctx, runAs)
	for _, t := range targets {
		cfg, err := r.eng.armedConfig(f, &g, t.node, runAs)
		if err != nil {
			log.Error("cannot arm trigger: its wired config could not be read",
				zap.String("node_id", t.node.ID), zap.Error(err))
			continue
		}
		tc := &triggerCtx{
			eng:       r.eng,
			flowID:    f.ID,
			versionID: version.ID,
			nodeID:    t.node.ID,
			meta:      t.trigger.Meta(),
			ports:     flow.NodePorts(t.trigger.Meta(), t.node, nil),
			wired:     wiredOutputs(&g, t.node.ID),
			cfg:       cfg,
			runAs:     runAs,
			state:     make(map[string]any),
		}
		emit := r.emitFunc(f, version, &g, t.node.ID)
		trigger := t.trigger
		nodeID := t.node.ID
		go func() {
			// Node execution recovers panics; a trigger runs on its own
			// goroutine, where an unrecovered panic ends the whole server.
			defer func() {
				if p := recover(); p != nil {
					log.Error("trigger panicked and is no longer armed",
						zap.String("node_id", nodeID), zap.Any("panic", p), zap.Stack("stack"))
				}
			}()
			if err := trigger.Start(userCtx, tc, emit); err != nil && !errors.Is(err, context.Canceled) {
				log.Error("trigger stopped with error",
					zap.String("node_id", nodeID), zap.Error(err))
			}
		}()
	}
	log.Info("flow armed", zap.Int("triggers", len(targets)))
}

// armedConfig returns a trigger node's config as a run would read it: the
// stored config with every field wired to a value source pulled and overlaid.
// Deploy treats the literal behind a wired field as dead, so arming on it
// would watch for a setting the author cannot see. The sources are evaluated
// once, when the trigger is armed; a redeploy rearms it and reads them again.
func (e *Engine) armedConfig(f *models.Flow, g *flow.Graph, n *flow.Node, runAs uuid.UUID) (json.RawMessage, error) {
	wired := false
	for i := range g.Edges {
		if g.Edges[i].IsValue() && g.Edges[i].Target == n.ID {
			wired = true
			break
		}
	}
	if !wired {
		return n.Config, nil
	}
	exec, err := e.executionGraph(g, n.ID)
	if err != nil {
		return nil, err
	}
	// The run is never started. It only gives the pulled nodes the graph and
	// the flow identity they evaluate against.
	run, err := newRun(e, RunOptions{FlowID: f.ID, FlowName: f.Name, FlowSlug: f.Slug, TriggerNodeID: n.ID, Graph: exec, RunAs: runAs})
	if err != nil {
		return nil, err
	}
	defer run.cancel()
	node := run.nodes[n.ID]
	params, err := run.paramValuesWithCache(run.ctx, node, []string{node.id}, nil, newPullState(), true)
	if err != nil {
		return nil, err
	}
	return mergeParams(node.cfg, params)
}

// emitFunc builds the emit callback for one armed trigger node: every emitted
// message starts a run of the deployed graph seeded through that node.
func (r *Runner) emitFunc(f *models.Flow, version *models.FlowVersion, g *flow.Graph, nodeID string) func(*Msg) {
	version.ApplySettings(f)
	flowID := f.ID
	flowName := f.Name
	flowSlug := f.Slug
	versionID := version.ID
	errorFlowID := f.ErrorFlowID
	runAs := RunAsFor(f, version)
	return func(msg *Msg) {
		if msg == nil {
			msg = &Msg{}
		}
		if _, err := r.eng.RunFlow(RunOptions{
			FlowID:        flowID,
			FlowName:      flowName,
			FlowSlug:      flowSlug,
			FlowVersionID: versionID,
			TriggerNodeID: nodeID,
			Graph:         g,
			InitialMsg:    msg,
			ErrorFlowID:   errorFlowID,
			DurableWaits:  true,
			RunAs:         runAs,
		}); err != nil {
			r.log.Warn("trigger emit dropped",
				zap.String("flow_id", flowID.String()),
				zap.String("node_id", nodeID),
				zap.Error(err))
		}
	}
}

// triggerCtx is the flow-scoped ExecContext handed to TriggerType.Start. No
// run exists yet, so Debug is a log-only no-op and the state store is scoped
// to the armed trigger instance.
type triggerCtx struct {
	eng       *Engine
	flowID    uuid.UUID
	versionID uint
	nodeID    string
	meta      NodeMeta
	// ports is the trigger's resolved signature, so an armed trigger reports
	// the same output ports the graph was validated against.
	ports flow.PortSet
	// wired marks the trigger's output ports the deployed graph takes an edge
	// from, answering ExecContext.Wired while armed.
	wired map[string]bool
	cfg   json.RawMessage
	// runAs is the user the deployed version acts as, whose connection and
	// agent access the trigger arms with.
	runAs uuid.UUID

	mu    sync.Mutex
	state map[string]any
}

// Config decodes the trigger node's config JSON into the given struct.
func (c *triggerCtx) Config(out any) error {
	if len(c.cfg) == 0 {
		return nil
	}
	if err := checkTriggerConnections(c.runAs, c.meta, c.cfg); err != nil {
		return err
	}
	if err := json.Unmarshal(c.cfg, out); err != nil {
		return fmt.Errorf("invalid node config: %w", err)
	}
	return nil
}

// Logger returns a logger annotated with flow and node identity.
func (c *triggerCtx) Logger() *zap.Logger {
	return c.eng.log.With(
		zap.String("flow_id", c.flowID.String()),
		zap.String("node_id", c.nodeID),
		zap.String("node_type", c.meta.Type),
	)
}

// Clock returns the engine's injectable clock.
func (c *triggerCtx) Clock() Clock { return c.eng.clock }

// evalEnv builds the expression environment for an armed trigger. No run
// exists yet, so there is no `nodes` map and no run variables, but the flow's
// own variables and the global ones are already there to be read.
func (c *triggerCtx) evalEnv(msg *Msg) map[string]any {
	if msg == nil {
		msg = &Msg{}
	}
	return expr.BuildEnv(expr.Env{
		Payload: msg.Payload,
		Topic:   msg.Topic,
		Meta:    msg.Meta,
		Flow:    c.eng.vars.snapshot(ScopeFlow, c.flowID),
		Global:  c.eng.vars.snapshot(ScopeGlobal, c.flowID),
	})
}

// Eval evaluates an expression against the given message.
func (c *triggerCtx) Eval(src string, msg *Msg) (any, error) {
	return expr.Eval(src, c.evalEnv(msg))
}

// Interpolate replaces {{ expr }} segments using the trigger environment.
func (c *triggerCtx) Interpolate(s string, msg *Msg) (string, error) {
	return expr.Interpolate(s, c.evalEnv(msg))
}

// NodeID returns the graph node ID of the armed trigger.
func (c *triggerCtx) NodeID() string { return c.nodeID }

// Ports returns the trigger's resolved signature.
func (c *triggerCtx) Ports() flow.PortSet { return c.ports }

// Wired reports whether the deployed graph takes an edge from the named
// output port.
func (c *triggerCtx) Wired(port string) bool { return c.wired[port] }

// wiredOutputs collects the output ports edges of either kind leave on one
// node of a graph.
func wiredOutputs(g *flow.Graph, nodeID string) map[string]bool {
	wired := make(map[string]bool)
	for i := range g.Edges {
		if g.Edges[i].Source == nodeID {
			wired[g.Edges[i].SourceHandle] = true
		}
	}
	return wired
}

// Debug logs only: there is no run to attach events to while armed.
func (c *triggerCtx) Debug(payload any, note string) {
	c.Logger().Debug("trigger debug", zap.Any("payload", payload), zap.String("note", note))
}

// GetState reads from the trigger-instance state store.
func (c *triggerCtx) GetState(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.state[key]
	return v, ok
}

// SetState writes to the trigger-instance state store.
func (c *triggerCtx) SetState(key string, value any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.state[key] = value
}

// GetVar reports no variable: an armed trigger belongs to no run, and run
// variables only exist once a run has started.
func (c *triggerCtx) GetVar(key string) (any, bool) { return nil, false }

// SetVar is a no-op outside a run.
func (c *triggerCtx) SetVar(key string, value any) {
	c.Logger().Debug("trigger variable write ignored outside a run", zap.String("key", key))
}

// Vars returns an empty snapshot outside a run.
func (c *triggerCtx) Vars() map[string]any { return map[string]any{} }

// GetScoped reads a flow or global variable. The run scope does not exist for
// an armed trigger, so it always reads empty.
func (c *triggerCtx) GetScoped(scope, key string) (any, bool) {
	switch scope {
	case ScopeFlow, ScopeGlobal:
		return c.eng.vars.get(scope, c.flowID, key)
	default:
		return nil, false
	}
}

// SetScoped writes a flow or global variable. Run writes are ignored: an armed
// trigger has no run to write them to.
func (c *triggerCtx) SetScoped(scope, key string, value any) error {
	switch scope {
	case ScopeFlow, ScopeGlobal:
		c.eng.vars.set(scope, c.flowID, key, value)
		return nil
	case ScopeRun:
		c.Logger().Debug("trigger variable write ignored outside a run", zap.String("key", key))
		return nil
	default:
		return fmt.Errorf("cannot write to scope %q", scope)
	}
}

// ScopedVars snapshots a flow or global scope.
func (c *triggerCtx) ScopedVars(scope string) map[string]any {
	switch scope {
	case ScopeFlow, ScopeGlobal:
		return c.eng.vars.snapshot(scope, c.flowID)
	default:
		return map[string]any{}
	}
}

// NodeStore returns the armed trigger's flow-scoped store, the same one its
// Execute sees inside a run.
func (c *triggerCtx) NodeStore() NodeStore {
	return c.eng.nodeStore(c.flowID, c.nodeID)
}

// DurableStore returns the armed trigger's persistent store, the same one its
// Execute sees inside a run.
func (c *triggerCtx) DurableStore() NodeStore {
	return c.eng.durableNodeStore(c.flowID, c.nodeID)
}

// Park waits on the engine clock. An armed trigger holds no worker slot and
// belongs to no run, so parking is a plain interruptible sleep.
func (c *triggerCtx) Park(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	select {
	case <-c.eng.clock.After(d):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Emit is a no-op for an armed trigger: there is no run to deliver into.
// Triggers start runs through the emit callback passed to Start instead.
func (c *triggerCtx) Emit(port string, msg *Msg) {
	c.Logger().Debug("trigger emit ignored outside a run", zap.String("port", port))
}

// CallTool is refused for an armed trigger: a tool call runs a branch of the
// graph, and there is no run to run it in.
func (c *triggerCtx) CallTool(ctx context.Context, toolNodeID string, args map[string]any, timeout time.Duration) (*Msg, error) {
	return nil, fmt.Errorf("tool calls cannot run from a trigger context")
}

// Connection resolves a configured connection ID via the engine's resolver.
func (c *triggerCtx) Connection(id string) (any, error) {
	if err := access.CheckRunConnection(c.runAs, id); err != nil {
		return nil, err
	}
	return c.eng.resolveConnection(id)
}

// Skipped records a run that settles at once as canceled for an event the
// trigger decided not to start a run for, so run history shows what was
// skipped and why.
func (c *triggerCtx) Skipped(msg *Msg, reason string) {
	c.eng.recordSkippedTrigger(c.flowID, c.versionID, c.nodeID, msg, reason)
}

// FlowID returns the armed trigger's flow.
func (c *triggerCtx) FlowID() string { return c.flowID.String() }
