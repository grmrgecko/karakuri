package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
)

// pythonNodeDefinitions is staged for the guest so item access remains limited
// to exact types and attribute access remains limited to advertised aliases.
type pythonNodeDefinitions struct {
	Types   map[string]string `json:"types"`
	Aliases map[string]string `json:"aliases"`
}

// pythonNodeDefinitionsJSON projects the same catalog used by the JavaScript
// binding. Both names in each mapping entry point to the exact registered type.
func pythonNodeDefinitionsJSON(metas []nodeapi.NodeMeta) (string, error) {
	definitions := pythonNodeDefinitions{
		Types:   map[string]string{},
		Aliases: map[string]string{},
	}
	for _, fn := range scriptsdk.Functions(metas) {
		definitions.Types[fn.Name] = fn.NodeType
		for _, alias := range fn.Aliases {
			definitions.Aliases[alias] = fn.NodeType
		}
	}
	encoded, err := json.Marshal(definitions)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// pythonNodeRequest keeps the JSON fields raw until their object shapes have
// been checked. Decoding directly into NodeCall would silently ignore unknown
// invocation and retry options.
type pythonNodeRequest struct {
	Type    string          `json:"type"`
	Config  json.RawMessage `json:"config"`
	Inputs  json.RawMessage `json:"inputs"`
	Options json.RawMessage `json:"options"`
}

// decodePythonNodeCall validates the transport before the tracker admits a
// call. The guest repeats these checks to provide immediate Python exceptions,
// but the host does not rely on guest code for that boundary.
func decodePythonNodeCall(data []byte) (nodeapi.NodeCall, error) {
	var request pythonNodeRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return nodeapi.NodeCall{}, fmt.Errorf("invalid node call request: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nodeapi.NodeCall{}, fmt.Errorf("invalid node call request: multiple JSON values")
		}
		return nodeapi.NodeCall{}, fmt.Errorf("invalid node call request: %w", err)
	}

	invocation := nodeapi.NodeCall{Type: request.Type, Config: request.Config}
	if invocation.Type == "" {
		return invocation, fmt.Errorf("node call type is required")
	}
	if len(invocation.Config) == 0 {
		return invocation, fmt.Errorf("node call config is required")
	}
	var config map[string]json.RawMessage
	if err := json.Unmarshal(invocation.Config, &config); err != nil || config == nil {
		if err == nil {
			err = errors.New("null is not an object")
		}
		return invocation, fmt.Errorf("node call config must be an object: %w", err)
	}

	if len(request.Inputs) > 0 && !bytes.Equal(bytes.TrimSpace(request.Inputs), []byte("null")) {
		if err := json.Unmarshal(request.Inputs, &invocation.Inputs); err != nil {
			return invocation, fmt.Errorf("node call inputs must be an object of complete messages: %w", err)
		}
	}
	options, err := decodePythonNodeOptions(request.Options)
	invocation.Options = options
	if err != nil {
		return invocation, err
	}
	return invocation, nil
}

// decodePythonNodeOptions rejects unknown members before decoding the shared
// options contract, including members nested under retry.
func decodePythonNodeOptions(raw json.RawMessage) (nodeapi.NodeCallOptions, error) {
	var options nodeapi.NodeCallOptions
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return options, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
		if err == nil {
			err = errors.New("null is not an object")
		}
		return options, fmt.Errorf("node call options must be an object: %w", err)
	}
	if err := rejectPythonNodeOptionKeys(fields, "id", "retry", "params"); err != nil {
		return options, err
	}
	if retryRaw := fields["retry"]; len(retryRaw) > 0 && !bytes.Equal(bytes.TrimSpace(retryRaw), []byte("null")) {
		var retry map[string]json.RawMessage
		if err := json.Unmarshal(retryRaw, &retry); err != nil || retry == nil {
			if err == nil {
				err = errors.New("null is not an object")
			}
			return options, fmt.Errorf("node call retry options must be an object: %w", err)
		}
		if err := rejectPythonNodeOptionKeys(retry, "max_attempts", "delay_ms", "backoff", "max_delay_ms", "jitter"); err != nil {
			return options, fmt.Errorf("retry: %w", err)
		}
	}
	if err := json.Unmarshal(raw, &options); err != nil {
		return options, fmt.Errorf("invalid node call options: %w", err)
	}
	return options, nil
}

// rejectPythonNodeOptionKeys reports the first unknown member in stable order.
func rejectPythonNodeOptionKeys(fields map[string]json.RawMessage, allowed ...string) error {
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

// pythonNodeResponse carries either plain output messages or a structured
// failure. Message is explicit because NodeCallError.Cause does not serialize.
type pythonNodeResponse struct {
	Outputs map[string][]*nodeapi.Msg `json:"outputs,omitempty"`
	Error   *pythonNodeError          `json:"error,omitempty"`
}

// pythonNodeError is the Python-facing form of nodeapi.NodeCallError.
type pythonNodeError struct {
	Message      string         `json:"message"`
	NodeType     string         `json:"node_type"`
	InvocationID string         `json:"invocation_id"`
	Sequence     int            `json:"sequence"`
	Attempts     int            `json:"attempts"`
	Retryable    bool           `json:"retryable"`
	RetryAfterMS int64          `json:"retry_after_ms,omitempty"`
	Details      map[string]any `json:"details,omitempty"`
	Fatal        bool           `json:"fatal"`
}

// newPythonNodeError preserves classified engine failures and wraps ordinary
// transport or invocation errors with the requested call identity.
func newPythonNodeError(invocation nodeapi.NodeCall, err error) *pythonNodeError {
	callErr := &nodeapi.NodeCallError{
		NodeType: invocation.Type, InvocationID: invocation.Options.ID, Cause: err,
	}
	var classified *nodeapi.NodeCallError
	if errors.As(err, &classified) {
		callErr = classified
	}
	return &pythonNodeError{
		Message: callErr.Error(), NodeType: callErr.NodeType,
		InvocationID: callErr.InvocationID, Sequence: callErr.Sequence,
		Attempts: callErr.Attempts, Retryable: callErr.Retryable,
		RetryAfterMS: callErr.RetryAfterMS, Details: callErr.Details, Fatal: callErr.Fatal,
	}
}

// performNode decodes and validates one staged request before admitting it to
// the per-run invoker, then stages outputs or a catchable error for the guest.
func (r *rpcFS) performNode() (fs.File, error) {
	staged, err := os.ReadFile(filepath.Join(r.scratch, "node_req.json"))
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: "node", Err: fs.ErrInvalid}
	}
	invocation, decodeErr := decodePythonNodeCall(staged)
	if decodeErr != nil {
		return r.writePythonNodeResponse(pythonNodeResponse{
			Error: newPythonNodeError(invocation, decodeErr),
		})
	}
	if r.invoker == nil {
		return nil, &fs.PathError{Op: "open", Path: "node", Err: fs.ErrNotExist}
	}

	result, callErr := r.invoker.CallScriptNode(r.ctx, invocation)
	if callErr != nil {
		return r.writePythonNodeResponse(pythonNodeResponse{
			Error: newPythonNodeError(invocation, callErr),
		})
	}
	if result == nil {
		return r.writePythonNodeResponse(pythonNodeResponse{
			Error: newPythonNodeError(invocation, errors.New("node call returned no result")),
		})
	}

	outputs := make(map[string][]*nodeapi.Msg, len(result.Outputs))
	for port, messages := range result.Outputs {
		if messages == nil {
			messages = []*nodeapi.Msg{}
		}
		outputs[port] = messages
	}
	return r.writePythonNodeResponse(pythonNodeResponse{Outputs: outputs})
}

// writePythonNodeResponse finishes the synchronous file-open RPC. A value that
// cannot cross the JSON boundary becomes a structured call failure instead.
func (r *rpcFS) writePythonNodeResponse(response pythonNodeResponse) (fs.File, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		encoded, err = json.Marshal(pythonNodeResponse{Error: &pythonNodeError{
			Message: fmt.Sprintf("node call response cannot cross the script boundary: %v", err),
		}})
		if err != nil {
			return nil, &fs.PathError{Op: "open", Path: "node", Err: fs.ErrInvalid}
		}
	}
	if err := os.WriteFile(filepath.Join(r.scratch, "node_res.json"), encoded, 0o600); err != nil {
		return nil, &fs.PathError{Op: "open", Path: "node", Err: fs.ErrPermission}
	}
	return &rpcFile{name: "node", data: []byte("ok")}, nil
}
