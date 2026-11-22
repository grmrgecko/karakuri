package core

import (
	"context"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// FaceplateNode is one tab of settings shown on the flow's page. Each control
// is a value output: the node runs only when a consumer pulls one, and emits
// every control's applied value, or its default when none was applied.
type FaceplateNode struct{}

func (FaceplateNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        flow.NodeTypeFaceplate,
		Label:       "Faceplate",
		Folder:      flow.FolderValues,
		Description: "A tab of settings on the flow's page; each control is a value output",
		Icon:        "las la-sliders-h",
		// The outputs are the controls, so the palette offers the node for any
		// value type a drag is looking for.
		DynamicPorts:      true,
		Pure:              true,
		TestWithoutParams: true,
		ConfigSpec: []engine.FieldSpec{
			{ID: "name", Label: "Tab name", Type: flow.FieldString, Required: true, NoParam: true, Placeholder: "Settings", Help: "Shown on the flow page's tab bar."},
			{ID: "controls", Label: "Controls", Type: flow.FieldFaceplateControls, ValueSchema: flow.FaceplateControlsSchema(), NoParam: true, Help: "Each control becomes a value output. Wire it into the setting it drives."},
			{ID: "script", Label: "Change script", Type: flow.FieldScript, NoParam: true, Placeholder: "// faceplate.onChange((change) => { ... })", Help: "Runs in the browser when a faceplate value changes, and may change other controls with faceplate.set(tab, control, value)."},
			{ID: "values", Label: "Applied values", Type: flow.FieldJSON, Control: "hidden", NoParam: true, Help: "Set from the faceplate tab and applied with Apply."},
			{ID: "order", Label: "Tab order", Type: flow.FieldNumber, Control: "hidden", NoParam: true},
		},
	}
}

// Execute emits each control's value on its own port.
func (FaceplateNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg flow.FaceplateConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	out := make(map[string][]*engine.Msg, len(cfg.Controls))
	for _, c := range cfg.Controls {
		if c.ID == "" {
			continue
		}
		out[c.ID] = []*engine.Msg{{Payload: cfg.ControlValue(c)}}
	}
	return out, nil
}
