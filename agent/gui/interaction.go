package gui

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

type interactionNode struct {
	kind    string
	sess    *automation.Session
	initErr error
}

func (n interactionNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{Type: n.kind, Folder: folder, Icon: "las la-mouse-pointer", Inputs: []nodeapi.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}}, Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortAny, TypeFrom: "in"}, flow.ErrorPort()}}
	if n.kind == "drag-drop" {
		m.Label = "Drag and drop"
		m.Description = "Drags between desktop or window-relative points and releases the button"
		m.ConfigSpec = []nodeapi.FieldSpec{
			{ID: "start_x", Label: "Start X", Type: flow.FieldNumber, Default: 0}, {ID: "start_y", Label: "Start Y", Type: flow.FieldNumber, Default: 0},
			{ID: "end_x", Label: "End X", Type: flow.FieldNumber, Default: 0}, {ID: "end_y", Label: "End Y", Type: flow.FieldNumber, Default: 0},
			{ID: "button", Label: "Button", Type: flow.FieldSelect, Default: "left", Options: []nodeapi.Option{{Value: "left", Label: "Left"}, {Value: "middle", Label: "Middle"}, {Value: "right", Label: "Right"}}},
			{ID: "duration", Label: "Duration (seconds)", Type: flow.FieldNumber, Default: dragDefaultDuration, Help: "Zero to thirty seconds. The button is released on failure or cancellation when the backend remains reachable."},
			{ID: "relative_to_window", Label: "Coordinates are inside a window", Type: flow.FieldBoolean, Default: false},
		}
		fields := windowFields()
		for i := range fields {
			fields[i].ShowIf = &nodeapi.ShowIf{Field: "relative_to_window", Equals: true}
		}
		m.ConfigSpec = append(m.ConfigSpec, fields...)
	} else {
		m.Label = "Window placement"
		m.Description = "Restores, moves, or resizes a window and reports observed bounds"
		m.Icon = "las la-window-restore"
		m.Outputs[0].Type = flow.PortObject
		m.Outputs[0].TypeFrom = ""
		m.ConfigSpec = append(windowFields(), []nodeapi.FieldSpec{
			{ID: "action", Label: "Action", Type: flow.FieldSelect, Default: "bounds", Options: []nodeapi.Option{{Value: "bounds", Label: "Set position and size"}, {Value: "move", Label: "Move"}, {Value: "resize", Label: "Resize"}, {Value: "restore", Label: "Restore"}}},
			{ID: "restore", Label: "Restore before placement", Type: flow.FieldBoolean, Default: true, Help: "Window-manager constraints still apply. Tiled Wayland windows must already be floating."},
			{ID: "x", Label: "Desktop X", Type: flow.FieldNumber, Default: 0}, {ID: "y", Label: "Desktop Y", Type: flow.FieldNumber, Default: 0},
			{ID: "width", Label: "Width", Type: flow.FieldNumber, Default: 640}, {ID: "height", Label: "Height", Type: flow.FieldNumber, Default: 480},
		}...)
	}
	return m
}

// interactionConfig is the decoded drag or placement configuration. Duration
// and Restore are pointers so an absent field keeps the default the editor
// shows, which a zero duration and a false restore are not.
type interactionConfig struct {
	windowFilters
	WindowID    string   `json:"window_id"`
	WindowMatch string   `json:"window_match"`
	Active      bool     `json:"active_window"`
	StartX      int      `json:"start_x"`
	StartY      int      `json:"start_y"`
	EndX        int      `json:"end_x"`
	EndY        int      `json:"end_y"`
	Button      string   `json:"button"`
	Duration    *float64 `json:"duration"`
	Relative    bool     `json:"relative_to_window"`
	Action      string   `json:"action"`
	Restore     *bool    `json:"restore"`
	X           int      `json:"x"`
	Y           int      `json:"y"`
	Width       int      `json:"width"`
	Height      int      `json:"height"`
}

func (n *interactionNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg interactionConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	if n.kind == "drag-drop" {
		req, err := cfg.dragRequest()
		if err != nil {
			return nil, err
		}
		if cfg.Relative {
			target, err := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, cfg.Active, false)
			if err != nil {
				return nil, err
			}
			w, err := automation.SelectWindow(ctx, n.sess, target)
			if err != nil {
				return nil, err
			}
			if w.Width <= 0 || w.Height <= 0 || w.X < -1000000 || w.X > 1000000 || w.Y < -1000000 || w.Y > 1000000 {
				return nil, fmt.Errorf("selected window has no usable geometry")
			}
			req.StartX += w.X
			req.EndX += w.X
			req.StartY += w.Y
			req.EndY += w.Y
		}
		if err := n.sess.Desktop.Drag(ctx, req); err != nil {
			return nil, err
		}
		out := agentnode.PassThrough(msg, map[string]any{"drag": req})
		return nodeapi.One("out", out), nil
	}
	p, err := cfg.placement()
	if err != nil {
		return nil, err
	}
	target, err := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, cfg.Active, false)
	if err != nil {
		return nil, err
	}
	w, err := automation.SelectWindow(ctx, n.sess, target)
	if err != nil {
		return nil, err
	}
	result, err := automation.PlaceAndObserveWindow(ctx, n.sess, w, p)
	if err != nil {
		return nil, err
	}
	out := agentnode.PassThrough(msg, nil)
	out.Payload = result
	return nodeapi.One("out", out), nil
}

// dragDefaultDuration is how long a drag takes when the configuration names
// no duration.
const dragDefaultDuration = 0.5

// dragRequest validates the drag settings and builds the request in the
// coordinates the configuration gave.
func (cfg interactionConfig) dragRequest() (automation.DragRequest, error) {
	duration := dragDefaultDuration
	if cfg.Duration != nil {
		duration = *cfg.Duration
	}
	if math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 || duration > 30 {
		return automation.DragRequest{}, fmt.Errorf("drag duration must be between zero and thirty seconds")
	}
	req := automation.DragRequest{StartX: cfg.StartX, StartY: cfg.StartY, EndX: cfg.EndX, EndY: cfg.EndY, Button: automation.Button(cfg.Button), Duration: time.Duration(duration * float64(time.Second))}
	for _, coordinate := range []int{req.StartX, req.StartY, req.EndX, req.EndY} {
		if coordinate < -1000000 || coordinate > 1000000 {
			return automation.DragRequest{}, fmt.Errorf("drag coordinates must be between -1000000 and 1000000")
		}
	}
	return req, nil
}

// placement validates the placement settings and builds the request.
func (cfg interactionConfig) placement() (automation.WindowPlacement, error) {
	p := automation.WindowPlacement{Restore: cfg.Restore == nil || *cfg.Restore, X: cfg.X, Y: cfg.Y, Width: cfg.Width, Height: cfg.Height}
	switch cfg.Action {
	case "", "bounds":
		p.Move, p.Resize = true, true
	case "move":
		p.Move = true
	case "resize":
		p.Resize = true
	case "restore":
		p.Restore = true
	default:
		return automation.WindowPlacement{}, fmt.Errorf("unknown placement action %q", cfg.Action)
	}
	if err := p.Validate(); err != nil {
		return automation.WindowPlacement{}, err
	}
	return p, nil
}
