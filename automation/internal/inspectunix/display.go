//go:build !windows && !darwin

package inspectunix

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

func output(ctx context.Context, exe string, args ...string) ([]byte, error) {
	b, err := exec.CommandContext(ctx, exe, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", exe, err, b)
	}
	return b, nil
}
func displayBackend() string {
	if os.Getenv("WAYLAND_DISPLAY") != "" || os.Getenv("XDG_SESSION_TYPE") == "wayland" {
		if os.Getenv("HYPRLAND_INSTANCE_SIGNATURE") != "" {
			return "hyprland"
		}
		if os.Getenv("SWAYSOCK") != "" {
			return "sway"
		}
		if strings.Contains(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), "kde") {
			return "kscreen"
		}
		if strings.Contains(strings.ToLower(os.Getenv("XDG_CURRENT_DESKTOP")), "gnome") {
			return "mutter"
		}
		return "wlr"
	}
	return "xrandr"
}

type hyprMonitor struct {
	Name                string
	X, Y, Width, Height int
	RefreshRate, Scale  float64
	Focused             bool
	Transform           int
}
type swayOutput struct {
	Name            string
	Active, Focused bool
	Rect            struct{ X, Y int }
	CurrentMode     struct {
		Width, Height, Refresh int `json:",omitempty"`
	} `json:"current_mode"`
}
type kscreenConfig struct {
	Outputs []struct {
		ID            int
		Name          string
		Enabled       bool
		Priority      int
		Pos           struct{ X, Y int }
		CurrentModeID string
		Modes         []struct {
			ID          string
			Size        struct{ Width, Height int }
			RefreshRate float64
		}
	}
}

func (Backend) Displays(ctx context.Context) ([]autoapi.DisplayInfo, error) {
	result := make([]autoapi.DisplayInfo, 0)
	switch displayBackend() {
	case "mutter":
		return mutterDisplays(ctx)
	case "hyprland":
		data, err := output(ctx, "hyprctl", "-j", "monitors")
		if err != nil {
			return nil, err
		}
		var monitors []hyprMonitor
		if err = json.Unmarshal(data, &monitors); err != nil {
			return nil, err
		}
		for _, m := range monitors {
			result = append(result, autoapi.DisplayInfo{ID: m.Name, X: m.X, Y: m.Y, Width: m.Width, Height: m.Height, Depth: 32, Refresh: m.RefreshRate, Primary: m.Focused})
		}
	case "sway":
		data, err := output(ctx, "swaymsg", "-r", "-t", "get_outputs")
		if err != nil {
			return nil, err
		}
		var monitors []swayOutput
		if err = json.Unmarshal(data, &monitors); err != nil {
			return nil, err
		}
		for _, m := range monitors {
			if m.Active {
				result = append(result, autoapi.DisplayInfo{ID: m.Name, X: m.Rect.X, Y: m.Rect.Y, Width: m.CurrentMode.Width, Height: m.CurrentMode.Height, Depth: 32, Refresh: float64(m.CurrentMode.Refresh) / 1000, Primary: m.Focused})
			}
		}
	case "kscreen":
		data, err := output(ctx, "kscreen-doctor", "-j")
		if err != nil {
			return nil, err
		}
		var config kscreenConfig
		if err = json.Unmarshal(data, &config); err != nil {
			return nil, err
		}
		for _, o := range config.Outputs {
			if !o.Enabled {
				continue
			}
			for _, m := range o.Modes {
				if m.ID == o.CurrentModeID {
					result = append(result, autoapi.DisplayInfo{ID: o.Name, X: o.Pos.X, Y: o.Pos.Y, Width: m.Size.Width, Height: m.Size.Height, Depth: 32, Refresh: m.RefreshRate, Primary: o.Priority == 1})
				}
			}
		}
	case "wlr":
		data, err := output(ctx, "wlr-randr", "--json")
		if err != nil {
			return nil, fmt.Errorf("display management needs a compositor output API (Hyprland, Sway, KScreen, or wlr-output-management): %w", err)
		}
		result, err = parseWLRDisplays(data)
		if err != nil {
			return nil, err
		}
	default:
		data, err := output(ctx, "xrandr", "--current")
		if err != nil {
			return nil, err
		}
		conn, err := xgb.NewConn()
		if err != nil {
			return nil, err
		}
		depth := int(xproto.Setup(conn).DefaultScreen(conn).RootDepth)
		conn.Close()
		result = parseXRandR(string(data), depth)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("no active displays reported")
	}
	primary := false
	for _, m := range result {
		primary = primary || m.Primary
	}
	if !primary {
		result[0].Primary = true
	}
	return result, nil
}

var xrandrOutput = regexp.MustCompile(`^(\S+) connected (primary )?(\d+)x(\d+)([+-]\d+)([+-]\d+)`)

func parseXRandR(text string, depth int) []autoapi.DisplayInfo {
	var out []autoapi.DisplayInfo
	for _, line := range strings.Split(text, "\n") {
		m := xrandrOutput.FindStringSubmatch(line)
		if m != nil {
			n := func(i int) int { v, _ := strconv.Atoi(m[i]); return v }
			out = append(out, autoapi.DisplayInfo{ID: m[1], Primary: m[2] != "", Width: n(3), Height: n(4), X: n(5), Y: n(6), Depth: depth})
			continue
		}
		if len(out) > 0 && strings.HasPrefix(line, " ") {
			for _, f := range strings.Fields(line) {
				if strings.Contains(f, "*") {
					out[len(out)-1].Refresh, _ = strconv.ParseFloat(strings.TrimRight(f, "*+"), 64)
				}
			}
		}
	}
	return out
}
func (b Backend) SetDisplay(ctx context.Context, mode autoapi.DisplayMode) error {
	if mode.Width <= 0 || mode.Height <= 0 || mode.Depth <= 0 {
		return fmt.Errorf("display dimensions and depth must be positive")
	}
	monitors, err := b.Displays(ctx)
	if err != nil {
		return err
	}
	m := monitors[0]
	for _, v := range monitors {
		if v.Primary {
			m = v
			break
		}
	}
	if mode.Depth != m.Depth {
		return fmt.Errorf("this display has depth %d; changing color depth is not supported by the desktop", m.Depth)
	}
	size := fmt.Sprintf("%dx%d", mode.Width, mode.Height)
	switch displayBackend() {
	case "mutter":
		err = setMutterDisplay(ctx, mode)
	case "hyprland":
		// The keyword command's failure is read after the switch, so this
		// branch assigns the function's err instead of declaring its own.
		var data []byte
		data, err = output(ctx, "hyprctl", "-j", "monitors")
		if err != nil {
			return err
		}
		var all []hyprMonitor
		if err = json.Unmarshal(data, &all); err != nil {
			return err
		}
		for _, v := range all {
			if v.Name == m.ID {
				_, err = output(ctx, "hyprctl", "keyword", "monitor", fmt.Sprintf("%s,%s,%dx%d,%g,transform,%d", m.ID, size, v.X, v.Y, v.Scale, v.Transform))
				break
			}
		}
	case "sway":
		var reply []struct {
			Success bool
			Error   string
		}
		var data []byte
		data, err = output(ctx, "swaymsg", "-r", "output", m.ID, "mode", size)
		if err == nil {
			err = json.Unmarshal(data, &reply)
			for _, r := range reply {
				if !r.Success {
					return fmt.Errorf("compositor rejected mode: %s", r.Error)
				}
			}
		}
	case "kscreen":
		_, err = output(ctx, "kscreen-doctor", "output."+m.ID+".mode."+size)
	case "wlr":
		if _, err = output(ctx, "wlr-randr", "--dryrun", "--output", m.ID, "--mode", size); err == nil {
			_, err = output(ctx, "wlr-randr", "--output", m.ID, "--mode", size)
		}
	default:
		if _, err = output(ctx, "xrandr", "--dryrun", "--output", m.ID, "--mode", size); err == nil {
			_, err = output(ctx, "xrandr", "--output", m.ID, "--mode", size)
		}
	}
	if err != nil {
		return err
	}
	actual, err := b.Displays(ctx)
	if err != nil {
		return err
	}
	for _, v := range actual {
		if v.ID == m.ID && v.Width == mode.Width && v.Height == mode.Height {
			return nil
		}
	}
	return fmt.Errorf("display did not switch to %s", size)
}

func parseWLRDisplays(data []byte) ([]autoapi.DisplayInfo, error) {
	result := []autoapi.DisplayInfo{}
	var monitors []struct {
		Name     string
		Enabled  bool
		Position struct{ X, Y int }
		Modes    []struct {
			Width, Height int
			Refresh       float64
			Current       bool
		}
	}
	if err := json.Unmarshal(data, &monitors); err != nil {
		return nil, err
	}
	for _, o := range monitors {
		if o.Enabled {
			for _, m := range o.Modes {
				if m.Current {
					result = append(result, autoapi.DisplayInfo{ID: o.Name, X: o.Position.X, Y: o.Position.Y, Width: m.Width, Height: m.Height, Depth: 32, Refresh: m.Refresh})
				}
			}
		}
	}
	return result, nil
}
