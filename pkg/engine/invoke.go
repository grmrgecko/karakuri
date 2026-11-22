package engine

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
)

const (
	maxScriptCallMessages = 1000
	maxScriptCallJSON     = 1 << 20
)

// invocationEvent identifies one synthetic call in the existing run-event
// stream without making it a schedulable graph node.
type invocationEvent struct {
	parentNodeID string
	nodeType     string
	invocationID string
	sequence     int
}

// addMetadata adds the invocation identity to a history or publisher payload.
func (e invocationEvent) addMetadata(payload map[string]any) {
	payload["parent_node_id"] = e.parentNodeID
	payload["node_type"] = e.nodeType
	payload["invocation_id"] = e.invocationID
	payload["sequence"] = e.sequence
}

// registerInvocation retains event metadata for the remainder of the run.
func (r *Run) registerInvocation(historyID string, event invocationEvent) {
	r.mu.Lock()
	if r.invocations == nil {
		r.invocations = make(map[string]invocationEvent)
	}
	r.invocations[historyID] = event
	r.mu.Unlock()
}

// invocationFor returns synthetic-call metadata associated with an event node ID.
func (r *Run) invocationFor(nodeID string) (invocationEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	event, ok := r.invocations[nodeID]
	return event, ok
}

// markInvocationFailure records a contained call failure without stopping graph
// scheduling. The calling script decides whether to catch it.
func (r *Run) markInvocationFailure() {
	r.mu.Lock()
	r.hadFailures = true
	r.mu.Unlock()
}

// ScriptNodeFunctions returns the currently eligible catalog. It is derived on
// every script execution so plugin and agent registry changes take effect.
func (c *execCtx) ScriptNodeFunctions() []NodeMeta {
	functions := scriptsdk.Functions(c.run.eng.reg.List())
	metas := make([]NodeMeta, 0, len(functions))
	for _, fn := range functions {
		metas = append(metas, fn.Meta)
	}
	return metas
}

// ScriptNodeCallCount reports admissions across outer attempts of this script.
func (c *execCtx) ScriptNodeCallCount() int {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	return c.callCount
}

// ScriptNodeCallStarted reports whether any admitted call reached Execute.
func (c *execCtx) ScriptNodeCallStarted() bool {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	return c.callStarted
}

// nextScriptNodeCall assigns the stable sequence for one admitted call.
func (c *execCtx) nextScriptNodeCall() int {
	c.callMu.Lock()
	defer c.callMu.Unlock()
	c.callCount++
	return c.callCount
}

// markScriptNodeCallStarted suppresses an outer script retry for this and every
// later attempt, including an interpreter failure after the call returns.
func (c *execCtx) markScriptNodeCallStarted() {
	c.callMu.Lock()
	c.callStarted = true
	c.callMu.Unlock()
}

// scriptInvocationParentID returns the originating graph node rather than a
// synthetic parent's per-run history identity.
func (c *execCtx) scriptInvocationParentID() string {
	if c.scriptParentID != "" {
		return c.scriptParentID
	}
	return c.node.id
}

// CallScriptNode executes one eligible node synchronously in the active run.
// Its synthetic instance never enters Run.nodes, so neither returned nor
// mid-execution outputs can reach graph edges or final scheduling bookkeeping.
func (c *execCtx) CallScriptNode(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
	r := c.run
	parentID := c.scriptInvocationParentID()
	sequence := c.nextScriptNodeCall()
	invocationID := call.Options.ID
	if invocationID == "" {
		invocationID = fmt.Sprintf("call-%d", sequence)
	}
	historyID := "script-call:" + uuid.NewString()
	event := invocationEvent{
		parentNodeID: parentID,
		nodeType:     call.Type,
		invocationID: invocationID,
		sequence:     sequence,
	}
	r.registerInvocation(historyID, event)

	failValidation := func(err error) (*nodeapi.NodeCallResult, error) {
		r.markInvocationFailure()
		r.eng.emitNodeState(r, historyID, NodeStateFailed, 0, true, err.Error())
		return nil, newScriptNodeCallError(call.Type, invocationID, sequence, 0, err)
	}

	nt, meta, ok := c.scriptNodeType(call.Type)
	if !ok {
		return failValidation(fmt.Errorf("node type %q is unavailable or not eligible for script calls", call.Type))
	}
	if err := scriptsdk.ValidateCall(meta, call, c.valueEval); err != nil {
		return failValidation(err)
	}
	if !c.valueEval && (c.inst == nil || c.slot == nil) {
		return failValidation(fmt.Errorf("script node calls require a message-path worker slot"))
	}
	if ctx == nil {
		ctx = r.ctx
	}
	if err := ctx.Err(); err != nil {
		return failValidation(err)
	}

	paramNames := make([]string, 0, len(call.Options.Params))
	for name := range call.Options.Params {
		paramNames = append(paramNames, name)
	}
	sort.Strings(paramNames)
	resolvedConfig, err := mergeParams(call.Config, call.Options.Params)
	if err != nil {
		return failValidation(err)
	}
	placed := &flow.Node{Type: call.Type, Config: resolvedConfig, Params: paramNames}
	ports := flow.NodePorts(meta, placed, nil)
	inputs, err := scriptCallInputs(ports, call.Inputs)
	if err != nil {
		return failValidation(err)
	}
	wired := make(map[string]bool)
	for _, port := range ports.Outputs {
		if port.Kind != flow.PortKindError {
			wired[port.ID] = true
		}
	}

	retry := call.Options.Retry
	if retry == nil && meta.DefaultRetry != nil {
		cp := *meta.DefaultRetry
		retry = &cp
	}
	if retry != nil {
		if err := retry.Validate(); err != nil {
			return failValidation(fmt.Errorf("invalid retry policy: %w", err))
		}
	}
	n := &runNode{
		id:        historyID,
		storeID:   scriptInvocationStoreID(parentID, call.Type, call.Options.ID),
		nt:        nt,
		meta:      meta,
		ports:     ports,
		cfg:       call.Config,
		retry:     retry,
		gating:    make(map[string]bool),
		optional:  make(map[string]bool),
		outEdges:  make(map[string][]edgeTarget),
		wired:     wired,
		params:    make(map[string][]edgeSource),
		instances: make(map[string]*nodeInstance),
	}
	inst := &nodeInstance{node: n, inputs: inputs, state: NodeStatePending}
	slot := c.slot
	if c.valueEval {
		slot = nil
	}
	child := &execCtx{
		run:            r,
		node:           n,
		inst:           inst,
		slot:           slot,
		params:         call.Options.Params,
		valueEval:      c.valueEval,
		directEmit:     true,
		scriptParentID: parentID,
	}

	attempts := 0
	result := r.executeWithRetry(inst, child, retryExecution{
		ctx:    ctx,
		policy: retry,
		pure:   c.valueEval,
		beforeAttempt: func(attempt int) {
			attempts = attempt
			c.markScriptNodeCallStarted()
			r.eng.emitNodeState(r, historyID, NodeStateRunning, attempt, false, "")
		},
		retrying: func(attempt int, err error) {
			r.eng.emitNodeState(r, historyID, NodeStateRetrying, attempt, false, err.Error())
		},
		failed: func(attempt int, err error) nodeResult {
			attempts = attempt
			r.markInvocationFailure()
			r.eng.emitNodeStateOutputs(r, historyID, NodeStateFailed, attempt, true, err.Error(), withEmissions(inst, nil))
			return nodeResult{inst: inst, status: NodeStateFailed, handled: true, err: err}
		},
		canceled: func(attempt int, err error) nodeResult {
			attempts = attempt
			if r.ctx.Err() != nil {
				return nodeResult{inst: inst, status: nodeCanceled, err: err}
			}
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			r.markInvocationFailure()
			r.eng.emitNodeStateOutputs(r, historyID, NodeStateFailed, attempt, true, err.Error(), withEmissions(inst, nil))
			return nodeResult{inst: inst, status: NodeStateFailed, handled: true, err: err}
		},
	})
	if result.status == nodeCanceled {
		err := result.err
		if err == nil {
			err = ctx.Err()
		}
		callErr := newScriptNodeCallError(call.Type, invocationID, sequence, attempts, err)
		callErr.Retryable = false
		return nil, callErr
	}
	if result.status != NodeStateSucceeded {
		return nil, newScriptNodeCallError(call.Type, invocationID, sequence, attempts, result.err)
	}

	reported := withEmissions(inst, result.outputs)
	outputs, err := boundedScriptCallOutputs(n, reported)
	if err != nil {
		r.markInvocationFailure()
		r.eng.emitNodeState(r, historyID, NodeStateFailed, attempts, true, err.Error())
		callErr := newScriptNodeCallError(call.Type, invocationID, sequence, attempts, err)
		callErr.Retryable = false
		return nil, callErr
	}
	r.eng.emitNodeStateOutputs(r, historyID, NodeStateSucceeded, attempts, false, "", outputs)
	return &nodeapi.NodeCallResult{
		NodeType:     call.Type,
		InvocationID: invocationID,
		Sequence:     sequence,
		Attempts:     attempts,
		Outputs:      outputs,
	}, nil
}

// scriptNodeType resolves an exact type through the current eligible catalog.
// It never substitutes another registration when a contributed type disappears.
func (c *execCtx) scriptNodeType(nodeType string) (NodeType, NodeMeta, bool) {
	eligible := false
	for _, fn := range scriptsdk.Functions(c.run.eng.reg.List()) {
		if fn.NodeType == nodeType {
			eligible = true
			break
		}
	}
	if !eligible {
		return nil, NodeMeta{}, false
	}
	nt, ok := c.run.eng.reg.Get(nodeType)
	if !ok {
		return nil, NodeMeta{}, false
	}
	meta := nt.Meta()
	return nt, meta, true
}

// newScriptNodeCallError preserves the execution failure's retry classification
// and details while attaching the invocation identity needed by script code.
func newScriptNodeCallError(nodeType, invocationID string, sequence, attempts int, err error) *nodeapi.NodeCallError {
	callErr := &nodeapi.NodeCallError{
		NodeType:     nodeType,
		InvocationID: invocationID,
		Sequence:     sequence,
		Attempts:     attempts,
		Cause:        err,
	}
	if attempts == 0 || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return callErr
	}
	var failure *ExecutionError
	if errors.As(err, &failure) {
		callErr.Retryable = failure.Retryable
		callErr.RetryAfterMS = failure.RetryAfterMS
		callErr.Details = failure.Details
	} else {
		callErr.Retryable = true
	}
	return callErr
}

// scriptCallInputs copies the input envelopes and restores binary payloads
// from their script base64 representation before the real node sees them.
func scriptCallInputs(ports flow.PortSet, inputs map[string]*Msg) (map[string]*Msg, error) {
	if len(inputs) == 0 {
		return inputs, nil
	}
	cloned := make(map[string]*Msg, len(inputs))
	for id, msg := range inputs {
		if msg == nil {
			cloned[id] = nil
			continue
		}
		cp := *msg
		port, ok := ports.Input(id)
		if ok && port.Type == flow.PortBinary {
			if encoded, ok := cp.Payload.(string); ok {
				decoded, err := base64.StdEncoding.DecodeString(encoded)
				if err != nil {
					return nil, fmt.Errorf("input %q payload must be standard base64 binary data: %w", id, err)
				}
				cp.Payload = decoded
			}
		}
		cloned[id] = &cp
	}
	return cloned, nil
}

// boundedScriptCallOutputs normalizes every declared ordinary output to an
// array, merges undeclared ordinary emissions, and enforces the data boundary
// before any result reaches the interpreter.
func boundedScriptCallOutputs(n *runNode, reported map[string][]*Msg) (map[string][]*Msg, error) {
	outputs := make(map[string][]*Msg, len(n.ports.Outputs)+len(reported))
	errorPorts := make(map[string]bool)
	for _, port := range n.ports.Outputs {
		if port.Kind == flow.PortKindError {
			errorPorts[port.ID] = true
			continue
		}
		outputs[port.ID] = make([]*Msg, 0)
	}
	ports := make([]string, 0, len(reported))
	for port := range reported {
		ports = append(ports, port)
	}
	sort.Strings(ports)
	messages := 0
	for _, port := range ports {
		if errorPorts[port] {
			continue
		}
		if _, ok := outputs[port]; !ok {
			outputs[port] = make([]*Msg, 0)
		}
		for _, msg := range reported[port] {
			if msg == nil {
				continue
			}
			messages++
			if messages > maxScriptCallMessages {
				return nil, fmt.Errorf("node call exceeded the %d-message result limit", maxScriptCallMessages)
			}
			outputs[port] = append(outputs[port], msg)
		}
	}
	encoded, err := json.Marshal(outputs)
	if err != nil {
		return nil, fmt.Errorf("node call result is not JSON-encodable: %w", err)
	}
	if len(encoded) > maxScriptCallJSON {
		return nil, fmt.Errorf("node call result is %d bytes of JSON; limit is %d", len(encoded), maxScriptCallJSON)
	}
	return outputs, nil
}

// scriptInvocationStoreID derives a stable reserved store identity from the
// parent graph node, exact type, and explicit invocation ID. Length prefixes
// make every tuple unambiguous; per-run sequence and history UUIDs do not enter
// durable state.
func scriptInvocationStoreID(parentNodeID, nodeType, invocationID string) string {
	h := sha256.New()
	for _, part := range []string{parentNodeID, nodeType, invocationID} {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(part))
	}
	return "script-node:" + hex.EncodeToString(h.Sum(nil))
}

var _ nodeapi.ScriptNodeInvoker = (*execCtx)(nil)
