package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/access"
	"github.com/grmrgecko/karakuri/pkg/events"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/metrics"
)

// retryExecution customizes the reporting and terminal handling around the
// retry loop shared by graph nodes and synchronous script-hosted calls.
type retryExecution struct {
	ctx           context.Context
	policy        *flow.RetryPolicy
	pure          bool
	beforeAttempt func(attempt int)
	retrying      func(attempt int, err error)
	succeeded     func(attempt int, outputs, reported map[string][]*Msg)
	failed        func(attempt int, err error) nodeResult
	canceled      func(attempt int, err error) nodeResult
}

// executeNode runs one graph node instance with its effective retry policy.
// On exhausted attempts a wired error branch contains the failure; otherwise
// the ordinary unhandled-failure path stops new graph scheduling.
func (r *Run) executeNode(inst *nodeInstance, slot *workSlot) nodeResult {
	if inst.resume != nil {
		return r.resumeNode(inst, slot)
	}
	e := r.eng
	n := inst.node
	signalOnly := len(inst.inputs) > 0
	for id := range inst.inputs {
		port, ok := n.ports.Input(id)
		if !ok || port.Kind != flow.PortKindSignal {
			signalOnly = false
			break
		}
	}

	// Run once per run when asked. A fan-out schedules one instance per part,
	// so the claim is on the node rather than the instance, and it is atomic
	// because parts run concurrently. Dropping the rest looks exactly like a
	// filter dropping a message: succeeded, nothing emitted, nothing
	// downstream scheduled.
	if !signalOnly && n.executeOnce && !n.ran.CompareAndSwap(false, true) {
		e.emitNodeState(r, n.id, NodeStateSucceeded, 1, false, "")
		return nodeResult{inst: inst, status: NodeStateSucceeded}
	}

	// Parameters are pulled once per execution, before the first attempt: a
	// retry is retrying this node, not re-evaluating the graph behind it. The
	// instance's group stack rides along so a tool's argument ports resolve
	// against the call this execution runs inside.
	var params map[string]any
	if !signalOnly {
		var err error
		params, err = r.resolveParams(r.ctx, n, callGroup(inst))
		if err != nil {
			// A pull abandoned by cancellation is not a node failure, the same
			// as an execution abandoned by it.
			if r.ctx.Err() != nil {
				return nodeResult{inst: inst, status: nodeCanceled, err: err}
			}
			return r.failUnhandled(inst, 1, err)
		}
	}

	ec := &execCtx{run: r, node: n, inst: inst, slot: slot, params: params}
	return r.executeWithRetry(inst, ec, retryExecution{
		ctx:    r.ctx,
		policy: n.retry,
		beforeAttempt: func(attempt int) {
			e.emitNodeState(r, n.id, NodeStateRunning, attempt, false, "")
		},
		retrying: func(attempt int, err error) {
			e.emitNodeState(r, n.id, NodeStateRetrying, attempt, false, err.Error())
		},
		succeeded: func(attempt int, _, reported map[string][]*Msg) {
			e.emitNodeStateOutputs(r, n.id, NodeStateSucceeded, attempt, false, "", reported)
		},
		failed: func(attempt int, err error) nodeResult {
			if port, wired := errorPortWired(n); wired {
				outputs := One(port, buildErrorMsg(inst, err, attempt))
				e.emitNodeStateOutputs(r, n.id, NodeStateFailed, attempt, true, err.Error(), withEmissions(inst, outputs))
				return nodeResult{inst: inst, status: NodeStateFailed, handled: true, outputs: outputs, err: err}
			}
			return r.failUnhandled(inst, attempt, err)
		},
	})
}

// resumeNode continues a stored wait through the node's Resume. It runs once:
// the action the wait follows has already happened, so a failure is not
// retried, and parameters are not pulled because the wait carries its own
// message.
func (r *Run) resumeNode(inst *nodeInstance, slot *workSlot) nodeResult {
	e := r.eng
	n := inst.node
	ec := &execCtx{run: r, node: n, inst: inst, slot: slot}
	e.emitNodeState(r, n.id, NodeStateRunning, 1, false, "")
	outputs, err := safeResume(r.ctx, inst, ec)
	if err == nil {
		r.stampResumed(outputs)
		e.emitNodeStateOutputs(r, n.id, NodeStateSucceeded, 1, false, "", withEmissions(inst, outputs))
		return nodeResult{inst: inst, status: NodeStateSucceeded, outputs: outputs}
	}
	if r.ctx.Err() != nil {
		return nodeResult{inst: inst, status: nodeCanceled, err: err}
	}
	if port, wired := errorPortWired(n); wired {
		outputs := One(port, buildErrorMsg(inst, err, 1))
		e.emitNodeStateOutputs(r, n.id, NodeStateFailed, 1, true, err.Error(), withEmissions(inst, outputs))
		return nodeResult{inst: inst, status: NodeStateFailed, handled: true, outputs: outputs, err: err}
	}
	return r.failUnhandled(inst, 1, err)
}

// safeResume invokes the node's Resume, converting panics into errors.
func safeResume(ctx context.Context, inst *nodeInstance, ec ExecContext) (outputs map[string][]*Msg, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic in node resume: %v", p)
		}
	}()
	res, ok := inst.node.nt.(Resumer)
	if !ok {
		return nil, fmt.Errorf("node type %q cannot resume a wait", inst.node.meta.Type)
	}
	return res.Resume(ctx, ec, inst.resume.key, inst.resume.wait)
}

// stampResumed marks every message a resumed wait emits with where it came
// from and how late the restore was.
func (r *Run) stampResumed(outputs map[string][]*Msg) {
	for _, msgs := range outputs {
		for _, msg := range msgs {
			r.stampResumedMsg(msg)
		}
	}
}

// stampResumedMsg adds meta.resumed to one message, copying its meta so a
// message shared with stored state is not changed underneath it.
func (r *Run) stampResumedMsg(msg *Msg) {
	if msg == nil || r.resume == nil {
		return
	}
	info := map[string]any{"deadline": r.resume.wait.Deadline.UTC().Format(time.RFC3339Nano)}
	if r.resumedFrom != nil {
		info["from_run_id"] = r.resumedFrom.String()
	}
	late := r.startedAt.Sub(r.resume.wait.Deadline)
	if late < 0 {
		late = 0
	}
	info["late_ms"] = late.Milliseconds()
	meta := make(map[string]any, len(msg.Meta)+1)
	for k, v := range msg.Meta {
		meta[k] = v
	}
	meta["resumed"] = info
	msg.Meta = meta
}

// executeWithRetry runs attempts synchronously under ctx. Pure evaluations are
// always single-attempt and call Execute without message inputs.
func (r *Run) executeWithRetry(inst *nodeInstance, ec *execCtx, opts retryExecution) nodeResult {
	ctx := opts.ctx
	if ctx == nil {
		ctx = r.ctx
	}
	maxAttempts := 1
	if !opts.pure && opts.policy != nil && opts.policy.MaxAttempts > 1 {
		maxAttempts = opts.policy.MaxAttempts
	}
	attempt := 1
	for {
		if opts.beforeAttempt != nil {
			opts.beforeAttempt(attempt)
		}
		var outputs map[string][]*Msg
		var err error
		if opts.pure {
			outputs, err = safeExecutePure(ctx, inst.node, ec)
		} else {
			outputs, err = safeExecute(ctx, inst, ec)
		}
		if err == nil {
			reported := withEmissions(inst, outputs)
			if opts.succeeded != nil {
				opts.succeeded(attempt, outputs, reported)
			}
			return nodeResult{inst: inst, status: NodeStateSucceeded, outputs: outputs}
		}

		// Cancellation never takes the graph failure path. Script-hosted calls
		// can override this to contain a child-only deadline into their caller.
		if ctx.Err() != nil {
			if opts.canceled != nil {
				return opts.canceled(attempt, err)
			}
			return nodeResult{inst: inst, status: nodeCanceled, err: err}
		}

		var failure *ExecutionError
		retryAllowed := !errors.As(err, &failure) || failure.Retryable
		if attempt < maxAttempts && retryAllowed {
			metrics.NodeRetriesTotal.WithLabelValues(r.flowSlug, inst.node.meta.Type).Inc()
			if opts.retrying != nil {
				opts.retrying(attempt, err)
			}
			select {
			case <-r.eng.clock.After(retryWait(opts.policy, attempt, failure)):
			case <-ctx.Done():
				if opts.canceled != nil {
					return opts.canceled(attempt, err)
				}
				return nodeResult{inst: inst, status: nodeCanceled, err: err}
			}
			attempt++
			continue
		}
		return opts.failed(attempt, err)
	}
}

// failUnhandled reports a node failure that has no error branch to land on.
// When the execution ran inside a tool-call branch the failure is contained:
// the waiting call receives the error as its observation and the run goes on,
// because a tool failure is the model's problem, not the run's. A branch can
// outlive its call through a timeout or an earlier return; its failure then
// has no caller to reach and is still contained. Everywhere else it stays the
// unhandled failure that ends the run.
func (r *Run) failUnhandled(inst *nodeInstance, attempt int, err error) nodeResult {
	n := inst.node
	if callID, ok := r.calls.findActive(inst.inputs); ok {
		r.eng.emitNodeState(r, n.id, NodeStateFailed, attempt, true, err.Error())
		return nodeResult{inst: inst, status: NodeStateFailed, handled: true, containedCall: callID, err: err}
	}
	r.eng.emitNodeState(r, n.id, NodeStateFailed, attempt, false, err.Error())
	return nodeResult{inst: inst, status: NodeStateFailed, err: err}
}

// safeExecute invokes the node's Execute, converting panics into errors.
func safeExecute(ctx context.Context, inst *nodeInstance, ec ExecContext) (outputs map[string][]*Msg, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic in node execution: %v", p)
		}
	}()
	return inst.node.nt.Execute(withFlowActor(ctx, ec), ec, inst.inputs)
}

// withFlowActor puts the running flow and run in ctx as the actor, so a
// device command, control write, or connection change a node makes is
// recorded in the event log as the flow's.
func withFlowActor(ctx context.Context, ec ExecContext) context.Context {
	c, ok := ec.(*execCtx)
	if !ok || c.run == nil {
		return ctx
	}
	ctx = access.WithRunUser(ctx, c.run.runAs)
	return events.WithActor(ctx, events.Actor{
		Kind: events.ActorFlow, ID: c.run.flowID.String(), Name: c.run.flowName,
		Via: events.ViaFlow, RunID: c.run.id.String(),
	})
}

// withEmissions merges messages the node delivered mid-execution into the
// outputs reported to history, so a node's event reflects everything it sent.
// Already-delivered emissions are never re-routed.
func withEmissions(inst *nodeInstance, outputs map[string][]*Msg) map[string][]*Msg {
	if len(inst.emitted) == 0 {
		return outputs
	}
	merged := make(map[string][]*Msg, len(outputs)+len(inst.emitted))
	for port, msgs := range inst.emitted {
		merged[port] = append(merged[port], msgs...)
	}
	for port, msgs := range outputs {
		merged[port] = append(merged[port], msgs...)
	}
	return merged
}

// retryDelay computes the wait before the next attempt:
// delay_ms * backoff^(attempt-1), varied by jitter and capped at max_delay_ms.
// A zero or unset backoff means a constant delay.
func retryDelay(p *flow.RetryPolicy, attempt int) time.Duration {
	if p == nil || p.DelayMs <= 0 {
		return 0
	}
	backoff := p.Backoff
	if backoff <= 0 {
		backoff = 1.0
	}
	ms := float64(p.DelayMs) * math.Pow(backoff, float64(attempt-1))
	if math.IsInf(ms, 1) {
		ms = float64(math.MaxInt64 / int64(time.Millisecond))
	}
	if p.Jitter > 0 {
		ms *= 1 - p.Jitter + 2*p.Jitter*rand.Float64()
	}
	if p.MaxDelayMs > 0 && ms > float64(p.MaxDelayMs) {
		ms = float64(p.MaxDelayMs)
	}
	// Without a cap, backoff 2 passes the int64 nanosecond range at attempt
	// 34 and the converted duration wraps negative, which retries at once
	// instead of waiting. Hold it at the longest wait a Duration can express.
	if ms <= 0 {
		return 0
	}
	if ms >= float64(math.MaxInt64/int64(time.Millisecond)) {
		return time.Duration(math.MaxInt64)
	}
	return time.Duration(ms) * time.Millisecond
}

// errorPortWired returns the node's error output port ID when it has a failure
// branch and at least one edge is wired to it. The port comes from the node's
// resolved ports, so a script that declared its own signature is read the way
// its author wrote it.
func errorPortWired(n *runNode) (string, bool) {
	port, ok := n.ports.ErrorOutput()
	if !ok || len(n.outEdges[port.ID]) == 0 {
		return "", false
	}
	return port.ID, true
}

// buildErrorMsg constructs the message emitted on a node's error branch: the
// last input payload (or a map of inputs when multiple), plus failure meta.
// The failing message's group stack carries over so a fan-out part that fails
// stays associated with its group.
func buildErrorMsg(inst *nodeInstance, err error, attempts int) *Msg {
	inputs := inst.inputs
	var payload any
	var group []GroupFrame
	var topic string
	meta := map[string]any{}
	if len(inputs) == 1 {
		for _, m := range inputs {
			if m != nil {
				payload = m.Payload
				group = m.Group
				topic = m.Topic
				for key, value := range m.Meta {
					meta[key] = value
				}
			}
		}
	} else {
		mp := make(map[string]any, len(inputs))
		for port, m := range inputs {
			if m != nil {
				mp[port] = m.Payload
				if group == nil {
					group = m.Group
				}
			}
		}
		payload = mp
	}
	delete(meta, "error_details")
	delete(meta, "retry_after_ms")
	delete(meta, "retryable")
	meta["error"], meta["attempts"], meta["node_id"] = err.Error(), attempts, inst.node.id
	var failure *ExecutionError
	if errors.As(err, &failure) {
		meta["retryable"] = failure.Retryable
		if len(failure.Details) > 0 {
			meta["error_details"] = failure.Details
		}
		if failure.RetryAfterMS > 0 {
			meta["retry_after_ms"] = failure.RetryAfterMS
		}
	}
	return &Msg{ID: uuid.New().String(), Payload: payload, Topic: topic, Group: group, Meta: meta}
}

// retryWait honors a server minimum after jitter, then applies the policy cap.
func retryWait(p *flow.RetryPolicy, attempt int, failure *ExecutionError) time.Duration {
	delay := retryDelay(p, attempt)
	if failure != nil && failure.RetryAfterMS > delay.Milliseconds() {
		ms := failure.RetryAfterMS
		if ms > math.MaxInt64/int64(time.Millisecond) {
			delay = time.Duration(math.MaxInt64)
		} else {
			delay = time.Duration(ms) * time.Millisecond
		}
	}
	if p != nil && p.MaxDelayMs > 0 && delay > time.Duration(p.MaxDelayMs)*time.Millisecond {
		delay = time.Duration(p.MaxDelayMs) * time.Millisecond
	}
	return delay
}
