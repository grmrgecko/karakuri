package nodeapi

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/flow"
)

// NodeCall describes one synchronous node invocation from a script. Config uses
// the same stored field IDs and JSON shapes as a graph node.
type NodeCall struct {
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	Inputs  map[string]*Msg `json:"inputs,omitempty"`
	Options NodeCallOptions `json:"options,omitempty"`
}

// NodeCallOptions controls invocation rather than node behavior. ID is required
// for stateful and unknown-state nodes. Params supplies values that a graph
// would pull through value edges.
type NodeCallOptions struct {
	ID     string            `json:"id,omitempty"`
	Retry  *flow.RetryPolicy `json:"retry,omitempty"`
	Params map[string]any    `json:"params,omitempty"`
}

// NodeCallResult is one completed invocation. Outputs contains every ordinary
// output port, including ports with no messages. Sequence is one-based within
// the parent script execution and remains stable across attempts.
type NodeCallResult struct {
	NodeType     string            `json:"node_type"`
	InvocationID string            `json:"invocation_id"`
	Sequence     int               `json:"sequence"`
	Attempts     int               `json:"attempts"`
	Outputs      map[string][]*Msg `json:"outputs"`
}

// NodeCallError is a contained invocation failure. Fatal errors, such as a
// root script's exhausted call budget, fail the script even when JavaScript
// catches the error. Validation failures have zero attempts.
type NodeCallError struct {
	NodeType     string         `json:"node_type"`
	InvocationID string         `json:"invocation_id"`
	Sequence     int            `json:"sequence"`
	Attempts     int            `json:"attempts"`
	Retryable    bool           `json:"retryable"`
	RetryAfterMS int64          `json:"retry_after_ms,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
	Fatal        bool           `json:"fatal,omitempty"`
	Cause        error          `json:"-"`
}

// Error returns the underlying failure without discarding its context.
func (e *NodeCallError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	if e.NodeType != "" {
		return fmt.Sprintf("node call %q failed", e.NodeType)
	}
	return "node call failed"
}

// Unwrap retains the underlying execution or validation error.
func (e *NodeCallError) Unwrap() error { return e.Cause }

// ScriptNodeInvoker is the optional engine capability exposed to a server flow
// script. Other sandbox users do not receive it. Implementations return the
// current catalog for each script execution so registry changes take effect.
type ScriptNodeInvoker interface {
	ScriptNodeFunctions() []NodeMeta
	CallScriptNode(ctx context.Context, call NodeCall) (*NodeCallResult, error)
}
