package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Parameter values are pulled, not pushed. A value edge records that a node's
// config field comes from another node's output; the value is produced when the
// consumer runs by executing the source node on demand and reading the payload
// it emits. Results are memoized for the run, so a node feeding three
// parameters still runs once.
//
// Only nodes marked Pure may be pulled. Validation rejects the rest, because
// pulling a node that calls the world would call it again at a moment nothing
// on the message path asked for.

// maxPullDepth bounds how deep a parameter chain may go. Validation has no
// cycle check across value edges, so the evaluator carries its own guard and
// reports the path it was walking when it gave up.
const maxPullDepth = 32

// pullState holds one run's memoized parameter values and the evaluations
// still in flight, so two consumers arriving at once share one evaluation.
type pullState struct {
	mu sync.Mutex
	// values maps node ID to that node's pulled outputs by port.
	values map[string]map[string]any
	// inflight holds a channel per node being evaluated, closed when that
	// evaluation ends whether or not it memoized a value.
	inflight map[string]chan struct{}
	// waiting maps a node being evaluated to the node its evaluation is
	// pulling right now, whether it runs that node itself or waits on another
	// goroutine's evaluation. Following it from an in-flight node shows what
	// that evaluation is blocked on, which is how two parallel branches that
	// each hold half of a cycle are told apart from two that share a node.
	waiting map[string]string
}

func newPullState() *pullState {
	return &pullState{
		values:   make(map[string]map[string]any),
		inflight: make(map[string]chan struct{}),
		waiting:  make(map[string]string),
	}
}

// blockedOn reports whether the evaluation of node is, through any chain of
// evaluations it waits on, waiting for a node on stack. Waiting on it would
// then never end. Callers hold the lock.
func (p *pullState) blockedOn(node string, stack []string) bool {
	seen := make(map[string]bool)
	for cur := node; !seen[cur]; {
		if slices.Contains(stack, cur) {
			return true
		}
		seen[cur] = true
		next, ok := p.waiting[cur]
		if !ok {
			return false
		}
		cur = next
	}
	return false
}

// pulling records that the evaluation on top of stack is pulling node.
// Only a stack deeper than the consumer has an evaluation of its own to
// record; the consumer at its root is never in flight. Callers hold the lock.
func (p *pullState) pulling(stack []string, node string) {
	if len(stack) > 1 {
		p.waiting[stack[len(stack)-1]] = node
	}
}

// pulled clears what pulling recorded for the evaluation on top of stack.
func (p *pullState) pulled(stack []string) {
	if len(stack) > 1 {
		p.mu.Lock()
		delete(p.waiting, stack[len(stack)-1])
		p.mu.Unlock()
	}
}

// ran reports whether the node produced a memoized value this run, meaning it
// executed on the value path and already reported its state.
func (p *pullState) ran(node string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.values[node]
	return ok
}

// resolveParams pulls every parameter bound on a node and returns them keyed by
// config field. A node with no bindings costs nothing. The group stack is the
// consuming execution's, carried so a tool's argument ports can resolve against
// the call the consumer runs inside.
func (r *Run) resolveParams(ctx context.Context, n *runNode, group []GroupFrame) (map[string]any, error) {
	return r.paramValues(ctx, n, []string{n.id}, group)
}

// paramValues pulls a node's bound parameters with the given pull stack. A
// multi parameter port delivers its sources as an array in edge order — one
// wire still arrives as a one-element array, so the node decodes the same
// shape however many tools or models are attached. Every other field binds a
// single source; validation guarantees it has exactly one.
func (r *Run) paramValues(ctx context.Context, n *runNode, stack []string, group []GroupFrame) (map[string]any, error) {
	return r.paramValuesWithCache(ctx, n, stack, group, r.pulls, false)
}

func (r *Run) paramValuesWithCache(ctx context.Context, n *runNode, stack []string, group []GroupFrame, cache *pullState, fresh bool) (map[string]any, error) {
	if len(n.params) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(n.params))
	for fieldID, srcs := range n.params {
		if spec, ok := n.ports.Input(flow.ParamPortID(fieldID)); ok && spec.Multi {
			values := make([]any, 0, len(srcs))
			for _, src := range srcs {
				value, err := r.pullWithCache(ctx, src, stack, group, cache, fresh)
				if err != nil {
					return nil, fmt.Errorf("parameter %q: %w", fieldID, err)
				}
				values = append(values, value)
			}
			out[fieldID] = values
			continue
		}
		value, err := r.pullWithCache(ctx, srcs[0], stack, group, cache, fresh)
		if err != nil {
			return nil, fmt.Errorf("parameter %q: %w", fieldID, err)
		}
		out[fieldID] = value
	}
	return out, nil
}

// readsToolArg reports whether any of a node's bound parameters is fed,
// directly or through other pulled nodes, by a tool node's argument port.
func (r *Run) readsToolArg(n *runNode, seen map[string]bool) bool {
	if len(n.params) == 0 {
		return false
	}
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[n.id] {
		return false
	}
	seen[n.id] = true
	for _, srcs := range n.params {
		for _, src := range srcs {
			up, ok := r.nodes[src.node]
			if !ok {
				continue
			}
			if up.meta.Type == flow.NodeTypeTool {
				if _, isArg := flow.ToolArgID(src.port); isArg {
					return true
				}
			}
			if r.readsToolArg(up, seen) {
				return true
			}
		}
	}
	return false
}

// pull produces the value on one output port, running the source node if it has
// not produced one yet. The stack carries the chain of nodes waiting on this
// value so a cycle names the path rather than just failing.
func (r *Run) pull(ctx context.Context, src edgeSource, stack []string, group []GroupFrame) (any, error) {
	return r.pullWithCache(ctx, src, stack, group, r.pulls, false)
}

func (r *Run) pullWithCache(ctx context.Context, src edgeSource, stack []string, group []GroupFrame, cache *pullState, fresh bool) (any, error) {
	if len(stack) > maxPullDepth {
		return nil, fmt.Errorf("parameter chain is too deep at %s", strings.Join(stack, " -> "))
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	node, ok := r.nodes[src.node]
	if !ok {
		return nil, fmt.Errorf("node %q is not in the graph", src.node)
	}

	// A tool node's argument port carries a per-call value: it is answered
	// from the pending call the consumer runs inside, never by pulling the
	// node and never memoized — the next call brings different arguments.
	if node.meta.Type == flow.NodeTypeTool {
		if argID, ok := flow.ToolArgID(src.port); ok {
			args, ok := r.calls.argsFor(group, src.node)
			if !ok {
				return nil, fmt.Errorf("tool argument %q can only be read by nodes running inside the tool's call branch", argID)
			}
			value, present := args[argID]
			if !present {
				if flow.ToolArgRequired(node.cfg, argID) {
					return nil, fmt.Errorf("the model did not supply required tool argument %q", argID)
				}
				return nil, nil
			}
			return value, nil
		}
	}

	// A node that already ran on the message path has an answer: reuse its
	// output rather than running it a second time. A fresh observation re-runs
	// it only when it has no message inputs, since a re-run cannot supply the
	// message its first result was computed from.
	if msg := r.nodeOutput(src.node, src.port); msg != nil && (!fresh || len(node.gating)+len(node.optional) > 0) {
		return msg.Payload, nil
	}

	// A pure node downstream of a tool argument has a different value in
	// every call, so its memo is written for the run view but never read.
	perCall := r.readsToolArg(node, nil)

	cache.mu.Lock()
	if ports, done := cache.values[src.node]; done && !perCall {
		value, ok := ports[src.port]
		cache.mu.Unlock()
		if !ok {
			return nil, fmt.Errorf("node %q produced nothing on port %q", src.node, src.port)
		}
		return value, nil
	}
	// A node already on this chain is a cycle. A node another goroutine is
	// evaluating right now is not: two fan-out parts or two parallel branches
	// bound to the same pure node share its one evaluation, so the later
	// arrival waits for the first to finish and then reads what it memoized.
	// It is a cycle again when that evaluation is itself waiting, directly or
	// through others, on a node of this chain: two branches that each started
	// half of a loop would otherwise wait on each other forever.
	if slices.Contains(stack, src.node) {
		cache.mu.Unlock()
		return nil, fmt.Errorf("parameters form a cycle: %s -> %s",
			strings.Join(stack, " -> "), src.node)
	}
	if wait, busy := cache.inflight[src.node]; busy {
		if cache.blockedOn(src.node, stack) {
			cache.mu.Unlock()
			return nil, fmt.Errorf("parameters form a cycle: %s -> %s",
				strings.Join(stack, " -> "), src.node)
		}
		cache.pulling(stack, src.node)
		cache.mu.Unlock()
		select {
		case <-wait:
			cache.pulled(stack)
			return r.pullWithCache(ctx, src, stack, group, cache, fresh)
		case <-ctx.Done():
			cache.pulled(stack)
			return nil, ctx.Err()
		}
	}
	done := make(chan struct{})
	cache.inflight[src.node] = done
	cache.pulling(stack, src.node)
	cache.mu.Unlock()

	defer func() {
		cache.mu.Lock()
		delete(cache.inflight, src.node)
		if len(stack) > 1 {
			delete(cache.waiting, stack[len(stack)-1])
		}
		cache.mu.Unlock()
		close(done)
	}()

	if !node.meta.Pure {
		return nil, fmt.Errorf("node %q has side effects and cannot be evaluated for a value", src.node)
	}

	// Pull this node's own parameters first, then run it with no message
	// inputs: a pure node produces its value from its config, not from traffic.
	params, err := r.paramValuesWithCache(ctx, node, append(stack, src.node), group, cache, fresh)
	if err != nil {
		return nil, err
	}

	ec := &execCtx{run: r, node: node, params: params, valueEval: true}
	outputs, err := safeExecutePure(ctx, node, ec)
	if err != nil {
		return nil, fmt.Errorf("evaluating node %q: %w", src.node, err)
	}

	// Memoize every port the node produced, so a second parameter reading a
	// different port of the same node does not run it again. A port that fanned
	// out has no single value to bind, and silently taking the first part would
	// look like a working wire that quietly drops the rest.
	byPort := make(map[string]any, len(outputs))
	for port, msgs := range outputs {
		if len(msgs) > 1 {
			return nil, fmt.Errorf("node %q emitted %d messages on port %q and cannot supply a single value",
				src.node, len(msgs), port)
		}
		if len(msgs) > 0 && msgs[0] != nil {
			byPort[port] = msgs[0].Payload
		}
	}
	cache.mu.Lock()
	cache.values[src.node] = byPort
	cache.mu.Unlock()

	// The node ran and produced its value; leave the same succeeded record the
	// message path would, or the run view reports a node that supplied a
	// parameter as skipped.
	if !fresh {
		r.eng.emitNodeStateOutputs(r, src.node, NodeStateSucceeded, 1, false, "", outputs)
	}

	value, ok := byPort[src.port]
	if !ok {
		return nil, fmt.Errorf("node %q produced nothing on port %q", src.node, src.port)
	}
	return value, nil
}

// nodeOutput returns a node's recorded output on a port from the message path,
// or nil when it has not run.
func (r *Run) nodeOutput(nodeID, port string) *Msg {
	r.mu.Lock()
	defer r.mu.Unlock()
	msg := r.outputs[nodeID]
	if msg == nil {
		return nil
	}
	// The recorded message answers only for the port it left on. A node that
	// failed into its error branch recorded the message it was handed, which is
	// not a result, and reading it for the result port would bind the input.
	if r.outputPorts[nodeID] != port {
		return nil
	}
	// outputs records the node's representative message rather than one per
	// port, so it can only answer for a node that has a single result port.
	// Resolved ports are what decide that: a script's static metadata always
	// shows two, so reading it here would never reuse a script's result.
	if node, ok := r.nodes[nodeID]; ok && resultPortCount(node) > 1 {
		return nil
	}
	return msg
}

// resultPortCount counts a node's output ports excluding its failure branch,
// which never carries a result a parameter could read.
func resultPortCount(n *runNode) int {
	count := 0
	for _, p := range n.ports.Outputs {
		if p.Kind != flow.PortKindError {
			count++
		}
	}
	return count
}

// safeExecutePure runs a pure node for its value, converting panics to errors
// the same way the message path does.
func safeExecutePure(ctx context.Context, n *runNode, ec ExecContext) (outputs map[string][]*Msg, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			outputs = nil
			err = fmt.Errorf("node panicked: %v", rec)
		}
	}()
	return n.nt.Execute(withFlowActor(ctx, ec), ec, nil)
}

// mergeParams overlays pulled parameter values onto a node's stored config, so
// a node decodes a wired field exactly as if it had been typed in. Fields
// without a wire keep their typed-in literal; strict validation requires a
// value edge on every exposed field, so a wired field always has a pulled
// value by the time a validated graph runs.
func mergeParams(cfg []byte, params map[string]any) ([]byte, error) {
	if len(params) == 0 {
		return cfg, nil
	}
	merged := map[string]json.RawMessage{}
	if len(cfg) > 0 {
		if err := json.Unmarshal(cfg, &merged); err != nil {
			return nil, fmt.Errorf("invalid node config: %w", err)
		}
	}
	// A stored config of JSON null decodes to a nil map.
	if merged == nil {
		merged = map[string]json.RawMessage{}
	}
	for field, value := range params {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("parameter %q cannot be encoded: %w", field, err)
		}
		merged[field] = encoded
	}
	return json.Marshal(merged)
}
