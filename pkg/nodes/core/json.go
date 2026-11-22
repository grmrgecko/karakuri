package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// JSONNode reads a JSON document into message data. It is the node a webhook
// body or an MQTT payload passes through on its way to being something a
// switch or a script can work with.
type JSONNode struct{}

func (JSONNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "json",
		Label:       "JSON",
		Folder:      flow.FolderParsers,
		Description: "Parses a JSON document into message data",
		Icon:        "las la-code",
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
func (JSONNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
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

	var result any
	if err := json.Unmarshal([]byte(text), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON in %q: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, result)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// JSONEncodeNode serializes message data as a JSON document, for a value on
// its way out over HTTP, MQTT, or into a file.
type JSONEncodeNode struct{}

func (JSONEncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "json-encode",
		Label:       "JSON Encode",
		Folder:      flow.FolderEncoders,
		Description: "Serializes message data as a JSON document",
		Icon:        "las la-code",
		// Anything JSON can represent may be encoded, so the input stays
		// undeclared where every other converter names a type.
		Inputs:  converterInput(flow.PortAny),
		Outputs: converterOutput(flow.PortString),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "indent",
				Label:   "Indent",
				Type:    flow.FieldSelect,
				Default: "",
				Options: []engine.Option{
					{Value: "", Label: "None, one line"},
					{Value: "2", Label: "Two spaces"},
					{Value: "4", Label: "Four spaces"},
					{Value: "tab", Label: "A tab"},
				},
			},
		},
	}
}

// jsonEncodeConfig is the decoded json-encode node configuration.
type jsonEncodeConfig struct {
	converterConfig
	Indent string `json:"indent"`
}

// Execute serializes the configured property.
func (JSONEncodeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg jsonEncodeConfig
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
	text, err := encodeJSON(value, cfg.Indent)
	if err != nil {
		return nil, fmt.Errorf("cannot serialize %q as JSON: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, text)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// encodeJSON serializes a value, leaving HTML characters unescaped so the
// output reads the way it was written.
func encodeJSON(value any, indent string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	switch indent {
	case "":
	case "tab":
		enc.SetIndent("", "\t")
	case "2":
		enc.SetIndent("", "  ")
	case "4":
		enc.SetIndent("", "    ")
	default:
		return "", fmt.Errorf("unknown indent %q", indent)
	}
	if err := enc.Encode(value); err != nil {
		return "", err
	}
	// Encode always appends a newline, which is not part of the document.
	return strings.TrimSuffix(buf.String(), "\n"), nil
}
