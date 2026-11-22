package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// ChangeNode rewrites parts of a message: setting, search-and-replacing,
// deleting, moving, and copying properties of the payload, topic, and
// metadata. It exists so the common case of reshaping a message does not
// require dropping into a script node.
type ChangeNode struct{}

func (ChangeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "change",
		Label:       "Change",
		Folder:      flow.FolderLogic,
		Description: "Sets, replaces, deletes, moves, or copies message properties",
		Icon:        "las la-edit",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny},
		},
		Pure: true,
		ConfigSpec: []engine.FieldSpec{
			{
				ID:       "rules",
				Label:    "Rules",
				Type:     flow.FieldChangeRules,
				Required: true,
				Default: []any{
					map[string]any{
						"op": flow.ChangeOpSet, "target": "payload.value",
						"from": flow.ChangeFromString, "value": "",
					},
				},
				Options: []engine.Option{
					{Value: flow.ChangeOpSet, Label: "Set"},
					{Value: flow.ChangeOpChange, Label: "Change"},
					{Value: flow.ChangeOpDelete, Label: "Delete"},
					{Value: flow.ChangeOpMove, Label: "Move"},
					{Value: flow.ChangeOpCopy, Label: "Copy"},
				},
				Help: "Rules apply in order, so a later rule sees what an earlier one wrote. " +
					"Property paths start with payload, topic, or meta; a bare path is read " +
					"from the payload.",
			},
		},
	}
}

// Execute applies each rule in order to a copy of the incoming message.
func (ChangeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg flow.ChangeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if len(cfg.Rules) == 0 {
		return nil, errors.New("change node has no rules")
	}
	in := inbound(ec, inputs)

	// Work on a copy so the upstream message the engine recorded stays intact.
	out := &engine.Msg{
		Payload: deepCopyValue(in.Payload),
		Topic:   in.Topic,
		Meta:    nodeutil.CloneMeta(in.Meta),
		Group:   in.Group,
	}

	for i, rule := range cfg.Rules {
		if err := applyChangeRule(ec, out, in, rule); err != nil {
			return nil, fmt.Errorf("rule %d (%s %s): %w", i+1, flow.ChangeRuleOp(rule), rule.Target, err)
		}
	}
	return engine.One("out", out), nil
}

// applyChangeRule applies one rule. Reads resolve against the message as it
// stands after earlier rules, so rules can build on each other; expressions
// evaluate against the original input for predictability.
func applyChangeRule(ec engine.ExecContext, out, in *engine.Msg, rule flow.ChangeRule) error {
	target := strings.TrimSpace(rule.Target)
	if target == "" {
		return errors.New("rule has no target property")
	}

	switch op := flow.ChangeRuleOp(rule); op {
	case flow.ChangeOpSet:
		value, err := changeRuleValue(ec, out, in, rule, rule.Value)
		if err != nil {
			return err
		}
		return setProperty(out, target, value)

	case flow.ChangeOpChange:
		return applyChangeReplace(ec, out, in, rule, target)

	case flow.ChangeOpDelete:
		return deleteProperty(out, target)

	case flow.ChangeOpMove, flow.ChangeOpCopy:
		source, ok := rule.Value.(string)
		if !ok || strings.TrimSpace(source) == "" {
			return errors.New("move and copy need the source property path in \"value\"")
		}
		source = strings.TrimSpace(source)
		value, found := resolveProperty(out, source)
		if !found {
			return fmt.Errorf("source property %q is not present", source)
		}
		if op == flow.ChangeOpCopy {
			// A copy is a snapshot. Sharing the map would let a later rule
			// that rewrites either path rewrite both.
			value = deepCopyValue(value)
		} else if err := deleteProperty(out, source); err != nil {
			// A move clears its source before writing the target. The two may
			// overlap, as when a payload is wrapped under a key of itself, and
			// clearing afterwards would delete what was just written.
			return err
		}
		return setProperty(out, target, value)

	default:
		return fmt.Errorf("unknown operation %q", rule.Op)
	}
}

// applyChangeReplace performs a search and replace on the target property. A
// string property has its matches rewritten in place; a non-string property is
// replaced wholesale when its text form equals the search term, which is how a
// number or boolean can be swapped for another value. A property that is not
// present, or that matches nothing, is left alone rather than failing: the rule
// describes a rewrite, not a requirement.
func applyChangeReplace(ec engine.ExecContext, out, in *engine.Msg, rule flow.ChangeRule, target string) error {
	actual, found := resolveProperty(out, target)
	if !found {
		return nil
	}

	search, err := ec.Interpolate(rule.Search, in)
	if err != nil {
		return err
	}
	if search == "" {
		return errors.New("change rule has nothing to search for")
	}

	// Non-string properties compare whole: an exact text match swaps the value
	// for the typed replacement, anything else is left as it is.
	current, isString := actual.(string)
	if !isString {
		if stringify(actual) != search {
			return nil
		}
		value, err := changeRuleValue(ec, out, in, rule, rule.Replace)
		if err != nil {
			return err
		}
		return setProperty(out, target, value)
	}

	// String properties are rewritten in place, so whatever the replacement
	// resolves to is inserted as text.
	replace, err := changeRuleValue(ec, out, in, rule, rule.Replace)
	if err != nil {
		return err
	}
	replacement := stringify(replace)
	if flow.ChangeRuleSearchType(rule) == flow.ChangeSearchRegex {
		re, err := regexp.Compile(search)
		if err != nil {
			return fmt.Errorf("invalid regular expression %q: %w", search, err)
		}
		return setProperty(out, target, replaceRegex(re, current, replacement, rule.ReplaceAll))
	}
	limit := 1
	if rule.ReplaceAll {
		limit = -1
	}
	return setProperty(out, target, strings.Replace(current, search, replacement, limit))
}

// replaceRegex applies a compiled pattern, expanding $1-style references in
// the replacement so captured groups can be reused.
func replaceRegex(re *regexp.Regexp, s, replace string, all bool) string {
	if all {
		return re.ReplaceAllString(s, replace)
	}
	// Rewrite only the first match: everything after it is left untouched.
	loc := re.FindStringSubmatchIndex(s)
	if loc == nil {
		return s
	}
	expanded := re.ExpandString(nil, replace, s, loc)
	return s[:loc[0]] + string(expanded) + s[loc[1]:]
}

// changeRuleValue resolves the value a rule writes, according to its from
// mode. The raw operand is passed in because a set reads it from the rule's
// value while a change reads it from the rule's replacement.
func changeRuleValue(ec engine.ExecContext, out, in *engine.Msg, rule flow.ChangeRule, raw any) (any, error) {
	switch flow.ChangeRuleFrom(rule) {
	case flow.ChangeFromString:
		// Strings are interpolated so {{ }} segments work without switching to
		// a full expression.
		s, ok := raw.(string)
		if !ok {
			s = stringify(raw)
		}
		return ec.Interpolate(s, in)

	case flow.ChangeFromNumber:
		switch v := raw.(type) {
		case float64:
			return v, nil
		case json.Number:
			return v.Float64()
		case string:
			rendered, err := ec.Interpolate(v, in)
			if err != nil {
				return nil, err
			}
			n, err := strconv.ParseFloat(strings.TrimSpace(rendered), 64)
			if err != nil {
				return nil, fmt.Errorf("%q is not a number", rendered)
			}
			return n, nil
		default:
			return nil, fmt.Errorf("value %v is not a number", raw)
		}

	case flow.ChangeFromBoolean:
		switch v := raw.(type) {
		case bool:
			return v, nil
		case string:
			rendered, err := ec.Interpolate(v, in)
			if err != nil {
				return nil, err
			}
			b, err := strconv.ParseBool(strings.TrimSpace(rendered))
			if err != nil {
				return nil, fmt.Errorf("%q is not a boolean", rendered)
			}
			return b, nil
		default:
			return nil, fmt.Errorf("value %v is not a boolean", raw)
		}

	case flow.ChangeFromJSON:
		// Objects and arrays arrive already decoded; a string is parsed so raw
		// JSON typed into the editor also works.
		if s, ok := raw.(string); ok {
			rendered, err := ec.Interpolate(s, in)
			if err != nil {
				return nil, err
			}
			var decoded any
			if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
				return nil, fmt.Errorf("value is not valid JSON: %w", err)
			}
			return decoded, nil
		}
		return deepCopyValue(raw), nil

	case flow.ChangeFromExpression:
		src, ok := raw.(string)
		if !ok {
			return nil, errors.New("expression rules need a string expression")
		}
		value, err := ec.Eval(src, in)
		if err != nil {
			return nil, err
		}
		// The result can be an object the input message holds, and a later
		// rule may write beneath it.
		return deepCopyValue(value), nil

	case flow.ChangeFromProperty:
		path, ok := raw.(string)
		if !ok {
			return nil, errors.New("property rules need a source path")
		}
		value, found := resolveProperty(out, strings.TrimSpace(path))
		if !found {
			return nil, fmt.Errorf("source property %q is not present", path)
		}
		// The source stays where it is, so the target gets its own copy.
		return deepCopyValue(value), nil

	default:
		return nil, fmt.Errorf("unknown value source %q", rule.From)
	}
}

// splitPropertyPath splits a message property path into its root (payload,
// topic, or meta) and the remaining segments. A bare path is read from the
// payload, matching the switch node.
func splitPropertyPath(path string) (root string, rest []string) {
	parts := strings.Split(path, ".")
	switch parts[0] {
	case "payload", "topic", "meta":
		return parts[0], parts[1:]
	default:
		return "payload", parts
	}
}

// setProperty writes a value at a dot path, creating intermediate objects as
// needed. Writing into a non-object replaces it with an object, since the
// alternative is failing on a path the user clearly intends to create.
func setProperty(msg *engine.Msg, path string, value any) error {
	root, rest := splitPropertyPath(path)
	switch root {
	case "topic":
		if len(rest) > 0 {
			return errors.New("topic has no sub-properties")
		}
		msg.Topic = stringify(value)
		return nil

	case "meta":
		if len(rest) == 0 {
			obj, ok := value.(map[string]any)
			if !ok {
				return errors.New("meta can only be replaced with an object")
			}
			msg.Meta = obj
			return nil
		}
		if msg.Meta == nil {
			msg.Meta = map[string]any{}
		}
		detachNested(msg.Meta, rest)
		return setNested(msg.Meta, rest, value)

	default:
		if len(rest) == 0 {
			msg.Payload = value
			return nil
		}
		obj, ok := msg.Payload.(map[string]any)
		if !ok {
			obj = map[string]any{}
			msg.Payload = obj
		}
		return setNested(obj, rest, value)
	}
}

// setNested walks (and creates) intermediate objects, then writes the leaf.
func setNested(obj map[string]any, path []string, value any) error {
	for _, key := range path[:len(path)-1] {
		next, ok := obj[key].(map[string]any)
		if !ok {
			next = map[string]any{}
			obj[key] = next
		}
		obj = next
	}
	obj[path[len(path)-1]] = value
	return nil
}

// detachNested replaces each object on the way to a path's leaf with its own
// copy. A node copies only the top level of a message's metadata, so the
// objects below it are still the ones the input message holds, and a write or
// a delete through them would reach run history and every sibling branch.
func detachNested(obj map[string]any, path []string) {
	for _, key := range path[:len(path)-1] {
		next, ok := obj[key].(map[string]any)
		if !ok {
			return
		}
		own := make(map[string]any, len(next))
		for k, v := range next {
			own[k] = v
		}
		obj[key] = own
		obj = own
	}
}

// deleteProperty removes a property at a dot path. Removing something already
// absent is not an error: the requested end state holds either way.
func deleteProperty(msg *engine.Msg, path string) error {
	root, rest := splitPropertyPath(path)
	switch root {
	case "topic":
		if len(rest) > 0 {
			return errors.New("topic has no sub-properties")
		}
		msg.Topic = ""
		return nil

	case "meta":
		if len(rest) == 0 {
			msg.Meta = nil
			return nil
		}
		detachNested(msg.Meta, rest)
		deleteNested(msg.Meta, rest)
		return nil

	default:
		if len(rest) == 0 {
			msg.Payload = nil
			return nil
		}
		if obj, ok := msg.Payload.(map[string]any); ok {
			deleteNested(obj, rest)
		}
		return nil
	}
}

// deleteNested removes a leaf key, walking only through objects that exist.
func deleteNested(obj map[string]any, path []string) {
	for _, key := range path[:len(path)-1] {
		next, ok := obj[key].(map[string]any)
		if !ok {
			return
		}
		obj = next
	}
	delete(obj, path[len(path)-1])
}

// deepCopyValue copies decoded JSON values so a node can rewrite a payload
// without touching the message it received. Scalars are returned as they are.
func deepCopyValue(v any) any {
	switch t := v.(type) {
	case []byte:
		// Binary payloads are shared by every copy of a message otherwise, and
		// a node writing into one would rewrite what history recorded.
		return append([]byte(nil), t...)
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = deepCopyValue(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = deepCopyValue(val)
		}
		return out
	default:
		return v
	}
}
