//go:build !windows && !darwin

package linuxdesk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	_ "image/jpeg" // decode captures the helpers write
	_ "image/png"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kbinani/screenshot"

	"github.com/grmrgecko/karakuri/automation/autoapi"
	"github.com/grmrgecko/karakuri/automation/internal/portal"
	"github.com/grmrgecko/karakuri/automation/toolsh"
)

// A Linux session is one of two things and the difference decides everything
// here. X11 has an interface for synthetic input and for the window list, so
// nothing needs installing. Wayland deliberately has neither: a compositor
// accepts input from its own devices, and the tools that get around that
// (wtype, ydotool) each need their own setup. So a Wayland session is served by
// whichever helper is installed, X11 is served directly, and a session with
// neither is told what to install rather than failing silently.

// waylandSession reports whether the session is a Wayland one. Both variables
// can be set at once under XWayland, and the compositor is the authority in that
// case: an X11 screenshot of a Wayland desktop is a picture of the few X clients
// on it and nothing else.
func waylandSession() bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
		return true
	}
	return strings.TrimSpace(os.Getenv("WAYLAND_DISPLAY")) != ""
}

// captureScreen photographs the screen, preferring the session's own helper on
// Wayland and the X server itself on X11.
func captureScreen(ctx context.Context, req autoapi.CaptureRequest) (image.Image, error) {
	if waylandSession() {
		if img, err := waylandCapture(ctx, req); err == nil {
			return img, nil
		} else if !autoapi.IsMissingTool(err) {
			return nil, err
		}
	}
	if x11Present() {
		return x11Capture(req)
	}
	return nil, autoapi.MissingTool("take a screenshot", "grim", "spectacle", "gnome-screenshot")
}

// x11Capture reads the screen out of the X server. The library talks the X
// protocol itself, so this needs no helper installed and no cgo.
func x11Capture(req autoapi.CaptureRequest) (image.Image, error) {
	displays := screenshot.NumActiveDisplays()
	if displays == 0 {
		return nil, fmt.Errorf("the X display reports no active screens")
	}
	if req.Display < 0 || req.Display >= displays {
		return nil, fmt.Errorf("screen %d does not exist; this session has %d", req.Display, displays)
	}

	bounds := screenshot.GetDisplayBounds(req.Display)
	if req.Region && req.DesktopCoordinates {
		for i := 0; i < displays; i++ {
			bounds = bounds.Union(screenshot.GetDisplayBounds(i))
		}
	}
	if req.Region {
		// A region is in the screen's own coordinates, so it is offset into
		// the desktop before the capture rather than after it.
		region := image.Rect(0, 0, req.Width, req.Height).Add(image.Pt(bounds.Min.X+req.X, bounds.Min.Y+req.Y))
		if req.DesktopCoordinates {
			region = image.Rect(0, 0, req.Width, req.Height).Add(image.Pt(req.X, req.Y))
		}
		clipped := region.Intersect(bounds)
		if clipped.Empty() {
			return nil, fmt.Errorf("the region %dx%d at %d,%d is outside screen %d",
				req.Width, req.Height, req.X, req.Y, req.Display)
		}
		img, err := screenshot.CaptureRect(clipped)
		if err != nil {
			return nil, fmt.Errorf("cannot capture the region: %w", err)
		}
		return autoapi.WithCaptureMapping(img, &autoapi.CaptureMapping{OriginX: float64(clipped.Min.X), OriginY: float64(clipped.Min.Y), UnitsPerPixelX: 1, UnitsPerPixelY: 1}), nil
	}

	img, err := screenshot.CaptureDisplay(req.Display)
	if err != nil {
		return nil, fmt.Errorf("cannot capture screen %d: %w", req.Display, err)
	}
	return autoapi.WithCaptureMapping(img, &autoapi.CaptureMapping{OriginX: float64(bounds.Min.X), OriginY: float64(bounds.Min.Y), UnitsPerPixelX: 1, UnitsPerPixelY: 1}), nil
}

// waylandCapture photographs through the Screenshot portal, which needs
// nothing installed, and behind it whichever screenshot helper the desktop
// ships. Everything but grim captures the whole desktop, so a region is cut
// out afterwards: cropping one image keeps every route answering the same
// request.
func waylandCapture(ctx context.Context, req autoapi.CaptureRequest) (image.Image, error) {
	if req.Display > 0 {
		return nil, fmt.Errorf("a Wayland screenshot captures the whole desktop, so screen %d "+
			"cannot be singled out; capture a region instead", req.Display)
	}

	// The portal is the compositor's own interface; a session without a portal
	// service, or one where the user declined the consent dialog, falls to the
	// installed helpers.
	if img, err := portal.CaptureScreenshot(ctx); err == nil {
		if req.Region {
			return cropImage(img, req.X, req.Y, req.Width, req.Height)
		}
		return img, nil
	}

	file, err := os.CreateTemp("", "karakuri-capture-*.png")
	if err != nil {
		return nil, fmt.Errorf("cannot create a file for the capture: %w", err)
	}
	path := file.Name()
	_ = file.Close()
	defer func() { _ = os.Remove(path) }()

	switch {
	case toolsh.HasTool("grim"):
		tool, _ := toolsh.ToolPath("grim")
		args := []string{}
		if req.Region {
			args = append(args, "-g", fmt.Sprintf("%d,%d %dx%d", req.X, req.Y, req.Width, req.Height))
		}
		if _, err := toolsh.RunTool(ctx, tool, append(args, path)...); err != nil {
			return nil, err
		}
	case toolsh.HasTool("spectacle"):
		tool, _ := toolsh.ToolPath("spectacle")
		// Background, no notification, full screen, to this file. Anything
		// interactive would wait for a person who is not there.
		if _, err := toolsh.RunTool(ctx, tool, "-b", "-n", "-f", "-o", path); err != nil {
			return nil, err
		}
	case toolsh.HasTool("gnome-screenshot"):
		tool, _ := toolsh.ToolPath("gnome-screenshot")
		if _, err := toolsh.RunTool(ctx, tool, "-f", path); err != nil {
			return nil, err
		}
	default:
		return nil, autoapi.MissingTool("take a screenshot on Wayland", "a desktop portal", "grim", "spectacle", "gnome-screenshot")
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("the screenshot helper wrote nothing to %s: %w", filepath.Base(path), err)
	}
	img, err := decodeCapture(data)
	if err != nil {
		return nil, err
	}
	if req.Region && !toolsh.HasTool("grim") {
		return cropImage(img, req.X, req.Y, req.Width, req.Height)
	}
	return img, nil
}

// listWindows reports the session's windows. Only X11 has an interface for
// this: a Wayland compositor does not tell a client what else is open, which is
// the point of the protocol rather than a gap in it.
func listWindows(ctx context.Context) ([]autoapi.WindowInfo, error) {
	if !x11Present() {
		return nil, errNoWindowList
	}
	windows, err := x11ListWindows(ctx)
	if err != nil {
		return nil, err
	}
	// Under XWayland the X server only knows about the X clients on the
	// desktop, so an empty list on a Wayland session means the compositor is
	// keeping its windows to itself rather than that nothing is open. Saying
	// "no windows" there would send a caller looking for a bad pattern.
	if len(windows) == 0 && waylandSession() {
		return nil, errNoWindowList
	}
	return windows, nil
}

// errNoWindowList is what a Wayland session has to say about its windows. The
// protocol has no interface for one client to see another's, by design, and
// XWayland only accounts for the X clients on the desktop.
var errNoWindowList = errors.New("this Wayland session does not expose its window list; " +
	"only windows running through XWayland can be seen, and there are none")

// windowAction acts on one window through the window manager.
func windowAction(ctx context.Context, target autoapi.WindowInfo, action string) error {
	if x11Present() {
		return x11WindowAction(ctx, target, action)
	}
	return fmt.Errorf("a Wayland compositor does not accept window commands from a client, and there is no X display to ask instead")
}

// typeText types into whatever has focus, through the session's own interface.
func typeText(ctx context.Context, clock autoapi.Clock, text string, delay time.Duration) error {
	if waylandSession() {
		if tool, ok := toolsh.ToolPath("wtype"); ok {
			_, err := toolsh.RunTool(ctx, tool, "--", text)
			return err
		}
		if tool, ok := toolsh.ToolPath("ydotool"); ok {
			_, err := toolsh.RunTool(ctx, tool, "type", "--", text)
			return err
		}
	}
	if x11Present() {
		return x11TypeText(ctx, clock, text, delay)
	}
	return autoapi.MissingTool("type into the session", "wtype", "ydotool")
}

// pressKeys presses one combination.
func pressKeys(ctx context.Context, stroke autoapi.KeyStroke) error {
	if waylandSession() {
		if tool, ok := toolsh.ToolPath("wtype"); ok {
			args, err := wtypeArgs(stroke)
			if err != nil {
				return err
			}
			_, err = toolsh.RunTool(ctx, tool, args...)
			return err
		}
	}
	if x11Present() {
		return x11PressKeys(ctx, stroke)
	}
	return autoapi.MissingTool("press keys in the session", "wtype")
}

// wtypeArgs renders a stroke as wtype's own arguments: modifiers down, the key,
// then the modifiers back up. wtype names keys with xkb keysyms, which is the
// same vocabulary the X11 table maps onto.
func wtypeArgs(stroke autoapi.KeyStroke) ([]string, error) {
	name := stroke.Key
	if stroke.Named() {
		mapped, ok := x11KeyNames[stroke.Key]
		if !ok {
			number, isFunction := autoapi.FunctionKeyNumber(stroke.Key)
			if !isFunction {
				return nil, fmt.Errorf("wtype has no key called %q", stroke.Key)
			}
			mapped = fmt.Sprintf("F%d", number)
		}
		name = mapped
	}

	args := make([]string, 0, len(stroke.Mods)*4+2)
	for _, mod := range stroke.Mods {
		args = append(args, "-M", wtypeModifier(mod))
	}
	args = append(args, "-k", name)
	for i := len(stroke.Mods) - 1; i >= 0; i-- {
		args = append(args, "-m", wtypeModifier(stroke.Mods[i]))
	}
	return args, nil
}

// wtypeModifier maps a canonical modifier onto wtype's name for it. Wayland
// calls the super key logo, after the keycap.
func wtypeModifier(mod string) string {
	if mod == autoapi.ModSuper {
		return "logo"
	}
	return mod
}

// moveMouse moves the pointer.
func moveMouse(ctx context.Context, x, y int, relative bool) error {
	if waylandSession() {
		if tool, ok := toolsh.ToolPath("ydotool"); ok {
			args := []string{"mousemove"}
			if !relative {
				args = append(args, "--absolute")
			}
			_, err := toolsh.RunTool(ctx, tool, append(args, "-x", strconv.Itoa(x), "-y", strconv.Itoa(y))...)
			return err
		}
	}
	if x11Present() {
		return x11MoveMouse(ctx, x, y, relative)
	}
	return autoapi.MissingTool("move the pointer", "ydotool")
}

// clickMouse clicks a button, however many times.
func clickMouse(ctx context.Context, button string, count int) error {
	if waylandSession() {
		if tool, ok := toolsh.ToolPath("ydotool"); ok {
			code, err := ydotoolButton(button, true, true)
			if err != nil {
				return err
			}
			for i := 0; i < count; i++ {
				if _, err := toolsh.RunTool(ctx, tool, "click", code); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if x11Present() {
		return x11ClickMouse(ctx, button, count)
	}
	return autoapi.MissingTool("click", "ydotool")
}

// holdMouse holds a button down or lets it up.
func holdMouse(ctx context.Context, button string, down bool) error {
	if waylandSession() {
		if tool, ok := toolsh.ToolPath("ydotool"); ok {
			code, err := ydotoolButton(button, down, !down)
			if err != nil {
				return err
			}
			_, err = toolsh.RunTool(ctx, tool, "click", code)
			return err
		}
	}
	if x11Present() {
		return x11HoldMouse(ctx, button, down)
	}
	return autoapi.MissingTool("hold a mouse button", "ydotool")
}

// scrollMouse turns the wheel.
func scrollMouse(ctx context.Context, amount int, horizontal bool) error {
	if waylandSession() {
		if tool, ok := toolsh.ToolPath("ydotool"); ok {
			x, y := 0, -amount
			if horizontal {
				x, y = amount, 0
			}
			_, err := toolsh.RunTool(ctx, tool, "mousemove", "--wheel", "-x", strconv.Itoa(x), "-y", strconv.Itoa(y))
			return err
		}
	}
	if x11Present() {
		return x11ScrollMouse(ctx, amount, horizontal)
	}
	return autoapi.MissingTool("scroll", "ydotool")
}

// mousePoint reports where the pointer is. Only X11 answers: a Wayland client
// is told the pointer position inside its own surfaces and there are none here.
func mousePoint(ctx context.Context) (int, int, error) {
	// The X server tracks the pointer only while it is over an XWayland
	// window, so on a Wayland session it answers 0,0 for most of the screen.
	// A position that is wrong without saying so is worse than none.
	if waylandSession() {
		return 0, 0, errors.New("a Wayland compositor does not report the pointer position to a client")
	}
	if !x11Present() {
		return 0, 0, errors.New("there is no X display to read the pointer position from")
	}
	return x11MousePoint(ctx)
}

// ydotoolButton renders ydotool's button code: the low bits name the button and
// the high bits say whether it goes down, up, or both.
func ydotoolButton(button string, down, up bool) (string, error) {
	var code int
	switch button {
	case "", buttonLeft:
		code = 0x00
	case buttonRight:
		code = 0x01
	case buttonMiddle:
		code = 0x02
	default:
		return "", fmt.Errorf("unknown mouse button %q", button)
	}
	if down {
		code |= 0x40
	}
	if up {
		code |= 0x80
	}
	return fmt.Sprintf("0x%02X", code), nil
}

// waitFor pauses through the injected clock, so the pace of synthetic input is
// the host's idea of time rather than the backend's own.
func waitFor(ctx context.Context, clock autoapi.Clock, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-clock.After(d):
		return nil
	}
}

// cropImage cuts a region out of a capture. A helper that photographs the whole
// desktop and a library that can capture a rectangle then answer the same
// request, with the crop happening once here rather than per platform.
func cropImage(img image.Image, x, y, width, height int) (image.Image, error) {
	region := image.Rect(x, y, x+width, y+height).Add(img.Bounds().Min)
	clipped := region.Intersect(img.Bounds())
	if clipped.Empty() {
		return nil, fmt.Errorf("the region %dx%d at %d,%d is outside the %dx%d capture",
			width, height, x, y, img.Bounds().Dx(), img.Bounds().Dy())
	}
	type subImager interface {
		SubImage(r image.Rectangle) image.Image
	}
	if sub, ok := img.(subImager); ok {
		return sub.SubImage(clipped), nil
	}
	out := image.NewRGBA(image.Rect(0, 0, clipped.Dx(), clipped.Dy()))
	draw.Draw(out, out.Bounds(), img, clipped.Min, draw.Src)
	return out, nil
}

// decodeCapture reads back the image a screenshot helper wrote. Every helper
// writes PNG, and the format is applied afterwards by the caller.
func decodeCapture(data []byte) (image.Image, error) {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("the screenshot helper did not return a readable image: %w", err)
	}
	return img, nil
}
