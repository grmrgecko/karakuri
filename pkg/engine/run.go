package engine

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"go.uber.org/zap"
)

// groupKey renders a fan-out stack as a node instance key. Ungrouped messages
// share the empty key, so graphs without fan-out schedule exactly one instance
// per node, the engine's original behavior.
func groupKey(g []GroupFrame) string {
	if len(g) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range g {
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(f.ID)
		b.WriteByte(':')
		b.WriteString(strconv.Itoa(f.Index))
	}
	return b.String()
}

// Node state constants for run events.
const (
	NodeStatePending   = "pending"
	NodeStateRunning   = "running"
	NodeStateRetrying  = "retrying"
	NodeStateSucceeded = "succeeded"
	NodeStateFailed    = "failed"
	NodeStateSkipped   = "skipped"
)

// nodeCanceled is the internal node result status for an execution that was
// abandoned because the run was canceled. It never reaches a run event: settle
// reports such nodes as skipped.
const nodeCanceled = "canceled"

// Run status constants.
const (
	RunStatusRunning   = "running"
	RunStatusSucceeded = "succeeded"
	RunStatusFailed    = "failed"
	RunStatusCanceled  = "canceled"
)

// RunOptions describes one run to start: the graph, the trigger, and the
// initial message that seeds the trigger node.
type RunOptions struct {
	FlowID   uuid.UUID
	FlowName string
	// FlowSlug labels the flow on Prometheus metrics; optional.
	FlowSlug       string
	FlowVersionID  uint
	TriggerNodeID  string
	Graph          *flow.Graph
	InitialMsg     *Msg
	ErrorFlowID    *uuid.UUID
	IsErrorHandler bool
	// StartNodeID runs a partial graph: instead of seeding the trigger, the
	// named node's connected input ports are seeded from SeedInputs and only
	// the subgraph reachable from it executes; everything else settles as
	// skipped. Used by run-from-node test runs.
	StartNodeID string
	// SeedInputs maps the start node's input port IDs to messages, including
	// optional or unwired inputs supplied by an isolated node test.
	// Required when StartNodeID is set; every gating input port must have an
	// entry or the run is rejected.
	SeedInputs map[string]*Msg
	// DurableWaits stores the run's pending waits so they survive restarts
	// and redeploys. Set for runs of a deployed graph; test runs of drafts
	// keep their waits in memory.
	DurableWaits bool
	// ResumeNodeID resumes one stored wait: instead of seeding the trigger,
	// the named node's Resume continues ResumeWait under ResumeKey and only
	// what it emits is routed.
	ResumeNodeID string
	ResumeKey    string
	ResumeWait   Wait
	// ResumedFromRunID is the run that armed the resumed wait.
	ResumedFromRunID *uuid.UUID
	// RunAs is the user the run acts as: whose connection and agent access
	// its nodes use. Runs of a deployed version act as its deployer and test
	// runs as the user who started them. Zero fails every connection and
	// agent the run reaches.
	RunAs uuid.UUID
}

// resumeSpec is the stored wait a resume run continues.
type resumeSpec struct {
	nodeID string
	key    string
	wait   Wait
}

// edgeTarget is one downstream delivery destination of an output port.
type edgeTarget struct {
	node string
	port string
}

// edgeSource identifies the output a parameter is pulled from.
type edgeSource struct {
	node string
	port string
}

// runNode is the per-run state of one graph node: its resolved type, config,
// wiring, and the instances scheduled for it. All fields are owned by the run
// loop goroutine.
type runNode struct {
	id   string
	nt   NodeType
	meta NodeMeta
	// ports is the node's resolved port set: its type's static ports for most
	// nodes, and the per-instance derivation for the types whose ports come
	// from config (script, switch, manual), plus any parameter ports. The
	// engine reads ports from here rather than from meta, or a script's
	// declared signature would be invisible to routing.
	ports flow.PortSet
	cfg   []byte
	retry *flow.RetryPolicy
	// storeID overrides the node ID used for node stores during synthetic
	// script-hosted calls. Graph nodes leave it empty and keep their existing
	// storage identity.
	storeID string
	// executeOnce runs the node for the first message of a run and drops the
	// rest, and ran records whether that has happened. Both live on the run's
	// node rather than the instance, because the point is to be once across
	// every instance a fan-out creates.
	executeOnce bool
	ran         atomic.Bool
	// gating holds connected input ports that must all have a message before
	// the node runs; optional holds connected ports that dispatch the node on
	// their own without waiting for the others.
	gating   map[string]bool
	optional map[string]bool
	outEdges map[string][]edgeTarget
	// wired marks output ports at least one edge of either kind leaves, which
	// is what ExecContext.Wired answers. outEdges cannot answer it alone: a
	// value edge routes no message, so it lands here and not there.
	wired map[string]bool
	// params maps a config field ID to the node outputs that supply it, in
	// graph edge order. The values are pulled when this node runs, not
	// delivered as messages. Only a multi parameter port validly holds more
	// than one source; its pulled values reach the config as an array.
	params map[string][]edgeSource

	// Scheduling state, owned by the run loop.
	instances map[string]*nodeInstance
	completed bool
}

// nodeInstance is one execution of a node within a run. A node has a single
// instance unless an upstream node fanned out, in which case it gets one
// instance per message group so the subgraph below a split runs once per part.
type nodeInstance struct {
	node   *runNode
	key    string
	inputs map[string]*Msg
	// emitted collects messages the node delivered mid-execution through
	// ExecContext.Emit so they still show up in the node's history event.
	emitted map[string][]*Msg

	dispatched bool
	state      string
	// resume marks the instance that continues a stored wait through the
	// node's Resume rather than executing it.
	resume *resumeSpec
}

// instanceFor returns the node's instance for a message group key, creating it
// on first use.
func (n *runNode) instanceFor(key string) *nodeInstance {
	if inst, ok := n.instances[key]; ok {
		return inst
	}
	inst := &nodeInstance{node: n, key: key, inputs: make(map[string]*Msg), state: NodeStatePending}
	n.instances[key] = inst
	return inst
}

// addInstance registers an extra instance under a key of its own. Messages on
// optional ports use it so they never share an instance with gated input.
func (n *runNode) addInstance(key string) *nodeInstance {
	inst := &nodeInstance{node: n, key: key, inputs: make(map[string]*Msg), state: NodeStatePending}
	n.instances[key] = inst
	return inst
}

// ready reports whether every gating input port of the instance has a message.
func (inst *nodeInstance) ready() bool {
	if len(inst.node.gating) == 0 {
		return len(inst.inputs) > 0
	}
	for port := range inst.node.gating {
		if _, ok := inst.inputs[port]; !ok {
			return false
		}
	}
	return true
}

// record appends a mid-execution emission so it can be reported alongside the
// node's returned outputs.
func (inst *nodeInstance) record(port string, msg *Msg) {
	if inst.emitted == nil {
		inst.emitted = make(map[string][]*Msg)
	}
	inst.emitted[port] = append(inst.emitted[port], msg)
}

// nodeResult is what a node goroutine reports back after executing.
type nodeResult struct {
	inst    *nodeInstance
	status  string // NodeStateSucceeded, NodeStateFailed, or nodeCanceled
	handled bool
	outputs map[string][]*Msg
	err     error
	// containedCall names the pending tool call an unhandled failure was
	// contained into; apply resolves the call with the error, loop-side.
	containedCall string
}

// emission is a message a node delivered mid-execution via ExecContext.Emit,
// or one the engine injected on a tool node's call output.
type emission struct {
	// inst is the emitting instance, nil for an injected tool call.
	inst *nodeInstance
	// node is the node the message leaves from: inst.node for a node's own
	// emission, the tool node for an injected call.
	node *runNode
	port string
	msg  *Msg
	// callID marks a tool-call injection so a halted run fails the waiting
	// caller instead of silently discarding the message.
	callID string
}

// Run is one in-memory execution of a flow graph.
type Run struct {
	id             uuid.UUID
	flowID         uuid.UUID
	flowName       string
	flowSlug       string
	flowVersionID  uint
	triggerNodeID  string
	startNodeID    string
	seedInputs     map[string]*Msg
	isErrorHandler bool
	errorFlowID    *uuid.UUID
	initial        *Msg
	durableWaits   bool
	resume         *resumeSpec
	resumedFrom    *uuid.UUID
	runAs          uuid.UUID

	eng         *Engine
	ctx         context.Context
	cancel      context.CancelFunc
	cancelCause context.CancelCauseFunc
	nodes       map[string]*runNode
	// pulls memoizes parameter values evaluated on demand over value edges.
	pulls *pullState
	// calls correlates pending graph-backed tool calls with their waiters.
	calls *toolCallRegistry
	// invocations holds event metadata for synthetic script-hosted calls. These
	// nodes deliberately do not enter nodes, whose membership drives scheduling
	// and final skipped-state reporting.
	invocations map[string]invocationEvent

	// sem bounds how many nodes execute at once. Each dispatched instance runs
	// on its own goroutine and holds a slot only while it is doing work.
	sem chan struct{}
	// parkedCh closes the first time any node parks, so a caller starting runs
	// in order can wait for one to settle into its wait before the next.
	parkedCh   chan struct{}
	parkedOnce sync.Once
	resultCh   chan nodeResult
	emitCh     chan emission
	done       chan struct{}
	// instSeq names extra instances created for optional-port messages.
	instSeq int

	mu           sync.Mutex
	status       string
	startedAt    time.Time
	endedAt      *time.Time
	errMsg       string
	hadFailures  bool
	failedNode   string
	cancelReason string
	state        map[string]any
	// vars is the run's variable store: values any node can read and write,
	// visible to expressions as `vars` and to scripts as their `vars` object.
	// It is scoped to the run, so concurrent webhook requests never share one.
	vars map[string]any
	// parked counts nodes currently parked; parkedTotal accumulates the wall
	// time during which at least one node was parked, which the run timeout
	// discounts.
	parked      int
	parkedSince time.Time
	parkedTotal time.Duration
	// outputs holds each completed node's last emitted output message (the
	// first message on its first non-empty output port, in metadata port
	// order) so expressions can reference other nodes' results via `nodes`.
	outputs map[string]*Msg
	// outputPorts names the port each entry of outputs was emitted on, so a
	// parameter bound to one port is never answered from another.
	outputPorts map[string]string
	// access caches the run user's connection checks by connection ID.
	access map[string]error
}

// newRun builds the run object and per-node scheduling state from a graph.
// Annotation nodes are excluded from scheduling entirely.
func newRun(e *Engine, opts RunOptions) (*Run, error) {
	if opts.Graph == nil {
		return nil, fmt.Errorf("run has no graph")
	}
	if opts.InitialMsg == nil {
		opts.InitialMsg = &Msg{}
	}
	if opts.InitialMsg.ID == "" {
		opts.InitialMsg.ID = uuid.New().String()
	}

	// Resolve node types and build scheduling state.
	nodes := make(map[string]*runNode, len(opts.Graph.Nodes))
	for i := range opts.Graph.Nodes {
		gn := &opts.Graph.Nodes[i]
		nt, ok := e.reg.Get(gn.Type)
		if !ok {
			return nil, fmt.Errorf("unknown node type %q", gn.Type)
		}
		meta := nt.Meta()
		if meta.Category == flow.CategoryAnnotation {
			continue
		}
		// Effective retry policy: node override, else the type default.
		retry := gn.Retry
		if retry == nil && meta.DefaultRetry != nil {
			cp := *meta.DefaultRetry
			retry = &cp
		}
		if retry != nil {
			if err := retry.Validate(); err != nil {
				return nil, fmt.Errorf("node %q retry policy: %w", gn.ID, err)
			}
		}
		// Subflow nodes are spliced out by flattening before a run is built, so
		// no node here needs a referenced graph to resolve its ports.
		nodes[gn.ID] = &runNode{
			id:          gn.ID,
			nt:          nt,
			meta:        meta,
			ports:       flow.NodePorts(meta, gn, nil),
			cfg:         gn.Config,
			retry:       retry,
			executeOnce: gn.ExecuteOnce,
			gating:      make(map[string]bool),
			optional:    make(map[string]bool),
			outEdges:    make(map[string][]edgeTarget),
			wired:       make(map[string]bool),
			params:      make(map[string][]edgeSource),
			instances:   make(map[string]*nodeInstance),
		}
	}

	// Wire edges: connected input ports gate readiness unless the port is
	// declared optional, output edges route emitted messages downstream.
	for i := range opts.Graph.Edges {
		ed := &opts.Graph.Edges[i]
		src, srcOK := nodes[ed.Source]
		tgt, tgtOK := nodes[ed.Target]
		if !srcOK || !tgtOK {
			continue
		}
		// A value edge records where a parameter comes from and stops there: it
		// delivers no message, so it neither gates the target nor routes output.
		if ed.IsValue() {
			if fieldID, ok := flow.ParamFieldID(ed.TargetHandle); ok {
				tgt.params[fieldID] = append(tgt.params[fieldID], edgeSource{node: ed.Source, port: ed.SourceHandle})
				src.wired[ed.SourceHandle] = true
			}
			continue
		}
		if spec, ok := tgt.ports.Input(ed.TargetHandle); ok && spec.Optional {
			tgt.optional[ed.TargetHandle] = true
		} else {
			tgt.gating[ed.TargetHandle] = true
		}
		src.outEdges[ed.SourceHandle] = append(src.outEdges[ed.SourceHandle], edgeTarget{node: ed.Target, port: ed.TargetHandle})
		src.wired[ed.SourceHandle] = true
	}

	var resume *resumeSpec
	if opts.ResumeNodeID != "" {
		n, ok := nodes[opts.ResumeNodeID]
		if !ok {
			return nil, fmt.Errorf("resumed node %q not found in graph", opts.ResumeNodeID)
		}
		if _, ok := n.nt.(Resumer); !ok {
			return nil, fmt.Errorf("node %q of type %q cannot resume a wait", n.id, n.meta.Type)
		}
		resume = &resumeSpec{nodeID: opts.ResumeNodeID, key: opts.ResumeKey, wait: opts.ResumeWait}
	} else if opts.StartNodeID == "" {
		if _, ok := nodes[opts.TriggerNodeID]; !ok {
			return nil, fmt.Errorf("trigger node %q not found in graph", opts.TriggerNodeID)
		}
	}

	// Partial runs must name an existing start node with every gating input
	// port seeded.
	if opts.StartNodeID != "" {
		start, ok := nodes[opts.StartNodeID]
		if !ok {
			return nil, fmt.Errorf("start node %q not found in graph", opts.StartNodeID)
		}
		for port := range start.gating {
			if opts.SeedInputs[port] == nil {
				return nil, fmt.Errorf("start node %q input port %q has no seed message", opts.StartNodeID, port)
			}
		}
	}

	ctx, cancelCause := context.WithCancelCause(e.baseCtx)
	return &Run{
		id:             uuid.New(),
		flowID:         opts.FlowID,
		flowName:       opts.FlowName,
		flowSlug:       opts.FlowSlug,
		flowVersionID:  opts.FlowVersionID,
		triggerNodeID:  opts.TriggerNodeID,
		startNodeID:    opts.StartNodeID,
		seedInputs:     opts.SeedInputs,
		isErrorHandler: opts.IsErrorHandler,
		errorFlowID:    opts.ErrorFlowID,
		initial:        opts.InitialMsg,
		durableWaits:   opts.DurableWaits || resume != nil,
		resume:         resume,
		resumedFrom:    opts.ResumedFromRunID,
		runAs:          opts.RunAs,
		eng:            e,
		ctx:            ctx,
		cancel:         func() { cancelCause(nil) },
		cancelCause:    cancelCause,
		nodes:          nodes,
		sem:            make(chan struct{}, e.maxParallel),
		resultCh:       make(chan nodeResult),
		emitCh:         make(chan emission),
		done:           make(chan struct{}),
		parkedCh:       make(chan struct{}),
		status:         RunStatusRunning,
		startedAt:      e.clock.Now(),
		state:          make(map[string]any),
		vars:           make(map[string]any),
		outputs:        make(map[string]*Msg),
		outputPorts:    make(map[string]string),
		pulls:          newPullState(),
		calls:          newToolCallRegistry(),
		invocations:    make(map[string]invocationEvent),
	}, nil
}

// getVar reads a run variable.
func (r *Run) getVar(key string) (any, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	v, ok := r.vars[key]
	return v, ok
}

// setVar writes a run variable. A nil value deletes the key so a script can
// clear a variable by assigning null.
func (r *Run) setVar(key string, value any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if value == nil {
		delete(r.vars, key)
		return
	}
	r.vars[key] = value
}

// varsEnv snapshots the run's variables. Callers get a copy so expression
// evaluation and script execution never read a map another node is writing.
func (r *Run) varsEnv() map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]any, len(r.vars))
	for k, v := range r.vars {
		out[k] = v
	}
	return out
}

// cancelWithReason records why the run was canceled (first reason wins) and
// cancels the run context, waking retry waits, parks, and in-flight executions.
func (r *Run) cancelWithReason(reason string) {
	r.mu.Lock()
	if r.cancelReason == "" {
		r.cancelReason = reason
	}
	r.mu.Unlock()
	r.cancel()
}

// suspend cancels the run with ErrSuspended, so its nodes leave their pending
// waits stored for a resume.
func (r *Run) suspend() {
	r.mu.Lock()
	if r.cancelReason == "" {
		r.cancelReason = ErrSuspended.Error()
	}
	r.mu.Unlock()
	r.cancelCause(ErrSuspended)
}

// snapshot returns the run's current public state.
func (r *Run) snapshot() RunSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return RunSnapshot{
		ID:             r.id,
		FlowID:         r.flowID,
		FlowName:       r.flowName,
		FlowVersionID:  r.flowVersionID,
		TriggerNodeID:  r.triggerNodeID,
		Status:         r.status,
		StartedAt:      r.startedAt,
		EndedAt:        r.endedAt,
		Error:          r.errMsg,
		HadFailures:    r.hadFailures,
		IsErrorHandler: r.isErrorHandler,
		ResumedFrom:    r.resumedFrom,
	}
}

// failedNodeID returns the node whose unhandled failure ended the run.
func (r *Run) failedNodeID() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.failedNode
}

// loop is the run's scheduler goroutine: it seeds the entry node, dispatches
// instances as their inputs fill, and settles the run when no work remains.
//
// Failure semantics: on the first unhandled node failure, no new instances are
// scheduled; those already in flight run to completion (their events are still
// emitted, but their outputs schedule nothing new), then the run ends failed.
// Handled failures (error branch wired) keep the run going.
func (r *Run) loop() {
	// Enforce the run timeout via the injectable clock.
	if r.eng.defaultTimeout > 0 {
		go r.watchTimeout()
	}

	// Seed the entry node: the trigger with the initial message on the
	// synthetic port, or the start node with the caller-provided inputs for
	// partial (run-from-node) runs.
	entryNode := r.nodes[r.triggerNodeID]
	if r.startNodeID != "" {
		entryNode = r.nodes[r.startNodeID]
	}
	if r.resume != nil {
		entryNode = r.nodes[r.resume.nodeID]
	}
	entry := entryNode.instanceFor("")
	if r.resume != nil {
		entry.resume = r.resume
	} else if r.startNodeID != "" {
		for port, msg := range r.seedInputs {
			entry.inputs[port] = msg
		}
	} else {
		entry.inputs[TriggerInput] = r.initial
	}
	r.dispatch(entry)
	inFlight := 1

	halted := false
	doneCh := r.ctx.Done()
	for inFlight > 0 {
		select {
		case res := <-r.resultCh:
			inFlight--
			for _, inst := range r.apply(res, &halted) {
				r.dispatch(inst)
				inFlight++
			}
			// A halted run dispatches nothing more, so no branch can reach a
			// tool's return input. Fail the calls still waiting rather than
			// leave their callers parked until the call timeout.
			if halted {
				r.calls.failAll(fmt.Errorf("run stopped before the tool call returned"))
			}
		case em := <-r.emitCh:
			// A node emitted before returning: route it now unless the run has
			// stopped scheduling new work. A discarded tool-call injection must
			// fail its waiter — the caller is parked, so nothing else would
			// ever wake it.
			if halted || r.ctx.Err() != nil {
				if em.callID != "" {
					r.calls.fail(em.callID, fmt.Errorf("run stopped before the tool call could start"))
				}
				continue
			}
			if em.callID != "" {
				if len(em.node.outEdges[em.port]) == 0 {
					r.calls.fail(em.callID, fmt.Errorf("tool node %q has nothing wired to its call output", em.node.id))
					continue
				}
				r.eng.emitNodeState(r, em.node.id, NodeStateRunning, 1, false, "")
			}
			for _, inst := range r.route(em.node, em.port, em.msg) {
				r.dispatch(inst)
				inFlight++
			}
		case <-doneCh:
			// Canceled: drain in-flight work, schedule nothing new.
			halted = true
			doneCh = nil
		}
	}

	r.settle()
	r.respondPending()
	r.eng.finishRun(r)
	close(r.done)
}

// respondPending answers a synchronous webhook whose request is still waiting
// when the run ends. A flow that returned to its Webhook already filled
// the slot and this is a no-op; a flow that did not gets a reply built from
// its response variables instead of leaving the caller to time out.
func (r *Run) respondPending() {
	requestID, _ := r.initial.Meta[RequestIDMeta].(string)
	if requestID == "" {
		return
	}
	resp := RunCompletionResponse(r.snapshot().Status, r.varsEnv())
	if Responses().Respond(requestID, resp) {
		r.eng.log.Debug("run answered a pending webhook request on completion",
			zap.String("run_id", r.id.String()),
			zap.Int("status", resp.Status))
	}
}

// dispatch starts a ready instance on its own goroutine, bounded by the run's
// worker semaphore. One goroutine per instance (rather than a fixed-size work
// queue) keeps fan-out from deadlocking when a node emits more messages than
// the queue could hold.
func (r *Run) dispatch(inst *nodeInstance) {
	inst.dispatched = true
	inst.state = NodeStateRunning
	go func() {
		slot := &workSlot{run: r}
		if !slot.acquire(r.ctx) {
			// Canceled before the node started; settle reports it skipped.
			r.resultCh <- nodeResult{inst: inst, status: nodeCanceled}
			return
		}
		res := r.executeNode(inst, slot)
		slot.release()
		r.resultCh <- res
	}()
}

// workSlot is one node's hold on the run's worker semaphore. Parking releases
// the slot so a parked node never starves the rest of the graph.
type workSlot struct {
	run  *Run
	held bool
}

// acquire takes a worker slot, reporting false when the context is canceled
// first.
func (s *workSlot) acquire(ctx context.Context) bool {
	if s.held {
		return true
	}
	select {
	case s.run.sem <- struct{}{}:
		s.held = true
	case <-ctx.Done():
	}
	return s.held
}

// release returns the worker slot if it is held.
func (s *workSlot) release() {
	if s.held {
		<-s.run.sem
		s.held = false
	}
}

// park suspends the calling node for d, releasing its worker slot and pausing
// the run timeout for the duration. It returns the context's error when the
// run is canceled before the wait completes.
func (r *Run) park(ctx context.Context, d time.Duration, slot *workSlot) error {
	return r.parkUntil(ctx, d, nil, slot)
}

// parkUntil releases the worker until a timer, signal or cancellation arrives.
func (r *Run) parkUntil(ctx context.Context, d time.Duration, signal <-chan struct{}, slot *workSlot) error {
	if d <= 0 {
		return ctx.Err()
	}
	// Register the wake-up before giving up the worker slot, so the timer is
	// already pending for whatever runs in this node's place.
	timer := r.eng.clock.After(d)
	r.beginPark()
	slot.release()

	var err error
	select {
	case <-timer:
	case <-signal:
	case <-ctx.Done():
		err = ctx.Err()
	}

	r.endPark()
	// Take the slot back against the run rather than a child execution's
	// deadline. A script-hosted node may time out while parked, but its caller
	// must still own a worker when control returns to the interpreter. A canceled
	// run cannot reacquire, which is harmless because release becomes a no-op.
	slot.acquire(r.ctx)
	return err
}

// beginPark marks the start of a parked interval.
func (r *Run) beginPark() {
	r.mu.Lock()
	if r.parked == 0 {
		r.parkedSince = r.eng.clock.Now()
	}
	r.parked++
	r.mu.Unlock()
	r.parkedOnce.Do(func() { close(r.parkedCh) })
}

// endPark closes a parked interval, folding it into the discounted total once
// the last parked node wakes.
func (r *Run) endPark() {
	r.mu.Lock()
	r.parked--
	if r.parked == 0 {
		r.parkedTotal += r.eng.clock.Now().Sub(r.parkedSince)
	}
	r.mu.Unlock()
}

// watchTimeout cancels the run once it has spent the engine's run timeout
// actually working. Time during which any node was parked is discounted, so a
// node may park far longer than the timeout without tripping it.
func (r *Run) watchTimeout() {
	for {
		remaining := r.timeoutRemaining()
		if remaining <= 0 {
			r.cancelWithReason("run timeout")
			return
		}
		select {
		case <-r.eng.clock.After(remaining):
		case <-r.ctx.Done():
			return
		}
	}
}

// timeoutRemaining returns how much unparked time the run has left.
func (r *Run) timeoutRemaining() time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.eng.clock.Now()
	parked := r.parkedTotal
	if r.parked > 0 {
		parked += now.Sub(r.parkedSince)
	}
	return r.eng.defaultTimeout - (now.Sub(r.startedAt) - parked)
}

// emit hands a mid-execution emission to the run loop for delivery.
func (r *Run) emit(inst *nodeInstance, port string, msg *Msg) {
	if msg == nil {
		return
	}
	inst.record(port, msg)
	select {
	case r.emitCh <- emission{inst: inst, node: inst.node, port: port, msg: msg}:
	case <-r.ctx.Done():
	}
}

// callGroup picks the group stack an execution runs under: the deepest stack
// among its input messages, read in sorted port order so the answer is stable.
func callGroup(inst *nodeInstance) []GroupFrame {
	ports := make([]string, 0, len(inst.inputs))
	for port := range inst.inputs {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	var group []GroupFrame
	for _, port := range ports {
		if m := inst.inputs[port]; m != nil && len(m.Group) > len(group) {
			group = m.Group
		}
	}
	return group
}

// injectCall hands a tool-call message to the run loop, delivered from the
// tool node's call output as though the node had emitted it.
func (r *Run) injectCall(node *runNode, msg *Msg, callID string) error {
	select {
	case r.emitCh <- emission{node: node, port: flow.ToolCallPort, msg: msg, callID: callID}:
		return nil
	case <-r.ctx.Done():
		return r.ctx.Err()
	}
}

// callTool runs one graph-backed tool call on behalf of the calling instance:
// the arguments are injected on the tool node's call output under a fresh
// group frame, the wired branch runs, and the message arriving back at the
// tool node's return input is the result. The wait works the way park does —
// slot released, run timeout paused — so a call can outlast the run timeout
// and never starves the branch it is waiting on.
func (r *Run) callTool(ctx context.Context, inst *nodeInstance, slot *workSlot, toolNodeID string, args map[string]any, timeout time.Duration) (*Msg, error) {
	node, ok := r.nodes[toolNodeID]
	if !ok {
		return nil, fmt.Errorf("tool node %q is not in the graph", toolNodeID)
	}
	if node.meta.Type != flow.NodeTypeTool {
		return nil, fmt.Errorf("node %q is not a tool node", toolNodeID)
	}
	if timeout <= 0 {
		timeout = defaultToolCallTimeout
	}

	callID := uuid.New().String()
	ch, cancelCall := r.calls.register(callID, toolNodeID, args)
	defer cancelCall()

	// The call frame rides on top of the caller's own stack, so a fanned-out
	// caller's branch instances stay distinct from its siblings' and every
	// branch node gets a fresh instance per call.
	base := callGroup(inst)
	group := make([]GroupFrame, 0, len(base)+1)
	group = append(group, base...)
	group = append(group, GroupFrame{ID: callID, Index: 0, Count: 1})
	name := flow.ToolName(node.cfg)
	msg := &Msg{
		ID:      uuid.New().String(),
		Payload: args,
		Topic:   name,
		Meta:    map[string]any{"tool": name, "call_id": callID},
		Group:   group,
	}

	// The injection is handed to the loop while this node still holds its
	// slot: the loop always drains emitCh, and the branch's own nodes queue on
	// the semaphore until the park below frees this one.
	if err := r.injectCall(node, msg, callID); err != nil {
		return nil, err
	}

	// Register the wake-up before giving up the worker slot, like park does.
	timer := r.eng.clock.After(timeout)
	r.beginPark()
	slot.release()
	var res toolCallResult
	var err error
	select {
	case res = <-ch:
		err = res.err
	case <-timer:
		err = fmt.Errorf("tool call timed out after %s", timeout)
	case <-ctx.Done():
		err = ctx.Err()
	}
	r.endPark()
	// As with Park, a child-only deadline must not return the caller to script
	// execution without its worker slot.
	slot.acquire(r.ctx)

	if err != nil {
		// The tool node's call ended without a return; say so in its events
		// unless the run itself is going away.
		if ctx.Err() == nil {
			r.eng.emitNodeState(r, toolNodeID, NodeStateFailed, 1, true, err.Error())
		}
		return nil, err
	}
	return res.msg, nil
}

// apply folds a node result into run state and returns newly ready instances.
// After an unhandled failure or cancellation (halted), results are still
// recorded but nothing new becomes ready.
func (r *Run) apply(res nodeResult, halted *bool) []*nodeInstance {
	inst := res.inst
	n := inst.node
	r.recordOutput(n, res.outputs)
	switch res.status {
	case NodeStateSucceeded:
		inst.state = NodeStateSucceeded
		n.completed = true
	case NodeStateFailed:
		inst.state = NodeStateFailed
		n.completed = true
		r.mu.Lock()
		r.hadFailures = true
		if !res.handled && r.failedNode == "" {
			r.failedNode = n.id
			r.errMsg = res.err.Error()
		}
		r.mu.Unlock()
		// A failure contained into a tool call resolves the call with the
		// error — loop-side, so it serializes with return interception.
		if res.containedCall != "" {
			r.calls.fail(res.containedCall, res.err)
		}
		if !res.handled {
			*halted = true
			return nil
		}
	default:
		// Canceled mid-execution: settle marks the node skipped unless some
		// other instance of it completed.
		inst.state = NodeStatePending
		return nil
	}

	if *halted || r.ctx.Err() != nil {
		return nil
	}
	return r.deliver(inst, res.outputs)
}

// recordOutput stores a completed node's representative output — the first
// message on its first non-empty output port in resolved port order — for
// cross-node expression access via the `nodes` env map.
func (r *Run) recordOutput(n *runNode, outputs map[string][]*Msg) {
	port, msg := firstOutput(n.ports.Outputs, outputs)
	if msg == nil {
		return
	}
	r.mu.Lock()
	r.outputs[n.id] = msg
	r.outputPorts[n.id] = port
	r.mu.Unlock()
}

// firstOutput picks a node's representative emitted message and the port it
// left on: the first non-nil message on the first non-empty output port in
// declared order, falling back to ports absent from the declaration in port ID
// order. A node that emitted on a port it never declared still has to answer
// reproducibly across runs, so the fallback is sorted rather than map order.
func firstOutput(declared []flow.PortSpec, outputs map[string][]*Msg) (string, *Msg) {
	if len(outputs) == 0 {
		return "", nil
	}
	for _, p := range declared {
		if m := firstNonNil(outputs[p.ID]); m != nil {
			return p.ID, m
		}
	}
	ports := make([]string, 0, len(outputs))
	for port := range outputs {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	for _, port := range ports {
		if m := firstNonNil(outputs[port]); m != nil {
			return port, m
		}
	}
	return "", nil
}

// firstNonNil returns the first non-nil message of a port's emissions.
func firstNonNil(msgs []*Msg) *Msg {
	for _, m := range msgs {
		if m != nil {
			return m
		}
	}
	return nil
}

// outputsEnv snapshots completed node outputs as plain maps for the
// expression environment's `nodes` name.
func (r *Run) outputsEnv() map[string]map[string]any {
	r.mu.Lock()
	defer r.mu.Unlock()
	env := make(map[string]map[string]any, len(r.outputs))
	for id, m := range r.outputs {
		meta := m.Meta
		if meta == nil {
			meta = map[string]any{}
		}
		env[id] = map[string]any{"payload": m.Payload, "topic": m.Topic, "meta": meta}
	}
	return env
}

// deliver routes every emitted message to its connected downstream input ports
// and returns the instances that became ready.
func (r *Run) deliver(src *nodeInstance, outputs map[string][]*Msg) []*nodeInstance {
	var ready []*nodeInstance
	for port, msgs := range outputs {
		for _, msg := range msgs {
			ready = append(ready, r.route(src.node, port, msg)...)
		}
	}
	return ready
}

// route delivers one message from a source output port to every wired target
// and returns the instances that became ready. A message whose group stack
// differs from an already-running instance's opens a new instance, which is
// how fan-out runs the downstream subgraph once per part.
func (r *Run) route(src *runNode, port string, msg *Msg) []*nodeInstance {
	if msg == nil {
		return nil
	}
	if msg.ID == "" {
		msg.ID = uuid.New().String()
	}
	var ready []*nodeInstance
	for _, t := range src.outEdges[port] {
		tn := r.nodes[t.node]
		if tn == nil {
			continue
		}
		// A message reaching a tool node's return input is the observation for
		// a pending call: it resolves the call instead of scheduling the node.
		// A return no call is waiting for — a second fan-out part after the
		// first won, or one arriving after the call timed out — is dropped.
		if tn.meta.Type == flow.NodeTypeTool && t.port == flow.ToolReturnPort {
			if callID, ok := r.calls.matchReturn(msg.Group, tn.id); ok && r.calls.resolve(callID, msg) {
				r.eng.emitNodeStateOutputs(r, tn.id, NodeStateSucceeded, 1, false, "",
					map[string][]*Msg{flow.ToolReturnPort: {msg}})
			} else {
				r.eng.log.Debug("tool return with no waiting call; dropped",
					zap.String("run_id", r.id.String()),
					zap.String("node_id", tn.id))
			}
			continue
		}
		// Optional ports never wait on their siblings: each message runs the
		// node on an instance of its own.
		if tn.optional[t.port] {
			r.instSeq++
			inst := tn.addInstance(groupKey(msg.Group) + "#" + strconv.Itoa(r.instSeq))
			inst.inputs[t.port] = msg
			ready = append(ready, inst)
			continue
		}
		key := groupKey(msg.Group)
		if targetPort, ok := tn.ports.Input(t.port); ok && targetPort.Kind == flow.PortKindReturn && tn.meta.IsTrigger() {
			key += "#return"
		}
		inst := tn.instanceFor(key)
		if inst.dispatched {
			continue
		}
		if _, dup := inst.inputs[t.port]; dup {
			continue
		}
		inst.inputs[t.port] = msg
		if inst.ready() {
			ready = append(ready, inst)
		}
	}
	return ready
}

// settle marks every node that never completed as skipped and records the
// run's terminal status: failed on unhandled node failure, canceled when the
// context was canceled, succeeded otherwise.
func (r *Run) settle() {
	for _, n := range r.nodes {
		if n.completed {
			continue
		}
		// A tool node that served a call was active — its Execute never runs on
		// the message path, so completion bookkeeping misses it — and reporting
		// it skipped would overwrite the per-call events.
		if n.meta.Type == flow.NodeTypeTool && r.calls.wasTouched(n.id) {
			continue
		}
		// A node pulled for a parameter value executed off the message path and
		// reported succeeded when it did; reporting it skipped would overwrite
		// that record.
		if r.pulls.ran(n.id) {
			continue
		}
		r.eng.emitNodeState(r, n.id, NodeStateSkipped, 0, false, "")
	}

	r.mu.Lock()
	switch {
	case r.failedNode != "":
		r.status = RunStatusFailed
	case r.ctx.Err() != nil:
		r.status = RunStatusCanceled
		if r.errMsg == "" {
			if r.cancelReason != "" {
				r.errMsg = r.cancelReason
			} else if Suspended(r.ctx) {
				r.errMsg = ErrSuspended.Error()
			} else {
				r.errMsg = "canceled"
			}
		}
	default:
		r.status = RunStatusSucceeded
	}
	ended := r.eng.clock.Now()
	r.endedAt = &ended
	status := r.status
	r.mu.Unlock()

	// Release the context resources for completed runs.
	r.cancel()

	r.eng.log.Debug("run settled",
		zap.String("run_id", r.id.String()),
		zap.String("flow_id", r.flowID.String()),
		zap.String("status", status))
}
