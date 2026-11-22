package core

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// This file holds what the format converters share: reading the property they
// convert, writing the result back, and the port pair that states what they
// carry.
//
// Decoding and encoding are separate node types rather than one node with a
// direction setting. A node that could go either way could not say what its
// ports held — the whole point of the type on a port is to tell an author what
// connects to what, and "it depends on a dropdown" answers nothing. Decoders
// live in the parser category and encoders in the encoder category.
//
// Every converter reads one message property and writes to another, defaulting
// to the payload in both directions, so they chain without configuration. The
// conversion itself never mutates the input message: the engine records it in
// the run history, and a node that rewrote it in place would corrupt the trail.

// Conversion directions, kept as the names a config saved before the split
// used, so a stale one can be recognized and reported.
const (
	parserDecode = "decode"
	parserEncode = "encode"
)

// converterConfig is the shape every decoder and encoder shares.
type converterConfig struct {
	Property string `json:"property"`
	Target   string `json:"target"`
	// Direction is no longer a setting. It is still read so a graph saved when
	// one node did both jobs fails loudly instead of quietly converting the
	// wrong way.
	Direction string `json:"direction"`
}

// requireDirection rejects a config left over from the single-node converters
// whose direction disagrees with the node it now sits on. An automatic or
// absent direction is accepted: it never named a direction to contradict.
func requireDirection(direction, want string) error {
	switch strings.TrimSpace(direction) {
	case "", "auto", want:
		return nil
	}
	return fmt.Errorf("this node only %ss; its saved configuration asks to %s, which is a separate node type now",
		want, strings.TrimSpace(direction))
}

// converterInput is the port a converter reads from, typed for the value it
// needs. The rule tracks the property being read: a converter pointed at the
// topic or metadata says nothing about the payload, and one pointed inside the
// payload says only that it is an object.
func converterInput(portType string) []engine.PortSpec {
	return []engine.PortSpec{{
		ID: "in", Label: "In", Type: portType,
		TypeRule: &flow.PortTypeRule{PayloadPath: []string{"property"}},
	}}
}

// converterOutput is the port a converter writes to. An empty target writes
// back over the property that was read, so the rule falls back to it.
func converterOutput(portType string) []engine.PortSpec {
	// TypeFrom covers the off-payload case: a converter writing its result to
	// the topic or metadata passes the payload through untouched, so the port
	// carries the upstream type instead of shrugging. On-payload targets
	// resolve to a concrete type, which wins over TypeFrom.
	return []engine.PortSpec{{
		ID: "out", Label: "Out", Type: portType, TypeFrom: "in",
		TypeRule: &flow.PortTypeRule{PayloadPath: []string{"target", "property"}},
	}}
}

// parserSourceField is the shared "which property to convert" config field.
func parserSourceField(placeholder string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "property",
		Label:       "Property",
		Type:        flow.FieldString,
		Default:     "payload",
		Placeholder: placeholder,
		Help:        "The value to convert. Dot path into the message; a bare path is read from the payload.",
	}
}

// parserTargetField is the shared "where the result goes" config field.
func parserTargetField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "target",
		Label:       "Write the result to",
		Type:        flow.FieldString,
		Placeholder: "payload",
		Help:        "Dot path into the message. Empty overwrites the property that was converted.",
	}
}

// parserInput resolves the value a format node converts, reporting a missing
// property rather than silently converting nothing.
func parserInput(msg *engine.Msg, property string) (string, any, error) {
	property = strings.TrimSpace(property)
	if property == "" {
		property = "payload"
	}
	value, found := resolveProperty(msg, property)
	if !found {
		return property, nil, fmt.Errorf("property %q is not present on the message", property)
	}
	return property, value, nil
}

// parserText reads the value as the text a decoder needs.
func parserText(value any, property string) (string, error) {
	switch t := value.(type) {
	case string:
		return t, nil
	case []byte:
		return string(t), nil
	default:
		return "", fmt.Errorf("property %q is %T, not text to decode", property, value)
	}
}

// parserResult builds the output message with the converted value written to
// the target property, leaving the input message untouched.
func parserResult(msg *engine.Msg, source, target string, value any) (*engine.Msg, error) {
	out := &engine.Msg{
		Payload: deepCopyValue(msg.Payload),
		Topic:   msg.Topic,
		Meta:    nodeutil.CloneMeta(msg.Meta),
		Group:   msg.Group,
	}
	dest := strings.TrimSpace(target)
	if dest == "" {
		dest = source
	}
	if err := setProperty(out, dest, value); err != nil {
		return nil, fmt.Errorf("cannot write the result to %q: %w", dest, err)
	}
	return out, nil
}

// parserFanOut emits one message per item under a shared group frame, so the
// downstream subgraph runs once per row or element and a fan-in node further
// down can put them back together.
func parserFanOut(msg *engine.Msg, source, target string, items []any) (map[string][]*engine.Msg, error) {
	if len(items) > maxSplitParts {
		return nil, fmt.Errorf("conversion would produce %d messages, over the %d limit", len(items), maxSplitParts)
	}
	dest := strings.TrimSpace(target)
	if dest == "" {
		dest = source
	}
	groupID := uuid.New().String()
	out := make([]*engine.Msg, 0, len(items))
	for i, item := range items {
		part := &engine.Msg{
			Payload: deepCopyValue(msg.Payload),
			Topic:   msg.Topic,
			Meta:    nodeutil.CloneMeta(msg.Meta),
			Group:   msg.PushGroup(groupID, i, len(items)),
		}
		if err := setProperty(part, dest, item); err != nil {
			return nil, fmt.Errorf("cannot write the result to %q: %w", dest, err)
		}
		out = append(out, part)
	}
	return map[string][]*engine.Msg{"out": out}, nil
}

// normalizeParsed rewrites a decoded document into the types the rest of the
// engine works in: expressions, scripts, and run history all assume JSON-shaped
// values, and YAML in particular hands back maps keyed by any type.
func normalizeParsed(v any) any {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = normalizeParsed(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[stringify(k)] = normalizeParsed(val)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = normalizeParsed(val)
		}
		return out
	case int:
		return float64(t)
	case int64:
		return float64(t)
	case uint64:
		return float64(t)
	case float32:
		return float64(t)
	default:
		return v
	}
}
