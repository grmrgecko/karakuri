//go:build !windows && !darwin

package wlwindow

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

// Place uses compositor-specific placement only for an already floating window.
func (b Backend) Place(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	if !p.Move && !p.Resize {
		return b.Act(ctx, w, autoapi.WindowAction("restore"))
	}
	if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
		return placeHyprland(ctx, w, p, func() error {
			return b.Act(ctx, w, autoapi.WindowAction("restore"))
		})
	}
	if os.Getenv("SWAYSOCK") != "" {
		return placeSway(ctx, w, p)
	}
	return fmt.Errorf("this Wayland compositor does not expose window placement: %w", autoapi.ErrUnsupported)
}
func placeHyprland(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement, restore func() error) error {
	data, err := exec.CommandContext(ctx, "hyprctl", "-j", "clients").Output()
	if err != nil {
		return err
	}
	var clients []struct {
		Address, Title, Class string
		PID                   int
		Floating              bool
	}
	if err = json.Unmarshal(data, &clients); err != nil {
		return err
	}
	address := ""
	count := 0
	for _, c := range clients {
		if c.Title == w.Title && c.Class == w.Class && (w.PID == 0 || w.PID == c.PID) {
			count++
			if !c.Floating {
				return fmt.Errorf("window placement requires a floating Hyprland window")
			}
			address = c.Address
		}
	}
	if count != 1 {
		return fmt.Errorf("%d compositor windows match; select a unique window", count)
	}
	if !strings.HasPrefix(address, "0x") {
		return fmt.Errorf("invalid Hyprland window address")
	}
	if _, err = strconv.ParseUint(address[2:], 16, 64); err != nil {
		return err
	}
	// Detect the dispatcher API before sending any mutating command.
	data, err = exec.CommandContext(ctx, "hyprctl", "-j", "version").Output()
	if err != nil {
		return err
	}
	var version struct{ Version, Tag string }
	if err = json.Unmarshal(data, &version); err != nil {
		return err
	}
	if version.Version == "" {
		version.Version = version.Tag
	}
	var major, minor int
	if _, err = fmt.Sscanf(strings.TrimPrefix(version.Version, "v"), "%d.%d", &major, &minor); err != nil {
		return fmt.Errorf("cannot identify Hyprland dispatcher API: %w", err)
	}
	lua := major > 0 || minor >= 55
	if p.Restore {
		if err := restore(); err != nil {
			return err
		}
	}
	for _, v := range []struct {
		on   bool
		verb string
		x, y int
	}{{p.Resize, "resizewindowpixel", p.Width, p.Height}, {p.Move, "movewindowpixel", p.X, p.Y}} {
		if !v.on {
			continue
		}
		args := []string{"dispatch", v.verb, fmt.Sprintf("exact %d %d,address:%s", v.x, v.y, address)}
		if lua {
			method := "move"
			if v.verb == "resizewindowpixel" {
				method = "resize"
			}
			args = []string{"dispatch", fmt.Sprintf("hl.dsp.window.%s({window=\"address:%s\",x=%d,y=%d,relative=false})", method, address, v.x, v.y)}
		}
		out, err := exec.CommandContext(ctx, "hyprctl", args...).CombinedOutput()
		if err != nil {
			return fmt.Errorf("Hyprland placement: %w: %s", err, strings.TrimSpace(string(out)))
		}
		if strings.TrimSpace(string(out)) != "ok" {
			return fmt.Errorf("Hyprland placement rejected: %s", strings.TrimSpace(string(out)))
		}
	}
	return nil
}

type swayPlacementWindow struct {
	ID               int64                  `json:"id"`
	Name             string                 `json:"name"`
	AppID            string                 `json:"app_id"`
	PID              int                    `json:"pid"`
	Floating         string                 `json:"floating"`
	WindowProperties struct{ Class string } `json:"window_properties"`
	Nodes            []swayPlacementWindow  `json:"nodes"`
	FloatingNodes    []swayPlacementWindow  `json:"floating_nodes"`
}

func placeSway(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	data, err := exec.CommandContext(ctx, "swaymsg", "-r", "-t", "get_tree").Output()
	if err != nil {
		return err
	}
	var root swayPlacementWindow
	if err = json.Unmarshal(data, &root); err != nil {
		return err
	}
	var matched []swayPlacementWindow
	var walk func(swayPlacementWindow)
	walk = func(n swayPlacementWindow) {
		if n.Name == w.Title && (n.AppID == w.Class || n.WindowProperties.Class == w.Class) && (w.PID == 0 || n.PID == w.PID) {
			matched = append(matched, n)
		}
		for _, c := range n.Nodes {
			walk(c)
		}
		for _, c := range n.FloatingNodes {
			walk(c)
		}
	}
	walk(root)
	if len(matched) != 1 {
		return fmt.Errorf("%d compositor windows match; select a unique window", len(matched))
	}
	n := matched[0]
	if n.Floating != "user_on" && n.Floating != "auto_on" {
		return fmt.Errorf("window placement requires a floating Sway window")
	}
	commands := []string{}
	if p.Restore {
		commands = append(commands, "fullscreen disable")
	}
	if p.Move {
		commands = append(commands, fmt.Sprintf("move absolute position %d px %d px", p.X, p.Y))
	}
	if p.Resize {
		commands = append(commands, fmt.Sprintf("resize set %d px %d px", p.Width, p.Height))
	}
	data, err = exec.CommandContext(ctx, "swaymsg", "-r", fmt.Sprintf("[con_id=%d] %s", n.ID, strings.Join(commands, ", "))).CombinedOutput()
	if err != nil {
		return fmt.Errorf("Sway placement: %w: %s", err, data)
	}
	var results []struct {
		Success bool
		Error   string
	}
	if err = json.Unmarshal(data, &results); err != nil {
		return err
	}
	if len(results) == 0 {
		return fmt.Errorf("Sway placement returned no result")
	}
	for _, r := range results {
		if !r.Success {
			return fmt.Errorf("Sway placement rejected: %s", r.Error)
		}
	}
	return nil
}
