package agentnode

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// Output formats a process's stdout is read as. The author declares which,
// because a port that guesses what it carries cannot be checked: auto is the
// one exception and says so on the port, resolving to an undeclared payload.
const (
	FormatAuto   = "auto"
	FormatJSON   = "json"
	FormatText   = "text"
	FormatLines  = "lines"
	FormatBinary = "binary"
)

// FormatOptions lists the formats for a config field.
func FormatOptions() []nodeapi.Option {
	return []nodeapi.Option{
		{Value: FormatAuto, Label: "Auto (JSON if it parses, else text)"},
		{Value: FormatJSON, Label: "JSON"},
		{Value: FormatText, Label: "Text"},
		{Value: FormatLines, Label: "Text, as an array of lines"},
		{Value: FormatBinary, Label: "Binary"},
	}
}

// FormatRule derives the output port's payload type from the declared
// format, so a node reading stdout as JSON reports an object to the graph
// instead of leaving every downstream port undeclared. Auto falls through to
// the default: it is the case where the payload really is unknown until the
// program has run.
func FormatRule() *flow.PortTypeRule {
	return &flow.PortTypeRule{
		Cases: []flow.PortTypeCase{
			{Match: map[string][]string{"output_format": {FormatJSON}}, Type: flow.PortObject},
			{Match: map[string][]string{"output_format": {FormatText}}, Type: flow.PortString},
			{Match: map[string][]string{"output_format": {FormatLines}}, Type: flow.PortObject},
			{Match: map[string][]string{"output_format": {FormatBinary}}, Type: flow.PortBinary},
		},
		Default: flow.PortAny,
	}
}

// DecodeOutput turns captured stdout into a payload per the declared format,
// never guessing beyond what auto allows. Text trims the trailing newline in
// both conventions: a Windows program ends its output with CRLF, and leaving
// the carriage return on the payload puts an invisible byte in every
// comparison downstream.
func DecodeOutput(format string, stdout []byte) (any, error) {
	switch format {
	case "", FormatAuto:
		var v any
		if json.Unmarshal(stdout, &v) == nil {
			return v, nil
		}
		return strings.TrimRight(string(stdout), "\r\n"), nil
	case FormatJSON:
		var v any
		if err := json.Unmarshal(stdout, &v); err != nil {
			return nil, fmt.Errorf("stdout is not valid JSON: %w", err)
		}
		return v, nil
	case FormatText:
		return strings.TrimRight(string(stdout), "\r\n"), nil
	case FormatLines:
		return TextLines(string(stdout)), nil
	case FormatBinary:
		// Binary rides the payload as base64 text, the same shape a sandbox
		// script's binary output takes.
		return base64.StdEncoding.EncodeToString(stdout), nil
	default:
		return nil, fmt.Errorf("unknown output format %q", format)
	}
}

// TextLines splits captured text into lines, dropping the empty line a final
// newline leaves behind and tolerating CRLF.
func TextLines(text string) []any {
	text = strings.TrimSuffix(text, "\n")
	if text == "" {
		// The payload is sent as JSON, where a nil slice is null rather than
		// the empty array a lines port declares.
		return []any{}
	}
	parts := strings.Split(text, "\n")
	out := make([]any, 0, len(parts))
	for _, line := range parts {
		out = append(out, strings.TrimSuffix(line, "\r"))
	}
	return out
}
