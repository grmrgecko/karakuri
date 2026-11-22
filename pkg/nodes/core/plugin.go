package core

import (
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/influxdb"
	"github.com/grmrgecko/karakuri/pkg/integrations/notification"
	"github.com/grmrgecko/karakuri/pkg/integrations/oauth2conn"
	"github.com/grmrgecko/karakuri/pkg/integrations/postgresql"
	"github.com/grmrgecko/karakuri/pkg/integrations/prometheus"
	"github.com/grmrgecko/karakuri/pkg/integrations/socket"
	"github.com/grmrgecko/karakuri/pkg/plugin"
)

// The oauth2 auth mode reaches the driver through the structural
// oauth2TokenSource interface, which nothing checks at runtime until a
// request runs; this pins the match at compile time.
var _ oauth2TokenSource = (*oauth2conn.Driver)(nil)

// corePlugin contributes the built-in node set plus the general-purpose field
// controls every plugin can reference from its config specs.
type corePlugin struct{}

// Plugin returns the core node set as a built-in plugin.
func Plugin() plugin.Plugin { return corePlugin{} }

func (corePlugin) Manifest() plugin.Manifest {
	return plugin.Manifest{
		Name:        "core",
		Description: "Built-in nodes: triggers, logic, sequence, parsers, encoders, storage, HTTP, and network.",
		Author:      "Karakuri",
		Builtin:     true,
	}
}

func (corePlugin) Register(r plugin.Registrar) error {
	RegisterAll(plugin.NodeRegistry{R: r})

	// The OAuth2 credential store http-request's oauth2 auth mode draws from.
	// It lives with core because http-request is what consumes it.
	r.RegisterConnectionKind(oauth2conn.KindDef())
	r.RegisterConnectionKind(notification.KindDef())
	for _, kind := range socket.KindDefs() {
		r.RegisterConnectionKind(kind)
	}
	// Time-series databases the query and write nodes, history forwards,
	// and dashboard charts use.
	r.RegisterConnectionKind(prometheus.KindDef())
	r.RegisterConnectionKind(influxdb.KindDef())
	r.RegisterConnectionKind(postgresql.KindDef())
	r.RegisterFieldControl(flow.FieldControlDef{Name: "notification-endpoint", Base: flow.FieldString, Description: "Selects one allowed endpoint of the configured notification service."})
	r.RegisterFieldControl(flow.FieldControlDef{Name: "dashboard-picker", Base: flow.FieldString, Description: "Picks a dashboard by name, storing its ID."})
	r.RegisterFieldControl(flow.FieldControlDef{Name: "dashboard-widgets", Base: flow.FieldString, Description: "Picks placements of the dashboard named by the sibling dashboard field, storing comma-separated placement IDs."})
	r.RegisterFieldControl(flow.FieldControlDef{Name: "agent-browser", Base: flow.FieldString, Description: "Selects an agent advertising Open URL."})

	// The image value type is general data rather than an AI capability: the
	// Image parser produces it, the AI agent's Images input consumes it, and
	// a future image tool takes the same wire.
	r.RegisterPortType(flow.PortTypeDef{
		Name: flow.PortImage, Label: "Image",
		Accepts: "an image value from an Image node",
	})

	// General-purpose controls. Each names the built-in editor it falls back
	// to, so a frontend without the control still renders a usable field.
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "password", Base: flow.FieldString,
		Description: "Masked text input for secrets.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "list", Base: flow.FieldJSON,
		Description: "Editable list of strings, stored as a JSON array.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "map", Base: flow.FieldJSON,
		Description: "Editable key/value pairs, stored as a JSON object.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "tokens", Base: flow.FieldString,
		Description: "Comma-separated tokens edited as chips. The field's options are the vocabulary to pick from; a field with no options takes typed entries.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "control-picker", Base: flow.FieldString,
		Description: "Picks a control by name, storing its ID. A sibling \"kind\" field narrows the list to controls of that kind.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "control-list", Base: flow.FieldString,
		Description: "Picks several controls, storing their IDs comma-separated.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "control-attribute", Base: flow.FieldString,
		Description: "Picks one attribute of a control, offering what the chosen kind has. Empty means the whole value.",
	})
	r.RegisterFieldControl(flow.FieldControlDef{
		Name: "choices", Base: flow.FieldJSON,
		Description: "Editable list of value/label choices, stored as a JSON array of objects.",
	})
	return nil
}
