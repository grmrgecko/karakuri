package automation

import (
	"bytes"
	"context"
	"image"
	"image/png"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/hrpc"
)

type desktopArgs struct {
	Window    WindowInfo
	Target    string
	Control   ControlTarget
	Dialogs   DialogOptions
	Drag      DragRequest
	Placement WindowPlacement
}
type helperDesktop struct{ h *helper }

func (d helperDesktop) Drag(ctx context.Context, request DragRequest) error {
	return d.h.callOnce(ctx, "desktop", "drag", desktopArgs{Drag: request}, nil)
}
func (d helperDesktop) PlaceWindow(ctx context.Context, w WindowInfo, request WindowPlacement) error {
	return d.h.callOnce(ctx, "desktop", "place_window", desktopArgs{Window: w, Placement: request}, nil)
}
func (d helperDesktop) Controls(ctx context.Context, w WindowInfo) ([]Control, error) {
	var out []Control
	err := d.h.call(ctx, "desktop", "controls", desktopArgs{Window: w}, &out)
	return out, err
}
func (d helperDesktop) Menus(ctx context.Context, w WindowInfo) ([]MenuItem, error) {
	var out []MenuItem
	err := d.h.call(ctx, "desktop", "menus", desktopArgs{Window: w}, &out)
	return out, err
}
func (d helperDesktop) InvokeMenu(ctx context.Context, w WindowInfo, target string) error {
	return d.h.callOnce(ctx, "desktop", "invoke_menu", desktopArgs{Window: w, Target: target}, nil)
}
func (d helperDesktop) ClickControl(ctx context.Context, w WindowInfo, target ControlTarget) error {
	return d.h.callOnce(ctx, "desktop", "click_control", desktopArgs{Window: w, Control: target}, nil)
}
func (d helperDesktop) CaptureWindow(ctx context.Context, w WindowInfo) (image.Image, error) {
	var out captureResult
	if err := d.h.call(ctx, "desktop", "capture_window", desktopArgs{Window: w}, &out); err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(out.PNG))
	if err != nil {
		return nil, err
	}
	return autoapi.WithCaptureMapping(img, out.Mapping), nil
}
func (d helperDesktop) Dialogs(ctx context.Context, options DialogOptions) (DialogResultList, error) {
	var out DialogResultList
	err := d.h.call(ctx, "desktop", "dialogs", desktopArgs{Dialogs: options}, &out)
	return out, err
}
func serveDesktop(ctx context.Context, s *Session, req hrpc.Request) (any, error) {
	var args desktopArgs
	if len(req.Args) > 0 {
		if err := hrpc.Unmarshal(req.Args, &args); err != nil {
			return nil, err
		}
	}
	switch req.Method {
	case "drag":
		return nil, s.Desktop.Drag(ctx, args.Drag)
	case "place_window":
		return nil, s.Desktop.PlaceWindow(ctx, args.Window, args.Placement)
	case "controls":
		return s.Desktop.Controls(ctx, args.Window)
	case "menus":
		return s.Desktop.Menus(ctx, args.Window)
	case "invoke_menu":
		return nil, s.Desktop.InvokeMenu(ctx, args.Window, args.Target)
	case "click_control":
		return nil, s.Desktop.ClickControl(ctx, args.Window, args.Control)
	case "dialogs":
		return s.Desktop.Dialogs(ctx, args.Dialogs)
	case "capture_window":
		img, err := s.Desktop.CaptureWindow(ctx, args.Window)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		err = png.Encode(&buf, img)
		return captureResult{PNG: buf.Bytes(), Mapping: autoapi.ImageCaptureMapping(img)}, err
	}
	return nil, unknownMethod(req)
}
