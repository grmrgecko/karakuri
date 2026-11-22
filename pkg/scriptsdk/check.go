package scriptsdk

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
	"github.com/dop251/goja/token"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// DiagnosticLimit bounds findings returned for one script.
const DiagnosticLimit = 100

const javascriptWrapperPrefix = "(function(msg, payload, topic, meta, input) {\n"

type scriptChecker struct {
	source    string
	pure      bool
	functions map[string]Function
	metas     map[string]flow.NodeMeta
	findings  []Diagnostic
}

// CheckSource parses and verifies statically inspectable JavaScript node calls.
// Dynamic expressions are deliberately left for runtime validation.
func CheckSource(source string, pure bool, metas []flow.NodeMeta) []Diagnostic {
	checker := &scriptChecker{
		source:    source,
		pure:      pure,
		functions: make(map[string]Function),
		metas:     make(map[string]flow.NodeMeta, len(metas)),
	}
	for _, meta := range metas {
		checker.metas[meta.Type] = meta
	}
	for _, function := range Functions(metas) {
		checker.functions[function.Name] = function
		for _, alias := range function.Aliases {
			checker.functions[alias] = function
		}
	}

	wrapped := javascriptWrapperPrefix + source + "\n})"
	program, err := parser.ParseFile(nil, "script.js", wrapped, 0)
	if err != nil {
		return syntaxDiagnostics(source, err)
	}
	if program == nil || nodesShadowed(program) {
		return nil
	}
	walkAST(program, func(node ast.Node) {
		if call, ok := node.(*ast.CallExpression); ok {
			checker.checkCall(call)
		}
	})
	sort.SliceStable(checker.findings, func(i, j int) bool {
		if checker.findings[i].From != checker.findings[j].From {
			return checker.findings[i].From < checker.findings[j].From
		}
		return checker.findings[i].Code < checker.findings[j].Code
	})
	return checker.findings
}

func (c *scriptChecker) checkCall(call *ast.CallExpression) {
	if len(c.findings) >= DiagnosticLimit {
		return
	}
	property, generic, recognized := nodeCallProperty(call.Callee)
	if !recognized {
		return
	}
	minArgs, maxArgs := 1, 3
	configIndex, inputsIndex, optionsIndex := 0, 1, 2
	if generic {
		minArgs, maxArgs = 2, 4
		configIndex, inputsIndex, optionsIndex = 1, 2, 3
	}
	if len(call.ArgumentList) < minArgs || len(call.ArgumentList) > maxArgs {
		c.add(call, "node-arguments", fmt.Sprintf("node call expects %d to %d arguments, got %d", minArgs, maxArgs, len(call.ArgumentList)), "")
		return
	}

	function, dynamicType, resolved := c.resolveFunction(property, generic, call)
	if !resolved {
		return
	}
	if dynamicType {
		return
	}
	if c.pure && !function.Meta.Pure {
		c.add(call.Callee, "node-pure", fmt.Sprintf("node type %q has side effects and cannot be called during pure evaluation", function.NodeType), function.NodeType)
		return
	}

	invocation, ok := c.literalInvocation(call, function, configIndex, inputsIndex, optionsIndex)
	if !ok {
		return
	}
	if err := ValidateCall(function.Meta, invocation, c.pure); err != nil {
		c.add(call.ArgumentList[configIndex], callDiagnosticCode(err.Error()), err.Error(), function.NodeType)
	}
}

func (c *scriptChecker) resolveFunction(property string, generic bool, call *ast.CallExpression) (Function, bool, bool) {
	if !generic {
		function, ok := c.functions[property]
		if ok {
			return function, false, true
		}
		if meta, exists := c.metas[property]; exists && !Eligible(meta) {
			c.add(call.Callee, "node-function-ineligible", fmt.Sprintf("node type %q is not eligible for script calls", property), property)
		} else {
			c.add(call.Callee, "node-function-unknown", fmt.Sprintf("unknown node function %q", property), property)
		}
		return Function{}, false, false
	}

	argument := call.ArgumentList[0]
	value, ok := literalString(argument)
	if !ok {
		if _, literal := literalValue(argument); literal {
			c.add(argument, "node-type", "the generic node type must be a string", "")
		}
		return Function{}, true, true
	}
	function, ok := c.functions[value]
	if ok && function.NodeType == value {
		return function, false, true
	}
	if meta, exists := c.metas[value]; exists && !Eligible(meta) {
		c.add(argument, "node-function-ineligible", fmt.Sprintf("node type %q is not eligible for script calls", value), value)
	} else {
		c.add(argument, "node-function-unknown", fmt.Sprintf("unknown node type %q", value), value)
	}
	return Function{}, false, false
}

func (c *scriptChecker) literalInvocation(call *ast.CallExpression, function Function, configIndex, inputsIndex, optionsIndex int) (nodeapi.NodeCall, bool) {
	arguments := call.ArgumentList
	config, ok := c.literalObjectArgument(arguments[configIndex], "node-config", function.NodeType)
	if !ok {
		return nodeapi.NodeCall{}, false
	}
	rawConfig, err := json.Marshal(config)
	if err != nil {
		c.add(arguments[configIndex], "node-config", "config must contain only JSON data", function.NodeType)
		return nodeapi.NodeCall{}, false
	}
	invocation := nodeapi.NodeCall{Type: function.NodeType, Config: rawConfig}

	if optionsIndex < len(arguments) && !isOmitted(arguments[optionsIndex]) {
		values, ok := c.literalObjectArgument(arguments[optionsIndex], "node-options", function.NodeType)
		if !ok {
			return nodeapi.NodeCall{}, false
		}
		for key := range values {
			if key != "id" && key != "retry" && key != "params" {
				c.add(arguments[optionsIndex], "node-options-unknown", fmt.Sprintf("unknown invocation option %q", key), function.NodeType)
				return nodeapi.NodeCall{}, false
			}
		}
		if retry, ok := values["retry"].(map[string]any); ok {
			for key := range retry {
				switch key {
				case "max_attempts", "delay_ms", "backoff", "max_delay_ms", "jitter":
				default:
					c.add(arguments[optionsIndex], "node-options-unknown", fmt.Sprintf("unknown retry option %q", key), function.NodeType)
					return nodeapi.NodeCall{}, false
				}
			}
		}
		raw, err := json.Marshal(values)
		if err != nil || json.Unmarshal(raw, &invocation.Options) != nil {
			c.add(arguments[optionsIndex], "node-options", "invocation options have an invalid shape", function.NodeType)
			return nodeapi.NodeCall{}, false
		}
	}

	if inputsIndex < len(arguments) && !isOmitted(arguments[inputsIndex]) {
		properties, ok := c.inputProperties(arguments[inputsIndex], function.NodeType)
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
		ports := flow.NodePorts(function.Meta, &flow.Node{Type: function.NodeType, Config: rawEffective, Params: sortedKeys(invocation.Options.Params)}, nil)
		invocation.Inputs = make(map[string]*nodeapi.Msg, len(properties))
		for port, expression := range properties {
			if value, literal := literalValue(expression); literal {
				raw, err := json.Marshal(value)
				if err != nil {
					c.add(expression, "node-inputs", "input messages must contain only JSON/base64 data", function.NodeType)
					return nodeapi.NodeCall{}, false
				}
				var msg *nodeapi.Msg
				if err := json.Unmarshal(raw, &msg); err != nil || msg == nil {
					c.add(expression, "node-inputs", fmt.Sprintf("input %q must be a complete message object", port), function.NodeType)
					return nodeapi.NodeCall{}, false
				}
				invocation.Inputs[port] = msg
				continue
			}
			payload := any(nil)
			if port, exists := ports.Input(port); exists {
				payload = placeholderPayload(port.Type)
			}
			invocation.Inputs[port] = &nodeapi.Msg{Payload: payload}
		}
	}
	return invocation, true
}

func (c *scriptChecker) inputProperties(expression ast.Expression, nodeType string) (map[string]ast.Expression, bool) {
	object, ok := expression.(*ast.ObjectLiteral)
	if !ok {
		if _, literal := literalValue(expression); literal {
			c.add(expression, "node-inputs", "expected input messages keyed by port", nodeType)
		}
		return nil, false
	}
	properties := make(map[string]ast.Expression, len(object.Value))
	for _, property := range object.Value {
		keyed, ok := property.(*ast.PropertyKeyed)
		if !ok || keyed.Computed || keyed.Kind != ast.PropertyKindValue {
			return nil, false
		}
		key, ok := literalPropertyKey(keyed.Key)
		if !ok {
			return nil, false
		}
		properties[key] = keyed.Value
	}
	return properties, true
}

func placeholderPayload(portType string) any {
	switch portType {
	case flow.PortString:
		return ""
	case flow.PortBool:
		return false
	case flow.PortNumber, flow.PortFloat, flow.PortInteger:
		return 0
	case flow.PortObject:
		return map[string]any{}
	case flow.PortBinary:
		return ""
	case flow.PortImage:
		return map[string]any{"mime": "image/png", "data": "AA=="}
	default:
		return nil
	}
}

func (c *scriptChecker) literalObjectArgument(expression ast.Expression, code, nodeType string) (map[string]any, bool) {
	if _, ok := expression.(*ast.ObjectLiteral); !ok {
		if _, literal := literalValue(expression); literal {
			c.add(expression, code, "expected an object", nodeType)
		}
		return nil, false
	}
	value, ok := literalObject(expression)
	if !ok {
		return nil, false
	}
	object, ok := value.(map[string]any)
	return object, ok
}

func (c *scriptChecker) add(node ast.Node, code, message, nodeType string) {
	if len(c.findings) >= DiagnosticLimit || node == nil {
		return
	}
	from, to := c.rangeOf(node)
	line, column := sourcePosition(c.source, from)
	c.findings = append(c.findings, Diagnostic{
		Severity: "error",
		Code:     code,
		Message:  message,
		From:     from,
		To:       to,
		Line:     line,
		Column:   column,
		NodeType: nodeType,
	})
}

func (c *scriptChecker) rangeOf(node ast.Node) (int, int) {
	from := int(node.Idx0()) - 1 - len(javascriptWrapperPrefix)
	to := int(node.Idx1()) - 1 - len(javascriptWrapperPrefix)
	if from < 0 {
		from = 0
	}
	if to < from {
		to = from
	}
	if from > len(c.source) {
		from = len(c.source)
	}
	if to > len(c.source) {
		to = len(c.source)
	}
	return from, to
}

func nodeCallProperty(expression ast.Expression) (string, bool, bool) {
	switch callee := expression.(type) {
	case *ast.DotExpression:
		if identifier, ok := callee.Left.(*ast.Identifier); ok && identifier.Name == "nodes" {
			property := string(callee.Identifier.Name)
			return property, property == "call", true
		}
	case *ast.BracketExpression:
		if identifier, ok := callee.Left.(*ast.Identifier); ok && identifier.Name == "nodes" {
			property, ok := literalString(callee.Member)
			if !ok {
				return "", false, false
			}
			return property, property == "call", true
		}
	}
	return "", false, false
}

func nodesShadowed(program *ast.Program) bool {
	shadowed := false
	walkAST(program, func(node ast.Node) {
		if shadowed {
			return
		}
		switch current := node.(type) {
		case *ast.Binding:
			shadowed = bindingNames(current.Target, "nodes")
		case *ast.ForDeclaration:
			shadowed = bindingNames(current.Target, "nodes")
		case *ast.CatchStatement:
			shadowed = bindingNames(current.Parameter, "nodes")
		case *ast.FunctionLiteral:
			shadowed = current.Name != nil && current.Name.Name == "nodes"
		case *ast.ClassLiteral:
			shadowed = current.Name != nil && current.Name.Name == "nodes"
		case *ast.AssignExpression:
			if identifier, ok := current.Left.(*ast.Identifier); ok {
				shadowed = identifier.Name == "nodes"
			}
		}
	})
	return shadowed
}

func bindingNames(target ast.BindingTarget, name string) bool {
	found := false
	walkAST(target, func(node ast.Node) {
		if identifier, ok := node.(*ast.Identifier); ok && string(identifier.Name) == name {
			found = true
		}
	})
	return found
}

func literalObject(expression ast.Expression) (any, bool) {
	object, ok := expression.(*ast.ObjectLiteral)
	if !ok {
		return nil, false
	}
	values := make(map[string]any, len(object.Value))
	for _, property := range object.Value {
		keyed, ok := property.(*ast.PropertyKeyed)
		if !ok || keyed.Computed || keyed.Kind != ast.PropertyKindValue {
			return nil, false
		}
		key, ok := literalPropertyKey(keyed.Key)
		if !ok {
			return nil, false
		}
		value, ok := literalValue(keyed.Value)
		if !ok {
			return nil, false
		}
		values[key] = value
	}
	return values, true
}

func literalPropertyKey(expression ast.Expression) (string, bool) {
	switch key := expression.(type) {
	case *ast.Identifier:
		return string(key.Name), true
	case *ast.StringLiteral:
		return string(key.Value), true
	case *ast.NumberLiteral:
		return key.Literal, true
	default:
		return "", false
	}
}

func literalValue(expression ast.Expression) (any, bool) {
	switch value := expression.(type) {
	case *ast.ObjectLiteral:
		return literalObject(value)
	case *ast.ArrayLiteral:
		values := make([]any, 0, len(value.Value))
		for _, element := range value.Value {
			item, ok := literalValue(element)
			if !ok {
				return nil, false
			}
			values = append(values, item)
		}
		return values, true
	case *ast.StringLiteral:
		return string(value.Value), true
	case *ast.NumberLiteral:
		return value.Value, true
	case *ast.BooleanLiteral:
		return value.Value, true
	case *ast.NullLiteral:
		return nil, true
	case *ast.TemplateLiteral:
		if len(value.Expressions) != 0 {
			return nil, false
		}
		var text strings.Builder
		for _, element := range value.Elements {
			if !element.Valid {
				return nil, false
			}
			text.WriteString(string(element.Parsed))
		}
		return text.String(), true
	case *ast.UnaryExpression:
		if value.Operator != token.PLUS && value.Operator != token.MINUS {
			return nil, false
		}
		numberLiteral, ok := value.Operand.(*ast.NumberLiteral)
		if !ok {
			return nil, false
		}
		number, ok := numericValue(numberLiteral.Value)
		if !ok {
			return nil, false
		}
		if value.Operator == token.MINUS {
			number = -number
		}
		return number, true
	default:
		return nil, false
	}
}

func literalString(expression ast.Expression) (string, bool) {
	value, ok := literalValue(expression)
	if !ok {
		return "", false
	}
	text, ok := value.(string)
	return text, ok
}

// isOmitted reports whether an inputs or options argument stands for nothing.
// The runtime decodes both undefined and null to no inputs and no options, so
// a script may skip either position with null to reach the next one.
func isOmitted(expression ast.Expression) bool {
	if _, ok := expression.(*ast.NullLiteral); ok {
		return true
	}
	identifier, ok := expression.(*ast.Identifier)
	return ok && identifier.Name == "undefined"
}

func callDiagnosticCode(message string) string {
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

func syntaxDiagnostics(source string, err error) []Diagnostic {
	list, ok := err.(parser.ErrorList)
	if !ok {
		line, column := 1, 1
		return []Diagnostic{{Severity: "error", Code: "script-syntax", Message: err.Error(), Line: line, Column: column}}
	}
	findings := make([]Diagnostic, 0, len(list))
	for _, item := range list {
		line, column := item.Position.Line-1, item.Position.Column
		if line < 1 {
			line, column = 1, 1
		}
		from := sourceOffset(source, line, column)
		to := from
		if from < len(source) {
			to++
		}
		findings = append(findings, Diagnostic{
			Severity: "error",
			Code:     "script-syntax",
			Message:  item.Message,
			From:     from,
			To:       to,
			Line:     line,
			Column:   column,
		})
		if len(findings) >= DiagnosticLimit {
			break
		}
	}
	return findings
}

func sourcePosition(source string, offset int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > len(source) {
		offset = len(source)
	}
	line, column := 1, 1
	for i := 0; i < offset; i++ {
		if source[i] == '\n' {
			line, column = line+1, 1
		} else {
			column++
		}
	}
	return line, column
}

func sourceOffset(source string, line, column int) int {
	if line <= 1 {
		line = 1
	}
	currentLine, currentColumn := 1, 1
	for i := 0; i < len(source); i++ {
		if currentLine == line && currentColumn == column {
			return i
		}
		if source[i] == '\n' {
			currentLine, currentColumn = currentLine+1, 1
		} else {
			currentColumn++
		}
	}
	return len(source)
}
