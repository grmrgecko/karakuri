package core

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"golang.org/x/net/html/charset"
)

// Default keys an XML element's attributes and text land under when a document
// is parsed, following the xml2js convention.
const (
	defaultXMLAttrKey = "$"
	defaultXMLTextKey = "_"
)

// XMLNode reads an XML document into message data. A parsed element becomes a
// map: its attributes under the attribute key, its own text under the text key,
// and each distinct child name under its own key. Elements with neither
// attributes nor children collapse to their text, so a document of simple
// fields reads as a plain object.
type XMLNode struct{}

func (XMLNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "xml",
		Label:       "XML",
		Folder:      flow.FolderParsers,
		Description: "Parses an XML document into message data",
		Icon:        "las la-sitemap",
		Inputs:      converterInput(flow.PortString),
		Outputs:     converterOutput(flow.PortObject),
		Pure:        true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			xmlAttributeKeyField(),
			xmlTextKeyField(),
			{
				ID:      "array_mode",
				Label:   "Repeated elements",
				Type:    flow.FieldSelect,
				Default: "always",
				Options: []engine.Option{
					{Value: "always", Label: "Always an array"},
					{Value: "auto", Label: "An array only when repeated"},
				},
				Help: "Always is predictable for expressions; auto is easier to read when most " +
					"elements appear once.",
			},
		},
	}
}

// XMLEncodeNode serializes message data as an XML document.
type XMLEncodeNode struct{}

func (XMLEncodeNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "xml-encode",
		Label:       "XML Encode",
		Folder:      flow.FolderEncoders,
		Description: "Serializes message data as an XML document",
		Icon:        "las la-sitemap",
		// An XML document is a tree of named elements, so without a configured
		// root there has to be a decoded document to write, not a bare scalar.
		// A root wraps whatever it is given — a string or number encodes as
		// the element's text — so setting one lifts the object requirement.
		Inputs: []engine.PortSpec{{
			ID: "in", Label: "In", Type: flow.PortObject,
			TypeRule: &flow.PortTypeRule{
				Cases:       []flow.PortTypeCase{{Match: map[string][]string{"root": {"*"}}, Type: flow.PortAny}},
				PayloadPath: []string{"property"},
			},
		}},
		Outputs: converterOutput(flow.PortString),
		Pure:    true,
		ConfigSpec: []engine.FieldSpec{
			parserSourceField("payload"),
			parserTargetField(),
			xmlAttributeKeyField(),
			xmlTextKeyField(),
			{
				ID:          "root",
				Label:       "Root element",
				Type:        flow.FieldString,
				Placeholder: "root",
				Help: "Wraps the data in this element. Empty uses the data's single top-level " +
					"key, which is what parsing produces.",
			},
			{
				ID:          "indent",
				Label:       "Indent",
				Type:        flow.FieldString,
				Placeholder: "2",
				Help:        "Spaces per level. Empty writes one line.",
			},
		},
	}
}

// xmlAttributeKeyField is the shared "where attributes live" config field.
func xmlAttributeKeyField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "attribute_key",
		Label:       "Attribute key",
		Type:        flow.FieldString,
		Default:     defaultXMLAttrKey,
		Placeholder: defaultXMLAttrKey,
		Help:        "The key an element's attributes are held under.",
	}
}

// xmlTextKeyField is the shared "where element text lives" config field.
func xmlTextKeyField() engine.FieldSpec {
	return engine.FieldSpec{
		ID:          "text_key",
		Label:       "Text key",
		Type:        flow.FieldString,
		Default:     defaultXMLTextKey,
		Placeholder: defaultXMLTextKey,
		Help:        "The key an element's own text is held under when it also has attributes or children.",
	}
}

// xmlConfig is the decoded configuration both XML nodes share.
type xmlConfig struct {
	converterConfig
	AttributeKey string `json:"attribute_key"`
	TextKey      string `json:"text_key"`
	ArrayMode    string `json:"array_mode"`
	Root         string `json:"root"`
	Indent       string `json:"indent"`
}

// keys returns the attribute and text keys, falling back to the defaults.
func (c xmlConfig) keys() (string, string) {
	attrKey := strings.TrimSpace(c.AttributeKey)
	if attrKey == "" {
		attrKey = defaultXMLAttrKey
	}
	textKey := strings.TrimSpace(c.TextKey)
	if textKey == "" {
		textKey = defaultXMLTextKey
	}
	return attrKey, textKey
}

// Execute parses the configured property.
func (XMLNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg xmlConfig
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
	alwaysArray, err := xmlAlwaysArray(cfg.ArrayMode)
	if err != nil {
		return nil, err
	}
	attrKey, textKey := cfg.keys()
	decoded, err := decodeXML(text, attrKey, textKey, alwaysArray)
	if err != nil {
		return nil, fmt.Errorf("invalid XML in %q: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, decoded)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// Execute serializes the configured property.
func (XMLEncodeNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg xmlConfig
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
	indent, err := parseXMLIndent(cfg.Indent)
	if err != nil {
		return nil, err
	}
	attrKey, textKey := cfg.keys()
	text, err := encodeXML(value, strings.TrimSpace(cfg.Root), attrKey, textKey, indent)
	if err != nil {
		return nil, fmt.Errorf("cannot serialize %q as XML: %w", property, err)
	}

	out, err := parserResult(msg, property, cfg.Target, text)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}

// xmlAlwaysArray resolves whether every child element becomes an array.
func xmlAlwaysArray(mode string) (bool, error) {
	switch mode {
	case "", "always":
		return true, nil
	case "auto":
		return false, nil
	default:
		return false, fmt.Errorf("unknown repeated-element mode %q", mode)
	}
}

// parseXMLIndent reads the indent field; empty means a single line.
func parseXMLIndent(raw string) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("indent %q is not a number", raw)
	}
	if n < 0 || n > 10 {
		return 0, fmt.Errorf("indent must be between 0 and 10, got %d", n)
	}
	return n, nil
}

// decodeXML parses a document into a map holding its root element.
func decodeXML(text, attrKey, textKey string, alwaysArray bool) (map[string]any, error) {
	dec := xml.NewDecoder(strings.NewReader(text))
	// Documents in the wild carry HTML entities XML does not define. Resolving
	// them is worth doing; staying strict about structure is worth keeping, so a
	// truncated or mismatched document fails instead of decoding to nonsense.
	dec.Entity = xml.HTMLEntity
	// A document may declare an encoding other than UTF-8, which the decoder
	// refuses outright unless it is told how to read one.
	dec.CharsetReader = charset.NewReaderLabel

	// Skip the prolog: comments, processing instructions, and stray whitespace
	// before the root element.
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil, errors.New("document has no root element")
		}
		if err != nil {
			return nil, err
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		value, err := decodeXMLElement(dec, start, attrKey, textKey, alwaysArray, 0)
		if err != nil {
			return nil, err
		}
		return map[string]any{start.Name.Local: value}, nil
	}
}

// maxXMLDepth bounds element nesting. Each level is one recursive call, and
// an open tag costs a few bytes, so an 8 MiB response of unclosed tags would
// drive the goroutine stack past Go's ceiling; that is a fatal error no
// recover catches, and it ends the whole server.
const maxXMLDepth = 1000

// decodeXMLElement builds one element's value, consuming tokens through its
// closing tag. depth is how many elements enclose this one.
func decodeXMLElement(dec *xml.Decoder, start xml.StartElement, attrKey, textKey string, alwaysArray bool, depth int) (any, error) {
	if depth > maxXMLDepth {
		return nil, fmt.Errorf("element <%s> is nested deeper than %d levels", start.Name.Local, maxXMLDepth)
	}
	attrs := make(map[string]any, len(start.Attr))
	for _, a := range start.Attr {
		attrs[a.Name.Local] = a.Value
	}
	children := map[string][]any{}
	var text strings.Builder

	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("element <%s> is not closed", start.Name.Local)
		}
		if err != nil {
			return nil, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			child, err := decodeXMLElement(dec, t, attrKey, textKey, alwaysArray, depth+1)
			if err != nil {
				return nil, err
			}
			children[t.Name.Local] = append(children[t.Name.Local], child)

		case xml.CharData:
			text.Write(t)

		case xml.EndElement:
			return finishXMLElement(attrs, children, text.String(), attrKey, textKey, alwaysArray), nil
		}
	}
}

// finishXMLElement assembles an element's value once its closing tag is read.
// An element with nothing but text collapses to that text.
func finishXMLElement(attrs map[string]any, children map[string][]any, text, attrKey, textKey string, alwaysArray bool) any {
	trimmed := strings.TrimSpace(text)
	if len(attrs) == 0 && len(children) == 0 {
		return trimmed
	}

	obj := make(map[string]any, len(children)+2)
	if len(attrs) > 0 {
		obj[attrKey] = attrs
	}
	if trimmed != "" {
		obj[textKey] = trimmed
	}
	for name, values := range children {
		if alwaysArray || len(values) > 1 {
			obj[name] = values
			continue
		}
		obj[name] = values[0]
	}
	return obj
}

// encodeXML serializes message data back into a document. The data names its
// own root when it has exactly one top-level key, which is the shape parsing
// produces; anything else needs a root element configured.
func encodeXML(value any, root, attrKey, textKey string, indent int) (string, error) {
	name := root
	body := value
	if name == "" {
		obj, ok := value.(map[string]any)
		if !ok {
			return "", fmt.Errorf("a root element is required to serialize a %T", value)
		}
		if len(obj) != 1 {
			return "", fmt.Errorf("data has %d top-level keys, so a root element is required", len(obj))
		}
		for k, v := range obj {
			name, body = k, v
		}
	}

	var buf bytes.Buffer
	enc := xml.NewEncoder(&buf)
	if indent > 0 {
		enc.Indent("", strings.Repeat(" ", indent))
	}
	if err := writeXMLElement(enc, name, body, attrKey, textKey); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// writeXMLElement writes one element, or a repeat of it for each member of an
// array.
func writeXMLElement(enc *xml.Encoder, name string, value any, attrKey, textKey string) error {
	if !xmlNameOK(name) {
		return fmt.Errorf("%q is not a usable element name", name)
	}

	switch t := value.(type) {
	case []any:
		for _, item := range t {
			if err := writeXMLElement(enc, name, item, attrKey, textKey); err != nil {
				return err
			}
		}
		return nil

	case map[string]any:
		start := xml.StartElement{Name: xml.Name{Local: name}}
		if attrs, ok := t[attrKey].(map[string]any); ok {
			for _, k := range sortedKeys(attrs) {
				if !xmlNameOK(k) {
					return fmt.Errorf("%q is not a usable attribute name", k)
				}
				start.Attr = append(start.Attr, xml.Attr{Name: xml.Name{Local: k}, Value: stringify(attrs[k])})
			}
		}
		if err := enc.EncodeToken(start); err != nil {
			return err
		}
		if text, ok := t[textKey]; ok {
			if err := enc.EncodeToken(xml.CharData(stringify(text))); err != nil {
				return err
			}
		}
		for _, k := range sortedKeys(t) {
			if k == attrKey || k == textKey {
				continue
			}
			if err := writeXMLElement(enc, k, t[k], attrKey, textKey); err != nil {
				return err
			}
		}
		return enc.EncodeToken(start.End())

	default:
		start := xml.StartElement{Name: xml.Name{Local: name}}
		if err := enc.EncodeToken(start); err != nil {
			return err
		}
		if value != nil {
			if err := enc.EncodeToken(xml.CharData(stringify(value))); err != nil {
				return err
			}
		}
		return enc.EncodeToken(start.End())
	}
}

// xmlNameOK reports whether a map key can be written as an element or
// attribute name, so a bad key fails loudly instead of producing a document
// nothing can read back.
func xmlNameOK(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_', r == ':':
		case i > 0 && (r >= '0' && r <= '9' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

// sortedKeys returns a map's keys in a stable order, so the same data always
// serializes to the same document.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
