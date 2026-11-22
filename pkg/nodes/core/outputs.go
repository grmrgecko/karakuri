package core

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Output types a node can produce from rendered text, re-exported from
// pkg/flow. A node that builds a value out of text needs to say what that text
// means: "3" is a string to one flow and a number to the next, and only the
// author knows which. The names live in pkg/flow because they also decide the
// port type a node advertises, which validation and the editor both read.
const (
	outputString  = flow.PayloadString
	outputInteger = flow.PayloadInteger
	outputFloat   = flow.PayloadFloat
	outputBoolean = flow.PayloadBoolean
	outputJSON    = flow.PayloadJSON
	outputYAML    = flow.PayloadYAML
)

// outputTypeField is the shared "what type is this" config field. The ID stays
// output_format, the name it had when the choice was only string or JSON, so
// already-deployed configs keep their setting. The default is per-node: each
// caller passes the type its node produced before the setting existed, so
// adding the field changes nothing for a config that predates it.
// payloadTypeRule derives a port's type from the output_format field, for
// nodes that emit a configured payload. An unset format resolves to "any":
// the text is read as JSON when it parses and kept as text when it does not
// (coerceConfigValue), so the type is not knowable from the config alone.
func payloadTypeRule() *flow.PortTypeRule {
	return &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{
			{Match: map[string][]string{"output_format": {outputString}}, Type: flow.PortString},
			{Match: map[string][]string{"output_format": {outputInteger}}, Type: flow.PortInteger},
			{Match: map[string][]string{"output_format": {outputFloat}}, Type: flow.PortFloat},
			{Match: map[string][]string{"output_format": {outputBoolean}}, Type: flow.PortBool},
			{Match: map[string][]string{"output_format": {outputJSON, outputYAML}}, Type: flow.PortObject},
		},
		Default: flow.PortAny,
	}
}

func outputTypeField(def string) engine.FieldSpec {
	return engine.FieldSpec{
		ID:      "output_format",
		Label:   "Output type",
		Type:    flow.FieldSelect,
		Default: def,
		Options: []engine.Option{
			{Value: outputString, Label: "String"},
			{Value: outputInteger, Label: "Integer"},
			{Value: outputFloat, Label: "Float"},
			{Value: outputBoolean, Label: "Boolean"},
			{Value: outputJSON, Label: "Object (JSON)"},
			{Value: outputYAML, Label: "Object (YAML)"},
		},
		Help: "How the rendered text is read, and the type the output port advertises. JSON and YAML parse it into an object, list, number, boolean, or null; the others convert the text itself and fail when it does not parse. A parameter asking for a number takes either Integer or Float.",
	}
}

// coerceConfigValue resolves a config value that an author types as text but
// that older configs stored as a decoded JSON value.
//
// Anything that is not a JSON string was stored structurally by the JSON
// editor these fields used before the type setting existed, and is used as-is:
// that is what the JSON type would have produced from the same document.
//
// A JSON string is ambiguous — it is either text the author typed into the new
// field, or a legacy payload that genuinely was a string. An empty kind means
// no type was chosen, which is exactly the state a config predating the
// setting is in, so there the JSON reading is tried and the string kept when it
// does not parse. An explicit kind is the author's own choice and is enforced
// strictly, so a mistyped value fails rather than passing a surprise on.
func coerceConfigValue(raw json.RawMessage, kind string) (any, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		var decoded any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			return nil, fmt.Errorf("invalid config value: %w", err)
		}
		return normalizeParsed(decoded), nil
	}

	if kind == "" {
		if value, err := coerceOutput(text, outputJSON); err == nil {
			return value, nil
		}
		return text, nil
	}
	return coerceOutput(text, kind)
}

// coerceOutput converts rendered text into the configured type. Conversion
// failures are errors rather than fallbacks: a flow that asked for a number
// and got "n/a" should stop, not carry a surprise downstream.
func coerceOutput(text, kind string) (any, error) {
	switch strings.TrimSpace(kind) {
	case "", outputString:
		return text, nil

	case outputInteger:
		n, err := strconv.ParseInt(strings.TrimSpace(text), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("output %q is not an integer", text)
		}
		// The engine works in JSON-shaped values throughout, where every
		// number is a float64; returning int64 here would read back as a
		// different type than the same value arriving over a webhook.
		return float64(n), nil

	case outputFloat:
		// ParseFloat also reads "NaN" and "Inf", which JSON cannot encode, so
		// a payload holding one could not be stored or sent on.
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, fmt.Errorf("output %q is not a number", text)
		}
		return f, nil

	case outputBoolean:
		b, err := strconv.ParseBool(strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("output %q is not a boolean", text)
		}
		return b, nil

	case outputJSON:
		var parsed any
		if err := json.Unmarshal([]byte(text), &parsed); err != nil {
			return nil, fmt.Errorf("output is not valid JSON: %w", err)
		}
		return normalizeParsed(parsed), nil

	case outputYAML:
		parsed, err := decodeYAML(text)
		if err != nil {
			return nil, fmt.Errorf("output is not valid YAML: %w", err)
		}
		return parsed, nil

	default:
		return nil, fmt.Errorf("unknown output type %q", kind)
	}
}
