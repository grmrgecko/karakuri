//go:build linux

package system

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strings"
)

func systemctlArgs(scope string, args ...string) []string {
	if scope == "user" {
		return append([]string{"--user"}, args...)
	}
	return args
}

func readServiceStatus(ctx context.Context, name, scope string) (map[string]any, error) {
	args := systemctlArgs(scope, "show", name, "--no-page",
		"--property=LoadState,ActiveState,SubState,UnitFileState,Description")
	out, err := osexec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading service %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	status := map[string]any{"name": name, "manager": "systemd", "scope": scope}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if ok {
			status[strings.ToLower(strings.TrimSuffix(key, "State"))] = value
		}
	}
	status["running"] = status["active"] == "active"
	status["state"] = "unknown"
	if status["load"] == "loaded" {
		if status["active"] == "active" {
			status["state"] = "running"
		}
		if status["active"] == "inactive" {
			status["state"] = "stopped"
		}
	}
	switch status["unitfile"] {
	case "enabled", "enabled-runtime":
		status["enabled"] = true
	case "disabled":
		status["enabled"] = false
	}
	return status, nil
}

func readServiceEnablement(ctx context.Context, name, scope string) (map[string]any, error) {
	return readServiceStatus(ctx, name, scope)
}

func controlService(ctx context.Context, name, scope, action string) error {
	switch action {
	case "", "restart", "start", "stop", "enable", "disable":
		if action == "" {
			action = "restart"
		}
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	out, err := osexec.CommandContext(ctx, "systemctl", systemctlArgs(scope, action, name)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot %s service %s: %w: %s", action, name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
