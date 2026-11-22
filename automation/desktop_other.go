//go:build !windows

package automation

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/grmrgecko/karakuri/automation/autoapi"
)

func desktopControls(ctx context.Context, s *Session, w WindowInfo) ([]Control, error) {
	elements, err := s.Inspection.Elements(ctx, w)
	out := make([]Control, 0, len(elements))
	for _, e := range elements {
		text := e.Text
		if e.Protected {
			text = ""
		}
		out = append(out, Control{ID: e.ID, Parent: e.Parent, Name: e.Name, Role: e.Role, Class: e.Class, Text: text, X: e.X, Y: e.Y, Width: e.Width, Height: e.Height, Enabled: e.Enabled, Visible: e.Visible, Protected: e.Protected})
	}
	return out, err
}
func desktopMenus(ctx context.Context, s *Session, w WindowInfo) ([]MenuItem, error) {
	elements, err := s.Inspection.Elements(ctx, w)
	out := make([]MenuItem, 0)
	for _, e := range elements {
		if e.Path != "" {
			out = append(out, MenuItem{ID: e.ID, Text: e.Name, Path: e.Path, Enabled: e.Enabled})
		}
	}
	return out, err
}
func desktopInvokeMenu(ctx context.Context, s *Session, w WindowInfo, target string) error {
	items, err := desktopMenus(ctx, s, w)
	if err != nil {
		return err
	}
	var found []MenuItem
	for _, e := range items {
		if e.ID == target || strings.EqualFold(menuPath(e.Path), menuPath(target)) {
			found = append(found, e)
		}
	}
	if len(found) != 1 {
		return fmt.Errorf("%d menu items match %q", len(found), target)
	}
	if !found[0].Enabled {
		return fmt.Errorf("menu item %q is disabled", target)
	}
	return s.Inspection.Invoke(ctx, w, found[0].ID)
}
func menuPath(s string) string {
	parts := strings.Split(s, ">")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, " > ")
}
func desktopClickControl(ctx context.Context, s *Session, w WindowInfo, target ControlTarget) error {
	if target.ID != "" {
		return invokeElement(ctx, s, w, target)
	}
	return nativeClickControl(ctx, s, w, target)
}
func isNativeDialog(w WindowInfo) bool { return false }

func nativeClickControl(ctx context.Context, s *Session, w WindowInfo, target ControlTarget) error {
	text := target.Text
	if text == "" {
		return fmt.Errorf("control label cannot be empty")
	}
	elements, err := s.Inspection.Elements(ctx, w)
	if err != nil {
		return err
	}
	var found []Element
	for _, exact := range controlMatchPasses(target) {
		for _, e := range elements {
			if !e.Enabled || !e.Visible || len(e.Actions) == 0 || (target.Role != "" && !strings.EqualFold(target.Role, e.Role)) || (target.Parent != "" && target.Parent != e.Parent) {
				continue
			}
			match := controlLabelMatches(e.Name, e.Text, e.Protected, target, exact)
			if match {
				found = append(found, e)
			}
		}
		if len(found) > 0 {
			break
		}
	}
	if len(found) != 1 {
		return fmt.Errorf("%d enabled controls with actions match %q", len(found), text)
	}
	control := found[0]
	if w.Width > 0 && w.Height > 0 && control.Width > 0 && control.Height > 0 && control.X > -100000 && control.Y > -100000 {
		x, y := control.X+control.Width/2, control.Y+control.Height/2
		if os.Getenv("WAYLAND_DISPLAY") != "" && len(elements) > 0 {
			x += w.X - elements[0].X
			y += w.Y - elements[0].Y
		}
		if x >= w.X && y >= w.Y && x < w.X+w.Width && y < w.Y+w.Height {
			if err := s.Window.Act(ctx, w, autoapi.WindowActivate); err == nil {
				if err = s.Mouse.Move(ctx, x, y, false); err == nil {
					if err = s.Mouse.Click(ctx, autoapi.ButtonLeft, 1); err != nil {
						return err
					}
					return nil
				}
			}
		}
	}
	if err := s.Inspection.Invoke(ctx, w, found[0].ID); err != nil {
		return err
	}
	return nil
}
