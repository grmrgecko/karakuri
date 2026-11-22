package system

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

const (
	nodeServiceStatus  = "service-status"
	nodeServiceControl = "service-control"
)

type serviceStatusNode struct{}

func (serviceStatusNode) Meta() nodeapi.NodeMeta {
	return nodeapi.NodeMeta{
		Type: nodeServiceStatus, Folder: folder, Label: "Service status",
		Description: "Reads a systemd, launchd, or Windows service on this agent host",
		Icon:        "las la-cogs", Pure: true, Retryable: true, TestWithoutParams: true,
		Outputs:    []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}},
		ConfigSpec: serviceFields(false),
	}
}

type serviceControlNode struct{}

func (serviceControlNode) Meta() nodeapi.NodeMeta {
	fields := []nodeapi.FieldSpec{{
		ID: "action", Label: "Action", Type: flow.FieldSelect, Default: "restart", Options: []nodeapi.Option{
			{Value: "start", Label: "Start"}, {Value: "stop", Label: "Stop"},
			{Value: "restart", Label: "Restart"}, {Value: "enable", Label: "Enable at boot"},
			{Value: "disable", Label: "Disable at boot"},
		},
	}}
	fields = append(fields, serviceFields(true)...)
	fields = append(fields,
		nodeapi.FieldSpec{ID: "confirm", Label: "Confirm resulting state", Type: flow.FieldBoolean, Default: false, Help: "Start/restart waits for running; stop for stopped; enable/disable for startup configuration. This does not establish application health."},
		nodeapi.FieldSpec{ID: "confirmation_timeout", Label: "Confirmation timeout", Type: flow.FieldDuration, Default: "30s", ShowIf: &nodeapi.ShowIf{Field: "confirm", Equals: true}},
		nodeapi.FieldSpec{ID: "confirmation_interval", Label: "Confirmation interval", Type: flow.FieldDuration, Default: "250ms", ShowIf: &nodeapi.ShowIf{Field: "confirm", Equals: true}})
	return nodeapi.NodeMeta{
		Type: nodeServiceControl, Folder: folder, Label: "Service control",
		Description: "Starts, stops, restarts, enables, or disables a service on this agent host",
		Icon:        "las la-tools",
		Inputs:      []nodeapi.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs:     []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()},
		ConfigSpec:  fields,
	}
}

func serviceFields(interpolated bool) []nodeapi.FieldSpec {
	help := "Service unit, launchd label, or Windows service name."
	if interpolated {
		help += " {{ }} segments are rendered against the message."
	}
	return []nodeapi.FieldSpec{
		{ID: "name", Label: "Service", Type: flow.FieldString, Required: true, Placeholder: "jellyfin", Help: help},
		{ID: "scope", Label: "Scope", Type: flow.FieldSelect, Default: "system", Options: []nodeapi.Option{
			{Value: "system", Label: "System"}, {Value: "user", Label: "Current user"},
		}, Help: "Selects systemd or launchd's system or user domain. Windows ignores this setting."},
	}
}

type serviceConfig struct {
	Name                 string `json:"name"`
	Scope                string `json:"scope"`
	Action               string `json:"action"`
	Confirm              bool   `json:"confirm"`
	ConfirmationTimeout  string `json:"confirmation_timeout"`
	ConfirmationInterval string `json:"confirmation_interval"`
}

func (serviceStatusNode) Execute(ctx context.Context, ec nodeapi.ExecContext, _ map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg serviceConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, fmt.Errorf("no service configured")
	}
	scope := serviceScope(cfg.Scope)
	status, err := readServiceStatus(ctx, name, scope)
	if err != nil {
		return nil, err
	}
	return nodeapi.One("out", &nodeapi.Msg{Payload: status, Topic: name, Meta: map[string]any{"source": "service-status"}}), nil
}

func (serviceControlNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg serviceConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	name, err := agentnode.Interpolate(ec, cfg.Name, msg)
	if err != nil {
		return nil, err
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("no service configured")
	}
	scope := serviceScope(cfg.Scope)
	action := cfg.Action
	if action == "" {
		action = "restart"
	}
	timeout, interval := 30*time.Second, 250*time.Millisecond
	if cfg.Confirm {
		var err error
		if cfg.ConfirmationTimeout != "" {
			timeout, err = time.ParseDuration(cfg.ConfirmationTimeout)
			if err != nil || timeout <= 0 || timeout > 24*time.Hour {
				return nil, fmt.Errorf("confirmation timeout must be positive and at most one day")
			}
		}
		if cfg.ConfirmationInterval != "" {
			interval, err = time.ParseDuration(cfg.ConfirmationInterval)
			if err != nil || interval <= 0 || interval > timeout {
				return nil, fmt.Errorf("confirmation interval must be positive and no greater than its timeout")
			}
		}
	}
	if err := controlService(ctx, name, scope, action); err != nil {
		return nil, err
	}
	meta := map[string]any{"service": name, "action": action, "scope": scope}
	if cfg.Confirm {
		confirmCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		status, err := awaitService(confirmCtx, ec.Clock(), name, scope, action, interval)
		if err != nil {
			return nil, fmt.Errorf("confirming service %s after %s: %w", name, action, err)
		}
		meta["confirmed"] = true
		meta["service_status"] = status
	}
	return nodeapi.One("out", agentnode.PassThrough(msg, meta)), nil
}

func awaitService(ctx context.Context, clock nodeapi.Clock, name, scope, action string, interval time.Duration) (map[string]any, error) {
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var status map[string]any
		var err error
		if action == "enable" || action == "disable" {
			status, err = readServiceEnablement(ctx, name, scope)
		} else {
			status, err = readServiceStatus(ctx, name, scope)
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err != nil {
			return nil, err
		}
		matched := false
		switch action {
		case "start", "restart":
			matched = status["state"] == "running"
		case "stop":
			matched = status["state"] == "stopped"
		case "enable":
			matched = status["enabled"] == true
		case "disable":
			matched = status["enabled"] == false
		default:
			return nil, fmt.Errorf("unknown confirmation action %q", action)
		}
		if matched {
			return status, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-clock.After(interval):
		}
	}
}

func serviceScope(scope string) string {
	if scope == "user" {
		return "user"
	}
	return "system"
}
