package automation

import (
	"context"
	"fmt"
	"image"
	"runtime"
	"strings"
)

// Control describes a native or accessible control. Protected text is omitted.
type Control struct {
	ID        string `json:"id"`
	Parent    string `json:"parent,omitempty"`
	Name      string `json:"name,omitempty"`
	Role      string `json:"role,omitempty"`
	Class     string `json:"class"`
	Text      string `json:"text"`
	ControlID int32  `json:"control_id,omitempty"`
	X         int    `json:"x"`
	Y         int    `json:"y"`
	Width     int    `json:"width"`
	Height    int    `json:"height"`
	Enabled   bool   `json:"enabled"`
	Visible   bool   `json:"visible"`
	Protected bool   `json:"protected,omitempty"`
	Error     string `json:"error,omitempty"`
}

// MenuItem retains a provider's command ID and normalized menu path.
type MenuItem struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Path     string     `json:"path"`
	Enabled  bool       `json:"enabled"`
	Checked  *bool      `json:"checked"`
	Children []MenuItem `json:"children,omitempty"`
}

// ControlTarget selects an accessible element ID or a unique native label.
type ControlTarget struct {
	ID        string `json:"id,omitempty"`
	Text      string `json:"text,omitempty"`
	Role      string `json:"role,omitempty"`
	Parent    string `json:"parent,omitempty"`
	MatchMode string `json:"match_mode,omitempty"`
}

// DialogInfo records a discovered application dialog and its controls.
type DialogInfo struct {
	Window   SnapshotWindowInfo `json:"window"`
	Controls []Control          `json:"controls"`
}

// DialogResultList distinguishes incomplete inspection from an empty desktop.
type DialogResultList struct {
	Dialogs []DialogInfo      `json:"dialogs"`
	Errors  map[string]string `json:"inspection_errors,omitempty"`
}

// DialogOptions constrains discovery before provider inspection.
type DialogOptions struct {
	Target          WindowTarget
	Scope           bool
	IncludeControls *bool
}

// Desktop runs native operations in the same desktop session as input and capture.
type Desktop interface {
	Drag(context.Context, DragRequest) error
	PlaceWindow(context.Context, WindowInfo, WindowPlacement) error
	Controls(context.Context, WindowInfo) ([]Control, error)
	Menus(context.Context, WindowInfo) ([]MenuItem, error)
	InvokeMenu(context.Context, WindowInfo, string) error
	ClickControl(context.Context, WindowInfo, ControlTarget) error
	CaptureWindow(context.Context, WindowInfo) (image.Image, error)
	Dialogs(context.Context, DialogOptions) (DialogResultList, error)
}

type localDesktop struct{ sess *Session }

func (d localDesktop) Controls(ctx context.Context, w WindowInfo) ([]Control, error) {
	return desktopControls(ctx, d.sess, w)
}
func (d localDesktop) Menus(ctx context.Context, w WindowInfo) ([]MenuItem, error) {
	return desktopMenus(ctx, d.sess, w)
}
func (d localDesktop) InvokeMenu(ctx context.Context, w WindowInfo, target string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("menu target is required")
	}
	release, err := d.sess.input.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return desktopInvokeMenu(ctx, rawInputSession(d.sess), w, target)
}
func (d localDesktop) ClickControl(ctx context.Context, w WindowInfo, target ControlTarget) error {
	if target.ID == "" && target.Text == "" {
		return fmt.Errorf("control label or element id is required")
	}
	if target.MatchMode != "" && target.MatchMode != "auto" && target.MatchMode != "exact" && target.MatchMode != "contains" {
		return fmt.Errorf("unknown control match mode %q", target.MatchMode)
	}
	release, err := d.sess.input.take(ctx)
	if err != nil {
		return err
	}
	defer release()
	return desktopClickControl(ctx, rawInputSession(d.sess), w, target)
}
func (d localDesktop) CaptureWindow(ctx context.Context, w WindowInfo) (image.Image, error) {
	if w.Width <= 0 || w.Height <= 0 {
		return nil, fmt.Errorf("window %q has no usable geometry", w.Title)
	}
	if img, err := captureSelectedWindow(ctx, w); err == nil {
		return img, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return d.sess.Screen.Capture(ctx, CaptureRequest{Region: true, DesktopCoordinates: true, X: w.X, Y: w.Y, Width: w.Width, Height: w.Height})
}
func (d localDesktop) Dialogs(ctx context.Context, options DialogOptions) (DialogResultList, error) {
	out := DialogResultList{Dialogs: make([]DialogInfo, 0), Errors: make(map[string]string)}
	windows, err := d.sess.Window.List(ctx)
	if err != nil {
		return out, err
	}
	if options.Scope {
		windows, err = SelectWindows(windows, options.Target)
		if err != nil {
			return out, err
		}
	}
	for _, w := range windows {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		dialog := isNativeDialog(w)
		if runtime.GOOS == "windows" && !dialog {
			continue
		}
		if !dialog {
			elements, err := d.sess.Inspection.Elements(ctx, w)
			if err != nil {
				out.Errors[w.ID] = err.Error()
				continue
			}
			for _, e := range elements {
				if strings.Contains(strings.ToLower(e.Role+" "+e.Class), "dialog") || e.Role == "AXSheet" || strings.EqualFold(e.Role, "alert") {
					dialog = true
					break
				}
			}
		}
		if !dialog {
			continue
		}
		var controls []Control
		var err error
		if options.IncludeControls == nil || *options.IncludeControls {
			controls, err = d.Controls(ctx, w)
		}
		if err != nil {
			out.Errors[w.ID] = err.Error()
		}
		out.Dialogs = append(out.Dialogs, DialogInfo{NewWindowSnapshot(d.sess, w).Window, controls})
	}
	return out, nil
}

func invokeElement(ctx context.Context, s *Session, w WindowInfo, target ControlTarget) error {
	id := target.ID
	elements, err := s.Inspection.Elements(ctx, w)
	if err != nil {
		return err
	}
	for _, e := range elements {
		if e.ID == id {
			if (target.Role != "" && !strings.EqualFold(target.Role, e.Role)) || (target.Parent != "" && target.Parent != e.Parent) {
				return fmt.Errorf("element %q does not satisfy control constraints", id)
			}
			if !e.Enabled || !e.Visible {
				return fmt.Errorf("element %q is disabled or hidden", id)
			}
			return s.Inspection.Invoke(ctx, w, id)
		}
	}
	return fmt.Errorf("element %q no longer belongs to the window", id)
}

func controlLabelMatches(name, text string, protected bool, target ControlTarget, exact bool) bool {
	if protected {
		text = ""
	}
	if exact {
		return name == target.Text || text == target.Text
	}
	return strings.Contains(name, target.Text) || strings.Contains(text, target.Text)
}
func controlMatchPasses(target ControlTarget) []bool {
	switch target.MatchMode {
	case "exact":
		return []bool{true}
	case "contains":
		return []bool{false}
	default:
		return []bool{true, false}
	}
}
