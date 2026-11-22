package core

import (
	"context"
	"errors"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// Template formats. Mustache renders {{ }} segments; plain text passes the
// template through untouched, which is how a flow sets a constant without
// escaping braces that happen to appear in it.
const (
	templateMustache = "mustache"
	templatePlain    = "plain"
)

// templateWritesPayload matches a template node that renders the given output
// type straight into the payload: the message scope, writing the payload
// itself. Anywhere else — a variable scope, a nested property, the topic — the
// payload comes out the way it went in.
func templateWritesPayload(outputType string) map[string][]string {
	match := map[string][]string{
		"scope":         {"", engine.ScopeMsg},
		"property":      {"", "payload"},
		"output_format": {outputType},
	}
	// An unconfigured output type coerces to a string, so the string case
	// also covers a node whose config never set the field.
	if outputType == outputString {
		match["output_format"] = []string{"", outputString}
	}
	return match
}

// TemplateNode renders text and writes the result to a scoped destination.
// What it produces is decided by three settings that compose: the format says
// whether {{ }} is rendered, the output type says how the rendered text is
// read, and the scope and property say where the value lands. Rendering is
// deterministic, so the node is not retryable: a bad template or unparseable
// output fails the same way every attempt.
type TemplateNode struct{}

func (TemplateNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "template",
		Label:       "Template",
		Folder:      flow.FolderLogic,
		Description: "Renders text and writes it to a message property or variable",
		Icon:        "las la-file-alt",
		Inputs: []engine.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in",
			// The rendered text lands in the payload as the chosen type, but
			// only when it is the payload being written. A message-scope write
			// anywhere else classifies its own path: a nested property makes
			// the payload an object, the topic or metadata leaves it alone.
			// Variable scopes fall to the default, so the untouched payload
			// carries the upstream type onward.
			TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: templateWritesPayload(outputString), Type: flow.PortString},
					{Match: templateWritesPayload(outputInteger), Type: flow.PortInteger},
					{Match: templateWritesPayload(outputFloat), Type: flow.PortFloat},
					{Match: templateWritesPayload(outputBoolean), Type: flow.PortBool},
					{Match: templateWritesPayload(outputJSON), Type: flow.PortObject},
					{Match: templateWritesPayload(outputYAML), Type: flow.PortObject},
					{
						Match:       map[string][]string{"scope": {"", engine.ScopeMsg}},
						Type:        flow.PortObject,
						PayloadPath: []string{"property"},
					},
				},
				Default: flow.PortAny,
			},
		}},
		Pure: true,
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "template",
				Label:       "Template",
				Type:        flow.FieldText,
				Required:    true,
				Placeholder: "Temperature is {{ payload.temperature }}C",
				Help:        "Expressions can access payload, topic, meta, msg, nodes.<id> for other nodes' outputs, and the vars, flow, and global variable scopes.",
			},
			{
				ID:      "format",
				Label:   "Format",
				Type:    flow.FieldSelect,
				Default: templateMustache,
				Options: []engine.Option{
					{Value: templateMustache, Label: "Mustache template"},
					{Value: templatePlain, Label: "Plain text"},
				},
				Help: "Mustache renders {{ }} segments as expressions. Plain text emits the template exactly as written.",
			},
			outputTypeField(outputString),
			scopeField(),
			scopePropertyField(),
		},
	}
}

// templateConfig is the decoded template node configuration.
type templateConfig struct {
	Template   string `json:"template"`
	Format     string `json:"format"`
	OutputType string `json:"output_format"`
	Scope      string `json:"scope"`
	Property   string `json:"property"`
}

// Execute renders the template and writes the result to the configured
// destination, passing topic and meta through unchanged.
func (TemplateNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg templateConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if cfg.Template == "" {
		return nil, errors.New("template node has no template")
	}

	msg := inbound(ec, inputs)

	// Render. Plain text skips interpolation entirely rather than escaping,
	// so a template full of braces needs no special handling.
	rendered := cfg.Template
	if cfg.Format != templatePlain {
		var err error
		rendered, err = ec.Interpolate(cfg.Template, msg)
		if err != nil {
			return nil, err
		}
	}

	// Read the rendered text as the configured type.
	value, err := coerceOutput(rendered, cfg.OutputType)
	if err != nil {
		return nil, err
	}

	// Write it to the configured scope and property.
	out, err := writeScoped(ec, cfg.Scope, cfg.Property, msg, value)
	if err != nil {
		return nil, err
	}
	return engine.One("out", out), nil
}
