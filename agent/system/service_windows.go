//go:build windows

package system

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/nodeapi"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

func readServiceStatus(ctx context.Context, name, _ string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return nil, fmt.Errorf("connecting to service manager: %w", err)
	}
	defer manager.Disconnect()
	nameUTF16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.OpenService(manager.Handle, nameUTF16, windows.SERVICE_QUERY_STATUS)
	if err != nil {
		return nil, fmt.Errorf("opening service %s: %w", name, err)
	}
	service := &mgr.Service{Name: name, Handle: handle}
	defer service.Close()
	observed, err := service.Query()
	if err != nil {
		return nil, fmt.Errorf("reading service %s: %w", name, err)
	}
	state := "unknown"
	switch observed.State {
	case svc.Running:
		state = "running"
	case svc.Stopped:
		state = "stopped"
	case svc.StartPending:
		state = "start_pending"
	case svc.StopPending:
		state = "stop_pending"
	case svc.Paused:
		state = "paused"
	}
	return map[string]any{"name": name, "manager": "windows", "state": state, "running": state == "running"}, ctx.Err()
}

func readServiceEnablement(ctx context.Context, name, _ string) (map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	manager, err := mgr.Connect()
	if err != nil {
		return nil, err
	}
	defer manager.Disconnect()
	nameUTF16, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, err
	}
	handle, err := windows.OpenService(manager.Handle, nameUTF16, windows.SERVICE_QUERY_CONFIG)
	if err != nil {
		return nil, err
	}
	service := &mgr.Service{Name: name, Handle: handle}
	defer service.Close()
	cfg, err := service.Config()
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "manager": "windows", "enabled": cfg.StartType != mgr.StartDisabled, "start_type": cfg.StartType}, ctx.Err()
}

func controlService(ctx context.Context, name, _ string, action string) error {
	if action == "" {
		action = "restart"
	}
	run := func(args ...string) error {
		out, err := osexec.CommandContext(ctx, "sc.exe", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("sc.exe %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
		}
		return nil
	}
	switch action {
	case "start", "stop":
		return run(action, name)
	case "restart":
		if err := run("stop", name); err != nil {
			return err
		}
		stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if _, err := awaitService(stopCtx, nodeapi.RealClock{}, name, "system", "stop", 250*time.Millisecond); err != nil {
			return fmt.Errorf("waiting for service stop before restart: %w", err)
		}
		return run("start", name)
	case "enable":
		return run("config", name, "start=", "auto")
	case "disable":
		return run("config", name, "start=", "disabled")
	default:
		return fmt.Errorf("unknown service action %q", action)
	}
}
