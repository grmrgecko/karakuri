//go:build !windows && !darwin

package inspectunix

import (
	"context"
	"fmt"
	"github.com/godbus/dbus/v5"
	"github.com/grmrgecko/karakuri/automation/autoapi"
	"strings"
)

func Windows(ctx context.Context) ([]autoapi.WindowInfo, error) {
	b, err := connect(ctx)
	if err != nil {
		return nil, err
	}
	defer b.Close()
	apps, err := children(ctx, b, reference{"org.a11y.atspi.Registry", "/org/a11y/atspi/accessible/root"})
	if err != nil {
		return nil, err
	}
	out := []autoapi.WindowInfo{}
	for _, app := range apps {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		pid := processID(ctx, b, app)
		name := textProperty(properties(ctx, b, app), "Name")
		refs, err := children(ctx, b, app)
		if err != nil {
			continue
		}
		for _, r := range refs {
			e := readElement(ctx, b, r)
			if e.Role != "frame" && e.Role != "dialog" && e.Role != "window" && e.Role != "alert" {
				continue
			}
			var states []uint32
			_ = call(ctx, b, r, accessible+".GetState").Store(&states)
			active := len(states) > 0 && states[0]&(1<<1) != 0
			w := autoapi.WindowInfo{ID: "atspi:" + refID(r), Title: e.Name, Class: name, PID: int(pid), Enabled: &e.Enabled, Active: active}
			// GTK's Wayland bridge may report window-relative coordinates for SCREEN.
			// Keep geometry unknown without a compositor source for the global position.
			if displayBackend() == "xrandr" {
				w.X = e.X
				w.Y = e.Y
				w.Width = e.Width
				w.Height = e.Height
			}
			out = append(out, w)
		}
	}
	return out, nil
}
func WindowAction(ctx context.Context, w autoapi.WindowInfo, action autoapi.WindowAction) error {
	if action != autoapi.WindowActivate {
		return fmt.Errorf("AT-SPI has no portable %s window action", action)
	}
	bus, path, ok := strings.Cut(strings.TrimPrefix(w.ID, "atspi:"), "|")
	if !ok {
		return fmt.Errorf("invalid accessibility window id")
	}
	b, err := connect(ctx)
	if err != nil {
		return err
	}
	defer b.Close()
	var result bool
	if err = call(ctx, b, reference{bus, dbus.ObjectPath(path)}, "org.a11y.atspi.Component.GrabFocus").Store(&result); err != nil {
		return err
	}
	if !result {
		return fmt.Errorf("application refused window focus")
	}
	return nil
}

// Place reports that AT-SPI window focus does not grant compositor geometry control.
func (Backend) Place(ctx context.Context, w autoapi.WindowInfo, p autoapi.WindowPlacement) error {
	return fmt.Errorf("accessibility-only window placement: %w", autoapi.ErrUnsupported)
}
