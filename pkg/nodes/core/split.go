package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/grmrgecko/karakuri/internal/nodeutil"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// maxSplitParts caps how many messages one split may produce. Every part runs
// the downstream subgraph, so an accidental split of a huge array would flood
// the engine; failing loudly is better than a run that never ends.
const maxSplitParts = 10000

// SplitNode turns one message into several: each element of an array, each key
// of an object, or each chunk of a string becomes its own message, and the
// downstream subgraph runs once per part. Every part carries a group frame so a
// join, batch, or aggregate node further down can put them back together.
type SplitNode struct{}

func (SplitNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "split",
		Label:       "Split",
		Folder:      flow.FolderSequence,
		Description: "Splits one message into a message per element, key, or chunk",
		Icon:        "las la-expand-arrows-alt",
		Inputs: []engine.PortSpec{{
			ID: "in", Label: "In", Type: flow.PortObject,
			// Splitting elements or keys needs a decoded document; splitting on
			// a separator needs text.
			TypeRule: &flow.PortTypeRule{
				Cases:       []flow.PortTypeCase{{Match: map[string][]string{"mode": {"string"}}, Type: flow.PortString}},
				Default:     flow.PortObject,
				PayloadPath: []string{"property"},
			},
		}},
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortAny,
			// A chunk of a split string is text; an element or a value from an
			// array or object is whatever it happened to be.
			TypeRule: &flow.PortTypeRule{
				Cases:   []flow.PortTypeCase{{Match: map[string][]string{"mode": {"string"}}, Type: flow.PortString}},
				Default: flow.PortAny,
			},
		}},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:      "mode",
				Label:   "Split",
				Type:    flow.FieldSelect,
				Default: "array",
				Options: []engine.Option{
					{Value: "array", Label: "Array elements"},
					{Value: "object", Label: "Object keys"},
					{Value: "string", Label: "String on a separator"},
				},
				Help: "What the value is split into. Naming it rather than deciding by what turns " +
					"up is what lets the input port say what this node accepts.",
			},
			{
				ID:          "property",
				Label:       "Property",
				Type:        flow.FieldString,
				Default:     "payload",
				Placeholder: "payload.items",
				Help:        "The value to split. Dot path into the message; a bare path is read from the payload.",
			},
			{
				ID:          "separator",
				Label:       "Separator",
				Type:        flow.FieldString,
				Default:     "\\n",
				ShowIf:      &flow.ShowIf{Field: "mode", Equals: "string"},
				Placeholder: "\\n",
				Help:        "Where to cut the string. \\n, \\r\\n, and \\t are understood.",
			},
			{
				ID:      "key_property",
				Label:   "Put the key in",
				Type:    flow.FieldString,
				Default: "topic",
				ShowIf:  &flow.ShowIf{Field: "mode", Equals: "object"},
				Help:    "Where each key is written on its message: topic, or a meta.* or payload.* path.",
			},
		},
	}
}

// splitConfig is the decoded split node configuration.
type splitConfig struct {
	Mode        string `json:"mode"`
	Property    string `json:"property"`
	Separator   string `json:"separator"`
	KeyProperty string `json:"key_property"`
}

// Execute splits the configured value and emits one message per part.
func (SplitNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg splitConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property := strings.TrimSpace(cfg.Property)
	if property == "" {
		property = "payload"
	}
	value, found := resolveProperty(msg, property)
	if !found {
		return nil, fmt.Errorf("property %q is not present on the message", property)
	}

	// Build the parts as payload/key pairs; the key is only used for objects.
	type part struct {
		payload any
		key     string
	}
	var parts []part

	// An unset mode is the default the field declares, which is what a config
	// saved before the modes were made explicit read as.
	mode := cfg.Mode
	if mode == "" || mode == "auto" {
		mode = "array"
	}

	switch mode {
	case "array":
		items, ok := value.([]any)
		if !ok {
			return nil, fmt.Errorf("property %q is %T, not an array", property, value)
		}
		for _, item := range items {
			parts = append(parts, part{payload: item})
		}

	case "object":
		obj, ok := value.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("property %q is %T, not an object", property, value)
		}
		// Sorted so the parts of one object are always numbered the same way.
		keys := make([]string, 0, len(obj))
		for k := range obj {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			parts = append(parts, part{payload: obj[k], key: k})
		}

	case "string":
		text, ok := value.(string)
		if !ok {
			text = stringify(value)
		}
		sep := unescapeSeparator(cfg.Separator)
		if sep == "" {
			sep = "\n"
		}
		for _, chunk := range strings.Split(text, sep) {
			parts = append(parts, part{payload: chunk})
		}

	default:
		return nil, fmt.Errorf("unknown split mode %q", cfg.Mode)
	}

	if len(parts) == 0 {
		// Nothing to split: no parts means no downstream work, which is a valid
		// outcome for an empty list.
		ec.Debug(value, "split produced no parts")
		return nil, nil
	}
	if len(parts) > maxSplitParts {
		return nil, fmt.Errorf("split would produce %d messages, over the %d limit", len(parts), maxSplitParts)
	}

	// Stamp every part with a shared group frame so a fan-in node downstream
	// can tell how many to expect.
	groupID := uuid.New().String()
	keyProperty := strings.TrimSpace(cfg.KeyProperty)
	if keyProperty == "" {
		keyProperty = "topic"
	}
	out := make([]*engine.Msg, 0, len(parts))
	for i, p := range parts {
		payload := p.payload
		if mode == "object" && keyProperty != "topic" {
			// The key may be written into the entry itself, and the entry
			// still belongs to the message being split.
			payload = deepCopyValue(payload)
		}
		partMsg := &engine.Msg{
			Payload: payload,
			Topic:   msg.Topic,
			Meta:    nodeutil.CloneMeta(msg.Meta),
			Group:   msg.PushGroup(groupID, i, len(parts)),
		}
		if mode == "object" {
			if err := setProperty(partMsg, keyProperty, p.key); err != nil {
				return nil, fmt.Errorf("cannot write the key to %q: %w", keyProperty, err)
			}
		}
		out = append(out, partMsg)
	}
	return map[string][]*engine.Msg{"out": out}, nil
}

// unescapeSeparator turns the escape sequences a user can type into a plain
// text field into the bytes they mean.
func unescapeSeparator(s string) string {
	if s == "" {
		return ""
	}
	replacer := strings.NewReplacer(`\n`, "\n", `\r`, "\r", `\t`, "\t", `\\`, `\`)
	return replacer.Replace(s)
}
