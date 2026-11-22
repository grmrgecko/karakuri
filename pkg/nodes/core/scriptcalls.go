package core

import (
	"context"
	"errors"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/sandbox"
)

const maxScriptNodeCalls = 100

// scriptCallExecutionState is the engine's cross-attempt execution state. It is
// intentionally optional so standalone script tests and other ExecContext
// implementations retain their existing behavior.
type scriptCallExecutionState interface {
	ScriptNodeCallCount() int
	ScriptNodeCallStarted() bool
}

// scriptCallTracker enforces the per-execution admission budget and remembers
// execution even when the interpreter fails after a call returns.
type scriptCallTracker struct {
	invoker nodeapi.ScriptNodeInvoker
	shared  scriptCallExecutionState
	count   int
	began   bool
	fatal   *nodeapi.NodeCallError
}

// newScriptCallTracker resumes from admissions recorded by earlier outer attempts.
func newScriptCallTracker(ec engine.ExecContext) *scriptCallTracker {
	tracker := &scriptCallTracker{}
	tracker.invoker, _ = ec.(nodeapi.ScriptNodeInvoker)
	tracker.shared, _ = ec.(scriptCallExecutionState)
	if tracker.shared != nil {
		tracker.count = tracker.shared.ScriptNodeCallCount()
		tracker.began = tracker.shared.ScriptNodeCallStarted()
	}
	return tracker
}

// capability exposes the tracked invoker to each server-flow script runtime.
// Unsupported languages and contexts without an invoker receive no capability.
func (t *scriptCallTracker) capability(language string) nodeapi.ScriptNodeInvoker {
	if t.invoker == nil {
		return nil
	}
	switch language {
	case sandbox.LangJavaScript, sandbox.LangPython, sandbox.LangBash:
		return t
	default:
		return nil
	}
}

// ScriptNodeFunctions forwards the active eligible catalog.
func (t *scriptCallTracker) ScriptNodeFunctions() []nodeapi.NodeMeta {
	return t.invoker.ScriptNodeFunctions()
}

// CallScriptNode admits one call or records fatal budget exhaustion. No action
// is replayed or memoized across attempts.
func (t *scriptCallTracker) CallScriptNode(ctx context.Context, call nodeapi.NodeCall) (*nodeapi.NodeCallResult, error) {
	t.count++
	if t.fatal != nil {
		return nil, t.fatal
	}
	if t.count > maxScriptNodeCalls {
		t.fatal = &nodeapi.NodeCallError{
			NodeType: call.Type, InvocationID: call.Options.ID, Fatal: true,
			Cause: fmt.Errorf("script node call limit of %d exceeded", maxScriptNodeCalls),
		}
		return nil, t.fatal
	}
	result, err := t.invoker.CallScriptNode(ctx, call)
	if result != nil {
		t.began = true
	}
	var callErr *nodeapi.NodeCallError
	if errors.As(err, &callErr) && callErr.Attempts > 0 {
		t.began = true
	}
	if t.shared != nil && t.shared.ScriptNodeCallStarted() {
		t.began = true
	}
	return result, err
}

// started reports whether any call reached node execution.
func (t *scriptCallTracker) started() bool {
	return t.began || (t.shared != nil && t.shared.ScriptNodeCallStarted())
}

// fatalError returns the terminal admission failure, if one occurred.
func (t *scriptCallTracker) fatalError() *nodeapi.NodeCallError { return t.fatal }
