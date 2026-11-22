package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"unicode"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

// shellNodeBridge owns the node-call state for one shell execution. mvdan runs
// pipeline commands concurrently, so invocation and output have separate locks
// and downstream handlers drain stdin before invoking.
type shellNodeBridge struct {
	invoker nodeapi.ScriptNodeInvoker
	aliases map[string]string
	mu      sync.Mutex

	outputMu sync.Mutex
}

// newShellNodeBridge derives aliases once per execution. Exact types need no
// map entry because unresolved names are forwarded unchanged.
func newShellNodeBridge(invoker nodeapi.ScriptNodeInvoker) *shellNodeBridge {
	if invoker == nil {
		return nil
	}
	bridge := &shellNodeBridge{invoker: invoker, aliases: map[string]string{}}
	functions := scriptsdk.Functions(invoker.ScriptNodeFunctions())
	exact := make(map[string]bool, len(functions))
	for _, fn := range functions {
		exact[fn.Name] = true
	}
	for _, fn := range functions {
		for _, alias := range fn.Aliases {
			if alias == "call" || exact[alias] {
				continue
			}
			bridge.aliases[alias] = fn.NodeType
		}
	}
	return bridge
}

// call implements node <name> and node call <exact-type>. Pipeline participation
// is rejected during preflight, so top-level stdin remains available to the
// script's other commands while invocation and output stay serialized.
func (b *shellNodeBridge) call(ctx context.Context, hc interp.HandlerContext, args []string) error {
	invocation, err := decodeShellNodeCall(args, b.aliases)
	if err != nil {
		return b.fail(hc, &nodeapi.NodeCallError{
			NodeType: invocation.Type, InvocationID: invocation.Options.ID, Cause: err,
		})
	}
	b.mu.Lock()
	result, err := b.invoker.CallScriptNode(ctx, invocation)
	b.mu.Unlock()
	if err != nil {
		var callErr *nodeapi.NodeCallError
		if !errors.As(err, &callErr) {
			callErr = &nodeapi.NodeCallError{
				NodeType: invocation.Type, InvocationID: invocation.Options.ID, Cause: err,
			}
		}
		return b.fail(hc, callErr)
	}
	if result == nil {
		return b.fail(hc, &nodeapi.NodeCallError{
			NodeType: invocation.Type, InvocationID: invocation.Options.ID,
			Cause: errors.New("node call returned no result"),
		})
	}
	outputs := make(map[string][]*nodeapi.Msg, len(result.Outputs))
	for port, messages := range result.Outputs {
		if messages == nil {
			messages = []*nodeapi.Msg{}
		}
		outputs[port] = messages
	}
	encoded, err := json.Marshal(outputs)
	if err != nil {
		return b.fail(hc, &nodeapi.NodeCallError{
			NodeType: result.NodeType, InvocationID: result.InvocationID,
			Sequence: result.Sequence, Attempts: result.Attempts,
			Cause: fmt.Errorf("node call result cannot cross the script boundary: %w", err),
		})
	}
	b.outputMu.Lock()
	defer b.outputMu.Unlock()
	if err := writeShellNodeJSON(hc, encoded); err != nil {
		return errors.Join(
			&nodeapi.NodeCallError{
				NodeType: result.NodeType, InvocationID: result.InvocationID,
				Sequence: result.Sequence, Attempts: result.Attempts,
				Cause: fmt.Errorf("writing node call result: %w", err),
			},
			interp.ExitStatus(1),
		)
	}
	return nil
}

// decodeShellNodeCall validates the complete JSON arguments before the engine
// sees an invocation. Named calls resolve aliases; the generic form never does.
func decodeShellNodeCall(args []string, aliases map[string]string) (nodeapi.NodeCall, error) {
	invocation := nodeapi.NodeCall{}
	generic := len(args) > 0 && args[0] == "call"
	configIndex := 1
	minArgs, maxArgs := 2, 4
	if generic {
		configIndex = 2
		minArgs, maxArgs = 3, 5
		if len(args) > 1 {
			invocation.Type = args[1]
		}
	} else if len(args) > 0 {
		invocation.Type = args[0]
		if exact, ok := aliases[invocation.Type]; ok {
			invocation.Type = exact
		}
	}
	if len(args) < minArgs || len(args) > maxArgs {
		return invocation, fmt.Errorf("node call expects %d to %d arguments, got %d (config is required)", minArgs, maxArgs, len(args))
	}

	var config map[string]json.RawMessage
	if err := decodeShellNodeJSON(args[configIndex], &config, false); err != nil {
		return invocation, fmt.Errorf("invalid node call config: %w", err)
	}
	if config == nil {
		return invocation, errors.New("invalid node call config: config must be a JSON object, not null")
	}
	invocation.Config = json.RawMessage(args[configIndex])

	if index := configIndex + 1; index < len(args) {
		if err := decodeShellNodeJSON(args[index], &invocation.Inputs, true); err != nil {
			return invocation, fmt.Errorf("invalid node call inputs: %w", err)
		}
		if invocation.Inputs == nil {
			return invocation, errors.New("invalid node call inputs: inputs must be a JSON object, not null")
		}
	}
	if index := configIndex + 2; index < len(args) {
		var fields map[string]json.RawMessage
		if err := decodeShellNodeJSON(args[index], &fields, false); err != nil {
			return invocation, fmt.Errorf("invalid node call options: %w", err)
		}
		if fields == nil {
			return invocation, errors.New("invalid node call options: options must be a JSON object, not null")
		}
		if err := decodeShellNodeJSON(args[index], &invocation.Options, true); err != nil {
			return invocation, fmt.Errorf("invalid node call options: %w", err)
		}
	}
	return invocation, nil
}

// decodeShellNodeJSON accepts exactly one JSON value. disallowUnknown applies to
// typed message and option envelopes while config and params retain catalog IDs.
func decodeShellNodeJSON(source string, target any, disallowUnknown bool) error {
	decoder := json.NewDecoder(strings.NewReader(source))
	if disallowUnknown {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

// shellNodeErrorDocument is the stable Bash error wire shape. Fatal remains
// visible when false, unlike nodeapi's Go-facing omitempty field.
type shellNodeErrorDocument struct {
	Name         string         `json:"name"`
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

// fail emits one catchable error document and preserves both the classified
// cause and an exit status for if, command substitution, and pipefail.
func (b *shellNodeBridge) fail(hc interp.HandlerContext, callErr *nodeapi.NodeCallError) error {
	if callErr.Cause == nil {
		callErr.Cause = errors.New(callErr.Error())
	}
	document := shellNodeErrorDocument{
		Name: "NodeCallError", Message: callErr.Error(), NodeType: callErr.NodeType,
		InvocationID: callErr.InvocationID, Sequence: callErr.Sequence, Attempts: callErr.Attempts,
		Retryable: callErr.Retryable, RetryAfterMS: callErr.RetryAfterMS,
		Details: callErr.Details, Fatal: callErr.Fatal,
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return errors.Join(callErr, err, interp.ExitStatus(1))
	}
	b.outputMu.Lock()
	defer b.outputMu.Unlock()
	writeErr := writeShellNodeJSON(hc, encoded)
	return errors.Join(callErr, writeErr, interp.ExitStatus(1))
}

// writeShellNodeJSON writes one complete document before the handler returns.
func writeShellNodeJSON(hc interp.HandlerContext, encoded []byte) error {
	encoded = append(encoded, '\n')
	_, err := hc.Stdout.Write(encoded)
	return err
}

// shellCommandNameSafe limits command words to spellings the preflight can
// compare literally. Backslashes, expansions, globs, quoting operators, and
// whitespace can otherwise change the builtin reached after evaluation.
func shellCommandNameSafe(name string) bool {
	if name == "" {
		return false
	}
	for _, char := range name {
		if unicode.IsLetter(char) || unicode.IsDigit(char) {
			continue
		}
		switch char {
		case '_', '-', '.', '/', '+', ':':
			continue
		default:
			return false
		}
	}
	return true
}

// rejectBackgroundShellNodeCalls rejects execution shapes that can conceal a
// call which outlives the script or deadlocks its output. Computed command names
// and alias/eval mechanisms are refused because the original AST cannot prove
// which builtin they will reach.
func rejectBackgroundShellNodeCalls(file *syntax.File) error {
	functions := map[string]*syntax.Stmt{}
	forbidden := ""
	syntax.Walk(file, func(node syntax.Node) bool {
		if declaration, ok := node.(*syntax.FuncDecl); ok && declaration.Name != nil {
			name := declaration.Name.Value
			if functions[name] != nil {
				forbidden = "function redefinition"
				return false
			}
			functions[name] = declaration.Body
		}
		return true
	})
	if forbidden != "" {
		return errors.New("node calls do not allow function redefinition because it makes pipeline resolution ambiguous")
	}

	syntax.Walk(file, func(node syntax.Node) bool {
		if forbidden != "" || node == nil {
			return forbidden == ""
		}
		if call, ok := node.(*syntax.CallExpr); ok && len(call.Args) > 0 {
			name := call.Args[0].Lit()
			if !shellCommandNameSafe(name) {
				forbidden = "computed or escaped command names"
				return false
			}
			if name == "eval" || name == "trap" || name == "alias" || name == "shopt" ||
				name == "command" || name == "builtin" || name == "exec" {
				forbidden = name
				return false
			}
		}
		switch node := node.(type) {
		case *syntax.Stmt:
			if node.Background || node.Disown {
				forbidden = "background commands"
				return false
			}
		case *syntax.ProcSubst, *syntax.CoprocClause:
			forbidden = "asynchronous commands"
			return false
		case *syntax.BinaryCmd:
			if (node.Op == syntax.Pipe || node.Op == syntax.PipeAll) && shellTreeCallsNode(node, functions, map[string]bool{}) {
				forbidden = "pipeline node calls"
				return false
			}
		}
		return true
	})
	if forbidden != "" {
		return fmt.Errorf("node calls do not allow %s because they can conceal background execution or block output", forbidden)
	}
	return nil
}

// shellTreeCallsNode recognizes direct calls and calls through declared shell
// functions. Computed commands, eval, and traps are rejected conservatively
// because the interpreter offers no later background check.
func shellTreeCallsNode(root syntax.Node, functions map[string]*syntax.Stmt, visiting map[string]bool) bool {
	found := false
	syntax.Walk(root, func(node syntax.Node) bool {
		if found || node == nil {
			return !found
		}
		if _, ok := node.(*syntax.FuncDecl); ok {
			return false
		}
		call, ok := node.(*syntax.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		name := call.Args[0].Lit()
		if name == "" || name == "node" || name == "eval" || name == "trap" {
			found = true
			return false
		}
		if (name == "command" || name == "builtin" || name == "exec") && len(call.Args) > 1 && call.Args[1].Lit() == "node" {
			found = true
			return false
		}
		body := functions[name]
		if body != nil && !visiting[name] {
			visiting[name] = true
			found = shellTreeCallsNode(body, functions, visiting)
			delete(visiting, name)
			return !found
		}
		return true
	})
	return found
}
