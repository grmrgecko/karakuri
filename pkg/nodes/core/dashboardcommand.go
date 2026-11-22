package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/grmrgecko/karakuri/pkg/dashlive"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/models"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// DashboardCommandNode acts on every open viewer of a dashboard: it hides and
// shows placements under named locks, changes page, raises alerts, dims the
// screen, reloads it, pushes content into a placement, or sets a variable. It
// never changes the dashboard's draft or deployment, and what it sets is lost
// on restart.
type DashboardCommandNode struct{}

// dashboardActionOptions lists the actions in the order the editor offers them.
var dashboardActionOptions = []engine.Option{
	{Value: dashlive.ActionShow, Label: "Show widgets"},
	{Value: dashlive.ActionHide, Label: "Hide widgets"},
	{Value: dashlive.ActionPage, Label: "Go to page"},
	{Value: dashlive.ActionNextPage, Label: "Next page"},
	{Value: dashlive.ActionPreviousPage, Label: "Previous page"},
	{Value: dashlive.ActionAlert, Label: "Show alert"},
	{Value: dashlive.ActionClearAlert, Label: "Clear alerts"},
	{Value: dashlive.ActionDim, Label: "Dim screen"},
	{Value: dashlive.ActionUndim, Label: "Undim screen"},
	{Value: dashlive.ActionReload, Label: "Reload viewers"},
	{Value: dashlive.ActionContent, Label: "Set widget content"},
	{Value: dashlive.ActionVariable, Label: "Set variable"},
}

func (DashboardCommandNode) Meta() engine.NodeMeta {
	on := func(actions ...string) *flow.ShowIf {
		values := make([]any, len(actions))
		for i, a := range actions {
			values[i] = a
		}
		return &flow.ShowIf{Field: "action", Equals: values}
	}
	return engine.NodeMeta{
		Type:        "dashboard-command",
		Label:       "Dashboard Command",
		Folder:      flow.FolderDashboard,
		Description: "Shows or hides widgets, changes page, alerts, dims, reloads, pushes content, or sets a variable on a live dashboard",
		Icon:        "las la-tv",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()},
		ConfigSpec: []engine.FieldSpec{
			{ID: "dashboard", Label: "Dashboard", Type: flow.FieldString, Control: "dashboard-picker", Required: true,
				Help: "The dashboard whose open viewers the command reaches. A flow can command any dashboard."},
			{ID: "action", Label: "Action", Type: flow.FieldSelect, Required: true, Default: dashlive.ActionShow, Options: dashboardActionOptions},
			{ID: "targets", Label: "Widgets", Type: flow.FieldString, Control: "dashboard-widgets", ShowIf: on(dashlive.ActionShow, dashlive.ActionHide, dashlive.ActionContent),
				Help: "Placements on the dashboard's served layout, stored as comma-separated placement IDs."},
			{ID: "lock", Label: "Lock", Type: flow.FieldString, Placeholder: "command", ShowIf: on(dashlive.ActionShow, dashlive.ActionHide),
				Help: "Name this automation's reason. A widget hidden under several locks shows again only when every one is released. " +
					"A widget authored hidden on start holds the lock start."},
			{ID: "force", Label: "Clear every lock", Type: flow.FieldBoolean, ShowIf: on(dashlive.ActionShow),
				Help: "Show the widget whatever else is hiding it, including start."},
			{ID: "page", Label: "Page", Type: flow.FieldString, ShowIf: on(dashlive.ActionPage),
				Help: "Page title, page ID, or 1-based number. Supports {{ }} expressions; empty returns to the first page."},
			{ID: "title", Label: "Title", Type: flow.FieldString, ShowIf: on(dashlive.ActionAlert), Help: "Up to 200 bytes. Supports {{ }} expressions."},
			{ID: "message", Label: "Message", Type: flow.FieldText, Placeholder: "{{ payload }}", ShowIf: on(dashlive.ActionAlert), Help: "Up to 4 KiB. Supports {{ }} expressions."},
			{ID: "level", Label: "Level", Type: flow.FieldSelect, Default: "info", ShowIf: on(dashlive.ActionAlert), Options: []engine.Option{
				{Value: "info", Label: "Info"}, {Value: "success", Label: "Success"}, {Value: "warning", Label: "Warning"}, {Value: "error", Label: "Error"},
			}},
			{ID: "style", Label: "Style", Type: flow.FieldSelect, Default: "toast", ShowIf: on(dashlive.ActionAlert), Options: []engine.Option{
				{Value: "toast", Label: "Toast in the corner"}, {Value: "fullscreen", Label: "Full screen over everything"},
			}},
			{ID: "speak", Label: "Read aloud", Type: flow.FieldBoolean, ShowIf: on(dashlive.ActionAlert),
				Help: "Each viewer open when the alert arrives reads its title and message with the browser's speech."},
			{ID: "duration", Label: "Duration (seconds)", Type: flow.FieldNumber, Default: 10, ShowIf: on(dashlive.ActionAlert),
				Help: "0 keeps the alert until a Clear alerts command. At most 86400."},
			{ID: "dim_level", Label: "Dim level", Type: flow.FieldNumber, Default: 100, ShowIf: on(dashlive.ActionDim),
				Help: "0 is full brightness and 100 is black. It replaces the dashboard's night schedule until Undim."},
			{ID: "format", Label: "Format", Type: flow.FieldSelect, Default: "text", ShowIf: on(dashlive.ActionContent), Options: []engine.Option{
				{Value: "text", Label: "Text"}, {Value: "markdown", Label: "Markdown"}, {Value: "json", Label: "JSON"},
			}},
			{ID: "value", Label: "Content", Type: flow.FieldText, ShowIf: on(dashlive.ActionContent),
				Help: "Leave empty to send the incoming payload as it is. Text supports {{ }} expressions; JSON format parses the text. " +
					"A flow-text widget shows it; a rotating message takes a string or a list of strings as its lines."},
			{ID: "variable", Label: "Variable", Type: flow.FieldString, ShowIf: on(dashlive.ActionVariable),
				Help: "Name of a variable in the dashboard's settings, as written in ${name} references."},
			{ID: "variable_value", Label: "Value", Type: flow.FieldString, Placeholder: "{{ payload }}", ShowIf: on(dashlive.ActionVariable),
				Help: "An option value or label of that variable. Supports {{ }} expressions; empty returns every viewer to the default."},
		},
	}
}

// dashboardCommandConfig is the decoded dashboard-command configuration.
type dashboardCommandConfig struct {
	Dashboard string `json:"dashboard"`
	Action    string `json:"action"`
	Targets   string `json:"targets"`
	Lock      string `json:"lock"`
	Force     bool   `json:"force"`
	Page      string `json:"page"`
	Title     string `json:"title"`
	Message   string `json:"message"`
	Level     string `json:"level"`
	Style     string `json:"style"`
	Speak     bool   `json:"speak"`
	Duration  *int   `json:"duration"`
	DimLevel  *int   `json:"dim_level"`
	Format    string `json:"format"`
	Value     string `json:"value"`
	Variable  string `json:"variable"`
	// VariableValue is separate from Value, which content reads as a payload.
	VariableValue string `json:"variable_value"`
}

// Execute applies the command and passes the message on. Every failure is
// nonretryable: a repeated alert or reload is a visible side effect.
func (DashboardCommandNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (outputs map[string][]*engine.Msg, err error) {
	defer func() {
		if err != nil {
			err = &nodeapi.ExecutionError{Message: err.Error(), Cause: err, Retryable: false}
		}
	}()
	var cfg dashboardCommandConfig
	if err = ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inbound(ec, inputs)

	dashboard, err := models.LookupDashboard(strings.TrimSpace(cfg.Dashboard))
	if err != nil {
		return nil, fmt.Errorf("dashboard %q not found", cfg.Dashboard)
	}

	cmd := dashlive.Command{
		Action:   cfg.Action,
		Lock:     strings.TrimSpace(cfg.Lock),
		Force:    cfg.Force,
		Level:    cfg.Level,
		Style:    cfg.Style,
		Speak:    cfg.Speak,
		Duration: cfg.Duration,
		DimLevel: cfg.DimLevel,
		Format:   cfg.Format,
	}
	for _, t := range strings.Split(cfg.Targets, ",") {
		if t = strings.TrimSpace(t); t != "" {
			cmd.Targets = append(cmd.Targets, t)
		}
	}
	if cmd.Page, err = ec.Interpolate(cfg.Page, msg); err != nil {
		return nil, err
	}
	if cmd.Title, err = ec.Interpolate(cfg.Title, msg); err != nil {
		return nil, err
	}
	if cmd.Message, err = ec.Interpolate(cfg.Message, msg); err != nil {
		return nil, err
	}
	if cfg.Action == dashlive.ActionVariable {
		cmd.Variable = strings.TrimSpace(cfg.Variable)
		value, err := ec.Interpolate(cfg.VariableValue, msg)
		if err != nil {
			return nil, err
		}
		if cmd.Value, err = json.Marshal(value); err != nil {
			return nil, err
		}
	}
	if cfg.Action == dashlive.ActionContent {
		if cmd.Value, err = commandContent(ec, cfg, msg); err != nil {
			return nil, err
		}
	}

	res, err := dashlive.S.Apply(dashboard.ID, cmd)
	if err != nil {
		return nil, err
	}
	ec.Logger().Debug("dashboard command applied")
	out := passThrough(msg)
	meta := map[string]any{"dashboard_id": dashboard.ID.String(), "dashboard_seq": res.Seq}
	if res.AlertID != "" {
		meta["alert_id"] = res.AlertID
	}
	out.Meta = mergeMeta(out.Meta, meta)
	return engine.One("out", out), nil
}

// commandContent builds the content value: the incoming payload when the
// field is empty, otherwise the interpolated text, parsed when the format is
// JSON.
func commandContent(ec engine.ExecContext, cfg dashboardCommandConfig, msg *engine.Msg) (json.RawMessage, error) {
	if strings.TrimSpace(cfg.Value) == "" {
		raw, err := json.Marshal(msg.Payload)
		if err != nil {
			return nil, fmt.Errorf("encoding the payload as content: %w", err)
		}
		return raw, nil
	}
	text, err := ec.Interpolate(cfg.Value, msg)
	if err != nil {
		return nil, err
	}
	if cfg.Format == "json" {
		if !json.Valid([]byte(text)) {
			return nil, fmt.Errorf("content is set to JSON but does not parse")
		}
		return json.RawMessage(text), nil
	}
	raw, err := json.Marshal(text)
	if err != nil {
		return nil, err
	}
	return raw, nil
}
