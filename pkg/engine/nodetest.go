package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// NodeTestResult contains a standalone execution; its state is never persisted.
type NodeTestResult struct {
	Type          string                       `json:"type"`
	Status        string                       `json:"status"`
	Error         string                       `json:"error,omitempty"`
	DurationMS    int64                        `json:"duration_ms"`
	Outputs       map[string][]json.RawMessage `json:"outputs"`
	Events        []json.RawMessage            `json:"events"`
	Truncated     bool                         `json:"truncated"`
	CaptureErrors []string                     `json:"capture_errors,omitempty"`
}

// TestNode runs a single-node graph with fresh stores and the server's connections.
// It neither persists history nor publishes events to document subscribers.
func (e *Engine) TestNode(ctx context.Context, opts RunOptions) (*NodeTestResult, error) {
	if opts.Graph == nil || len(opts.Graph.Nodes) != 1 || len(opts.Graph.Edges) != 0 || len(opts.Graph.Subgraphs) != 0 {
		return nil, fmt.Errorf("a standalone node test requires one node and no edges or subgraphs")
	}
	if e.baseCtx.Err() != nil {
		return nil, ErrStopped
	}
	node := opts.Graph.Nodes[0]
	nt, ok := e.reg.Get(node.Type)
	if !ok {
		return nil, fmt.Errorf("unknown node type %q", node.Type)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	stopParent := context.AfterFunc(e.baseCtx, cancel)
	defer stopParent()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	capture := &nodeTestCapture{outputs: map[string][]json.RawMessage{}, events: []json.RawMessage{}}
	registry := NewRegistry()
	for _, meta := range e.reg.List() {
		registered, ok := e.reg.Get(meta.Type)
		if !ok {
			continue
		}
		if meta.Type == node.Type {
			registered = &testNode{NodeType: nt, capture: capture}
		}
		registry.Register(registered)
	}
	isolated := New(Options{Registry: registry, Clock: e.clock, Logger: e.log,
		ConnResolver: e.connResolver, Publisher: capture, MaxParallelNodes: 1})
	stopRequest := context.AfterFunc(ctx, func() { isolated.baseCancel(nil) })
	defer stopRequest()
	defer isolated.Stop()
	// A standalone execution has no database identity or parent error flow.
	opts.FlowID, opts.FlowName, opts.FlowSlug = uuid.New(), "Node test", "node-test"
	opts.FlowVersionID, opts.ErrorFlowID, opts.IsErrorHandler = 0, nil, false
	started := time.Now()
	handle, err := isolated.RunFlow(opts)
	if err != nil {
		return nil, err
	}
	select {
	case <-handle.Done():
	case <-ctx.Done():
		isolated.Stop()
	}
	snapshot := handle.Snapshot()
	result := capture.finish()
	result.Type, result.Status, result.Error = node.Type, snapshot.Status, snapshot.Error
	result.DurationMS = time.Since(started).Milliseconds()
	if snapshot.Status == RunStatusRunning {
		result.Status, result.Error = "canceling", "node has not stopped after cancellation"
	} else if snapshot.Status == RunStatusCanceled && ctx.Err() != nil {
		result.Error = ctx.Err().Error()
	}
	return result, nil
}

// nodeTestCapture bounds the combined outputs and events to 1 MiB and 1000 items.
// Captured values are serialized immediately because nodes may reuse messages.
type nodeTestCapture struct {
	mu        sync.Mutex
	outputs   map[string][]json.RawMessage
	events    []json.RawMessage
	errors    []string
	bytes     int
	items     int
	truncated bool
	closed    bool
}

func (c *nodeTestCapture) encode(value any) json.RawMessage {
	if c.closed {
		return nil
	}
	if c.items >= 1000 || c.bytes >= 1<<20 {
		c.truncated = true
		return nil
	}
	c.items++
	raw, err := json.Marshal(value)
	if err != nil {
		if len(c.errors) < 10 {
			c.errors = append(c.errors, err.Error())
		}
		return nil
	}
	if len(raw)+c.bytes > 1<<20 {
		c.truncated = true
		return nil
	}
	c.bytes += len(raw)
	return raw
}

func (c *nodeTestCapture) output(port string, msg *Msg) {
	if msg == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if raw := c.encode(msg); raw != nil {
		c.outputs[port] = append(c.outputs[port], raw)
	}
}

// Publish captures node transitions and debug events without broadcasting them.
func (c *nodeTestCapture) Publish(_ string, event string, payload any) {
	if event != "node_state" && event != "debug" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if raw := c.encode(payload); raw != nil {
		c.events = append(c.events, raw)
	}
}

func (c *nodeTestCapture) finish() *NodeTestResult {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	return &NodeTestResult{Outputs: c.outputs, Events: c.events, Truncated: c.truncated, CaptureErrors: c.errors}
}

// testNode records outputs while keeping scheduling and retries in the engine.
type testNode struct {
	NodeType
	capture *nodeTestCapture
}

func (n *testNode) Execute(ctx context.Context, ec ExecContext, inputs map[string]*Msg) (map[string][]*Msg, error) {
	outputs, err := n.NodeType.Execute(ctx, &testExecContext{ExecContext: ec, capture: n.capture}, inputs)
	if err == nil {
		// Sort ports so truncation retains the same subset on repeated tests.
		ports := make([]string, 0, len(outputs))
		for port := range outputs {
			ports = append(ports, port)
		}
		sort.Strings(ports)
		for _, port := range ports {
			for _, msg := range outputs[port] {
				n.capture.output(port, msg)
			}
		}
	}
	return outputs, err
}

type testExecContext struct {
	ExecContext
	capture *nodeTestCapture
}

// ScriptNodeFunctions forwards the full cloned catalog to an isolated script.
func (c *testExecContext) ScriptNodeFunctions() []NodeMeta {
	if invoker, ok := c.ExecContext.(nodeapi.ScriptNodeInvoker); ok {
		return invoker.ScriptNodeFunctions()
	}
	return nil
}

// CallScriptNode forwards invocation into the isolated run and its worker slot.
func (c *testExecContext) CallScriptNode(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
	if invoker, ok := c.ExecContext.(nodeapi.ScriptNodeInvoker); ok {
		return invoker.CallScriptNode(ctx, call)
	}
	return nil, fmt.Errorf("script node calls are not available")
}

// ScriptNodeCallCount preserves the call budget across outer script attempts.
func (c *testExecContext) ScriptNodeCallCount() int {
	if state, ok := c.ExecContext.(interface{ ScriptNodeCallCount() int }); ok {
		return state.ScriptNodeCallCount()
	}
	return 0
}

// ScriptNodeCallStarted preserves outer-retry suppression through the capture wrapper.
func (c *testExecContext) ScriptNodeCallStarted() bool {
	if state, ok := c.ExecContext.(interface{ ScriptNodeCallStarted() bool }); ok {
		return state.ScriptNodeCallStarted()
	}
	return false
}

// FlowID preserves the isolated engine's identity for server-scoped storage.
func (c *testExecContext) FlowID() string {
	if identity, ok := c.ExecContext.(FlowIdentity); ok {
		return identity.FlowID()
	}
	return ""
}

// Wired observes every declared output so nodes include optional output data.
func (c *testExecContext) Wired(port string) bool {
	_, ok := c.Ports().Output(port)
	return ok
}

// Emit captures messages emitted before execution returns, including failed attempts.
func (c *testExecContext) Emit(port string, msg *Msg) {
	c.capture.output(port, msg)
	c.ExecContext.Emit(port, msg)
}

var _ Publisher = (*nodeTestCapture)(nil)
var _ NodeType = (*testNode)(nil)
var _ ExecContext = (*testExecContext)(nil)

// RefreshParameter forwards explicit fresh reads through standalone capture.
func (c *testExecContext) RefreshParameter(ctx context.Context, field string) (any, bool, error) {
	if refresher, ok := c.ExecContext.(ParameterRefresher); ok {
		return refresher.RefreshParameter(ctx, field)
	}
	return nil, false, nil
}

// ParkUntil preserves signal-aware parking through standalone capture.
func (c *testExecContext) ParkUntil(ctx context.Context, d time.Duration, signal <-chan struct{}) error {
	if parker, ok := c.ExecContext.(SignalParker); ok {
		return parker.ParkUntil(ctx, d, signal)
	}
	return c.ExecContext.Park(ctx, d)
}
