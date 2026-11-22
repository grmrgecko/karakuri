package scriptsdk

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"mvdan.cc/sh/v3/syntax"
)

type bashChecker struct {
	source    string
	pure      bool
	functions map[string]Function
	metas     map[string]flow.NodeMeta
	findings  []Diagnostic
}

type bashArgument struct {
	word   *syntax.Word
	text   string
	static bool
}

// CheckBashSource parses Bash and verifies statically inspectable node commands.
// Shell expansions remain runtime work; the checker never evaluates them.
func CheckBashSource(source string, pure bool, metas []flow.NodeMeta) []Diagnostic {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(source), "script.sh")
	if err != nil {
		return bashSyntaxDiagnostics(source, err)
	}
	checker := &bashChecker{
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

	shadowed := false
	syntax.Walk(file, func(node syntax.Node) bool {
		switch current := node.(type) {
		case *syntax.FuncDecl:
			if current.Name != nil && current.Name.Value == "node" {
				shadowed = true
			}
			for _, name := range current.Names {
				if name.Value == "node" {
					shadowed = true
				}
			}
		case *syntax.CallExpr:
			checker.checkCall(current)
		}
		return true
	})
	if shadowed {
		return nil
	}
	sort.SliceStable(checker.findings, func(i, j int) bool {
		if checker.findings[i].From != checker.findings[j].From {
			return checker.findings[i].From < checker.findings[j].From
		}
		return checker.findings[i].Code < checker.findings[j].Code
	})
	return checker.findings
}

func (c *bashChecker) checkCall(call *syntax.CallExpr) {
	if len(c.findings) >= DiagnosticLimit || len(call.Args) == 0 {
		return
	}
	command, static, _ := staticBashWord(call.Args[0])
	if !static || command != "node" {
		return
	}
	arguments := make([]bashArgument, 0, len(call.Args)-1)
	countCertain := true
	for _, word := range call.Args[1:] {
		text, static, certain := staticBashWord(word)
		arguments = append(arguments, bashArgument{word: word, text: text, static: static})
		countCertain = countCertain && certain
	}

	generic := len(arguments) > 0 && arguments[0].static && arguments[0].text == "call"
	minArgs, maxArgs := 2, 4
	targetIndex := 0
	if generic {
		minArgs, maxArgs = 3, 5
		targetIndex = 1
	}
	if countCertain && (len(arguments) < minArgs || len(arguments) > maxArgs) {
		c.add(call, "node-arguments", fmt.Sprintf("node call expects %d to %d arguments, got %d", minArgs, maxArgs, len(arguments)), "")
		return
	}
	if targetIndex >= len(arguments) || !arguments[targetIndex].static {
		return
	}
	target := arguments[targetIndex]
	function, resolved := c.resolveFunction(target.text, generic, target.word)
	if !resolved {
		return
	}
	if c.pure && !function.Meta.Pure {
		c.add(target.word, "node-pure", fmt.Sprintf("node type %q has side effects and cannot be called during pure evaluation", function.NodeType), function.NodeType)
		return
	}
	if !countCertain {
		return
	}

	configIndex := targetIndex + 1
	invocation, ok := c.literalInvocation(arguments, function, configIndex, configIndex+1, configIndex+2)
	if !ok {
		return
	}
	if err := ValidateCall(function.Meta, invocation, c.pure); err != nil {
		c.add(arguments[configIndex].word, callDiagnosticCode(err.Error()), err.Error(), function.NodeType)
	}
}

func (c *bashChecker) resolveFunction(target string, generic bool, word *syntax.Word) (Function, bool) {
	function, ok := c.functions[target]
	if ok && (!generic || function.NodeType == target) {
		return function, true
	}
	if meta, exists := c.metas[target]; exists && !Eligible(meta) {
		c.add(word, "node-function-ineligible", fmt.Sprintf("node type %q is not eligible for script calls", target), target)
	} else {
		c.add(word, "node-function-unknown", fmt.Sprintf("unknown node type %q", target), target)
	}
	return Function{}, false
}

func (c *bashChecker) literalInvocation(arguments []bashArgument, function Function, configIndex, inputsIndex, optionsIndex int) (nodeapi.NodeCall, bool) {
	_, dynamic, ok := c.jsonObject(arguments[configIndex], "node-config", function.NodeType)
	if dynamic || !ok {
		return nodeapi.NodeCall{}, false
	}
	invocation := nodeapi.NodeCall{Type: function.NodeType, Config: json.RawMessage(arguments[configIndex].text)}

	if optionsIndex < len(arguments) {
		argument := arguments[optionsIndex]
		values, dynamic, ok := c.jsonObject(argument, "node-options", function.NodeType)
		if dynamic || !ok {
			return nodeapi.NodeCall{}, false
		}
		for _, key := range sortedKeys(values) {
			if key != "id" && key != "retry" && key != "params" {
				c.add(argument.word, "node-options-unknown", fmt.Sprintf("unknown invocation option %q", key), function.NodeType)
				return nodeapi.NodeCall{}, false
			}
		}
		if raw := values["retry"]; len(raw) > 0 && string(raw) != "null" {
			var retry map[string]json.RawMessage
			if err := json.Unmarshal(raw, &retry); err != nil || retry == nil {
				c.add(argument.word, "node-options", "retry must be an object", function.NodeType)
				return nodeapi.NodeCall{}, false
			}
			for _, key := range sortedKeys(retry) {
				switch key {
				case "max_attempts", "delay_ms", "backoff", "max_delay_ms", "jitter":
				default:
					c.add(argument.word, "node-options-unknown", fmt.Sprintf("unknown retry option %q", key), function.NodeType)
					return nodeapi.NodeCall{}, false
				}
			}
		}
		if err := json.Unmarshal([]byte(argument.text), &invocation.Options); err != nil {
			c.add(argument.word, "node-options", "invocation options have an invalid shape", function.NodeType)
			return nodeapi.NodeCall{}, false
		}
	}

	if inputsIndex < len(arguments) {
		argument := arguments[inputsIndex]
		values, dynamic, ok := c.jsonObject(argument, "node-inputs", function.NodeType)
		if !ok {
			return nodeapi.NodeCall{}, false
		}
		if dynamic {
			c.addDynamicInputPlaceholders(&invocation, function.Meta)
		} else {
			invocation.Inputs = make(map[string]*nodeapi.Msg, len(values))
			for _, port := range sortedKeys(values) {
				var msg *nodeapi.Msg
				if err := json.Unmarshal(values[port], &msg); err != nil || msg == nil {
					c.add(argument.word, "node-inputs", fmt.Sprintf("input %q must be a complete message object", port), function.NodeType)
					return nodeapi.NodeCall{}, false
				}
				invocation.Inputs[port] = msg
			}
		}
	}
	return invocation, true
}

func (c *bashChecker) jsonObject(argument bashArgument, code, nodeType string) (map[string]json.RawMessage, bool, bool) {
	if !argument.static {
		return nil, true, true
	}
	var values map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argument.text), &values); err != nil {
		c.add(argument.word, code, fmt.Sprintf("expected a JSON object: %v", err), nodeType)
		return nil, false, false
	}
	if values == nil {
		c.add(argument.word, code, "expected an object", nodeType)
		return nil, false, false
	}
	return values, false, true
}

func (c *bashChecker) addDynamicInputPlaceholders(invocation *nodeapi.NodeCall, meta flow.NodeMeta) {
	var config map[string]any
	if json.Unmarshal(invocation.Config, &config) != nil {
		return
	}
	effective := make(map[string]any, len(config)+len(invocation.Options.Params))
	for key, value := range config {
		effective[key] = value
	}
	for key, value := range invocation.Options.Params {
		effective[key] = value
	}
	raw, err := json.Marshal(effective)
	if err != nil {
		return
	}
	ports := flow.NodePorts(meta, &flow.Node{Type: meta.Type, Config: raw, Params: sortedKeys(invocation.Options.Params)}, nil)
	invocation.Inputs = make(map[string]*nodeapi.Msg)
	for _, port := range ports.Inputs {
		if port.Optional || port.Kind == flow.PortKindParam || port.Kind == flow.PortKindReturn || port.Kind == flow.PortKindSignal {
			continue
		}
		invocation.Inputs[port.ID] = &nodeapi.Msg{Payload: placeholderPayload(port.Type)}
	}
}

func (c *bashChecker) add(node syntax.Node, code, message, nodeType string) {
	if len(c.findings) >= DiagnosticLimit || node == nil {
		return
	}
	from, to := int(node.Pos().Offset()), int(node.End().Offset())
	if from < 0 {
		from = 0
	}
	if from > len(c.source) {
		from = len(c.source)
	}
	if to < from {
		to = from
	}
	if to > len(c.source) {
		to = len(c.source)
	}
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

func staticBashWord(word *syntax.Word) (string, bool, bool) {
	if word == nil {
		return "", false, false
	}
	var text strings.Builder
	static, countCertain := true, true
	for index, part := range word.Parts {
		switch current := part.(type) {
		case *syntax.Lit:
			value, glob := bashLiteral(current.Value, false)
			text.WriteString(value)
			if index == 0 && strings.HasPrefix(current.Value, "~") {
				static = false
			}
			if glob {
				static, countCertain = false, false
			}
		case *syntax.SglQuoted:
			if current.Dollar {
				static = false
			} else {
				text.WriteString(current.Value)
			}
		case *syntax.DblQuoted:
			if current.Dollar {
				static = false
			}
			for _, quoted := range current.Parts {
				switch part := quoted.(type) {
				case *syntax.Lit:
					value, _ := bashLiteral(part.Value, true)
					text.WriteString(value)
				case *syntax.ParamExp:
					static = false
					if bashParameterCanSplit(part) {
						countCertain = false
					}
				case *syntax.BraceExp:
					static, countCertain = false, false
				default:
					static = false
				}
			}
		case *syntax.ParamExp, *syntax.CmdSubst, *syntax.ArithmExp, *syntax.ProcSubst, *syntax.BraceExp, *syntax.ExtGlob:
			static, countCertain = false, false
		default:
			static, countCertain = false, false
		}
	}
	return text.String(), static, countCertain
}

func bashLiteral(value string, doubleQuoted bool) (string, bool) {
	var out strings.Builder
	out.Grow(len(value))
	glob := false
	for index := 0; index < len(value); index++ {
		current := value[index]
		if current != '\\' {
			if !doubleQuoted && (current == '*' || current == '?' || current == '[') {
				glob = true
			}
			out.WriteByte(current)
			continue
		}
		if index+1 >= len(value) {
			out.WriteByte(current)
			continue
		}
		next := value[index+1]
		if next == '\n' {
			index++
			continue
		}
		if doubleQuoted && next != '$' && next != '`' && next != '"' && next != '\\' {
			out.WriteByte(current)
			continue
		}
		out.WriteByte(next)
		index++
	}
	return out.String(), glob
}

func bashParameterCanSplit(parameter *syntax.ParamExp) bool {
	if parameter.Index != nil {
		return true
	}
	return parameter.Param != nil && (parameter.Param.Value == "@" || parameter.Param.Value == "*")
}

func bashSyntaxDiagnostics(source string, err error) []Diagnostic {
	position := syntax.Pos{}
	message := err.Error()
	var parseError syntax.ParseError
	var langError syntax.LangError
	switch {
	case errors.As(err, &parseError):
		position, message = parseError.Pos, parseError.Text
	case errors.As(err, &langError):
		position, message = langError.Pos, langError.Feature
	}
	from := int(position.Offset())
	if from > len(source) {
		from = len(source)
	}
	to := from
	if from < len(source) {
		to++
	}
	line, column := sourcePosition(source, from)
	return []Diagnostic{{
		Severity: "error",
		Code:     "script-syntax",
		Message:  message,
		From:     from,
		To:       to,
		Line:     line,
		Column:   column,
	}}
}
