//go:build darwin

package system

import (
	"context"
	"fmt"
	"os"
	osexec "os/exec"
	"strings"
)

func launchDomain(scope string) string {
	if scope == "user" {
		return fmt.Sprintf("gui/%d", os.Getuid())
	}
	return "system"
}

func readServiceStatus(ctx context.Context, name, scope string) (map[string]any, error) {
	target := launchDomain(scope) + "/" + name
	out, err := osexec.CommandContext(ctx, "launchctl", "print", target).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading service %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	text := string(out)
	state := "unknown"
	for _, line := range strings.Split(text, "\n") {
		if value, ok := strings.CutPrefix(strings.TrimSpace(line), "state = "); ok {
			switch value {
			case "running":
				state = "running"
			case "not running", "waiting", "exited":
				state = "stopped"
			}
			break
		}
	}
	return map[string]any{"name": name, "manager": "launchd", "scope": scope,
		"running": state == "running", "state": state, "details": text}, nil
}

func readServiceEnablement(ctx context.Context, name, scope string) (map[string]any, error) {
	out, err := osexec.CommandContext(ctx, "launchctl", "print-disabled", launchDomain(scope)).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("reading disabled service overrides: %w", err)
	}
	status := map[string]any{"name": name, "manager": "launchd", "scope": scope}
	for _, line := range strings.Split(string(out), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), " => ")
		if !ok || key != fmt.Sprintf("%q", name) {
			continue
		}
		// Older launchctl prints the disabled flag; newer releases print the state.
		switch strings.TrimSpace(value) {
		case "true", "disabled":
			status["enabled"] = false
		case "false", "enabled":
			status["enabled"] = true
		}
		return status, nil
	}
	return status, nil
}

func controlService(ctx context.Context, name, scope, action string) error {
	target := launchDomain(scope) + "/" + name
	var args []string
	switch action {
	case "", "restart":
		args = []string{"kickstart", "-k", target}
	case "start":
		args = []string{"kickstart", target}
	case "stop":
		args = []string{"kill", "SIGTERM", target}
	case "enable", "disable":
		args = []string{action, target}
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
	out, err := osexec.CommandContext(ctx, "launchctl", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("cannot %s service %s: %w: %s", action, name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
