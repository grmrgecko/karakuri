//go:build !windows && !darwin

package inspectunix

import (
	"context"
	"encoding/json"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func EnrichWindow(ctx context.Context, w autoapi.WindowInfo) autoapi.WindowInfo {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	switch displayBackend() {
	case "hyprland":
		data, err := output(ctx, "hyprctl", "-j", "clients")
		if err == nil {
			var clients []struct {
				Address, Title, Class string
				PID                   int
				At, Size              []int
				FocusHistoryID        int
			}
			if json.Unmarshal(data, &clients) == nil {
				var matches []int
				for i, c := range clients {
					if c.Title == w.Title && c.Class == w.Class {
						matches = append(matches, i)
					}
				}
				if len(matches) == 1 {
					c := clients[matches[0]]
					w.PID = c.PID
					w.Active = c.FocusHistoryID == 0
					if len(c.At) == 2 && len(c.Size) == 2 {
						w.X = c.At[0]
						w.Y = c.At[1]
						w.Width = c.Size[0]
						w.Height = c.Size[1]
					}
				}
			}
		}
	case "sway":
		data, err := output(ctx, "swaymsg", "-r", "-t", "get_tree")
		if err == nil {
			var root swayWindow
			if json.Unmarshal(data, &root) == nil {
				var matches []swayWindow
				var walk func(swayWindow)
				walk = func(n swayWindow) {
					if n.Name == w.Title && (n.AppID == w.Class || n.WindowProperties.Class == w.Class) {
						matches = append(matches, n)
					}
					for _, c := range append(n.Nodes, n.FloatingNodes...) {
						walk(c)
					}
				}
				walk(root)
				if len(matches) == 1 {
					n := matches[0]
					w.PID = n.PID
					w.Active = n.Focused
					w.X = n.Rect.X
					w.Y = n.Rect.Y
					w.Width = n.Rect.Width
					w.Height = n.Rect.Height
				}
			}
		}
	}
	bus, err := connect(ctx)
	if err == nil {
		defer bus.Close()
		ref, err := root(ctx, bus, w)
		if err == nil {
			e := readElement(ctx, bus, ref)
			w.Enabled = &e.Enabled
			if w.Width == 0 && displayBackend() == "xrandr" && e.X > -100000 && e.Y > -100000 {
				w.X = e.X
				w.Y = e.Y
				w.Width = e.Width
				w.Height = e.Height
			}
			if w.PID == 0 {
				w.PID = processID(ctx, bus, ref)
			}
		}
	}
	if w.PID > 0 {
		w.Image, _ = os.Readlink("/proc/" + strconv.Itoa(w.PID) + "/exe")
		if w.Image == "" {
			if data, err := exec.CommandContext(ctx, "procstat", "-b", strconv.Itoa(w.PID)).Output(); err == nil {
				lines := strings.Split(string(data), "\n")
				if len(lines) > 1 {
					fields := strings.Fields(lines[1])
					if len(fields) > 2 {
						w.Image = strings.Join(fields[2:], " ")
					}
				}
			}
		}
	}
	return w
}

type swayWindow struct {
	Name             string
	AppID            string `json:"app_id"`
	PID              int
	Focused          bool
	Rect             struct{ X, Y, Width, Height int }
	WindowProperties struct{ Class string } `json:"window_properties"`
	Nodes            []swayWindow
	FloatingNodes    []swayWindow `json:"floating_nodes"`
}
