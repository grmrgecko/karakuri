package gui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

// The GUI nodes drive the session the same way the desktop nodes do, but they
// aim at whatever is on screen rather than at a service: a keystroke lands
// wherever focus is. That is why the window nodes sit beside them — a flow
// activates the window it means first, and then types. On X11 all of this goes
// through the X server directly; a Wayland session has no such interface, so
// the same nodes fall back to whatever helper is installed and say which one is
// missing when none is.

// Screenshot capture modes.
const (
	captureScreenMode   = "screen"
	captureRegionMode   = "region"
	captureWindowMode   = "active-window"
	captureSelectedMode = "window"
)

// Screenshot encodings. PNG is exact and JPEG is small, which is the whole
// difference that matters for a picture of a screen.
const (
	imagePNG  = "png"
	imageJPEG = "jpeg"
)

// Window actions.
const (
	windowActivate = "activate"
	windowMinimize = "minimize"
	windowMaximize = "maximize"
	windowClose    = "close"
)

// Keyboard actions. Typing text and pressing a combination are different jobs:
// one follows the session's layout character by character, the other names keys
// the layout does not move.
const (
	keyboardType  = "type"
	keyboardPress = "press"
)

// Mouse actions.
const (
	mouseMove     = "move"
	mouseClick    = "click"
	mousePress    = "press"
	mouseRelease  = "release"
	mouseScroll   = "scroll"
	mousePosition = "position"
)

// Mouse buttons, named rather than numbered because the numbers differ by
// platform and a flow author should not have to know X11's.
const (
	buttonLeft   = "left"
	buttonMiddle = "middle"
	buttonRight  = "right"
)

// typeDelayDefault is the pause between keystrokes when typing. Synthetic input
// arriving faster than a person can type is dropped by some toolkits, and 12ms
// is the delay xdotool settled on for the same reason.
const typeDelayDefault = 12 * time.Millisecond

// Node type names this group contributes. They are the names an operator
// writes in the disable list and the names the host namespaces as
// agent.<id>.<name>, so they are constants rather than literals in the metadata.
const (
	nodeScreenshot = "screenshot"
	nodeWindowList = "window-list"
	nodeWindow     = "window"
	nodeKeyboard   = "keyboard"
	nodeMouse      = "mouse"
)

// screenshotNode photographs the agent host's screen. It is an output-only pure
// source: the capture reads the world and changes nothing in it, so a flow
// binds it to a config field and several consumers share one photograph.
type screenshotNode struct {
	sess    *automation.Session
	initErr error
}

func (screenshotNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:              nodeScreenshot,
		Folder:            folder,
		Label:             "Screenshot",
		Description:       "Captures this agent host's screen as an image, pulled when a consumer needs it",
		Icon:              "las la-camera",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Image", Type: flow.PortBinary},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "capture",
				Label:   "Capture",
				Type:    flow.FieldSelect,
				Default: captureScreenMode,
				Options: []nodeapi.Option{
					{Value: captureScreenMode, Label: "A whole screen"},
					{Value: captureRegionMode, Label: "A region"},
					{Value: captureWindowMode, Label: "The active window"},
					{Value: captureSelectedMode, Label: "A selected window"},
				},
			},
			{ID: "window_id", Label: "Window id", Type: flow.FieldString, ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureSelectedMode}, Help: "Exact window id. Supports {{ }} expressions."},
			{ID: "window_match", Label: "Window match pattern", Type: flow.FieldString, ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureSelectedMode}, Help: "Go regular expression over title and class. Requires one match. Supports {{ }} expressions."},
			{
				ID:      "display",
				Label:   "Screen",
				Type:    flow.FieldNumber,
				Default: 0,
				Help:    "Which screen to photograph, numbered from zero in the order the session reports them.",
			},
			{
				ID:     "x",
				Label:  "Left",
				Type:   flow.FieldNumber,
				ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureRegionMode},
				Help:   "Region offset in pixels, from the top left of the chosen screen.",
			},
			{
				ID:     "y",
				Label:  "Top",
				Type:   flow.FieldNumber,
				ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureRegionMode},
			},
			{
				ID:     "width",
				Label:  "Width",
				Type:   flow.FieldNumber,
				ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureRegionMode},
			},
			{
				ID:     "height",
				Label:  "Height",
				Type:   flow.FieldNumber,
				ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureRegionMode},
			},
			{
				ID:      "format",
				Label:   "Format",
				Type:    flow.FieldSelect,
				Default: imagePNG,
				Options: []nodeapi.Option{
					{Value: imagePNG, Label: "PNG"},
					{Value: imageJPEG, Label: "JPEG"},
				},
			},
			{
				ID:      "quality",
				Label:   "JPEG quality",
				Type:    flow.FieldNumber,
				Default: 85,
				ShowIf:  &nodeapi.ShowIf{Field: "format", Equals: imageJPEG},
			},
		},
	}
	fields := windowFilterFields()
	for i := range fields {
		fields[i].ShowIf = &nodeapi.ShowIf{Field: "capture", Equals: captureSelectedMode}
	}
	m.ConfigSpec = append(m.ConfigSpec, fields...)
	m.ConfigSpec = append(m.ConfigSpec, nodeapi.FieldSpec{ID: "desktop_coordinates", Label: "Use desktop coordinates", Type: flow.FieldBoolean, ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: captureRegionMode}, Help: "Region coordinates span displays and may be negative."})
	return m
}

// screenshotConfig is the decoded capture configuration.
type screenshotConfig struct {
	windowFilters
	Capture            string `json:"capture"`
	DesktopCoordinates bool   `json:"desktop_coordinates"`
	WindowID           string `json:"window_id"`
	WindowMatch        string `json:"window_match"`
	Display            int    `json:"display"`
	X                  int    `json:"x"`
	Y                  int    `json:"y"`
	Width              int    `json:"width"`
	Height             int    `json:"height"`
	Format             string `json:"format"`
	Quality            int    `json:"quality"`
}

// Execute photographs the screen and emits the encoded image as the payload.
func (n *screenshotNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg screenshotConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	req := automation.CaptureRequest{DesktopCoordinates: cfg.DesktopCoordinates, Display: cfg.Display, Region: cfg.Capture == captureRegionMode,
		X: cfg.X, Y: cfg.Y, Width: cfg.Width, Height: cfg.Height}
	var img image.Image
	var err error
	if cfg.Capture == captureWindowMode || cfg.Capture == captureSelectedMode {
		msg := agentnode.PrimaryInput(inputs)
		target, e := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, false, false)
		if e != nil {
			return nil, e
		}
		if cfg.Capture == captureWindowMode {
			target = automation.WindowTarget{Active: true}
		}
		window, e := automation.SelectWindow(ctx, n.sess, target)
		if e != nil {
			return nil, e
		}
		img, err = n.sess.Desktop.CaptureWindow(ctx, window)
	} else {
		if cfg.Capture != "" && cfg.Capture != captureScreenMode && cfg.Capture != captureRegionMode {
			return nil, fmt.Errorf("unknown capture mode %q", cfg.Capture)
		}
		if req.Region && (req.Width <= 0 || req.Height <= 0) {
			return nil, fmt.Errorf("a region needs a width and a height")
		}
		img, err = n.sess.Screen.Capture(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	// One capture path, one encoder: every platform hands back an image and
	// the format is applied here, so a JPEG from a helper that only writes PNG
	// is still a JPEG.
	data, err := encodeImage(img, cfg.Format, cfg.Quality)
	if err != nil {
		return nil, err
	}

	bounds := img.Bounds()
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: data,
		Meta: map[string]any{
			"format":  imageFormat(cfg.Format),
			"width":   bounds.Dx(),
			"height":  bounds.Dy(),
			"display": cfg.Display,
			"bytes":   len(data),
		},
	}), nil
}

// encodeImage renders a captured image in the configured format.
func encodeImage(img image.Image, format string, quality int) ([]byte, error) {
	var buf bytes.Buffer
	switch imageFormat(format) {
	case imageJPEG:
		if quality <= 0 || quality > 100 {
			quality = 85
		}
		if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality}); err != nil {
			return nil, fmt.Errorf("encoding the capture as JPEG: %w", err)
		}
	default:
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encoding the capture as PNG: %w", err)
		}
	}
	return buf.Bytes(), nil
}

// imageFormat resolves the configured format, defaulting to PNG.
func imageFormat(format string) string {
	if format == imageJPEG {
		return imageJPEG
	}
	return imagePNG
}

// windowValue shapes a window from the automation module for a payload.
func windowValue(w automation.WindowInfo) map[string]any {
	out := map[string]any{
		"id":             w.ID,
		"title":          w.Title,
		"active":         w.Active,
		"image":          w.Image,
		"enabled":        w.Enabled,
		"geometry_known": w.Width > 0 && w.Height > 0,
	}
	if w.Class != "" {
		out["class"] = w.Class
	}
	if w.PID > 0 {
		out["pid"] = w.PID
	}
	if w.Width > 0 && w.Height > 0 {
		out["geometry"] = map[string]any{"x": w.X, "y": w.Y, "width": w.Width, "height": w.Height}
	}
	return out
}

// windowListNode reports the windows open on the agent host. It is a reading
// like the metrics nodes, and it carries the focused window alongside the list
// because "what is the user looking at" and "what is open" are one look at the
// session, not two.
type windowListNode struct {
	sess    *automation.Session
	initErr error
}

func (windowListNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:              nodeWindowList,
		Folder:            folder,
		Label:             "Window list",
		Description:       "Lists the windows open in the agent user's session, with the focused one",
		Icon:              "las la-window-restore",
		Pure:              true,
		Retryable:         true,
		TestWithoutParams: true,
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortObject},
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:          "match",
				Label:       "Match pattern",
				Type:        flow.FieldString,
				Placeholder: "Firefox",
				Help: "Matches each window's title or class using Window match mode. " +
					"Empty lists everything. The focused window is reported whether it matches or not.",
			},
		},
	}
	m.ConfigSpec = append(m.ConfigSpec, windowFilterFields()...)
	return m
}

// windowListConfig is the decoded listing configuration.
type windowListConfig struct {
	windowFilters
	Match string `json:"match"`
}

// Execute reads the window list once.
func (n *windowListNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg windowListConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	target, err := cfg.windowFilters.target(ec, agentnode.PrimaryInput(inputs), "", cfg.Match, false, true)
	if err != nil {
		return nil, err
	}
	if cfg.MatchMode == "" || cfg.MatchMode == "regex" {
		target.Match = strings.TrimSpace(target.Match)
	}
	windows, err := n.sess.Window.List(ctx)
	if err != nil {
		return nil, err
	}

	selected, err := automation.SelectWindows(windows, target)
	if err != nil {
		return nil, err
	}
	matched := make([]any, 0, len(selected))
	for _, w := range selected {
		matched = append(matched, windowValue(w))
	}
	var active any
	for _, w := range windows {
		if w.Active {
			active = windowValue(w)
			break
		}
	}
	return nodeapi.One("out", &nodeapi.Msg{
		Payload: map[string]any{
			"count":   len(matched),
			"windows": matched,
			"active":  active,
		},
		Meta: map[string]any{"source": "window-list"},
	}), nil
}

// windowNode acts on one window: the half of window management that changes
// something, and the node a flow puts in front of a keystroke so it lands where
// it was meant to.
type windowNode struct {
	sess    *automation.Session
	initErr error
}

func (windowNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:        nodeWindow,
		Folder:      folder,
		Label:       "Window",
		Description: "Focuses, minimizes, maximizes, or closes a window in the agent user's session",
		Icon:        "las la-window-maximize",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "action",
				Label:   "Action",
				Type:    flow.FieldSelect,
				Default: windowActivate,
				Options: []nodeapi.Option{
					{Value: windowActivate, Label: "Focus it"},
					{Value: windowMinimize, Label: "Minimize it"},
					{Value: windowMaximize, Label: "Maximize it"},
					{Value: windowClose, Label: "Close it"},
				},
			},
			{
				ID:          "match",
				Label:       "Match pattern",
				Type:        flow.FieldString,
				Placeholder: "Firefox",
				Help: "Matches each window's title or class using Window match mode. " +
					"The pattern must match exactly one window; use an id when windows are similar.",
			},
			{
				ID:          "id",
				Label:       "Window id",
				Type:        flow.FieldString,
				Placeholder: "0x2a00007",
				Help:        "Exact window from a window list entry. It wins over the pattern when both are set.",
			},
		},
	}
	m.ConfigSpec = append(m.ConfigSpec, windowFilterFields()...)
	return m
}

// windowConfig is the decoded window action configuration.
type windowConfig struct {
	windowFilters
	Action string `json:"action"`
	Match  string `json:"match"`
	ID     string `json:"id"`
}

// Execute finds the window and performs the action.
func (n *windowNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg windowConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)

	selector, err := cfg.windowFilters.target(ec, msg, cfg.ID, cfg.Match, false, false)
	if err != nil {
		return nil, err
	}
	target, err := automation.SelectWindow(ctx, n.sess, selector)
	if err != nil {
		return nil, err
	}
	action := cfg.Action
	if action == "" {
		action = windowActivate
	}
	if err := n.sess.Window.Act(ctx, target, automation.WindowAction(action)); err != nil {
		return nil, err
	}
	ec.Logger().Debug("window action", zap.String("action", action), zap.String("title", target.Title))

	out := agentnode.PassThrough(msg, map[string]any{"window": windowValue(target), "action": action})
	return nodeapi.One("out", out), nil
}

// keyboardNode optionally verifies a selected window before sending input.
type keyboardNode struct {
	sess    *automation.Session
	initErr error
}

func (keyboardNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:        nodeKeyboard,
		Folder:      folder,
		Label:       "Keyboard",
		Description: "Types text or presses a key combination in the agent user's session",
		Icon:        "las la-keyboard",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "action",
				Label:   "Action",
				Type:    flow.FieldSelect,
				Default: keyboardType,
				Options: []nodeapi.Option{
					{Value: keyboardType, Label: "Type text"},
					{Value: keyboardPress, Label: "Press a key combination"},
				},
			},
			{
				ID:          "text",
				Label:       "Text",
				Type:        flow.FieldText,
				ShowIf:      &nodeapi.ShowIf{Field: "action", Equals: keyboardType},
				Placeholder: "{{ payload.message }}",
				Help:        "{{ }} segments are rendered against the message.",
			},
			{
				ID:          "keys",
				Label:       "Keys",
				Type:        flow.FieldString,
				ShowIf:      &nodeapi.ShowIf{Field: "action", Equals: keyboardPress},
				Placeholder: "ctrl+alt+t",
				Help: "Modifiers joined with +, then one key: ctrl, alt, shift, super, and names " +
					"like return, escape, tab, space, up, f5, or a single character.",
			},
			{
				ID:      "repeat",
				Label:   "Repeat",
				Type:    flow.FieldNumber,
				Default: 1,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: keyboardPress},
			},
			{
				ID:      "delay",
				Label:   "Keystroke delay",
				Type:    flow.FieldNumber,
				Default: 12,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: keyboardType},
				Help: "Milliseconds between keystrokes. Some applications drop input that arrives " +
					"faster than a person types; zero sends it as fast as the session accepts it.",
			},
		},
	}
	m.ConfigSpec = append(m.ConfigSpec, windowFields()...)
	m.ConfigSpec = append(m.ConfigSpec, nodeapi.FieldSpec{ID: "activate_window", Label: "Activate target window", Type: flow.FieldBoolean, Help: "With a target, focus must be verified before input. Enabling this brings that window forward first."})
	return m
}

// keyboardConfig is the decoded keyboard configuration.
type keyboardConfig struct {
	windowFilters
	Action      string `json:"action"`
	WindowID    string `json:"window_id"`
	WindowMatch string `json:"window_match"`
	Active      bool   `json:"active_window"`
	Activate    bool   `json:"activate_window"`
	Text        string `json:"text"`
	Keys        string `json:"keys"`
	Repeat      int    `json:"repeat"`
	Delay       *int   `json:"delay"`
}

// Execute types or presses, and passes the message through.
func (n *keyboardNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg keyboardConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)

	selector, err := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, cfg.Active, false)
	if err != nil {
		return nil, err
	}
	if hasWindowTarget(selector) {
		window, err := automation.SelectWindow(ctx, n.sess, selector)
		if err != nil {
			return nil, err
		}
		if cfg.Activate {
			if err := n.sess.Window.Act(ctx, window, automation.WindowAction(windowActivate)); err != nil {
				return nil, err
			}
			focusCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			for {
				focused, err := automation.SelectWindow(focusCtx, n.sess, automation.WindowTarget{ID: window.ID})
				if err != nil {
					return nil, fmt.Errorf("checking keyboard focus: %w", err)
				}
				if focused.Active {
					break
				}
				select {
				case <-focusCtx.Done():
					return nil, fmt.Errorf("window %q did not gain focus: %w", window.ID, focusCtx.Err())
				case <-ec.Clock().After(50 * time.Millisecond):
				}
			}
		} else if !window.Active {
			return nil, fmt.Errorf("window %q is not focused; enable target activation", window.ID)
		}
	} else if cfg.Activate {
		return nil, fmt.Errorf("target activation requires a window selector")
	}

	switch cfg.Action {
	case "", keyboardType:
		// The text is typed as written. A trailing line break is the Return
		// that submits a command, so it cannot be trimmed with the template.
		text, err := ec.Interpolate(cfg.Text, msg)
		if err != nil {
			return nil, fmt.Errorf("invalid template %q: %w", cfg.Text, err)
		}
		if text == "" {
			return nil, fmt.Errorf("no text to type")
		}
		delay := typeDelayDefault
		if cfg.Delay != nil {
			delay = time.Duration(*cfg.Delay) * time.Millisecond
		}
		if err := n.sess.Keyboard.TypeText(ctx, ec.Clock(), text, delay); err != nil {
			return nil, err
		}
		out := agentnode.PassThrough(msg, map[string]any{"typed": len([]rune(text))})
		return nodeapi.One("out", out), nil

	case keyboardPress:
		raw, err := agentnode.Interpolate(ec, cfg.Keys, msg)
		if err != nil {
			return nil, err
		}
		stroke, err := automation.ParseKeyStroke(raw)
		if err != nil {
			return nil, err
		}
		repeat := cfg.Repeat
		if repeat <= 0 {
			repeat = 1
		}
		for i := 0; i < repeat; i++ {
			if err := n.sess.Keyboard.PressKeys(ctx, stroke); err != nil {
				return nil, err
			}
		}
		out := agentnode.PassThrough(msg, map[string]any{"pressed": stroke.String(), "repeat": repeat})
		return nodeapi.One("out", out), nil

	default:
		return nil, fmt.Errorf("unknown keyboard action %q", cfg.Action)
	}
}

// mouseNode moves, clicks, or scrolls the pointer in the agent user's session.
type mouseNode struct {
	sess    *automation.Session
	initErr error
}

func (mouseNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{
		Type:        nodeMouse,
		Folder:      folder,
		Label:       "Mouse",
		Description: "Moves, clicks, or scrolls the pointer in the agent user's session",
		Icon:        "las la-mouse-pointer",
		Inputs: []nodeapi.PortSpec{
			{ID: "in", Label: "In", Type: flow.PortAny},
		},
		Outputs: []nodeapi.PortSpec{
			// Reading the pointer replaces the payload with the coordinates;
			// every other action hands the message along untouched, which is
			// what the rule's default defers to.
			{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in", TypeRule: &flow.PortTypeRule{
				Cases: []flow.PortTypeCase{
					{Match: map[string][]string{"action": {mousePosition}}, Type: flow.PortObject},
				},
				Default: flow.PortAny,
			}},
			flow.ErrorPort(),
		},
		ConfigSpec: []nodeapi.FieldSpec{
			{
				ID:      "action",
				Label:   "Action",
				Type:    flow.FieldSelect,
				Default: mouseClick,
				Options: []nodeapi.Option{
					{Value: mouseMove, Label: "Move the pointer"},
					{Value: mouseClick, Label: "Click"},
					{Value: mousePress, Label: "Hold a button down"},
					{Value: mouseRelease, Label: "Let a button up"},
					{Value: mouseScroll, Label: "Scroll"},
					{Value: mousePosition, Label: "Read the pointer position"},
				},
			},
			{
				ID:    "x",
				Label: "X",
				Type:  flow.FieldNumber,
				Help:  "Used when moving, or when Click at coordinates is on.",
			},
			{
				ID:    "y",
				Label: "Y",
				Type:  flow.FieldNumber,
				Help:  "Used when moving, or when Click at coordinates is on.",
			},
			{
				ID:      "move_before_click",
				Label:   "Click at coordinates",
				Type:    flow.FieldBoolean,
				Default: false,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: mouseClick},
			},
			{
				ID:      "relative_to_window",
				Label:   "Coordinates are inside a window",
				Type:    flow.FieldBoolean,
				Default: false,
				Help:    "Adds the matched window's top-left position to X and Y.",
			},
			{ID: "window_id", Label: "Window id", Type: flow.FieldString, Placeholder: "0x2a00007",
				Help: "Exact window id used when coordinates are inside a window."},
			{ID: "window_match", Label: "Window match pattern", Type: flow.FieldString, Placeholder: "Firefox",
				Help: "Used when no window id is set."},
			{
				ID:      "relative",
				Label:   "Relative to where the pointer is",
				Type:    flow.FieldBoolean,
				Default: false,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: mouseMove},
			},
			{
				ID:      "button",
				Label:   "Button",
				Type:    flow.FieldSelect,
				Default: buttonLeft,
				Options: []nodeapi.Option{
					{Value: buttonLeft, Label: "Left"},
					{Value: buttonMiddle, Label: "Middle"},
					{Value: buttonRight, Label: "Right"},
				},
				Help: "Used by the click, hold, and release actions.",
			},
			{
				ID:      "count",
				Label:   "Clicks",
				Type:    flow.FieldNumber,
				Default: 1,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: mouseClick},
				Help:    "Two is a double click. The pointer is not moved first; put a move in front of it.",
			},
			{
				ID:      "amount",
				Label:   "Amount",
				Type:    flow.FieldNumber,
				Default: 3,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: mouseScroll},
				Help:    "Wheel steps. Negative scrolls the other way: down, or right when horizontal.",
			},
			{
				ID:      "horizontal",
				Label:   "Scroll sideways",
				Type:    flow.FieldBoolean,
				Default: false,
				ShowIf:  &nodeapi.ShowIf{Field: "action", Equals: mouseScroll},
			},
		},
	}
	m.ConfigSpec = append(m.ConfigSpec, windowFilterFields()...)
	return m
}

// mouseConfig is the decoded pointer configuration.
type mouseConfig struct {
	windowFilters
	Action           string `json:"action"`
	X                int    `json:"x"`
	Y                int    `json:"y"`
	Relative         bool   `json:"relative"`
	Button           string `json:"button"`
	Count            int    `json:"count"`
	Amount           int    `json:"amount"`
	Horizontal       bool   `json:"horizontal"`
	MoveBeforeClick  bool   `json:"move_before_click"`
	RelativeToWindow bool   `json:"relative_to_window"`
	WindowID         string `json:"window_id"`
	WindowMatch      string `json:"window_match"`
}

// Execute performs the pointer action.
func (n *mouseNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg mouseConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	if cfg.RelativeToWindow {
		selector, err := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, false, false)
		if err != nil {
			return nil, err
		}
		cfg.WindowID, cfg.WindowMatch = selector.ID, selector.Match
		cfg.Title, cfg.Class, cfg.Image = selector.Title, selector.Class, selector.Image
	}
	button := cfg.Button
	if button == "" {
		button = buttonLeft
	}

	switch cfg.Action {
	case mouseMove:
		x, y, relative, err := n.mousePoint(ctx, cfg)
		if err != nil {
			return nil, err
		}
		if err := n.sess.Mouse.Move(ctx, x, y, relative); err != nil {
			return nil, err
		}
		out := agentnode.PassThrough(msg, map[string]any{"moved": map[string]any{"x": x, "y": y}, "relative": relative})
		return nodeapi.One("out", out), nil

	case "", mouseClick:
		count := cfg.Count
		if count <= 0 {
			count = 1
		}
		if cfg.MoveBeforeClick {
			x, y, relative, err := n.mousePoint(ctx, cfg)
			if err != nil {
				return nil, err
			}
			if err := n.sess.Mouse.Move(ctx, x, y, relative); err != nil {
				return nil, err
			}
		}
		if err := n.sess.Mouse.Click(ctx, automation.Button(button), count); err != nil {
			return nil, err
		}
		out := agentnode.PassThrough(msg, map[string]any{"clicked": button, "count": count})
		return nodeapi.One("out", out), nil

	case mousePress, mouseRelease:
		if err := n.sess.Mouse.Hold(ctx, automation.Button(button), cfg.Action == mousePress); err != nil {
			return nil, err
		}
		out := agentnode.PassThrough(msg, map[string]any{"button": button, "held": cfg.Action == mousePress})
		return nodeapi.One("out", out), nil

	case mouseScroll:
		amount := cfg.Amount
		if amount == 0 {
			amount = 3
		}
		if err := n.sess.Mouse.Scroll(ctx, amount, cfg.Horizontal); err != nil {
			return nil, err
		}
		out := agentnode.PassThrough(msg, map[string]any{"scrolled": amount, "horizontal": cfg.Horizontal})
		return nodeapi.One("out", out), nil

	case mousePosition:
		x, y, err := n.sess.Mouse.Position(ctx)
		if err != nil {
			return nil, err
		}
		// Reading the pointer is the one action with an answer, so it replaces
		// the payload; the port type rule says so too.
		out := agentnode.PassThrough(msg, nil)
		out.Payload = map[string]any{"x": x, "y": y}
		return nodeapi.One("out", out), nil

	default:
		return nil, fmt.Errorf("unknown mouse action %q", cfg.Action)
	}
}

func (n *mouseNode) mousePoint(ctx context.Context, cfg mouseConfig) (int, int, bool, error) {
	if !cfg.RelativeToWindow {
		return cfg.X, cfg.Y, cfg.Relative, nil
	}
	if cfg.Relative {
		return 0, 0, false, fmt.Errorf("coordinates cannot be relative to both the pointer and a window")
	}
	window, err := automation.SelectWindow(ctx, n.sess, automation.WindowTarget{ID: cfg.WindowID, Match: cfg.WindowMatch, Regex: true, MatchMode: cfg.MatchMode, Title: cfg.Title, Class: cfg.Class, PID: cfg.PID, Image: cfg.Image})
	if err != nil {
		return 0, 0, false, err
	}
	if window.Width <= 0 || window.Height <= 0 {
		return 0, 0, false, fmt.Errorf("the window backend did not report geometry for %q", window.Title)
	}
	return window.X + cfg.X, window.Y + cfg.Y, false, nil
}
