//go:build !windows && !darwin

package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

var desktopEnvKeys = map[string]bool{"DISPLAY": true, "WAYLAND_DISPLAY": true, "XAUTHORITY": true, "XDG_RUNTIME_DIR": true, "DBUS_SESSION_BUS_ADDRESS": true, "XDG_SESSION_TYPE": true, "XDG_CURRENT_DESKTOP": true, "HYPRLAND_INSTANCE_SIGNATURE": true, "SWAYSOCK": true, "XDG_SESSION_ID": true}

func desktopEnvironment(ctx context.Context) ([]string, error) {
	best := map[string]string{}
	score := 0
	candidates, err := desktopProcessEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	for _, entries := range candidates {
		values := map[string]string{}
		for _, entry := range entries {
			key, value, ok := strings.Cut(entry, "=")
			if ok && desktopEnvKeys[key] && value != "" {
				values[key] = value
			}
		}
		if !liveDesktop(values) {
			continue
		}
		n := len(values)
		if values["WAYLAND_DISPLAY"] != "" {
			n += 10
		}
		if values["DISPLAY"] != "" {
			n += 5
		}
		if n > score {
			best = values
			score = n
		}
	}
	if best["DISPLAY"] == "" && best["WAYLAND_DISPLAY"] == "" {
		return nil, fmt.Errorf("no live graphical session found for this user; log in at the desktop, or run resident install from a graphical terminal")
	}
	if best["DBUS_SESSION_BUS_ADDRESS"] == "" {
		if runtime := best["XDG_RUNTIME_DIR"]; runtime != "" {
			best["DBUS_SESSION_BUS_ADDRESS"] = "unix:path=" + filepath.Join(runtime, "bus")
		}
	}
	return mergeDesktopEnvironment(best, desktopEnvKeys), nil
}
func liveDesktop(values map[string]string) bool {
	path := values["WAYLAND_DISPLAY"]
	if path != "" {
		if !filepath.IsAbs(path) {
			path = filepath.Join(values["XDG_RUNTIME_DIR"], path)
		}
	} else {
		display := values["DISPLAY"]
		if !strings.HasPrefix(display, ":") {
			return false
		}
		number, _, _ := strings.Cut(display[1:], ".")
		if _, err := strconv.Atoi(number); err != nil {
			return false
		}
		path = filepath.Join("/tmp/.X11-unix", "X"+number)
		if auth := values["XAUTHORITY"]; auth != "" {
			if _, err := os.Stat(auth); err != nil {
				return false
			}
		}
	}
	connection, err := net.DialTimeout("unix", path, 100*time.Millisecond)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}
func configureUnixStartup(ctx context.Context, exe string, remove bool) error {
	config, err := os.UserConfigDir()
	if err != nil {
		return err
	}
	path := filepath.Join(config, "autostart", "karakuri-auto.desktop")
	if remove {
		if err = os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		fmt.Println("resident autostart removed")
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	quoted := strings.NewReplacer(`\`, `\\`, `"`, `\"`, `$`, `\$`, "`", "\\`", "%", "%%").Replace(exe)
	return os.WriteFile(path, []byte("[Desktop Entry]\nType=Application\nName=Karakuri automation resident\nExec=\""+quoted+"\" resident serve\nTerminal=false\nX-GNOME-Autostart-enabled=true\n"), 0600)
}
func freeBSDEnvironments(ctx context.Context) ([][]string, error) {
	data, err := exec.CommandContext(ctx, "ps", "-U", strconv.Itoa(os.Geteuid()), "-o", "pid=").Output()
	if err != nil {
		return nil, err
	}
	all := [][]string{os.Environ()}
	for _, pid := range strings.Fields(string(data)) {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if _, err := strconv.Atoi(pid); err != nil {
			continue
		}
		data, err := exec.CommandContext(ctx, "procstat", "-e", pid).Output()
		if err != nil {
			continue
		}
		lines := strings.Split(string(data), "\n")
		for _, line := range lines[1:] {
			fields := strings.Fields(line)
			if len(fields) > 2 && fields[1] != "resident" && !strings.Contains(fields[1], "karakuri") {
				all = append(all, fields[2:])
			}
		}
	}
	return all, nil
}

// watchDesktopSession releases the resident when its display socket is removed
// or replaced, so a later login can start with the new session's environment.
func watchDesktopSession(ctx context.Context, cancel context.CancelFunc) {
	path := os.Getenv("WAYLAND_DISPLAY")
	if path != "" && !filepath.IsAbs(path) {
		path = filepath.Join(os.Getenv("XDG_RUNTIME_DIR"), path)
	}
	if path == "" {
		display := os.Getenv("DISPLAY")
		if !strings.HasPrefix(display, ":") {
			return
		}
		number, _, _ := strings.Cut(strings.TrimPrefix(display, ":"), ".")
		if _, err := strconv.Atoi(number); err != nil {
			return
		}
		path = filepath.Join("/tmp/.X11-unix", "X"+number)
	}
	original, err := os.Stat(path)
	if err != nil {
		return
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			current, err := os.Stat(path)
			if err != nil || !os.SameFile(original, current) {
				cancel()
				return
			}
		}
	}
}
