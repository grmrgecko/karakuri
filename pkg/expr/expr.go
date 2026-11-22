// Package expr wraps expr-lang/expr for karakuri's node expressions: a
// process-wide compile cache, single-expression evaluation, and `{{ }}`
// string interpolation. The engine builds the evaluation environment; this
// package stays free of engine dependencies so both can evolve independently.
package expr

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	exprlang "github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
)

// cacheCap bounds the compile cache. When full the whole cache is dropped;
// at this scale (a few hundred distinct expressions per deployment) a full
// evict is simpler than LRU bookkeeping and just as effective.
const cacheCap = 512

// cache is the process-wide compiled program cache keyed by source text.
var cache = struct {
	mu       sync.Mutex
	programs map[string]*vm.Program
}{programs: make(map[string]*vm.Program)}

// Program is a compiled expression ready for evaluation.
type Program = vm.Program

// Compile compiles an expression, serving repeated sources from the cache.
// Programs are compiled without a typed environment so any env map works;
// undefined variables evaluate to nil instead of failing.
func Compile(src string) (*Program, error) {
	cache.mu.Lock()
	if p, ok := cache.programs[src]; ok {
		cache.mu.Unlock()
		return p, nil
	}
	cache.mu.Unlock()

	p, err := exprlang.Compile(src, exprlang.Env(map[string]any{}), exprlang.AllowUndefinedVariables())
	if err != nil {
		return nil, fmt.Errorf("invalid expression %q: %w", src, err)
	}

	cache.mu.Lock()
	if len(cache.programs) >= cacheCap {
		cache.programs = make(map[string]*vm.Program)
	}
	cache.programs[src] = p
	cache.mu.Unlock()
	return p, nil
}

// Eval compiles (or fetches from cache) and runs an expression against the
// given environment.
func Eval(src string, env map[string]any) (any, error) {
	p, err := Compile(src)
	if err != nil {
		return nil, err
	}
	out, err := exprlang.Run(p, env)
	if err != nil {
		return nil, fmt.Errorf("expression %q failed: %w", src, err)
	}
	return out, nil
}

// Env is the input to BuildEnv. It is a struct rather than a parameter list
// because four of its fields are maps of the same type and swapping two of
// them would compile cleanly while silently reading the wrong scope.
type Env struct {
	// Payload, Topic, and Meta are the message being evaluated against.
	Payload any
	Topic   string
	Meta    map[string]any
	// Nodes maps node ID to that node's last emitted output within the current
	// run. Nil outside a run (armed triggers), where it is left out entirely.
	Nodes map[string]map[string]any
	// Vars, Flow, and Global are the run, flow, and global variable scopes.
	Vars   map[string]any
	Flow   map[string]any
	Global map[string]any
}

// BuildEnv assembles the standard expression environment for one message:
// payload, topic, and meta as top-level names, msg as the whole message map,
// nodes as the run's completed node outputs, and vars, flow, and global as the
// three variable scopes. The scope maps are always present, so an expression
// reading an unset variable gets nil rather than failing.
func BuildEnv(e Env) map[string]any {
	meta := e.Meta
	if meta == nil {
		meta = map[string]any{}
	}
	env := map[string]any{
		"payload": e.Payload,
		"topic":   e.Topic,
		"meta":    meta,
		"vars":    orEmpty(e.Vars),
		"flow":    orEmpty(e.Flow),
		"global":  orEmpty(e.Global),
		"msg": map[string]any{
			"payload": e.Payload,
			"topic":   e.Topic,
			"meta":    meta,
		},
	}
	if e.Nodes != nil {
		env["nodes"] = e.Nodes
	}
	return env
}

// orEmpty substitutes an empty map for a nil scope.
func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// Interpolate replaces every `{{ expr }}` segment in s with its evaluated
// value. String results are inserted as-is; all other results are inserted as
// their JSON encoding. A string that is exactly one `{{ expr }}` segment is
// treated the same way, so its result still arrives as a string. There is no
// escape syntax for literal `{{`; templates needing literal braces can emit
// them from an expression (e.g. {{ "{{" }}).
func Interpolate(s string, env map[string]any) (string, error) {
	out, _, err := interpolate(s, env)
	return out, err
}

// InterpolateValue is Interpolate for config values that want to keep the
// evaluated type: when s is exactly one `{{ expr }}` segment the raw result
// is returned (numbers stay numbers); otherwise it behaves like Interpolate
// and returns the built string.
func InterpolateValue(s string, env map[string]any) (any, error) {
	if inner, ok := SingleExpression(s); ok {
		return Eval(inner, env)
	}
	out, _, err := interpolate(s, env)
	return out, err
}

// SingleExpression reports whether s is exactly one `{{ expr }}` segment and
// returns the inner expression source. Callers use it to evaluate
// whole-value expressions with their native type instead of a string.
func SingleExpression(s string) (string, bool) {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "{{") || !strings.HasSuffix(t, "}}") {
		return "", false
	}
	// One segment only when the closing marker that ends the first segment is
	// the last one, so `{{a}} and {{b}}` stays two.
	if segmentEnd(t[2:]) != len(t)-4 {
		return "", false
	}
	return strings.TrimSpace(t[2 : len(t)-2]), true
}

// segmentEnd returns the offset in rest, the text after an opening `{{`, of
// the `}}` that closes the segment, or -1 when there is none. A nested map
// literal ends in `}}` too, as can a string, so the closing marker is the
// first one that leaves a valid expression. When none does, the first marker
// stands and evaluating the segment reports the error.
func segmentEnd(rest string) int {
	first := strings.Index(rest, "}}")
	if first < 0 {
		return -1
	}
	for end := first; ; {
		if _, err := Compile(strings.TrimSpace(rest[:end])); err == nil {
			return end
		}
		// Markers can overlap, as in `{"a": {"b": 1}}}}`.
		next := strings.Index(rest[end+1:], "}}")
		if next < 0 {
			return first
		}
		end += 1 + next
	}
}

// interpolate scans for `{{ }}` segments and builds the substituted string.
// The bool reports whether any segment was found.
func interpolate(s string, env map[string]any) (string, bool, error) {
	if !strings.Contains(s, "{{") {
		return s, false, nil
	}
	var b strings.Builder
	rest := s
	found := false
	for {
		start := strings.Index(rest, "{{")
		if start < 0 {
			b.WriteString(rest)
			break
		}
		end := segmentEnd(rest[start+2:])
		if end < 0 {
			// Unterminated segment: keep the remaining text literally.
			b.WriteString(rest)
			break
		}
		found = true
		b.WriteString(rest[:start])
		src := strings.TrimSpace(rest[start+2 : start+2+end])
		val, err := Eval(src, env)
		if err != nil {
			return "", found, err
		}
		b.WriteString(stringifyResult(val))
		rest = rest[start+2+end+2:]
	}
	return b.String(), found, nil
}

// stringifyResult renders an interpolation result: strings as-is, everything
// else as JSON so structured values stay machine-readable.
func stringifyResult(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return "null"
	}
	// The text lands in message bodies and request documents, not in a page,
	// so the encoder's HTML escaping would only turn "&" and "<" into codes.
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return fmt.Sprint(v)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// InterpolateDocument fills every string inside a decoded JSON document,
// keys included, and returns the rebuilt document. It exists because a
// structured payload is authored as a document rather than as text: the
// template stays something an editor can parse and lint, and a field holding
// one whole `{{ }}` segment keeps that expression's own type, so
// `"brightness": "{{ level }}"` emits a number rather than a quoted one.
// Substituting into the raw text instead would force the author to leave a
// placeholder unquoted to get a number out, which is not valid JSON to edit.
func InterpolateDocument(node any, env map[string]any) (any, error) {
	switch v := node.(type) {
	case string:
		return InterpolateValue(v, env)
	case []any:
		out := make([]any, len(v))
		for i, item := range v {
			filled, err := InterpolateDocument(item, env)
			if err != nil {
				return nil, err
			}
			out[i] = filled
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, item := range v {
			// A key can only be text, however the expression is typed.
			filledKey, err := Interpolate(key, env)
			if err != nil {
				return nil, err
			}
			filled, err := InterpolateDocument(item, env)
			if err != nil {
				return nil, err
			}
			out[filledKey] = filled
		}
		return out, nil
	default:
		return node, nil
	}
}
