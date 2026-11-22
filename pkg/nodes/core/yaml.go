package core

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"gopkg.in/yaml.v3"
)

// YAMLNode reads a YAML document into message data. Decoded values are
// normalized to the JSON-shaped types the rest of the engine works in, so what
// a YAML document produces behaves like what a JSON one does.
type YAMLNode struct{}

func (YAMLNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "yaml",
		Label:       "YAML",
		Folder:      flow.FolderParsers,
		Description: "Parses a YAML document into message data",
		Icon:        "las la-file-alt",
		Inputs:      converterInput(flow.PortString),
		Outputs:     converterOutput(flow.PortObject),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
		},
	}
}

// Execute parses the configured property.
func (YAMLNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg converterConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if err := requireDirection(cfg.Direction, parserDecode); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	text, err := parserText(value, property)
	if err != nil {
		return nil, err
	}
	decoded, err := decodeYAML(text)
	if err != nil {
		return nil, fmt.Errorf("invalid YAML in %q: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, decoded)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// YAMLEncodeNode serializes message data as a YAML document.
type YAMLEncodeNode struct{}

func (YAMLEncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "yaml-encode",
		Label:       "YAML Encode",
		Folder:      flow.FolderEncoders,
		Description: "Serializes message data as a YAML document",
		Icon:        "las la-file-alt",
		// Anything YAML can represent may be encoded, so the input stays
		// undeclared where every other converter names a type.
		Inputs:  converterInput(flow.PortAny),
		Outputs: converterOutput(flow.PortString),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:          "indent",
				Label:       "Indent",
				Type:        flow.FieldString,
				Default:     "2",
				Placeholder: "2",
				Help:        "Spaces per level.",
			},
		},
	}
}

// yamlEncodeConfig is the decoded yaml-encode node configuration.
type yamlEncodeConfig struct {
	converterConfig
	Indent string `json:"indent"`
}

// Execute serializes the configured property.
func (YAMLEncodeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg yamlEncodeConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if err := requireDirection(cfg.Direction, parserEncode); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	indent, err := parseYAMLIndent(cfg.Indent)
	if err != nil {
		return nil, err
	}
	text, err := encodeYAML(value, indent)
	if err != nil {
		return nil, fmt.Errorf("cannot serialize %q as YAML: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, text)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// parseYAMLIndent reads the indent field, defaulting to two spaces.
func parseYAMLIndent(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 2, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("indent %q is not a number", raw)
	}
	if n < 1 || n > 10 {
		return 0, fmt.Errorf("indent must be between 1 and 10, got %d", n)
	}
	return n, nil
}

// encodeYAML serializes a value at the requested indentation.
func encodeYAML(value any, indent int) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(indent)
	if err := enc.Encode(value); err != nil {
		enc.Close()
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// decodeYAML parses a document into JSON-shaped values. An unquoted date and a
// non-finite float are kept as the text the document holds: the library would
// decode them to a time value and to NaN or an infinity, neither of which run
// history, expressions, or an encoder downstream can carry.
func decodeYAML(text string) (any, error) {
	if err := yamlNestingWithin(text, maxYAMLDepth); err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	// An empty document leaves the node unset, and decoding one fails.
	if doc.Kind == 0 {
		return nil, nil
	}
	yamlKeepText(&doc)
	var decoded any
	if err := doc.Decode(&decoded); err != nil {
		return nil, err
	}
	return normalizeParsed(decoded), nil
}

// yamlKeepText retags the scalars under a node that have no JSON form as
// strings, so they decode as written. Keys are scalars in the same tree and
// are covered with the values.
func yamlKeepText(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode {
		switch n.ShortTag() {
		case "!!timestamp":
			n.Tag = "!!str"
		case "!!float":
			var f float64
			if err := n.Decode(&f); err == nil && (math.IsNaN(f) || math.IsInf(f, 0)) {
				n.Tag = "!!str"
			}
		}
		return
	}
	for _, child := range n.Content {
		yamlKeepText(child)
	}
}

// maxYAMLDepth bounds flow-collection nesting. yaml.v3 recurses once per level
// with no limit of its own, and "[" costs one byte per level, so an 8 MiB
// response could drive the goroutine stack past Go's ceiling; that is a fatal
// error no recover catches, and it ends the whole server.
const maxYAMLDepth = 1000

// yamlNestingWithin rejects a document whose bracket nesting exceeds limit.
// Brackets inside quoted scalars do not count; brackets in comments do, which
// only ever over-counts.
func yamlNestingWithin(text string, limit int) error {
	depth := 0
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '[' || c == '{':
			depth++
			if depth > limit {
				return fmt.Errorf("nested deeper than %d levels", limit)
			}
		case c == ']' || c == '}':
			if depth > 0 {
				depth--
			}
		}
	}
	return nil
}
