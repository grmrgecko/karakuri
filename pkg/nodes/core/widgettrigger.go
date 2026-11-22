package core

import (
	"context"
	"fmt"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
)

// WidgetTriggerNode is the dashboard's way into a flow: a named entry point a
// dashboard widget presses. Unlike a webhook it needs no shared secret and
// unlike a test run it names the trigger the press starts from, so a button on
// a wall tablet fires exactly the branch its author picked out of a list.
//
// Nothing is armed for it: the press arrives at the API, which resolves the
// name against the flow's deployed graph and starts the run there. A trigger
// therefore only exists for a dashboard once the flow is deployed and enabled.
type WidgetTriggerNode struct{}

func (WidgetTriggerNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              flow.NodeTypeWidgetTrigger,
		Label:             "Widget Trigger",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderDashboard,
		Description:       "Starts a run when a dashboard widget presses this trigger by name",
		Icon:              "las la-hand-pointer",
		TestWithoutParams: true,
		// A press carries a JSON object of button parameters, and the endpoint
		// answers 400 to anything else, so the port is a promise rather than a
		// guess about what the dashboard sent.
		Outputs: []engine.PortSpec{{
			ID: "out", Label: "Out", Type: flow.PortObject,
		}},
		ConfigSpec: []engine.FieldSpec{
			{
				ID:          "name",
				Label:       "Trigger name",
				Type:        flow.FieldString,
				Required:    true,
				Placeholder: "Movie night",
				Help: "What a dashboard widget presses this trigger by. It is listed under the " +
					"flow's name in the widget's trigger picker, so name it after what pressing " +
					"it does. Two triggers in one flow may not share a name.",
			},
			{
				ID:      "allow_public",
				Label:   "Allow anonymous viewers",
				Type:    flow.FieldBoolean,
				Default: false,
				Help: "Let anyone viewing a public dashboard press this trigger. Leave it off and " +
					"a press has to come from a signed-in user holding api:flows:trigger.",
			},
			{
				ID:    "test_params",
				Label: "Test parameters",
				Type:  flow.FieldJSON,
				Help: "Object the Test Run button seeds this trigger with, standing in for the " +
					"parameters a widget would send. A press with no parameters arrives as an " +
					"empty object either way.",
			},
		},
	}
}

// widgetTriggerConfig is the decoded widget trigger node configuration.
type widgetTriggerConfig struct {
	Name string `json:"name"`
}

// Execute passes the press through: the API seeds the run with the widget's
// parameters, and a test run with the configured test payload. Either way the
// message carries the trigger's identity in its metadata, so a flow with
// several widget triggers can tell which button was pressed.
func (WidgetTriggerNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg widgetTriggerConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}

	out := &engine.Msg{
		Payload: map[string]any{},
		Meta: map[string]any{
			"source":  "widget",
			"trigger": cfg.Name,
		},
	}

	msg := inputs[engine.TriggerInput]
	if msg != nil {
		out.ID = msg.ID
		out.Topic = msg.Topic
		out.Group = msg.Group
		for k, v := range msg.Meta {
			out.Meta[k] = v
		}
		if msg.Payload != nil {
			// The declared output type is an object; the press endpoint enforces
			// that, so anything else came from test parameters and is refused
			// here rather than passed downstream as the wrong shape.
			params, ok := msg.Payload.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("widget trigger parameters must be a JSON object, got %T", msg.Payload)
			}
			out.Payload = params
		}
	}
	return engine.One("out", out), nil
}
