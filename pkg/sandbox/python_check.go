package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"github.com/grmrgecko/karakuri/pkg/scriptsdk"
)

// ErrPythonVerificationUnavailable reports that CPython cannot parse source
// because its runtime is not installed or cannot be initialized.
var ErrPythonVerificationUnavailable = errors.New("python verification unavailable")

// pythonCheckProgram treats the submitted source as the payload string. It only
// parses that string into CPython AST nodes and extracts bounded literal data.
const pythonCheckProgram = `
import ast
import math

_MAX_CALLS = 1000
_MAX_LITERAL_DEPTH = 64
_literal_budget = [8192]


def _range(node):
    line = getattr(node, "lineno", None) or 1
    column = getattr(node, "col_offset", None) or 0
    return {
        "line": line,
        "column": column,
        "end_line": getattr(node, "end_lineno", None) or line,
        "end_column": getattr(node, "end_col_offset", None)
            if getattr(node, "end_col_offset", None) is not None else column,
    }


def _number(value):
    if type(value) not in (int, float):
        return None
    try:
        if not math.isfinite(float(value)):
            return None
    except (OverflowError, ValueError):
        return None
    return {"kind": "value", "value": value}


def _literal(node, depth=0):
    _literal_budget[0] -= 1
    if _literal_budget[0] < 0 or depth > _MAX_LITERAL_DEPTH:
        return {"kind": "dynamic"}
    if isinstance(node, ast.Constant):
        value = node.value
        if value is None or type(value) in (str, bool):
            return {"kind": "value", "value": value}
        number = _number(value)
        return number if number is not None else {"kind": "dynamic"}
    if isinstance(node, ast.UnaryOp) and isinstance(node.op, (ast.UAdd, ast.USub)):
        if isinstance(node.operand, ast.Constant):
            value = node.operand.value
            if type(value) in (int, float):
                number = value if isinstance(node.op, ast.UAdd) else -value
                encoded = _number(number)
                return encoded if encoded is not None else {"kind": "dynamic"}
        return {"kind": "dynamic"}
    if isinstance(node, ast.List):
        return {
            "kind": "list",
            "items": [_literal(item, depth + 1) for item in node.elts],
        }
    if isinstance(node, ast.Dict):
        items = []
        for key, value in zip(node.keys, node.values):
            if not isinstance(key, ast.Constant) or type(key.value) is not str:
                return {"kind": "dynamic"}
            items.append({"key": key.value, "value": _literal(value, depth + 1)})
        return {"kind": "dict", "items": items}
    return {"kind": "dynamic"}


def _argument_names(arguments):
    names = list(arguments.posonlyargs) + list(arguments.args) + list(arguments.kwonlyargs)
    if arguments.vararg is not None:
        names.append(arguments.vararg)
    if arguments.kwarg is not None:
        names.append(arguments.kwarg)
    return names


def _nodes_shadowed(tree):
    for node in ast.walk(tree):
        if isinstance(node, ast.Name) and node.id == "nodes":
            if isinstance(node.ctx, (ast.Store, ast.Del)):
                return True
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.ClassDef)):
            if node.name == "nodes":
                return True
        if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda)):
            if any(argument.arg == "nodes" for argument in _argument_names(node.args)):
                return True
        if isinstance(node, ast.ExceptHandler) and node.name == "nodes":
            return True
        if isinstance(node, (ast.Import, ast.ImportFrom)):
            for alias in node.names:
                bound = alias.asname or alias.name.split(".")[0]
                if bound == "nodes":
                    return True
    return False


def _nodes_prefix(expression):
    if isinstance(expression, ast.Name) and expression.id == "nodes":
        return True
    return (isinstance(expression, ast.Attribute) and expression.attr == "nodes"
        and isinstance(expression.value, ast.Name) and expression.value.id == "karakuri")


def _target(call):
    function = call.func
    if isinstance(function, ast.Attribute) and _nodes_prefix(function.value):
        return {"kind": "value", "value": function.attr}, function.attr == "call", True
    if isinstance(function, ast.Subscript) and _nodes_prefix(function.value):
        return _literal(function.slice), False, False
    return None, False, False


def _extract_call(call):
    target, generic, attribute = _target(call)
    if target is None:
        return None
    dynamic_arguments = bool(call.keywords) or any(
        isinstance(argument, ast.Starred) for argument in call.args)
    return {
        "target": target,
        "generic": generic,
        "attribute": attribute,
        "dynamic_arguments": dynamic_arguments,
        "arguments": [
            {"value": _literal(argument), "range": _range(argument)}
            for argument in call.args
        ],
        "range": _range(call),
        "callee": _range(call.func),
    }


try:
    _tree = ast.parse(payload)
except SyntaxError as _error:
    _line = _error.lineno or 1
    _column = max((_error.offset or 1) - 1, 0)
    result = {
        "syntax": {
            "message": _error.msg,
            "line": _line,
            "column": _column,
            "end_line": _error.end_lineno or _line,
            "end_column": max((_error.end_offset or (_column + 1)) - 1, 0),
        },
        "calls": [],
    }
else:
    _calls = []
    if not _nodes_shadowed(_tree):
        for _node in ast.walk(_tree):
            if isinstance(_node, ast.Call):
                _call = _extract_call(_node)
                if _call is not None:
                    _calls.append(_call)
                    if len(_calls) >= _MAX_CALLS:
                        break
    result = {"calls": _calls}
`

type pythonCheckDocument struct {
	Syntax *pythonSyntax `json:"syntax,omitempty"`
	Calls  []pythonCall  `json:"calls"`
}

type pythonSyntax struct {
	Message   string `json:"message"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	EndLine   int    `json:"end_line"`
	EndColumn int    `json:"end_column"`
}

type pythonRange struct {
	Line      int `json:"line"`
	Column    int `json:"column"`
	EndLine   int `json:"end_line"`
	EndColumn int `json:"end_column"`
}

type pythonCall struct {
	Target           pythonValueIR      `json:"target"`
	Generic          bool               `json:"generic"`
	Attribute        bool               `json:"attribute"`
	DynamicArguments bool               `json:"dynamic_arguments"`
	Arguments        []pythonArgumentIR `json:"arguments"`
	Range            pythonRange        `json:"range"`
	Callee           pythonRange        `json:"callee"`
}

type pythonArgumentIR struct {
	Value pythonValueIR `json:"value"`
	Range pythonRange   `json:"range"`
}

type pythonValueIR struct {
	Kind  string          `json:"kind"`
	Value json.RawMessage `json:"value,omitempty"`
	Items []pythonItemIR  `json:"items,omitempty"`
}

type pythonItemIR struct {
	Key   string        `json:"key,omitempty"`
	Value pythonValueIR `json:"value"`
}

type pythonChecker struct {
	source    string
	pure      bool
	functions map[string]scriptsdk.Function
	metas     map[string]flow.NodeMeta
	findings  []scriptsdk.Diagnostic
}

// CheckPythonSource parses source with the real CPython runtime and validates
// literal node calls. The submitted source is data to a trusted AST extractor;
// its body and any node invoker remain unexecuted.
func CheckPythonSource(ctx context.Context, source string, pure bool, metas []flow.NodeMeta) ([]scriptsdk.Diagnostic, error) {
	runtime, ok := Get(LangPython)
	if !ok {
		return nil, fmt.Errorf("%w: no Python runtime is registered; enable script.python_enabled", ErrPythonVerificationUnavailable)
	}
	if err := runtime.Available(); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrPythonVerificationUnavailable, err)
	}
	result, err := Run(ctx, Request{
		Language: LangPython,
		Source:   pythonCheckProgram,
		Payload:  source,
		Network:  NetworkNone,
	})
	if err != nil {
		return nil, fmt.Errorf("running python AST verifier: %w", err)
	}
	if result == nil {
		return nil, errors.New("python AST verifier returned no result")
	}
	raw, err := json.Marshal(result.Value)
	if err != nil {
		return nil, fmt.Errorf("encoding python AST verifier result: %w", err)
	}
	var document pythonCheckDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decoding python AST verifier result: %w", err)
	}
	if document.Syntax != nil {
		r := pythonRange{Line: document.Syntax.Line, Column: document.Syntax.Column, EndLine: document.Syntax.EndLine, EndColumn: document.Syntax.EndColumn}
		diagnostic := scriptsdk.Diagnostic{Severity: "error", Code: "script-syntax", Message: document.Syntax.Message}
		setPythonRange(source, &diagnostic, r)
		if diagnostic.To == diagnostic.From && diagnostic.From < len(source) {
			diagnostic.To++
		}
		return []scriptsdk.Diagnostic{diagnostic}, nil
	}

	checker := &pythonChecker{
		source:    source,
		pure:      pure,
		functions: make(map[string]scriptsdk.Function),
		metas:     make(map[string]flow.NodeMeta, len(metas)),
	}
	for _, meta := range metas {
		checker.metas[meta.Type] = meta
	}
	for _, function := range scriptsdk.Functions(metas) {
		checker.functions[function.Name] = function
		for _, alias := range function.Aliases {
			checker.functions[alias] = function
		}
	}
	for _, call := range document.Calls {
		checker.checkCall(call)
	}
	sort.SliceStable(checker.findings, func(i, j int) bool {
		if checker.findings[i].From != checker.findings[j].From {
			return checker.findings[i].From < checker.findings[j].From
		}
		return checker.findings[i].Code < checker.findings[j].Code
	})
	return checker.findings, nil
}

func (c *pythonChecker) checkCall(call pythonCall) {
	if len(c.findings) >= scriptsdk.DiagnosticLimit {
		return
	}
	minArgs, maxArgs := 1, 3
	configIndex := 0
	if call.Generic {
		minArgs, maxArgs = 2, 4
		configIndex = 1
	}
	if !call.DynamicArguments && (len(call.Arguments) < minArgs || len(call.Arguments) > maxArgs) {
		c.add(call.Range, "node-arguments", fmt.Sprintf("node call expects %d to %d arguments, got %d", minArgs, maxArgs, len(call.Arguments)), "")
		return
	}

	function, dynamicType, resolved := c.resolveFunction(call)
	if !resolved || dynamicType {
		return
	}
	if c.pure && !function.Meta.Pure {
		c.add(call.Callee, "node-pure", fmt.Sprintf("node type %q has side effects and cannot be called during pure evaluation", function.NodeType), function.NodeType)
		return
	}
	if call.DynamicArguments {
		return
	}
	invocation, ok := c.literalInvocation(call, function, configIndex, configIndex+1, configIndex+2)
	if !ok {
		return
	}
	if err := scriptsdk.ValidateCall(function.Meta, invocation, c.pure); err != nil {
		c.add(call.Arguments[configIndex].Range, pythonCallDiagnosticCode(err.Error()), err.Error(), function.NodeType)
	}
}

func (c *pythonChecker) resolveFunction(call pythonCall) (scriptsdk.Function, bool, bool) {
	if !call.Generic {
		value, literal := pythonLiteral(call.Target)
		if !literal {
			return scriptsdk.Function{}, true, true
		}
		target, ok := value.(string)
		if !ok {
			c.add(call.Callee, "node-type", "the node type must be a string", "")
			return scriptsdk.Function{}, false, false
		}
		function, ok := c.functions[target]
		if ok && ((call.Attribute && function.NodeType != target) || (!call.Attribute && function.NodeType == target)) {
			return function, false, true
		}
		if meta, exists := c.metas[target]; exists && !scriptsdk.Eligible(meta) {
			c.add(call.Callee, "node-function-ineligible", fmt.Sprintf("node type %q is not eligible for script calls", target), target)
		} else {
			c.add(call.Callee, "node-function-unknown", fmt.Sprintf("unknown node function %q", target), target)
		}
		return scriptsdk.Function{}, false, false
	}
	if len(call.Arguments) == 0 {
		return scriptsdk.Function{}, false, false
	}
	argument := call.Arguments[0]
	value, literal := pythonLiteral(argument.Value)
	if !literal {
		return scriptsdk.Function{}, true, true
	}
	target, ok := value.(string)
	if !ok {
		c.add(argument.Range, "node-type", "the generic node type must be a string", "")
		return scriptsdk.Function{}, false, false
	}
	function, ok := c.functions[target]
	if ok && function.NodeType == target {
		return function, false, true
	}
	if meta, exists := c.metas[target]; exists && !scriptsdk.Eligible(meta) {
		c.add(argument.Range, "node-function-ineligible", fmt.Sprintf("node type %q is not eligible for script calls", target), target)
	} else {
		c.add(argument.Range, "node-function-unknown", fmt.Sprintf("unknown node type %q", target), target)
	}
	return scriptsdk.Function{}, false, false
}

func (c *pythonChecker) literalInvocation(call pythonCall, function scriptsdk.Function, configIndex, inputsIndex, optionsIndex int) (nodeapi.NodeCall, bool) {
	arguments := call.Arguments
	config, ok := c.literalObjectArgument(arguments[configIndex], "node-config", function.NodeType)
	if !ok {
		return nodeapi.NodeCall{}, false
	}
	rawConfig, err := json.Marshal(config)
	if err != nil {
		c.add(arguments[configIndex].Range, "node-config", "config must contain only JSON data", function.NodeType)
		return nodeapi.NodeCall{}, false
	}
	invocation := nodeapi.NodeCall{Type: function.NodeType, Config: rawConfig}

	if optionsIndex < len(arguments) && !pythonNull(arguments[optionsIndex].Value) {
		argument := arguments[optionsIndex]
		values, ok := c.literalObjectArgument(argument, "node-options", function.NodeType)
		if !ok {
			return nodeapi.NodeCall{}, false
		}
		for _, key := range pythonSortedKeys(values) {
			if key != "id" && key != "retry" && key != "params" {
				c.add(argument.Range, "node-options-unknown", fmt.Sprintf("unknown invocation option %q", key), function.NodeType)
				return nodeapi.NodeCall{}, false
			}
		}
		if retry, ok := values["retry"].(map[string]any); ok {
			for _, key := range pythonSortedKeys(retry) {
				switch key {
				case "max_attempts", "delay_ms", "backoff", "max_delay_ms", "jitter":
				default:
					c.add(argument.Range, "node-options-unknown", fmt.Sprintf("unknown retry option %q", key), function.NodeType)
					return nodeapi.NodeCall{}, false
				}
			}
		}
		raw, err := json.Marshal(values)
		if err != nil || json.Unmarshal(raw, &invocation.Options) != nil {
			c.add(argument.Range, "node-options", "invocation options have an invalid shape", function.NodeType)
			return nodeapi.NodeCall{}, false
		}
	}

	if inputsIndex < len(arguments) && !pythonNull(arguments[inputsIndex].Value) {
		argument := arguments[inputsIndex]
		properties, ok := c.inputProperties(argument, function.NodeType)
		if !ok {
			return nodeapi.NodeCall{}, false
		}
		effective := make(map[string]any, len(config)+len(invocation.Options.Params))
		for key, value := range config {
			effective[key] = value
		}
		for key, value := range invocation.Options.Params {
			effective[key] = value
		}
		rawEffective, err := json.Marshal(effective)
		if err != nil {
			return nodeapi.NodeCall{}, false
		}
		ports := flow.NodePorts(function.Meta, &flow.Node{Type: function.NodeType, Config: rawEffective, Params: pythonSortedKeys(invocation.Options.Params)}, nil)
		invocation.Inputs = make(map[string]*nodeapi.Msg, len(properties))
		for _, port := range pythonSortedKeys(properties) {
			value, literal := pythonLiteral(properties[port])
			if literal {
				raw, err := json.Marshal(value)
				if err != nil {
					c.add(argument.Range, "node-inputs", "input messages must contain only JSON/base64 data", function.NodeType)
					return nodeapi.NodeCall{}, false
				}
				var msg *nodeapi.Msg
				if err := json.Unmarshal(raw, &msg); err != nil || msg == nil {
					c.add(argument.Range, "node-inputs", fmt.Sprintf("input %q must be a complete message object", port), function.NodeType)
					return nodeapi.NodeCall{}, false
				}
				invocation.Inputs[port] = msg
				continue
			}
			var payload any
			if portSpec, exists := ports.Input(port); exists {
				payload = pythonPlaceholderPayload(portSpec.Type)
			}
			invocation.Inputs[port] = &nodeapi.Msg{Payload: payload}
		}
	}
	return invocation, true
}

func (c *pythonChecker) literalObjectArgument(argument pythonArgumentIR, code, nodeType string) (map[string]any, bool) {
	if argument.Value.Kind == "dynamic" {
		return nil, false
	}
	if argument.Value.Kind != "dict" {
		if _, literal := pythonLiteral(argument.Value); literal {
			c.add(argument.Range, code, "expected an object", nodeType)
		}
		return nil, false
	}
	value, ok := pythonLiteral(argument.Value)
	if !ok {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

func (c *pythonChecker) inputProperties(argument pythonArgumentIR, nodeType string) (map[string]pythonValueIR, bool) {
	if argument.Value.Kind == "dynamic" {
		return nil, false
	}
	if argument.Value.Kind != "dict" {
		if _, literal := pythonLiteral(argument.Value); literal {
			c.add(argument.Range, "node-inputs", "expected input messages keyed by port", nodeType)
		}
		return nil, false
	}
	properties := make(map[string]pythonValueIR, len(argument.Value.Items))
	for _, item := range argument.Value.Items {
		properties[item.Key] = item.Value
	}
	return properties, true
}

func pythonLiteral(value pythonValueIR) (any, bool) {
	switch value.Kind {
	case "value":
		var decoded any
		if err := json.Unmarshal(value.Value, &decoded); err != nil {
			return nil, false
		}
		return decoded, true
	case "dict":
		decoded := make(map[string]any, len(value.Items))
		for _, item := range value.Items {
			itemValue, ok := pythonLiteral(item.Value)
			if !ok {
				return nil, false
			}
			decoded[item.Key] = itemValue
		}
		return decoded, true
	case "list":
		decoded := make([]any, 0, len(value.Items))
		for _, item := range value.Items {
			itemValue, ok := pythonLiteral(item.Value)
			if !ok {
				return nil, false
			}
			decoded = append(decoded, itemValue)
		}
		return decoded, true
	default:
		return nil, false
	}
}

func pythonNull(value pythonValueIR) bool {
	return value.Kind == "value" && string(value.Value) == "null"
}

func (c *pythonChecker) add(r pythonRange, code, message, nodeType string) {
	if len(c.findings) >= scriptsdk.DiagnosticLimit {
		return
	}
	diagnostic := scriptsdk.Diagnostic{Severity: "error", Code: code, Message: message, NodeType: nodeType}
	setPythonRange(c.source, &diagnostic, r)
	c.findings = append(c.findings, diagnostic)
}

func setPythonRange(source string, diagnostic *scriptsdk.Diagnostic, r pythonRange) {
	from := pythonOffset(source, r.Line, r.Column)
	to := pythonOffset(source, r.EndLine, r.EndColumn)
	if to < from {
		to = from
	}
	diagnostic.From, diagnostic.To = from, to
	diagnostic.Line, diagnostic.Column = pythonSourcePosition(source, from)
}

func pythonOffset(source string, line, column int) int {
	if line < 1 {
		line = 1
	}
	if column < 0 {
		column = 0
	}
	start, currentLine := 0, 1
	for start < len(source) && currentLine < line {
		if source[start] == '\n' {
			currentLine++
		}
		start++
	}
	if currentLine < line {
		return len(source)
	}
	end := start
	for end < len(source) && source[end] != '\n' {
		end++
	}
	if column > end-start {
		column = end - start
	}
	return start + column
}

func pythonSourcePosition(source string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line, column := 1, 1
	for index := 0; index < offset; index++ {
		if source[index] == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return line, column
}

func pythonPlaceholderPayload(portType string) any {
	switch portType {
	case flow.PortString, flow.PortBinary:
		return ""
	case flow.PortBool:
		return false
	case flow.PortNumber, flow.PortFloat, flow.PortInteger:
		return 0
	case flow.PortObject:
		return map[string]any{}
	case flow.PortImage:
		return map[string]any{"mime": "image/png", "data": "AA=="}
	default:
		return nil
	}
}

func pythonCallDiagnosticCode(message string) string {
	switch {
	case strings.Contains(message, "required config field"):
		return "node-config-required"
	case strings.Contains(message, "unknown config field"):
		return "node-config-unknown"
	case strings.Contains(message, "required input"):
		return "node-input-required"
	case strings.Contains(message, "unknown input port"):
		return "node-input-unknown"
	case strings.Contains(message, "requires a non-empty options.id"):
		return "node-identity-required"
	case strings.Contains(message, "retry"):
		return "node-retry"
	case strings.Contains(message, "input"):
		return "node-input-invalid"
	case strings.Contains(message, "parameter"):
		return "node-parameter-invalid"
	default:
		return "node-config-invalid"
	}
}

func pythonSortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
