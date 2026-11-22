package core

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/connops"
	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations"
	"github.com/grmrgecko/karakuri/pkg/models"
)

// ConnectionStatusNode starts runs for integration lifecycle state changes.
type ConnectionStatusNode struct{}

func (ConnectionStatusNode) Meta() engine.NodeMeta {
	properties := map[string]any{}
	for _, field := range []string{"connection_id", "connection_name", "connection_kind", "state", "previous_state", "error", "since"} {
		properties[field] = map[string]any{"type": "string"}
	}
	properties["initial"] = map[string]any{"type": "boolean"}
	return engine.NodeMeta{
		Type: "connection-status", Label: "Connection status trigger", Folder: flow.FolderNetwork,
		Category: flow.CategoryTrigger, Icon: "las la-plug", TestWithoutParams: true,
		Description: "Starts a run when an integration connects, fails, reconnects, or is disabled",
		Outputs:     []engine.PortSpec{{ID: "out", Label: "Status", Type: flow.PortObject, PayloadSchema: map[string]any{"type": "object", "properties": properties}}},
		ConfigSpec: []engine.FieldSpec{
			{ID: "connection", Label: "Connection", Type: flow.FieldConnection, Required: true, Help: "Connection to observe. Works with every integration kind."},
			{ID: "states", Label: "Emit states", Type: flow.FieldString, Control: "tokens", Options: []engine.Option{
				{Value: integrations.StateConnecting, Label: "Connecting"}, {Value: integrations.StateConnected, Label: "Connected"},
				{Value: integrations.StateError, Label: "Error"}, {Value: integrations.StateDisabled, Label: "Disabled"},
			}, Help: "Empty emits every state change. A disconnect reports error or connecting; recovery reports connected."},
			{ID: "emit_initial", Label: "Emit initial status", Type: flow.FieldBoolean, Default: false, Help: "Emit the current selected status once when the trigger is armed."},
			{ID: "test_params", Label: "Test parameters", Type: flow.FieldJSON, Help: "Optional sample payload for a test. Without a sample, a test reads the current status without subscribing."},
		},
	}
}

// connectionStatusConfig selects emissions without losing suppressed transitions.
type connectionStatusConfig struct {
	Connection  string `json:"connection"`
	States      string `json:"states"`
	EmitInitial bool   `json:"emit_initial"`
}

// stateSelection validates the native connection state vocabulary.
func (cfg connectionStatusConfig) stateSelection() (map[string]bool, error) {
	selected := make(map[string]bool)
	if strings.TrimSpace(cfg.States) == "" {
		return selected, nil
	}
	for _, raw := range strings.Split(cfg.States, ",") {
		state := strings.TrimSpace(raw)
		switch state {
		case integrations.StateConnecting, integrations.StateConnected, integrations.StateError, integrations.StateDisabled:
			selected[state] = true
		default:
			return nil, fmt.Errorf("unknown connection state %q", raw)
		}
	}
	return selected, nil
}

// statusMessage identifies the observed integration without including its config.
func statusMessage(conn *models.Connection, status integrations.Status, previous string, initial bool) *engine.Msg {
	return &engine.Msg{
		Topic: conn.ID.String(),
		Payload: map[string]any{
			"connection_id": conn.ID.String(), "connection_name": conn.Name, "connection_kind": conn.Kind,
			"state": status.State, "previous_state": previous, "error": status.Error,
			"since": status.Since.Format(time.RFC3339Nano), "initial": initial,
		},
	}
}

// unobservedStatus describes a stored connection before it has a retained report.
func unobservedStatus(conn *models.Connection) integrations.Status {
	return integrations.Status{State: integrations.StateDisabled, Since: conn.UpdatedAt}
}

// Execute forwards a trigger sample or reads one current status without watching.
func (ConnectionStatusNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg connectionStatusConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	if _, err := cfg.stateSelection(); err != nil {
		return nil, err
	}
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	conn, err := connops.Load(strings.TrimSpace(cfg.Connection))
	if err != nil {
		return nil, err
	}
	mgr := connops.Manager()
	if mgr == nil {
		return nil, fmt.Errorf("connection manager is not running")
	}
	status, found := mgr.Status(conn.ID)
	if !found {
		status = unobservedStatus(conn)
	}
	return engine.One("out", statusMessage(conn, status, "", true)), nil
}

// Start consumes manager reports outside driver callbacks and releases its watcher
// when disarmed. Repeated state reports update the baseline without starting runs.
func (ConnectionStatusNode) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg connectionStatusConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	selected, err := cfg.stateSelection()
	if err != nil {
		return err
	}
	conn, err := connops.Load(strings.TrimSpace(cfg.Connection))
	if err != nil {
		return err
	}
	mgr := connops.Manager()
	if mgr == nil {
		return fmt.Errorf("connection manager is not running")
	}
	status, found, updates, unsubscribe := mgr.WatchStatus(conn.ID)
	defer unsubscribe()
	if !found {
		status = unobservedStatus(conn)
	}
	if cfg.EmitInitial && (len(selected) == 0 || selected[status.State]) && ctx.Err() == nil {
		emit(statusMessage(conn, status, "", true))
	}
	previous := status.State
	for {
		select {
		case <-ctx.Done():
			return nil
		case status, ok := <-updates:
			if !ok || ctx.Err() != nil {
				return nil
			}
			last := previous
			previous = status.State
			if last == status.State || (len(selected) > 0 && !selected[status.State]) {
				continue
			}
			// Names can change while the stable connection ID remains armed.
			if current, err := models.FindConnectionByID(conn.ID); err == nil {
				conn = current
			}
			if ctx.Err() == nil {
				emit(statusMessage(conn, status, last, false))
			}
		}
	}
}
