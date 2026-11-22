package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/dop251/goja"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
)

// installJSNodes installs the mediated server-flow capability. Callers without
// an invoker never receive a nodes global at all.
func installJSNodes(vm *goja.Runtime, ctx context.Context, invoker nodeapi.ScriptNodeInvoker) error {
	nodes := vm.NewObject()
	functions := scriptsdk.Functions(invoker.ScriptNodeFunctions())

	// Canonical exact-type properties are installed before aliases, so an alias
	// can never redirect another type's registered name.
	for _, fn := range functions {
		if err := nodes.Set(fn.Name, jsNodeCallHandler(vm, ctx, invoker, fn.NodeType, false)); err != nil {
			return err
		}
	}
	for _, fn := range functions {
		for _, alias := range fn.Aliases {
			existing := nodes.Get(alias)
			if alias == "call" || (existing != nil && !goja.IsUndefined(existing)) {
				continue
			}
			if err := nodes.Set(alias, jsNodeCallHandler(vm, ctx, invoker, fn.NodeType, false)); err != nil {
				return err
			}
		}
	}
	// The dynamic form is installed last and owns its reserved name even if a
	// contributed node attempts to register the type "call".
	if err := nodes.Set("call", jsNodeCallHandler(vm, ctx, invoker, "", true)); err != nil {
		return err
	}
	return vm.Set("nodes", nodes)
}

// jsNodeCallHandler builds one canonical, alias, or generic synchronous call.
func jsNodeCallHandler(vm *goja.Runtime, ctx context.Context, invoker nodeapi.ScriptNodeInvoker, nodeType string, generic bool) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		invocation, err := decodeJSNodeCall(vm, call, nodeType, generic)
		if err != nil {
			throwJSNodeCallError(vm, &nodeapi.NodeCallError{
				NodeType: invocation.Type, InvocationID: invocation.Options.ID, Cause: err,
			})
		}
		result, err := invoker.CallScriptNode(ctx, invocation)
		if err != nil {
			var callErr *nodeapi.NodeCallError
			if !errors.As(err, &callErr) {
				callErr = &nodeapi.NodeCallError{
					NodeType: invocation.Type, InvocationID: invocation.Options.ID, Cause: err,
				}
			}
			throwJSNodeCallError(vm, callErr)
		}
		if result == nil {
			throwJSNodeCallError(vm, &nodeapi.NodeCallError{
				NodeType: invocation.Type, InvocationID: invocation.Options.ID,
				Cause: fmt.Errorf("node call returned no result"),
			})
		}
		value, err := jsNodeCallResult(vm, result)
		if err != nil {
			throwJSNodeCallError(vm, &nodeapi.NodeCallError{
				NodeType: result.NodeType, InvocationID: result.InvocationID,
				Sequence: result.Sequence, Attempts: result.Attempts, Cause: err,
			})
		}
		return value
	}
}

// decodeJSNodeCall decodes the shared named and generic argument lists. Config
// is required; inputs and options may be omitted or undefined.
func decodeJSNodeCall(vm *goja.Runtime, call goja.FunctionCall, nodeType string, generic bool) (nodeapi.NodeCall, error) {
	invocation := nodeapi.NodeCall{Type: nodeType}
	configIndex := 0
	minArgs, maxArgs := 1, 3
	if generic {
		configIndex = 1
		minArgs, maxArgs = 2, 4
		if len(call.Arguments) > 0 {
			invocation.Type, _ = call.Argument(0).Export().(string)
		}
	}
	if len(call.Arguments) < minArgs || len(call.Arguments) > maxArgs {
		return invocation, fmt.Errorf("node call expects %d to %d arguments, got %d (config is required)", minArgs, maxArgs, len(call.Arguments))
	}
	if generic {
		nodeType, ok := call.Argument(0).Export().(string)
		if !ok {
			return invocation, fmt.Errorf("nodes.call node type must be a string")
		}
		invocation.Type = nodeType
	}
	if goja.IsUndefined(call.Argument(configIndex)) {
		return invocation, fmt.Errorf("node call config is required")
	}
	if err := jsDecode(vm, call.Argument(configIndex), &invocation.Config); err != nil {
		return invocation, fmt.Errorf("invalid node call config: %w", err)
	}
	if index := configIndex + 1; index < len(call.Arguments) && !goja.IsUndefined(call.Argument(index)) {
		if err := jsDecode(vm, call.Argument(index), &invocation.Inputs); err != nil {
			return invocation, fmt.Errorf("invalid node call inputs: %w", err)
		}
	}
	if index := configIndex + 2; index < len(call.Arguments) && !goja.IsUndefined(call.Argument(index)) {
		if err := decodeJSNodeCallOptions(vm, call.Argument(index), &invocation.Options); err != nil {
			return invocation, fmt.Errorf("invalid node call options: %w", err)
		}
	}
	return invocation, nil
}

// decodeJSNodeCallOptions rejects unknown keys before decoding into the Go
// contract, whose ordinary JSON decoder would otherwise discard them.
func decodeJSNodeCallOptions(vm *goja.Runtime, value goja.Value, options *nodeapi.NodeCallOptions) error {
	var fields map[string]json.RawMessage
	if err := jsDecode(vm, value, &fields); err != nil {
		return err
	}
	if err := rejectJSOptionKeys(fields, "id", "retry", "params"); err != nil {
		return err
	}
	if raw := fields["retry"]; len(raw) > 0 && string(raw) != "null" {
		var retry map[string]json.RawMessage
		if err := json.Unmarshal(raw, &retry); err != nil {
			return fmt.Errorf("retry must be an object: %w", err)
		}
		if err := rejectJSOptionKeys(retry, "max_attempts", "delay_ms", "backoff", "max_delay_ms", "jitter"); err != nil {
			return fmt.Errorf("retry: %w", err)
		}
	}
	return jsDecode(vm, value, options)
}

// rejectJSOptionKeys reports the first unknown member in stable order.
func rejectJSOptionKeys(fields map[string]json.RawMessage, allowed ...string) error {
	known := make(map[string]bool, len(allowed))
	for _, key := range allowed {
		known[key] = true
	}
	var unknown []string
	for key := range fields {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	return fmt.Errorf("unknown option %q", unknown[0])
}

// jsNodeCallResult converts the output map through JSON. Besides detaching the
// interpreter from host values, this gives []byte the same base64 form used by
// the rest of the sandbox boundary and preserves empty output arrays.
func jsNodeCallResult(vm *goja.Runtime, result *nodeapi.NodeCallResult) (goja.Value, error) {
	encoded, err := json.Marshal(result.Outputs)
	if err != nil {
		return nil, fmt.Errorf("node call result cannot cross the script boundary: %w", err)
	}
	var plain any
	if err := json.Unmarshal(encoded, &plain); err != nil {
		return nil, fmt.Errorf("node call result cannot be decoded for JavaScript: %w", err)
	}
	return vm.ToValue(plain), nil
}

// throwJSNodeCallError raises an Error object with stable, catchable fields.
func throwJSNodeCallError(vm *goja.Runtime, callErr *nodeapi.NodeCallError) {
	if callErr.Cause == nil {
		callErr.Cause = errors.New(callErr.Error())
	}
	object := vm.NewGoError(callErr)
	_ = object.Set("name", "NodeCallError")
	_ = object.Set("message", callErr.Error())
	_ = object.Set("nodeType", callErr.NodeType)
	_ = object.Set("invocationId", callErr.InvocationID)
	_ = object.Set("sequence", callErr.Sequence)
	_ = object.Set("attempts", callErr.Attempts)
	_ = object.Set("retryable", callErr.Retryable)
	_ = object.Set("fatal", callErr.Fatal)
	if callErr.RetryAfterMS > 0 {
		_ = object.Set("retryAfterMs", callErr.RetryAfterMS)
	}
	if len(callErr.Details) > 0 {
		_ = object.Set("details", callErr.Details)
	}
	panic(vm.ToValue(object))
}
