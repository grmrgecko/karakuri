package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// PreviewNode runs a graph with fresh state and captures one node's outputs.
// Connections and tool branches remain live; history and document events are excluded.
func (e *Engine) PreviewNode(ctx context.Context, opts RunOptions, nodeID string, nt NodeType, output func(string, *Msg)) error {
	if e.baseCtx.Err() != nil {
		return ErrStopped
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stopParent := context.AfterFunc(e.baseCtx, cancel)
	defer stopParent()
	registry := NewRegistry()
	for _, meta := range e.reg.List() {
		registered, ok := e.reg.Get(meta.Type)
		if !ok {
			continue
		}
		if meta.Type == nt.Meta().Type {
			registered = &previewNode{NodeType: registered, target: nodeID, preview: nt, output: output}
		}
		registry.Register(registered)
	}
	isolated := New(Options{Registry: registry, Clock: e.clock, Logger: e.log,
		ConnResolver: e.connResolver, SubflowResolver: e.subflowResolver, MaxParallelNodes: e.maxParallel})
	stopRequest := context.AfterFunc(ctx, func() { isolated.baseCancel(nil) })
	defer stopRequest()
	defer isolated.Stop()
	if err := ctx.Err(); err != nil {
		return err
	}
	opts.FlowID, opts.FlowName, opts.FlowSlug = uuid.New(), "Node chat", "node-chat"
	opts.FlowVersionID, opts.ErrorFlowID, opts.IsErrorHandler = 0, nil, false
	handle, err := isolated.RunFlow(opts)
	if err != nil {
		return err
	}
	select {
	case <-handle.Done():
	case <-ctx.Done():
		isolated.Stop()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := handle.Snapshot()
	if snapshot.Status != RunStatusSucceeded {
		return fmt.Errorf("node chat: %s", snapshot.Error)
	}
	return nil
}

type previewNode struct {
	NodeType
	target  string
	preview NodeType
	output  func(string, *Msg)
}

func (n *previewNode) Execute(ctx context.Context, ec ExecContext, inputs map[string]*Msg) (map[string][]*Msg, error) {
	if ec.NodeID() != n.target {
		return n.NodeType.Execute(ctx, ec, inputs)
	}
	outputs, err := n.preview.Execute(ctx, &previewContext{ExecContext: ec, output: n.output}, inputs)
	if err == nil {
		for port, messages := range outputs {
			for _, msg := range messages {
				n.output(port, msg)
			}
		}
	}
	return outputs, err
}

type previewContext struct {
	ExecContext
	output func(string, *Msg)
}

// Wired enables optional response envelopes without sending them downstream.
func (c *previewContext) Wired(port string) bool {
	_, ok := c.Ports().Output(port)
	return ok
}

// Emit forwards streaming replies to the preview client.
func (c *previewContext) Emit(port string, msg *Msg) {
	c.output(port, msg)
	c.ExecContext.Emit(port, msg)
}

// RefreshParameter preserves fresh value reads in preview executions.
func (c *previewContext) RefreshParameter(ctx context.Context, field string) (any, bool, error) {
	if refresher, ok := c.ExecContext.(ParameterRefresher); ok {
		return refresher.RefreshParameter(ctx, field)
	}
	return nil, false, nil
}

// ParkUntil preserves signal-aware parking in preview executions.
func (c *previewContext) ParkUntil(ctx context.Context, d time.Duration, signal <-chan struct{}) error {
	if parker, ok := c.ExecContext.(SignalParker); ok {
		return parker.ParkUntil(ctx, d, signal)
	}
	return c.ExecContext.Park(ctx, d)
}
