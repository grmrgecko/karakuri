//go:build linux || freebsd

package desktop

import (
	"context"
	"fmt"
	osexec "os/exec"
	"strconv"
	"strings"
)

func readMediaStatus(ctx context.Context, player string) (map[string]any, error) {
	if _, err := osexec.LookPath("playerctl"); err != nil {
		return nil, fmt.Errorf("playerctl is not installed")
	}
	args := []string{}
	if player != "" {
		args = append(args, "--player", player)
	}
	run := func(command ...string) string {
		out, _ := osexec.CommandContext(ctx, "playerctl", append(args, command...)...).Output()
		return strings.TrimSpace(string(out))
	}
	state := strings.ToLower(run("status"))
	if state == "" {
		return nil, fmt.Errorf("no media player is available")
	}
	out := map[string]any{
		"player": player, "state": state, "playing": state == "playing",
		"title": run("metadata", "xesam:title"), "artist": run("metadata", "xesam:artist"),
		"album": run("metadata", "xesam:album"),
	}
	if value, err := strconv.ParseFloat(run("position"), 64); err == nil {
		out["position_seconds"] = value
	}
	if value, err := strconv.ParseFloat(run("metadata", "mpris:length"), 64); err == nil {
		out["duration_seconds"] = value / 1_000_000
	}
	if value, err := strconv.ParseFloat(run("volume"), 64); err == nil {
		out["volume"] = value * 100
	}
	return out, nil
}
