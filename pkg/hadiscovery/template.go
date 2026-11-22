package hadiscovery

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

// jsHelpers are the functions translated templates call. They mirror the
// Jinja behavior the translation relies on: attribute access on a missing
// value is undefined rather than an error, and float and int fall back to a
// default when the value does not parse.
const jsHelpers = `const __g = (o, k) => (o === null || o === undefined) ? undefined : o[k];
const __f = (x, d) => { const n = parseFloat(x); return isNaN(n) ? (d === undefined ? 0 : d) : n; };
const __i = (x, d) => { const n = parseInt(x, 10); return isNaN(n) ? (d === undefined ? 0 : d) : n; };
const __r = (x, n) => { const p = Math.pow(10, n || 0); return Math.round(Number(x) * p) / p; };
const __s = (x) => x === undefined || x === null ? "None" : (typeof x === "boolean" ? (x ? "True" : "False") : (typeof x === "object" ? JSON.stringify(x) : String(x)));
const __in = (a, b) => typeof b === "string" ? b.includes(String(a)) : Array.isArray(b) ? b.includes(a) : (b !== null && typeof b === "object" ? a in b : false);
const __len = (x) => x === null || x === undefined ? 0 : (typeof x === "object" && !Array.isArray(x) ? Object.keys(x).length : x.length);
`

// TranslateTemplate turns a Jinja value or command template into a
// JavaScript expression over the variables value and value_json. A template
// that is a single {{ }} expression keeps that expression's type; one with
// surrounding text becomes a string. It fails for anything outside the
// supported subset, including {% %} blocks.
func TranslateTemplate(tpl string) (string, error) {
	if strings.Contains(tpl, "{%") || strings.Contains(tpl, "{#") {
		return "", fmt.Errorf("template uses {%% %%} blocks")
	}
	trimmed := strings.TrimSpace(tpl)
	if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") && strings.Count(trimmed, "{{") == 1 {
		expr, err := translateExpr(trimmed[2 : len(trimmed)-2])
		if err != nil {
			return "", err
		}
		return "(" + expr + ")", nil
	}
	var parts []string
	rest := tpl
	for rest != "" {
		start := strings.Index(rest, "{{")
		if start < 0 {
			parts = append(parts, jsQuote(rest))
			break
		}
		if start > 0 {
			parts = append(parts, jsQuote(rest[:start]))
		}
		end := strings.Index(rest[start:], "}}")
		if end < 0 {
			return "", fmt.Errorf("template has an unclosed {{")
		}
		expr, err := translateExpr(rest[start+2 : start+end])
		if err != nil {
			return "", err
		}
		parts = append(parts, "__s("+expr+")")
		rest = rest[start+end+2:]
	}
	if len(parts) == 0 {
		return `""`, nil
	}
	return "(" + strings.Join(parts, " + ") + ")", nil
}

// jsQuote renders a string as a JavaScript literal.
func jsQuote(s string) string {
	raw, _ := json.Marshal(s)
	return string(raw)
}

// token is one lexical element of a template expression.
type token struct {
	kind string // num, str, ident, op, end
	text string
}

// lex splits an expression into tokens.
func lex(src string) ([]token, error) {
	var out []token
	i := 0
	for i < len(src) {
		c := rune(src[i])
		switch {
		case unicode.IsSpace(c):
			i++
		case unicode.IsDigit(c) || (c == '.' && i+1 < len(src) && unicode.IsDigit(rune(src[i+1]))):
			j := i
			for j < len(src) && (unicode.IsDigit(rune(src[j])) || src[j] == '.') {
				j++
			}
			out = append(out, token{"num", src[i:j]})
			i = j
		case c == '\'' || c == '"':
			j := i + 1
			var b strings.Builder
			for j < len(src) && rune(src[j]) != c {
				if src[j] == '\\' && j+1 < len(src) {
					j++
				}
				b.WriteByte(src[j])
				j++
			}
			if j >= len(src) {
				return nil, fmt.Errorf("template has an unclosed string")
			}
			out = append(out, token{"str", b.String()})
			i = j + 1
		case unicode.IsLetter(c) || c == '_':
			j := i
			for j < len(src) && (unicode.IsLetter(rune(src[j])) || unicode.IsDigit(rune(src[j])) || src[j] == '_') {
				j++
			}
			out = append(out, token{"ident", src[i:j]})
			i = j
		default:
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "==", "!=", "<=", ">=", "//", "**":
				out = append(out, token{"op", two})
				i += 2
				continue
			}
			if strings.ContainsRune("+-*/%<>()[].|,~:=", c) {
				out = append(out, token{"op", string(c)})
				i++
				continue
			}
			return nil, fmt.Errorf("template uses %q, which is not supported", string(c))
		}
	}
	return append(out, token{"end", ""}), nil
}

// parser translates tokens by recursive descent.
type parser struct {
	toks []token
	pos  int
}

// translateExpr translates one {{ }} expression.
func translateExpr(src string) (string, error) {
	toks, err := lex(src)
	if err != nil {
		return "", err
	}
	p := &parser{toks: toks}
	out, err := p.ternary()
	if err != nil {
		return "", err
	}
	if p.peek().kind != "end" {
		return "", fmt.Errorf("template has unexpected %q", p.peek().text)
	}
	return out, nil
}

func (p *parser) peek() token { return p.toks[p.pos] }

func (p *parser) next() token {
	t := p.toks[p.pos]
	if t.kind != "end" {
		p.pos++
	}
	return t
}

func (p *parser) isOp(text string) bool {
	t := p.peek()
	return t.kind == "op" && t.text == text
}

func (p *parser) isWord(text string) bool {
	t := p.peek()
	return t.kind == "ident" && t.text == text
}

func (p *parser) expectOp(text string) error {
	if !p.isOp(text) {
		return fmt.Errorf("template expects %q", text)
	}
	p.next()
	return nil
}

// ternary is "a if cond else b".
func (p *parser) ternary() (string, error) {
	a, err := p.or()
	if err != nil {
		return "", err
	}
	if !p.isWord("if") {
		return a, nil
	}
	p.next()
	cond, err := p.or()
	if err != nil {
		return "", err
	}
	b := "undefined"
	if p.isWord("else") {
		p.next()
		if b, err = p.ternary(); err != nil {
			return "", err
		}
	}
	return "(" + cond + " ? " + a + " : " + b + ")", nil
}

func (p *parser) or() (string, error) {
	a, err := p.and()
	if err != nil {
		return "", err
	}
	for p.isWord("or") {
		p.next()
		b, err := p.and()
		if err != nil {
			return "", err
		}
		a = "(" + a + " || " + b + ")"
	}
	return a, nil
}

func (p *parser) and() (string, error) {
	a, err := p.not()
	if err != nil {
		return "", err
	}
	for p.isWord("and") {
		p.next()
		b, err := p.not()
		if err != nil {
			return "", err
		}
		a = "(" + a + " && " + b + ")"
	}
	return a, nil
}

func (p *parser) not() (string, error) {
	if p.isWord("not") {
		p.next()
		a, err := p.not()
		if err != nil {
			return "", err
		}
		return "(!" + a + ")", nil
	}
	return p.compare()
}

func (p *parser) compare() (string, error) {
	a, err := p.add()
	if err != nil {
		return "", err
	}
	for {
		t := p.peek()
		switch {
		case t.kind == "op" && (t.text == "==" || t.text == "!=" || t.text == "<" || t.text == ">" || t.text == "<=" || t.text == ">="):
			p.next()
			b, err := p.add()
			if err != nil {
				return "", err
			}
			op := t.text
			if op == "==" {
				op = "==="
			} else if op == "!=" {
				op = "!=="
			}
			a = "(" + a + " " + op + " " + b + ")"
		case p.isWord("in"):
			p.next()
			b, err := p.add()
			if err != nil {
				return "", err
			}
			a = "__in(" + a + ", " + b + ")"
		case p.isWord("not") && p.pos+1 < len(p.toks) && p.toks[p.pos+1].kind == "ident" && p.toks[p.pos+1].text == "in":
			p.next()
			p.next()
			b, err := p.add()
			if err != nil {
				return "", err
			}
			a = "(!__in(" + a + ", " + b + "))"
		case p.isWord("is"):
			p.next()
			negate := false
			if p.isWord("not") {
				p.next()
				negate = true
			}
			test := p.next()
			if test.kind != "ident" {
				return "", fmt.Errorf("template has an unsupported test")
			}
			var expr string
			switch test.text {
			case "defined":
				expr = "(" + a + " !== undefined)"
			case "none":
				expr = "(" + a + " === null || " + a + " === undefined)"
			case "number":
				expr = "(typeof " + a + " === \"number\")"
			case "string":
				expr = "(typeof " + a + " === \"string\")"
			default:
				return "", fmt.Errorf("template uses the test %q, which is not supported", test.text)
			}
			if negate {
				expr = "(!" + expr + ")"
			}
			a = expr
		default:
			return a, nil
		}
	}
}

func (p *parser) add() (string, error) {
	a, err := p.mul()
	if err != nil {
		return "", err
	}
	for p.isOp("+") || p.isOp("-") || p.isOp("~") {
		op := p.next().text
		b, err := p.mul()
		if err != nil {
			return "", err
		}
		if op == "~" {
			a = "(__s(" + a + ") + __s(" + b + "))"
		} else {
			a = "(" + a + " " + op + " " + b + ")"
		}
	}
	return a, nil
}

func (p *parser) mul() (string, error) {
	a, err := p.unary()
	if err != nil {
		return "", err
	}
	for p.isOp("*") || p.isOp("/") || p.isOp("//") || p.isOp("%") || p.isOp("**") {
		op := p.next().text
		b, err := p.unary()
		if err != nil {
			return "", err
		}
		switch op {
		case "//":
			a = "Math.floor(" + a + " / " + b + ")"
		case "**":
			a = "Math.pow(" + a + ", " + b + ")"
		default:
			a = "(" + a + " " + op + " " + b + ")"
		}
	}
	return a, nil
}

func (p *parser) unary() (string, error) {
	if p.isOp("-") {
		p.next()
		a, err := p.unary()
		if err != nil {
			return "", err
		}
		return "(-" + a + ")", nil
	}
	return p.postfix()
}

func (p *parser) postfix() (string, error) {
	a, err := p.primary()
	if err != nil {
		return "", err
	}
	for {
		switch {
		case p.isOp("."):
			p.next()
			name := p.next()
			if name.kind != "ident" && name.kind != "num" {
				return "", fmt.Errorf("template has a bad attribute")
			}
			if p.isOp("(") {
				if a, err = p.method(a, name.text); err != nil {
					return "", err
				}
				continue
			}
			key := jsQuote(name.text)
			if name.kind == "num" {
				key = name.text
			}
			a = "__g(" + a + ", " + key + ")"
		case p.isOp("["):
			p.next()
			idx, err := p.ternary()
			if err != nil {
				return "", err
			}
			if err := p.expectOp("]"); err != nil {
				return "", err
			}
			a = "__g(" + a + ", " + idx + ")"
		case p.isOp("|"):
			p.next()
			if a, err = p.filter(a); err != nil {
				return "", err
			}
		default:
			return a, nil
		}
	}
}

// args reads a parenthesised argument list.
func (p *parser) args() ([]string, error) {
	if err := p.expectOp("("); err != nil {
		return nil, err
	}
	var out []string
	for !p.isOp(")") {
		// Keyword arguments keep only their value.
		if p.peek().kind == "ident" && p.pos+1 < len(p.toks) && p.toks[p.pos+1].text == "=" {
			p.next()
			p.next()
		}
		a, err := p.ternary()
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if p.isOp(",") {
			p.next()
			continue
		}
		if !p.isOp(")") {
			return nil, fmt.Errorf("template has a bad argument list")
		}
	}
	p.next()
	return out, nil
}

// method translates the string methods templates commonly call.
func (p *parser) method(a, name string) (string, error) {
	args, err := p.args()
	if err != nil {
		return "", err
	}
	switch name {
	case "lower":
		return "__s(" + a + ").toLowerCase()", nil
	case "upper":
		return "__s(" + a + ").toUpperCase()", nil
	case "strip":
		return "__s(" + a + ").trim()", nil
	case "split":
		sep := `" "`
		if len(args) > 0 {
			sep = args[0]
		}
		return "__s(" + a + ").split(" + sep + ")", nil
	case "replace":
		if len(args) < 2 {
			return "", fmt.Errorf("template replace needs two arguments")
		}
		return "__s(" + a + ").split(" + args[0] + ").join(" + args[1] + ")", nil
	case "get":
		if len(args) == 0 {
			return "", fmt.Errorf("template get needs a key")
		}
		def := "undefined"
		if len(args) > 1 {
			def = args[1]
		}
		return "(__g(" + a + ", " + args[0] + ") ?? " + def + ")", nil
	}
	return "", fmt.Errorf("template calls %s(), which is not supported", name)
}

// filter translates one filter applied to a.
func (p *parser) filter(a string) (string, error) {
	name := p.next()
	if name.kind != "ident" {
		return "", fmt.Errorf("template has a bad filter")
	}
	var args []string
	if p.isOp("(") {
		var err error
		if args, err = p.args(); err != nil {
			return "", err
		}
	}
	arg := func(i int, def string) string {
		if i < len(args) {
			return args[i]
		}
		return def
	}
	switch name.text {
	case "float":
		return "__f(" + a + ", " + arg(0, "undefined") + ")", nil
	case "int":
		return "__i(" + a + ", " + arg(0, "undefined") + ")", nil
	case "round":
		return "__r(" + a + ", " + arg(0, "0") + ")", nil
	case "default", "d":
		return "(" + a + " ?? " + arg(0, `""`) + ")", nil
	case "lower":
		return "__s(" + a + ").toLowerCase()", nil
	case "upper":
		return "__s(" + a + ").toUpperCase()", nil
	case "string":
		return "__s(" + a + ")", nil
	case "trim":
		return "__s(" + a + ").trim()", nil
	case "abs":
		return "Math.abs(" + a + ")", nil
	case "length", "count":
		return "__len(" + a + ")", nil
	case "bool":
		return "(" + a + " === true || [\"true\", \"on\", \"yes\", \"1\"].includes(__s(" + a + ").toLowerCase()))", nil
	case "tojson", "to_json":
		return "JSON.stringify(" + a + ")", nil
	case "from_json":
		return "JSON.parse(__s(" + a + "))", nil
	}
	return "", fmt.Errorf("template uses the filter %q, which is not supported", name.text)
}

func (p *parser) primary() (string, error) {
	t := p.next()
	switch t.kind {
	case "num":
		if _, err := strconv.ParseFloat(t.text, 64); err != nil {
			return "", fmt.Errorf("template has a bad number %q", t.text)
		}
		return t.text, nil
	case "str":
		return jsQuote(t.text), nil
	case "ident":
		switch t.text {
		case "value", "value_json":
			return t.text, nil
		case "true", "True":
			return "true", nil
		case "false", "False":
			return "false", nil
		case "none", "None":
			return "null", nil
		case "float", "int":
			// float(x) and int(x) as functions rather than filters.
			args, err := p.args()
			if err != nil || len(args) == 0 {
				return "", fmt.Errorf("template calls %s() without a value", t.text)
			}
			def := "undefined"
			if len(args) > 1 {
				def = args[1]
			}
			if t.text == "float" {
				return "__f(" + args[0] + ", " + def + ")", nil
			}
			return "__i(" + args[0] + ", " + def + ")", nil
		}
		return "", fmt.Errorf("template uses %q, which is not supported", t.text)
	case "op":
		switch t.text {
		case "(":
			a, err := p.ternary()
			if err != nil {
				return "", err
			}
			if err := p.expectOp(")"); err != nil {
				return "", err
			}
			return "(" + a + ")", nil
		case "[":
			var items []string
			for !p.isOp("]") {
				a, err := p.ternary()
				if err != nil {
					return "", err
				}
				items = append(items, a)
				if p.isOp(",") {
					p.next()
				} else if !p.isOp("]") {
					return "", fmt.Errorf("template has a bad list")
				}
			}
			p.next()
			return "[" + strings.Join(items, ", ") + "]", nil
		}
	}
	return "", fmt.Errorf("template has unexpected %q", t.text)
}
