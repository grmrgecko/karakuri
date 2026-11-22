package core

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Base64Node decodes base64 text into binary data. It is how bytes enter a
// flow from anything that carries them as text: a JSON field holding an image,
// a webhook body from a device that encodes its payload, a base64 column in a
// CSV file.
type Base64Node struct{}

func (Base64Node) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "base64",
		Label:       "Base64",
		Folder:      flow.FolderParsers,
		Description: "Decodes base64 text into binary data",
		Icon:        "las la-file-code",
		Inputs:      converterInput(flow.PortString),
		Outputs:     converterOutput(flow.PortBinary),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "encoding",
				Label:   "Alphabet",
				Type:    flow.FieldSelect,
				Default: base64Standard,
				Options: []engine.Option{
					{Value: base64Standard, Label: "Standard"},
					{Value: base64URL, Label: "URL-safe (- and _)"},
				},
				Help: "Padding is optional either way: text with or without trailing = is accepted.",
			},
		},
	}
}

// Base64EncodeNode encodes binary data or text as base64.
type Base64EncodeNode struct{}

func (Base64EncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "base64-encode",
		Label:       "Base64 Encode",
		Folder:      flow.FolderEncoders,
		Description: "Encodes binary data or text as base64",
		Icon:        "las la-file-code",
		// The encoder takes bytes or text and nothing else. Declaring string
		// accepts both — binary widens to string — while refusing the objects
		// and numbers the runtime rejects anyway.
		Inputs:  converterInput(flow.PortString),
		Outputs: converterOutput(flow.PortString),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			{
				ID:      "encoding",
				Label:   "Alphabet",
				Type:    flow.FieldSelect,
				Default: base64Standard,
				Options: []engine.Option{
					{Value: base64Standard, Label: "Standard"},
					{Value: base64URL, Label: "URL-safe (- and _)"},
				},
			},
			{
				ID:      "padding",
				Label:   "Pad the result",
				Type:    flow.FieldBoolean,
				Default: true,
				Help:    "Off drops the trailing = characters, which some APIs require.",
			},
		},
	}
}

// Base64 alphabets a node may use.
const (
	base64Standard = "standard"
	base64URL      = "url"
)

// base64Config is the decoded configuration both base64 nodes share.
type base64Config struct {
	converterConfig
	Encoding string `json:"encoding"`
	Padding  *bool  `json:"padding"`
}

// padded reports whether the encoder writes trailing padding, defaulting to
// yes since that is what a plain base64 document has.
func (c base64Config) padded() bool { return c.Padding == nil || *c.Padding }

// Execute decodes the configured property.
func (Base64Node) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg base64Config
	if err := ec.Config(&cfg); err != nil {
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
	decoded, err := decodeBase64(text, cfg.Encoding)
	if err != nil {
		return nil, fmt.Errorf("invalid base64 in %q: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, decoded)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// Execute encodes the configured property.
func (Base64EncodeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg base64Config
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	property, value, err := parserInput(msg, cfg.Property)
	if err != nil {
		return nil, err
	}
	raw, err := base64Source(value, property)
	if err != nil {
		return nil, err
	}
	enc, err := base64Alphabet(cfg.Encoding, cfg.padded())
	if err != nil {
		return nil, err
	}

	out, err := parserResult(msg, property, cfg.Target, enc.EncodeToString(raw))
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// base64Source reads the bytes to encode. Text is encoded as it is written;
// anything else has no byte form a flow author would recognize, so it is
// reported rather than guessed at.
func base64Source(value any, property string) ([]byte, error) {
	switch t := value.(type) {
	case []byte:
		return t, nil
	case string:
		return []byte(t), nil
	default:
		return nil, fmt.Errorf("property %q is %T, not binary or text to encode", property, value)
	}
}

// decodeBase64 reads text in the configured alphabet. Padding is optional on
// the way in: trailing = is stripped and the unpadded encoding used, so a
// document decodes the same whether or not whoever wrote it padded.
func decodeBase64(text, encoding string) ([]byte, error) {
	enc, err := base64Alphabet(encoding, false)
	if err != nil {
		return nil, err
	}
	return enc.DecodeString(strings.TrimRight(strings.TrimSpace(text), "="))
}

// base64Alphabet resolves the encoding a node is configured for.
func base64Alphabet(encoding string, padded bool) (*base64.Encoding, error) {
	switch encoding {
	case "", base64Standard:
		if padded {
			return base64.StdEncoding, nil
		}
		return base64.RawStdEncoding, nil
	case base64URL:
		if padded {
			return base64.URLEncoding, nil
		}
		return base64.RawURLEncoding, nil
	default:
		return nil, fmt.Errorf("unknown base64 alphabet %q", encoding)
	}
}
