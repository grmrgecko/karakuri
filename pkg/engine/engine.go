package engine

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/metrics"
	"github.com/grmrgecko/karakuri/pkg/models"
	"go.uber.org/zap"
)

// Publisher publishes engine events to the in-process message broker.
// Implemented by pkg/mq.Broker.
type Publisher interface {
	Publish(topic, msgType string, payload any)
}

// ConnResolver resolves a connection ID from node config to a live driver
// handle. Implemented by the integrations manager; injected at startup so the
// engine does not depend on the integrations package.
type ConnResolver func(id string) (any, error)

// Sentinel errors returned by RunFlow.
var (
	// ErrRunLimit is returned when a flow already has the maximum number of
	// concurrent runs; the new run is dropped.
	ErrRunLimit = errors.New("per-flow concurrent run limit reached")
	// ErrStopped is returned when the engine has been stopped.
	ErrStopped = errors.New("engine is stopped")
)

// stopWaitTimeout bounds how long Stop waits for run goroutines to settle
// before abandoning them.
const stopWaitTimeout = 5 * time.Second

// Options configures a new Engine. Zero values fall back to sane defaults.
type Options struct {
	Registry *Registry
	Clock    Clock
	Logger   *zap.Logger
	// Publisher receives run/node events for WebSocket delivery. Optional.
	Publisher Publisher
	// History receives run rows and run events for persistence. Optional.
	History History
	// VarSink persists flow and global variables. Optional; without one they
	// live only as long as the process.
	VarSink VarSink
	// NodeStateSink persists durable node store entries. Optional; without one
	// DurableStore state lives only as long as the process.
	NodeStateSink NodeStateSink
	// WaitSink persists pending waits of deployed runs. Optional; without one
	// waits live only as long as the process.
	WaitSink WaitSink
	// MaxParallelNodes bounds the per-run worker pool (default 4).
	MaxParallelNodes int
	// MaxRunsPerFlow caps concurrent runs per flow; 0 means unlimited.
	MaxRunsPerFlow int
	// DefaultRunTimeout cancels runs that exceed it; 0 means no timeout.
	DefaultRunTimeout time.Duration
	// ErrorFlowRunner is invoked when a run fails and its flow has an error
	// handler flow configured. Optional.
	ErrorFlowRunner func(errorFlowID uuid.UUID, msg *Msg)
	// ConnResolver resolves connection IDs for ExecContext.Connection. Optional.
	ConnResolver ConnResolver
	// SubflowResolver resolves shared subflow references when flattening
	// graphs at run creation. Optional; runs of graphs with shared subflow
	// nodes fail without it.
	SubflowResolver SharedSubflowResolver
}

// Engine executes flow graphs as in-memory runs with bounded parallelism,
// per-node retry, and bounded run history through the history sink.
type Engine struct {
	reg             *Registry
	clock           Clock
	log             *zap.Logger
	pub             Publisher
	history         History
	maxParallel     int
	maxRunsPerFlow  int
	defaultTimeout  time.Duration
	errorFlowRunner func(uuid.UUID, *Msg)
	connResolver    ConnResolver
	subflowResolver SharedSubflowResolver
	stores          *nodeStores
	durable         *durableStores
	waits           *waitRegistry
	vars            *varStore

	mu         sync.Mutex
	runs       map[uuid.UUID]*Run
	flowRuns   map[uuid.UUID]int
	stopped    bool
	baseCtx    context.Context
	baseCancel context.CancelCauseFunc
	wg         sync.WaitGroup
}

// New creates an Engine from the given options.
func New(opts Options) *Engine {
	if opts.Registry == nil {
		opts.Registry = Default()
	}
	if opts.Clock == nil {
		opts.Clock = RealClock{}
	}
	if opts.Logger == nil {
		opts.Logger = zap.NewNop()
	}
	if opts.MaxParallelNodes <= 0 {
		opts.MaxParallelNodes = 4
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	return &Engine{
		reg:             opts.Registry,
		clock:           opts.Clock,
		log:             opts.Logger,
		pub:             opts.Publisher,
		history:         opts.History,
		maxParallel:     opts.MaxParallelNodes,
		maxRunsPerFlow:  opts.MaxRunsPerFlow,
		defaultTimeout:  opts.DefaultRunTimeout,
		errorFlowRunner: opts.ErrorFlowRunner,
		connResolver:    opts.ConnResolver,
		subflowResolver: opts.SubflowResolver,
		stores:          newNodeStores(),
		durable:         newDurableStores(opts.NodeStateSink, opts.Logger),
		waits:           newWaitRegistry(opts.WaitSink, opts.Logger),
		vars:            newVarStore(opts.VarSink),
		runs:            make(map[uuid.UUID]*Run),
		flowRuns:        make(map[uuid.UUID]int),
		baseCtx:         ctx,
		baseCancel:      cancel,
	}
}

// SetErrorFlowRunner installs the callback used to trigger error handler flows.
func (e *Engine) SetErrorFlowRunner(fn func(errorFlowID uuid.UUID, msg *Msg)) {
	e.errorFlowRunner = fn
}

// SetConnResolver installs the connection resolver used by ExecContext.Connection.
func (e *Engine) SetConnResolver(fn ConnResolver) {
	e.connResolver = fn
}

// SetSubflowResolver installs the shared subflow resolver used when
// flattening graphs at run creation.
func (e *Engine) SetSubflowResolver(fn SharedSubflowResolver) {
	e.subflowResolver = fn
}

// nodeStore returns the flow-scoped store for one node, creating it on first
// use.
func (e *Engine) nodeStore(flowID uuid.UUID, nodeID string) NodeStore {
	return e.stores.get(flowID, nodeID)
}

// durableNodeStore returns the durable store for one node, creating it on
// first use.
func (e *Engine) durableNodeStore(flowID uuid.UUID, nodeID string) NodeStore {
	return e.durable.get(flowID, nodeID)
}

// PurgeNodeState drops the flow-scoped state of every node in a flow. Called
// when a flow is rearmed or disarmed so a redeploy or a disable resets the
// nodes that reason across messages (filter, trigger, rate limits, batching).
// Durable stores are deliberately left alone: surviving exactly these events
// is what they are for.
func (e *Engine) PurgeNodeState(flowID uuid.UUID) {
	e.stores.purge(flowID)
}

// PurgeDurableNodeState drops a flow's durable node stores from memory.
// Called when the flow is deleted; the persisted rows go in the flow's own
// delete transaction.
func (e *Engine) PurgeDurableNodeState(flowID uuid.UUID) {
	e.durable.purge(flowID)
}

// resolveConnection resolves a connection ID through the installed resolver.
func (e *Engine) resolveConnection(id string) (any, error) {
	if e.connResolver == nil {
		return nil, errors.New("connections are not available")
	}
	if id == "" {
		return nil, errors.New("node has no connection configured")
	}
	return e.connResolver(id)
}

// Start marks the engine ready. Runs may be submitted after Start.
func (e *Engine) Start() {
	e.log.Info("execution engine started",
		zap.Int("max_parallel_nodes", e.maxParallel),
		zap.Int("max_runs_per_flow", e.maxRunsPerFlow),
		zap.Duration("default_run_timeout", e.defaultTimeout))
}

// LoadVariables seeds the flow and global variable cache from persisted rows.
// Call it before starting flows so the first run already sees stored values.
func (e *Engine) LoadVariables(rows []*models.Variable) {
	e.vars.load(rows, e.log)
}

// LoadNodeState seeds the durable node stores from persisted rows. Call it
// before starting flows so the first message already compares against what
// the last shutdown left behind.
func (e *Engine) LoadNodeState(rows []*models.NodeStoreEntry) {
	e.durable.load(rows, e.log)
}

// LoadWaits seeds the pending-wait registry from stored rows. Call it before
// arming flows so their restored waits resume before any trigger fires.
func (e *Engine) LoadWaits(rows []*models.PendingWait) {
	e.waits.load(rows)
}

// PendingWaits returns a flow's pending waits ordered by deadline, excluding
// state-only entries.
func (e *Engine) PendingWaits(flowID uuid.UUID) []PendingWaitInfo {
	return e.waits.list(flowID, false)
}

// CancelWait removes one pending wait. Its waiter, if parked, finds its claim
// refused at the deadline and emits nothing. It reports whether the wait
// existed.
func (e *Engine) CancelWait(flowID uuid.UUID, nodeID, key string) bool {
	return e.waits.remove(flowID, nodeID, key)
}

// PurgeWaits forgets a flow's pending waits in memory. Called when the flow is
// deleted; its rows go in the flow's own delete transaction.
func (e *Engine) PurgeWaits(flowID uuid.UUID) {
	e.waits.purge(flowID)
}

// Stop cancels all active runs, waits for their goroutines to settle up to a
// timeout, then flushes and closes the history sink.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		return
	}
	e.stopped = true
	e.mu.Unlock()

	// Cancel every run via the shared base context. Suspension leaves their
	// pending waits stored for the next start to resume.
	e.baseCancel(ErrSuspended)

	// Wait for run goroutines with a timeout, then abandon.
	settled := make(chan struct{})
	go func() {
		e.wg.Wait()
		close(settled)
	}()
	select {
	case <-settled:
	case <-e.clock.After(stopWaitTimeout):
		e.log.Warn("engine stop timed out waiting for runs; abandoning")
	}

	if e.history != nil {
		e.history.Close()
	}
	if e.vars.sink != nil {
		e.vars.sink.Close()
	}
	if e.durable.sink != nil {
		e.durable.sink.Close()
	}
	if e.waits.sink != nil {
		e.waits.sink.Close()
	}
	e.log.Info("execution engine stopped")
}

// hasNode reports whether the graph places a node with the given ID.
func hasNode(g *flow.Graph, id string) bool {
	for i := range g.Nodes {
		if g.Nodes[i].ID == id {
			return true
		}
	}
	return false
}

// executionGraph cuts a graph down to what entryID can reach, then expands
// subflow nodes into a flat node set with namespaced IDs and splices out the
// link routing nodes. Deploy validates only the reachable part, so a draft left
// on the canvas may name a link nobody sets or a subflow that no longer exists;
// flattening the whole graph first would fail every run of a flow that
// deployed fine.
//
// A node inside a subflow is addressed by its flattened ID,
// "<subflowNode>/<child>", as a wait it holds is keyed. The graph is cut at the
// top-level subflow node placing it, then cut again at the node itself once the
// body is expanded.
func (e *Engine) executionGraph(g *flow.Graph, entryID string) (*flow.Graph, error) {
	nested := ""
	if !hasNode(g, entryID) {
		if top, _, ok := strings.Cut(entryID, "/"); ok && hasNode(g, top) {
			nested = entryID
			entryID = top
		}
	}
	g = flow.ExecutionGraph(g, entryID)
	if needsFlatten(g) {
		flat, err := FlattenGraph(g, e.subflowResolver, e.reg)
		if err != nil {
			return nil, fmt.Errorf("graph flattening failed: %w", err)
		}
		g = flat
	}
	if nested != "" {
		g = flow.ExecutionGraph(g, nested)
	}
	return g, nil
}

// RunFlow starts a new run for a flow graph. The returned handle exposes the
// run ID, a done channel, and status snapshots. Returns ErrRunLimit when the
// flow's concurrent-run cap is reached and ErrStopped after Stop.
func (e *Engine) RunFlow(opts RunOptions) (*RunHandle, error) {
	if opts.Graph != nil {
		entryID := opts.TriggerNodeID
		if opts.StartNodeID != "" {
			entryID = opts.StartNodeID
		}
		if opts.ResumeNodeID != "" {
			entryID = opts.ResumeNodeID
		}
		g, err := e.executionGraph(opts.Graph, entryID)
		if err != nil {
			return nil, err
		}
		opts.Graph = g
	}

	run, err := newRun(e, opts)
	if err != nil {
		return nil, err
	}

	// Admission control under the engine lock.
	// A rejected run never loops, so its context must be released here or it
	// stays registered under the engine context for the life of the process.
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		run.cancel()
		return nil, ErrStopped
	}
	if e.maxRunsPerFlow > 0 && e.flowRuns[opts.FlowID] >= e.maxRunsPerFlow {
		e.mu.Unlock()
		run.cancel()
		e.log.Warn("run dropped: per-flow concurrent run limit reached",
			zap.String("flow_id", opts.FlowID.String()),
			zap.Int("limit", e.maxRunsPerFlow))
		return nil, ErrRunLimit
	}
	e.flowRuns[opts.FlowID]++
	e.runs[run.id] = run
	if run.resume != nil {
		// The resume run is the wait's waiter from here on, so a rearm while
		// it is parked does not start another.
		e.waits.attach(run.resume.wait.Token, run.id)
	}
	e.wg.Add(1)
	e.mu.Unlock()
	metrics.ActiveRuns.Inc()

	// Persist the new running row and announce
	// the run before any node events can be emitted.
	e.historyRunStarted(run)
	e.publishRunStarted(run)

	go run.loop()
	return &RunHandle{ID: run.id, run: run}, nil
}

// CancelRun cancels the active run with the given ID. Returns false when no
// such run is active.
func (e *Engine) CancelRun(id uuid.UUID) bool {
	e.mu.Lock()
	run, ok := e.runs[id]
	e.mu.Unlock()
	if !ok {
		return false
	}
	run.cancelWithReason("canceled")
	return true
}

// CancelFlowRuns cancels every active run of the given flow and returns how
// many were canceled. Used on flow disable.
func (e *Engine) CancelFlowRuns(flowID uuid.UUID) int {
	e.mu.Lock()
	var targets []*Run
	for _, run := range e.runs {
		if run.flowID == flowID {
			targets = append(targets, run)
		}
	}
	e.mu.Unlock()
	for _, run := range targets {
		run.cancelWithReason("canceled")
	}
	return len(targets)
}

// SuspendFlowRuns cancels every active run of the given flow with
// ErrSuspended, leaving their pending waits stored, and waits up to the stop
// bound for them to settle so none can claim a wait after its resume starts.
// Used on redeploy.
func (e *Engine) SuspendFlowRuns(flowID uuid.UUID) int {
	e.mu.Lock()
	var targets []*Run
	for _, run := range e.runs {
		if run.flowID == flowID {
			targets = append(targets, run)
		}
	}
	e.mu.Unlock()
	if len(targets) == 0 {
		return 0
	}
	for _, run := range targets {
		run.suspend()
	}
	deadline := e.clock.After(stopWaitTimeout)
	for _, run := range targets {
		select {
		case <-run.done:
		case <-deadline:
			e.log.Warn("redeploy timed out waiting for suspended runs",
				zap.String("flow_id", flowID.String()))
			return len(targets)
		}
	}
	return len(targets)
}

// ActiveRuns returns snapshots of all in-memory runs sorted by start time.
func (e *Engine) ActiveRuns() []RunSnapshot {
	e.mu.Lock()
	snaps := make([]RunSnapshot, 0, len(e.runs))
	for _, run := range e.runs {
		snaps = append(snaps, run.snapshot())
	}
	e.mu.Unlock()
	sort.Slice(snaps, func(i, j int) bool { return snaps[i].StartedAt.Before(snaps[j].StartedAt) })
	return snaps
}

// finishRun removes a settled run from the active maps, persists and
// publishes the terminal state, and triggers the error handler flow when the
// run failed. Called exactly once from the run goroutine.
func (e *Engine) finishRun(run *Run) {
	e.mu.Lock()
	delete(e.runs, run.id)
	e.flowRuns[run.flowID]--
	if e.flowRuns[run.flowID] <= 0 {
		delete(e.flowRuns, run.flowID)
	}
	e.mu.Unlock()

	e.waits.releaseRun(run.id)

	snap := run.snapshot()
	metrics.ActiveRuns.Dec()
	metrics.RunsTotal.WithLabelValues(run.flowSlug, snap.Status).Inc()
	e.historyRunFinished(run, snap)
	e.publishRunFinished(run, snap)

	// Trigger the flow-level error handler on failure. Runs started by an
	// error handler never trigger another handler, preventing loops.
	if snap.Status == "failed" && !run.isErrorHandler && run.errorFlowID != nil && e.errorFlowRunner != nil {
		msg := &Msg{
			ID: uuid.New().String(),
			Payload: map[string]any{
				"flow_id":        run.flowID.String(),
				"flow_name":      run.flowName,
				"run_id":         run.id.String(),
				"failed_node_id": run.failedNodeID(),
				"error":          snap.Error,
			},
			Meta: map[string]any{"source": "error_handler"},
		}
		e.errorFlowRunner(*run.errorFlowID, msg)
	}

	e.wg.Done()
}

// RunSnapshot is a point-in-time view of a run's public state.
type RunSnapshot struct {
	ID             uuid.UUID  `json:"id" description:"Run identifier."`
	FlowID         uuid.UUID  `json:"flow_id" description:"Flow being executed."`
	FlowName       string     `json:"flow_name,omitempty" description:"Flow name when the run started."`
	FlowVersionID  uint       `json:"flow_version_id" description:"Graph version executed."`
	TriggerNodeID  string     `json:"trigger_node_id" description:"Node that started the run."`
	Status         string     `json:"status" description:"Run status."`
	StartedAt      time.Time  `json:"started_at" description:"Start timestamp."`
	EndedAt        *time.Time `json:"ended_at,omitempty" description:"End timestamp."`
	Error          string     `json:"error,omitempty" description:"Terminal error message."`
	HadFailures    bool       `json:"had_failures" description:"Whether any node failed, even if handled."`
	IsErrorHandler bool       `json:"is_error_handler" description:"Whether the run was started by an error handler."`
	ResumedFrom    *uuid.UUID `json:"resumed_from_run_id,omitempty" description:"Run that armed the restored wait this run continues."`
}

// RunHandle is the caller's reference to a started run.
type RunHandle struct {
	ID  uuid.UUID
	run *Run
}

// Done returns a channel closed when the run has fully settled.
func (h *RunHandle) Done() <-chan struct{} { return h.run.done }

// Parked returns a channel closed the first time one of the run's nodes
// parks.
func (h *RunHandle) Parked() <-chan struct{} { return h.run.parkedCh }

// Snapshot returns the run's current public state.
func (h *RunHandle) Snapshot() RunSnapshot { return h.run.snapshot() }
