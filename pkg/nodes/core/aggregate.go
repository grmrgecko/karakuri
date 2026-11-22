package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// AggregateNode reduces several messages to one summary: counts, sums, extremes,
// and averages over a property. It collects messages the same way join does —
// by split group, or by count or timeout for a plain stream — and emits an
// object with one field per requested operation.
type AggregateNode struct{}

func (AggregateNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "aggregate",
		Label:       "Aggregate",
		Folder:      flow.FolderSequence,
		Description: "Reduces several messages to counts, sums, and averages",
		Icon:        "las la-calculator",
		// Aggregating a property inside each payload means those payloads are
		// objects; topic, meta, and the payload itself leave it undeclared.
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny,
				TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}}},
		},
		Outputs: []engine.PortSpec{
			// The summary always has one field per requested operation, and
			// grouping makes that one object per group. Both are "object",
			// which is this system's name for a decoded document rather than
			// for a map specifically.
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: append([]engine.FieldSpec{
			{
				ID:          "property",
				Label:       "Property",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.temperature",
				Help:        "The value to aggregate. Dot path into each message; a bare path is read from the payload.",
			},
			{
				ID:          "group_by",
				Label:       "Group by",
				Type:        flow.FieldString,
				Placeholder: "payload.room",
				Help: "Summarize each distinct value of this separately. Dot path into each message; " +
					"a bare path is read from the payload. Empty summarizes everything as one. " +
					"Grouping makes the result a list, one object per group, with the key beside the operations.",
			},
			{
				ID:      "operations",
				Label:   "Operations",
				Type:    flow.FieldString,
				Default: "count,min,max,avg",
				Help: "Comma-separated list of count, sum, min, max, avg, median, first, last, " +
					"values, or concat. The result payload has one field per operation.",
			},
			{
				ID:          "separator",
				Label:       "Concat separator",
				Type:        flow.FieldString,
				Default:     ", ",
				Placeholder: ", ",
				Help:        "Used by the concat operation.",
			},
			{
				ID:          "count",
				Label:       "Message count",
				Type:        flow.FieldString,
				Placeholder: "10",
				Help:        "How many messages make one summary. Leave empty for messages from a split.",
			},
			{
				ID:          "timeout",
				Label:       "Timeout",
				Type:        flow.FieldDuration,
				Placeholder: "5m",
				Help:        "Summarize whatever has arrived this long after the first message.",
			},
		}, engine.MissedDeadlineFields()...),
	}
}

// aggregateConfig is the decoded aggregate node configuration.
type aggregateConfig struct {
	Property   string `json:"property"`
	GroupBy    string `json:"group_by"`
	Operations string `json:"operations"`
	Separator  string `json:"separator"`
	Count      string `json:"count"`
	Timeout    string `json:"timeout"`
}

// Execute collects the message and, once its group is done, emits the summary.
func (AggregateNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg aggregateConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	ops, err := parseAggregateOps(cfg.Operations)
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
	msg := inbound(ec, inputs)
	if err := requireCompletion(msg, count, timeout); err != nil {
		return nil, err
	}

	res, err := collect(ctx, ec, msg, count, timeout)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, nil
	}

	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}

	var payload any
	if groupBy := strings.TrimSpace(cfg.GroupBy); groupBy != "" {
		payload, err = aggregateByGroup(ops, res.parts, property, groupBy, cfg.Separator)
	} else {
		payload, err = aggregatePayload(ops, res.parts, property, cfg.Separator)
	}
	if err != nil {
		return nil, err
	}
	return engine.One("out", &engine.Msg{
		Payload: payload,
		Topic:   msg.Topic,
		Meta:    combinedMeta(res),
		Group:   res.rest,
	}), nil
}

// aggregateByGroup splits the collected messages by a key and summarizes each
// bucket, which is the difference between "the average temperature" and "the
// average temperature per room". The result is a list rather than an object
// keyed by the value, because a key can be anything a message carries and a
// list keeps the group's own value visible as data.
//
// Buckets come back in first-seen order, so a run over the same input twice
// produces the same output.
func aggregateByGroup(ops []string, parts []groupedPart, property, groupBy, separator string) (any, error) {
	order := make([]string, 0, 8)
	buckets := make(map[string][]groupedPart)
	values := make(map[string]any)

	for _, p := range parts {
		value, _ := resolveProperty(&engine.Msg{Payload: p.payload, Topic: p.topic, Meta: p.meta}, groupBy)
		key := stringify(value)
		if _, seen := buckets[key]; !seen {
			order = append(order, key)
			values[key] = value
		}
		buckets[key] = append(buckets[key], p)
	}

	out := make([]any, 0, len(order))
	for _, key := range order {
		row, err := aggregatePayload(ops, buckets[key], property, separator)
		if err != nil {
			return nil, fmt.Errorf("group %q: %w", key, err)
		}
		// The key rides beside the operations under the field it was read from,
		// so a downstream node names the same thing it grouped by.
		row[lastPathSegment(groupBy)] = values[key]
		out = append(out, row)
	}
	return out, nil
}

// lastPathSegment names the field a dot path ends at, which is what a grouped
// row calls its key.
func lastPathSegment(path string) string {
	if i := strings.LastIndex(path, "."); i >= 0 && i+1 < len(path) {
		return path[i+1:]
	}
	return path
}

// aggregateOps are the supported reductions.
var aggregateOps = map[string]bool{
	"count": true, "sum": true, "min": true, "max": true, "avg": true,
	"median": true, "first": true, "last": true, "values": true, "concat": true,
}

// parseAggregateOps splits and validates the configured operation list.
func parseAggregateOps(raw string) ([]string, error) {
	var ops []string
	for _, part := range strings.Split(raw, ",") {
		op := strings.ToLower(strings.TrimSpace(part))
		if op == "" {
			continue
		}
		if !aggregateOps[op] {
			return nil, fmt.Errorf("unknown aggregate operation %q", op)
		}
		ops = append(ops, op)
	}
	if len(ops) == 0 {
		return nil, fmt.Errorf("aggregate node has no operations")
	}
	return ops, nil
}

// aggregatePayload computes each requested operation over the collected values.
// Operations that need numbers skip non-numeric values rather than failing the
// whole group, since one bad reading should not lose the rest.
func aggregatePayload(ops []string, parts []groupedPart, property, separator string) (map[string]any, error) {
	values := make([]any, 0, len(parts))
	for _, p := range parts {
		msg := &engine.Msg{Payload: p.payload, Topic: p.topic, Meta: p.meta}
		if value, found := resolveProperty(msg, property); found {
			values = append(values, value)
		}
	}
	numbers := make([]float64, 0, len(values))
	for _, v := range values {
		if n, ok := toNumber(v); ok {
			numbers = append(numbers, n)
		}
	}

	out := make(map[string]any, len(ops)+1)
	for _, op := range ops {
		switch op {
		case "count":
			out["count"] = len(values)
		case "values":
			out["values"] = values
		case "first":
			if len(values) > 0 {
				out["first"] = values[0]
			}
		case "last":
			if len(values) > 0 {
				out["last"] = values[len(values)-1]
			}
		case "concat":
			sep := separator
			if sep == "" {
				sep = ", "
			}
			pieces := make([]string, 0, len(values))
			for _, v := range values {
				pieces = append(pieces, stringify(v))
			}
			out["concat"] = strings.Join(pieces, sep)
		case "sum", "min", "max", "avg", "median":
			if len(numbers) == 0 {
				// Nothing numeric to reduce; leave the field out so downstream
				// expressions can tell "no data" from a real zero.
				continue
			}
			out[op] = reduceNumbers(op, numbers)
		}
	}
	return out, nil
}

// reduceNumbers computes one numeric reduction over a non-empty slice.
func reduceNumbers(op string, numbers []float64) float64 {
	switch op {
	case "sum":
		total := 0.0
		for _, n := range numbers {
			total += n
		}
		return total
	case "min":
		min := numbers[0]
		for _, n := range numbers[1:] {
			if n < min {
				min = n
			}
		}
		return min
	case "max":
		max := numbers[0]
		for _, n := range numbers[1:] {
			if n > max {
				max = n
			}
		}
		return max
	case "avg":
		total := 0.0
		for _, n := range numbers {
			total += n
		}
		return total / float64(len(numbers))
	default:
		return median(numbers)
	}
}

// median returns the middle value, averaging the two middles for an even count.
// The input is copied so the caller's order stays intact for other reductions.
func median(numbers []float64) float64 {
	sorted := make([]float64, len(numbers))
	copy(sorted, numbers)
	sort.Float64s(sorted)
	mid := len(sorted) / 2
	if len(sorted)%2 == 1 {
		return sorted[mid]
	}
	return (sorted[mid-1] + sorted[mid]) / 2
}

// Resume waits out a aggregate group stored before a restart or a redeploy.
func (n AggregateNode) Resume(ctx context.Context, ec engine.ExecContext, key string, w engine.Wait) (map[string][]*engine.Msg, error) {
	return resumeFanIn(ctx, ec, key, w, n.Execute)
}
