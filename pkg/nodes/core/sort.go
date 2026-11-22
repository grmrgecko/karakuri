package core

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// sortTypeRule derives what a sort port carries. Property mode reads and
// rewrites an array on the message, so the path check is scoped to that case:
// sequence mode never touches the property field, and a stale path left behind
// by a mode switch must not color its ports. Sequence mode resolves to the
// default "any", which is what lets TypeFrom carry the upstream type through
// on the output.
func sortTypeRule() *flow.PortTypeRule {
	return &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{{
			Match:       map[string][]string{"mode": {"", "property"}},
			Type:        flow.PortObject,
			PayloadPath: []string{"property"},
		}},
		Default: flow.PortAny,
	}
}

// SortNode puts things in order. In property mode it rewrites an array on the
// message in place, which needs nothing from the engine. In sequence mode it
// collects a whole group the way join does, sorts the messages, and fans them
// back out under a fresh group frame, so the downstream subgraph runs once per
// message but in the new order and a join further down recombines them sorted.
type SortNode struct{}

func (SortNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "sort",
		Label:       "Sort",
		Folder:      flow.FolderSequence,
		Description: "Sorts the elements of an array or a sequence of messages",
		Icon:        "las la-sort-amount-down",
		// Sorting an array needs one to sort and hands the same one back;
		// sequence mode passes whole messages through untouched, so the
		// output carries the upstream type there.
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny, TypeRule: sortTypeRule()},
		},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in", TypeRule: sortTypeRule()},
		},
		ConfigSpec: append([]engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Sort",
				Type:    flow.FieldSelect,
				Default: "property",
				Options: []engine.Option{
					{Value: "property", Label: "The elements of an array"},
					{Value: "sequence", Label: "A sequence of messages"},
				},
				Help: "Sequence mode holds the messages of a split group until they have all " +
					"arrived, then releases them in order.",
			},
			{
				ID:          "property",
				Label:       "Array",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.readings",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "property"},
				Help:        "The array to sort. Dot path into the message; a bare path is read from the payload.",
			},
			{
				ID:          "element_key",
				Label:       "Sort each element by",
				Type:        flow.FieldString,
				Placeholder: "temperature",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "property"},
				Help:        "Dot path inside each element. Empty sorts by the element itself.",
			},
			{
				ID:          "key",
				Label:       "Sort each message by",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.temperature",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sequence"},
				Help:        "Dot path into each message; a bare path is read from the payload.",
			},
			{
				ID:      "order",
				Label:   "Order",
				Type:    flow.FieldSelect,
				Default: "asc",
				Options: []engine.Option{
					{Value: "asc", Label: "Ascending"},
					{Value: "desc", Label: "Descending"},
				},
			},
			{
				ID:      "value_type",
				Label:   "Compare as",
				Type:    flow.FieldSelect,
				Default: "auto",
				Options: []engine.Option{
					{Value: "auto", Label: "Automatically"},
					{Value: "number", Label: "Numbers"},
					{Value: "string", Label: "Text"},
				},
				Help: "Auto compares numerically when both values are numeric and as text otherwise.",
			},
			{
				ID:      "case_sensitive",
				Label:   "Case sensitive",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Applies to text comparisons only.",
			},
			{
				ID:          "count",
				Label:       "Message count",
				Type:        flow.FieldString,
				Placeholder: "10",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sequence"},
				Help: "How many messages make one sequence. Leave empty for messages from a split, " +
					"which already say how many parts there are.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout",
				Type:        flow.FieldDuration,
				Placeholder: "30s",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "sequence"},
				Help: "Release whatever has arrived this long after the first message. Empty waits " +
					"indefinitely for the count to be reached.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// sortConfig is the decoded sort node configuration.
type sortConfig struct {
	Mode          string `json:"mode"`
	Property      string `json:"property"`
	ElementKey    string `json:"element_key"`
	Key           string `json:"key"`
	Order         string `json:"order"`
	ValueType     string `json:"value_type"`
	CaseSensitive bool   `json:"case_sensitive"`
	Count         string `json:"count"`
	Timeout       string `json:"timeout"`
}

// descending reports whether the configured order reverses the comparison.
func (c sortConfig) descending() (bool, error) {
	switch c.Order {
	case "", "asc":
		return false, nil
	case "desc":
		return true, nil
	default:
		return false, fmt.Errorf("unknown sort order %q", c.Order)
	}
}

// Execute sorts either an array on the message or the message sequence itself.
func (SortNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg sortConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if err := validateSortValueType(cfg.ValueType); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	switch cfg.Mode {
	case "", "property":
		return sortProperty(cfg, msg)
	case "sequence":
		return sortSequence(ctx, ec, cfg, msg)
	default:
		return nil, fmt.Errorf("unknown sort mode %q", cfg.Mode)
	}
}

// sortProperty reorders an array already on the message and writes it back.
func sortProperty(cfg sortConfig, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	desc, err := cfg.descending()
	if err != nil {
		return nil, err
	}

	// Resolve the array to sort.
	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := resolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("property %q is %T, not an array", property, value)
	}

	// Order a copy of the array, leaving the input message's own untouched.
	elementKey := strings.TrimSpace(cfg.ElementKey)
	sorted := make([]any, len(items))
	copy(sorted, items)
	sort.SliceStable(sorted, func(i, j int) bool {
		c := compareSortValues(sortElementKey(sorted[i], elementKey), sortElementKey(sorted[j], elementKey), cfg)
		if desc {
			return c > 0
		}
		return c < 0
	})

	// Write it back onto a copy so the upstream message the engine recorded
	// stays intact.
	out := &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	if err := setProperty(out, property, sorted); err != nil {
		return nil, fmt.Errorf("cannot write the sorted array to %q: %w", property, err)
	}
	return engine.One("out", out), nil
}

// sortSequence collects the message's group and, once it is done, releases the
// messages in sorted order as a fresh fan-out.
func sortSequence(ctx context.Context, ec engine.ExecContext, cfg sortConfig, msg *engine.Msg) (map[string][]*engine.Msg, error) {
	desc, err := cfg.descending()
	if err != nil {
		return nil, err
	}
	count, err := parseGroupCount(cfg.Count)
	if err != nil {
		return nil, err
	}
	timeout, err := parseDurationDefault(cfg.Timeout, 0)
	if err != nil {
		return nil, fmt.Errorf("invalid timeout %q: %w", cfg.Timeout, err)
	}
	if err := requireCompletion(msg, count, timeout); err != nil {
		return nil, err
	}

	res, err := collect(ctx, ec, msg, count, timeout)
	if err != nil {
		return nil, err
	}
	if res == nil {
		// Still waiting for more messages, or another run will emit.
		return nil, nil
	}

	key := strings.TrimSpace(cfg.Key)
	if key == "" {
		key = "payload"
	}
	parts := make([]groupedPart, len(res.parts))
	copy(parts, res.parts)
	sort.SliceStable(parts, func(i, j int) bool {
		c := compareSortValues(sortPartKey(parts[i], key), sortPartKey(parts[j], key), cfg)
		if desc {
			return c > 0
		}
		return c < 0
	})

	// Re-emit under a new group frame: the indices carry the sorted order, so a
	// join downstream rebuilds the sequence the way this node ordered it.
	groupID := uuid.New().String()
	out := make([]*engine.Msg, 0, len(parts))
	for i, p := range parts {
		out = append(out, &engine.Msg{
			Payload: p.payload,
			Topic:   p.topic,
			Meta:    nodeutil.CloneMeta(p.meta),
			Group:   append(append([]engine.GroupFrame{}, res.rest...), engine.GroupFrame{ID: groupID, Index: i, Count: len(parts)}),
		})
	}
	return map[string][]*engine.Msg{"out": out}, nil
}

// sortElementKey reads the sort key out of one array element. A missing path
// yields nil, which sorts ahead of every present value.
func sortElementKey(element any, key string) any {
	if key == "" {
		return element
	}
	cur := element
	for _, part := range strings.Split(key, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = obj[part]
		if !ok {
			return nil
		}
	}
	return cur
}

// sortPartKey reads the sort key out of one collected message.
func sortPartKey(p groupedPart, key string) any {
	value, found := resolveProperty(&engine.Msg{Payload: p.payload, Topic: p.topic, Meta: p.meta}, key)
	if !found {
		return nil
	}
	return value
}

// validateSortValueType rejects an unknown comparison type up front rather than
// silently falling back mid-sort.
func validateSortValueType(valueType string) error {
	switch valueType {
	case "", "auto", "number", "string":
		return nil
	default:
		return fmt.Errorf("unknown comparison type %q", valueType)
	}
}

// compareSortValues orders two sort keys, returning the usual -1/0/1. Values
// that cannot be read as the requested type sort ahead of ones that can, so a
// missing or malformed key never reorders the rest of the list.
func compareSortValues(a, b any, cfg sortConfig) int {
	switch cfg.ValueType {
	case "number":
		return compareSortNumbers(a, b)
	case "string":
		return compareSortStrings(a, b, cfg.CaseSensitive)
	}

	// Auto: numeric when both sides are numbers, text otherwise.
	if _, ok := sortNumber(a); ok {
		if _, ok := sortNumber(b); ok {
			return compareSortNumbers(a, b)
		}
	}
	return compareSortStrings(a, b, cfg.CaseSensitive)
}

// compareSortNumbers orders two values numerically, with non-numeric values
// first.
func compareSortNumbers(a, b any) int {
	af, aok := sortNumber(a)
	bf, bok := sortNumber(b)
	switch {
	case !aok && !bok:
		return 0
	case !aok:
		return -1
	case !bok:
		return 1
	case af < bf:
		return -1
	case af > bf:
		return 1
	default:
		return 0
	}
}

// compareSortStrings orders two values as text.
func compareSortStrings(a, b any, caseSensitive bool) int {
	as, bs := stringify(a), stringify(b)
	if !caseSensitive {
		as, bs = strings.ToLower(as), strings.ToLower(bs)
	}
	return strings.Compare(as, bs)
}

// sortNumber reads a value as a number, accepting numeric strings so a payload
// decoded as text still sorts numerically. NaN is not a number here: it is
// neither below nor above anything, so comparing it would make it equal to
// every neighbor and leave the values around it unordered. Text reading "NaN"
// or "Inf" stays text, since ParseFloat accepts those words in any case.
func sortNumber(v any) (float64, bool) {
	if n, ok := toNumber(v); ok {
		return n, !math.IsNaN(n)
	}
	if s, ok := v.(string); ok {
		if n, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			return n, true
		}
	}
	return 0, false
}

// Resume waits out a sort sequence stored before a restart or a redeploy.
func (n SortNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	var cfg sortConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Mode != "sequence" {
		// The node no longer collects sequences, so the group has no owner.
		waits, _ := engine.WaitsFor(ec)
		releaseWait(waits, key, w.Token)
		return nil, nil
	}
	return resumeFanIn(ctx, ec, key, w, n.Execute)
}
