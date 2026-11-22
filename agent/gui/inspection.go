package gui

import (
	"context"
	"fmt"
	"image"
	"math"
	"time"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

type inspectionNode struct {
	kind    string
	sess    *automation.Session
	initErr error
}

type inspectionConfig struct {
	windowFilters
	Source          string  `json:"source"`
	MaxResults      int     `json:"max_results"`
	ParentID        string  `json:"parent_id"`
	TextMatch       string  `json:"text_match"`
	EnabledState    string  `json:"enabled_state"`
	CheckOnce       bool    `json:"check_once"`
	OnTimeout       string  `json:"on_timeout"`
	ScopeWindow     bool    `json:"scope_window"`
	IncludeControls *bool   `json:"include_controls"`
	WindowID        string  `json:"window_id"`
	WindowMatch     string  `json:"window_match"`
	Active          bool    `json:"active_window"`
	ElementID       string  `json:"element_id"`
	Text            string  `json:"text"`
	Role            string  `json:"role"`
	All             bool    `json:"all"`
	Enabled         bool    `json:"enabled"`
	Gone            bool    `json:"gone"`
	Timeout         float64 `json:"timeout"`
	Stable          float64 `json:"stable"`
	Region          bool    `json:"region"`
	X               int     `json:"x"`
	Y               int     `json:"y"`
	Width           int     `json:"width"`
	Height          int     `json:"height"`
	Depth           int     `json:"depth"`
	Target          string  `json:"target"`
	Evidence        *bool   `json:"evidence"`
	EvidenceDir     string  `json:"evidence_dir"`
}

func windowFields() []nodeapi.FieldSpec {
	return append([]nodeapi.FieldSpec{
		{ID: "window_id", Label: "Window id", Type: flow.FieldString, Help: "Exact window id. Takes precedence over the active window and match pattern. Supports {{ }} expressions."},
		{ID: "window_match", Label: "Window match pattern", Type: flow.FieldString, Help: "Matches title or class using the window match mode. Actions require one match. Supports {{ }} expressions."},
		{ID: "active_window", Label: "Use active window", Type: flow.FieldBoolean, Default: false},
	}, windowFilterFields()...)
}

func (n inspectionNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{Type: n.kind, Folder: folder, Icon: "las la-desktop", Inputs: []nodeapi.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}}, Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Out", Type: flow.PortObject}, flow.ErrorPort()}}
	switch n.kind {
	case "ui-snapshot":
		m.Label = "UI snapshot"
		m.Description = "Reads current window metadata and accessible elements"
		m.ConfigSpec = append(windowFields(), []nodeapi.FieldSpec{
			{ID: "source", Label: "Source", Type: flow.FieldSelect, Default: "accessibility", Options: []nodeapi.Option{{Value: "accessibility", Label: "Accessibility"}, {Value: "native", Label: "Native controls"}, {Value: "both", Label: "Both"}}},
			{ID: "all", Label: "Include hidden elements", Type: flow.FieldBoolean}, {ID: "enabled", Label: "Only enabled elements", Type: flow.FieldBoolean},
			{ID: "role", Label: "Role", Type: flow.FieldString, Help: "Exact provider role, or native Windows class."}, {ID: "text", Label: "Text contains", Type: flow.FieldString},
			{ID: "max_results", Label: "Maximum results per source", Type: flow.FieldNumber, Default: 0, Help: "Zero returns all provider results. Output reports truncated sources."},
		}...)
	case "click-control":
		m.Label = "Click control"
		m.Description = "Clicks a unique control label or invokes an accessible element"
		m.ConfigSpec = append(windowFields(), nodeapi.FieldSpec{ID: "element_id", Label: "Element id", Type: flow.FieldString, Help: "ID from UI snapshot. Takes precedence over the label."}, nodeapi.FieldSpec{ID: "text", Label: "Control label", Type: flow.FieldString, Help: "An exact label wins over a unique substring. Supports {{ }} expressions."})
		m.ConfigSpec = append(m.ConfigSpec,
			nodeapi.FieldSpec{ID: "role", Label: "Role", Type: flow.FieldString, Help: "Exact provider role, or native Windows class."},
			nodeapi.FieldSpec{ID: "parent_id", Label: "Immediate parent id", Type: flow.FieldString},
			nodeapi.FieldSpec{ID: "text_match", Label: "Label match", Type: flow.FieldSelect, Default: "auto", Options: []nodeapi.Option{{Value: "auto", Label: "Exact first, then contains"}, {Value: "exact", Label: "Exact"}, {Value: "contains", Label: "Contains"}}})
	case "wait-window":
		m.Label = "Wait for window"
		m.Description = "Waits for a window to appear or disappear"
		m.ConfigSpec = append(windowFields(), nodeapi.FieldSpec{ID: "gone", Label: "Wait until absent", Type: flow.FieldBoolean})
	case "wait-ui-state":
		m.Label = "Wait for UI state"
		m.Description = "Waits for accessible state or stable visible pixels"
		m.ConfigSpec = append(windowFields(), []nodeapi.FieldSpec{
			{ID: "element_id", Label: "Element id", Type: flow.FieldString}, {ID: "text", Label: "Text contains", Type: flow.FieldString}, {ID: "role", Label: "Role", Type: flow.FieldString, Help: "Exact provider role, such as button, AXButton, or ControlType.Button."},
			{ID: "enabled", Label: "Require enabled", Type: flow.FieldBoolean, ShowIf: &nodeapi.ShowIf{Field: "enabled_state", Equals: ""}, Help: "Used when Enabled state is unset."},
			{ID: "enabled_state", Label: "Enabled state", Type: flow.FieldSelect, Default: "", Options: []nodeapi.Option{{Value: "", Label: "Use require enabled"}, {Value: "either", Label: "Either"}, {Value: "enabled", Label: "Enabled"}, {Value: "disabled", Label: "Disabled"}}}, {ID: "gone", Label: "Wait until absent", Type: flow.FieldBoolean},
			{ID: "stable", Label: "Stable pixels (seconds)", Type: flow.FieldNumber, Default: 0, Help: "Use instead of element conditions. Zero disables pixel stability. Occlusion and blinking affect the result."},
			{ID: "region", Label: "Sample a window region", Type: flow.FieldBoolean},
			{ID: "x", Label: "Left", Type: flow.FieldNumber, ShowIf: &nodeapi.ShowIf{Field: "region", Equals: true}},
			{ID: "y", Label: "Top", Type: flow.FieldNumber, ShowIf: &nodeapi.ShowIf{Field: "region", Equals: true}},
			{ID: "width", Label: "Width", Type: flow.FieldNumber, ShowIf: &nodeapi.ShowIf{Field: "region", Equals: true}},
			{ID: "height", Label: "Height", Type: flow.FieldNumber, ShowIf: &nodeapi.ShowIf{Field: "region", Equals: true}},
			{ID: "evidence", Label: "Capture failure evidence", Type: flow.FieldBoolean, Default: true, Help: "Saves a screenshot and snapshot on this agent host. Screenshots can contain sensitive content."},
			{ID: "evidence_dir", Label: "Evidence directory", Type: flow.FieldString, Help: "Optional parent directory on the agent host. Captures are retained until removed."},
		}...)
	case "menu-list":
		m.Label = "Menu list"
		m.Description = "Reads the selected window's native or accessible menus"
		m.ConfigSpec = windowFields()
	case "menu-invoke":
		m.Label = "Invoke menu"
		m.Description = "Invokes one enabled menu path or command id"
		m.ConfigSpec = append(windowFields(), nodeapi.FieldSpec{ID: "target", Label: "Menu path or id", Type: flow.FieldString, Required: true, Placeholder: "File > Save", Help: "Supports {{ }} expressions."})
	case "display-list":
		m.Label = "Display list"
		m.Description = "Reads display geometry and current modes"
	case "display-mode":
		m.Label = "Set display mode"
		m.Description = "Temporarily changes the primary display's mode"
		m.ConfigSpec = []nodeapi.FieldSpec{{ID: "width", Label: "Width", Type: flow.FieldNumber, Required: true}, {ID: "height", Label: "Height", Type: flow.FieldNumber, Required: true}, {ID: "depth", Label: "Color depth", Type: flow.FieldNumber, Required: true, Help: "Use the depth reported by Display list. Unix backends do not change color depth."}}
	case "dialog-list":
		m.Label = "Dialog list"
		m.Description = "Finds application dialogs and reports incomplete inspections"
		m.ConfigSpec = append([]nodeapi.FieldSpec{{ID: "scope_window", Label: "Filter windows", Type: flow.FieldBoolean}, {ID: "include_controls", Label: "Include controls", Type: flow.FieldBoolean, Default: true}}, windowFields()...)
	}
	if n.kind == "wait-window" || n.kind == "wait-ui-state" {
		m.ConfigSpec = append(m.ConfigSpec,
			nodeapi.FieldSpec{ID: "timeout", Label: "Timeout (seconds)", Type: flow.FieldNumber, Default: 30, Help: "Accepts fractional seconds, up to one day."},
			nodeapi.FieldSpec{ID: "check_once", Label: "Check once", Type: flow.FieldBoolean, Help: "Returns matched: false for an unmet condition without waiting. Pixel stability requires polling."},
			nodeapi.FieldSpec{ID: "on_timeout", Label: "On timeout", Type: flow.FieldSelect, Default: "error", Options: []nodeapi.Option{{Value: "error", Label: "Fail"}, {Value: "unmatched", Label: "Return matched: false"}}, Help: "Provider failures and cancellation still fail."})
	}
	if n.kind == "click-control" || n.kind == "menu-invoke" {
		m.Outputs[0].Type = flow.PortAny
		m.Outputs[0].TypeFrom = "in"
	}
	return m
}

func (n *inspectionNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	if n.initErr != nil {
		return nil, n.initErr
	}
	var cfg inspectionConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	for _, field := range []*string{&cfg.ElementID, &cfg.ParentID, &cfg.Text, &cfg.Role, &cfg.Target, &cfg.EvidenceDir} {
		value, err := agentnode.Interpolate(ec, *field, msg)
		if err != nil {
			return nil, err
		}
		*field = value
	}
	target, err := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, cfg.Active, false)
	if err != nil {
		return nil, err
	}
	if (n.kind == "wait-window" || n.kind == "wait-ui-state") && cfg.OnTimeout != "" && cfg.OnTimeout != "error" && cfg.OnTimeout != "unmatched" {
		return nil, fmt.Errorf("unknown timeout outcome %q", cfg.OnTimeout)
	}
	var result any
	switch n.kind {
	case "display-list":
		var displays []automation.DisplayInfo
		displays, err = n.sess.Inspection.Displays(ctx)
		result = map[string]any{"displays": displays}
	case "display-mode":
		if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Depth <= 0 {
			return nil, fmt.Errorf("display dimensions and depth must be positive")
		}
		err = n.sess.Inspection.SetDisplay(ctx, automation.DisplayMode{Width: cfg.Width, Height: cfg.Height, Depth: cfg.Depth})
		if err == nil {
			var displays []automation.DisplayInfo
			displays, err = n.sess.Inspection.Displays(ctx)
			result = map[string]any{"displays": displays}
		}
	case "dialog-list":
		result, err = n.sess.Desktop.Dialogs(ctx, automation.DialogOptions{Target: target, Scope: cfg.ScopeWindow, IncludeControls: cfg.IncludeControls})
	case "wait-window":
		timeout, e := waitTimeout(cfg.Timeout)
		if e != nil {
			return nil, e
		}
		waitCtx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var w *automation.WindowInfo
		if cfg.CheckOnce {
			w, err = automation.CheckWindow(waitCtx, n.sess, target, cfg.Gone)
		} else {
			w, err = automation.WaitWindow(waitCtx, n.sess, target, cfg.Gone)
		}
		matched := err == nil
		if ctx.Err() == nil && ((cfg.CheckOnce && err == automation.ErrConditionUnmet) || (!cfg.CheckOnce && cfg.OnTimeout == "unmatched" && err == context.DeadlineExceeded)) {
			err = nil
		}
		result = map[string]any{"matched": matched, "gone": cfg.Gone}
		if w != nil {
			result.(map[string]any)["window"] = windowValue(*w)
		}
	default:
		var condition automation.StateOptions
		var timeout time.Duration
		if n.kind == "wait-ui-state" {
			condition, timeout, err = stateCondition(cfg)
			if err != nil {
				return nil, err
			}
		}
		selectCtx := ctx
		var cancel context.CancelFunc
		if n.kind == "wait-ui-state" {
			selectCtx, cancel = context.WithTimeout(ctx, timeout)
			defer cancel()
		}
		w, e := automation.SelectWindow(selectCtx, n.sess, target)
		if e != nil {
			return nil, e
		}
		switch n.kind {
		case "ui-snapshot":
			result, err = automation.ReadWindowSnapshotWithOptions(ctx, n.sess, w, automation.SnapshotOptions{Source: cfg.Source, All: cfg.All, Enabled: cfg.Enabled, Role: cfg.Role, Text: cfg.Text, MaxResults: cfg.MaxResults})
		case "click-control":
			err = n.sess.Desktop.ClickControl(ctx, w, automation.ControlTarget{ID: cfg.ElementID, Text: cfg.Text, Role: cfg.Role, Parent: cfg.ParentID, MatchMode: cfg.TextMatch})
			if err == nil {
				return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"window": windowValue(w)})), nil
			}
		case "menu-list":
			var items []automation.MenuItem
			items, err = n.sess.Desktop.Menus(ctx, w)
			result = map[string]any{"window": windowValue(w), "items": items}
		case "menu-invoke":
			err = n.sess.Desktop.InvokeMenu(ctx, w, cfg.Target)
			if err == nil {
				return nodeapi.One("out", agentnode.PassThrough(msg, map[string]any{"window": windowValue(w), "menu": cfg.Target})), nil
			}
		case "wait-ui-state":
			if cfg.CheckOnce {
				err = automation.CheckWindowState(selectCtx, n.sess, &w, condition)
			} else {
				err = automation.PollWindowState(selectCtx, n.sess, &w, condition)
			}
			matched := err == nil
			if ctx.Err() == nil && ((cfg.CheckOnce && err == automation.ErrConditionUnmet) || (!cfg.CheckOnce && cfg.OnTimeout == "unmatched" && err == context.DeadlineExceeded)) {
				err = nil
			}
			if err != nil && ctx.Err() == nil && (cfg.Evidence == nil || *cfg.Evidence) {
				evidenceCtx, stop := context.WithTimeout(ctx, 8*time.Second)
				directory, captureErr := automation.SaveStateEvidence(evidenceCtx, n.sess, w, condition, cfg.EvidenceDir, err)
				stop()
				if captureErr != nil {
					return nil, fmt.Errorf("waiting for UI state: %w; evidence %q: %v", err, directory, captureErr)
				}
				return nil, fmt.Errorf("waiting for UI state: %w; evidence saved to %s", err, directory)
			}
			result = map[string]any{"window": windowValue(w), "matched": matched, "gone": cfg.Gone}
		default:
			return nil, fmt.Errorf("unknown GUI operation %q", n.kind)
		}
	}
	if err != nil {
		return nil, err
	}
	out := agentnode.PassThrough(msg, nil)
	out.Payload = result
	return nodeapi.One("out", out), nil
}

func waitTimeout(seconds float64) (time.Duration, error) {
	if seconds == 0 {
		seconds = 30
	}
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) || seconds < 0.000000001 || seconds > 86400 {
		return 0, fmt.Errorf("timeout must be positive and at most 86400 seconds")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}
func stateCondition(cfg inspectionConfig) (automation.StateOptions, time.Duration, error) {
	timeout, err := waitTimeout(cfg.Timeout)
	if err != nil {
		return automation.StateOptions{}, 0, err
	}
	if math.IsNaN(cfg.Stable) || math.IsInf(cfg.Stable, 0) || cfg.Stable < 0 || cfg.Stable > 86400 {
		return automation.StateOptions{}, 0, fmt.Errorf("stable interval must be between 0 and 86400 seconds")
	}
	if cfg.CheckOnce && cfg.Stable > 0 {
		return automation.StateOptions{}, 0, fmt.Errorf("a single check cannot establish pixel stability")
	}
	o := automation.StateOptions{ID: cfg.ElementID, Text: cfg.Text, Role: cfg.Role, Enabled: cfg.Enabled, EnabledState: cfg.EnabledState, Gone: cfg.Gone, Stable: time.Duration(cfg.Stable * float64(time.Second))}
	if cfg.Region {
		maxInt := int(^uint(0) >> 1)
		if cfg.X < 0 || cfg.Y < 0 || cfg.Width <= 0 || cfg.Height <= 0 || cfg.X > maxInt-cfg.Width || cfg.Y > maxInt-cfg.Height {
			return o, 0, fmt.Errorf("region needs nonnegative coordinates and positive dimensions without overflow")
		}
		o.Region = image.Rect(cfg.X, cfg.Y, cfg.X+cfg.Width, cfg.Y+cfg.Height)
	}
	return o, timeout, o.Validate(timeout)
}

// freshNode keeps existing pure sources available while adding execution reads.
type freshNode struct {
	nodeapi.NodeType
	kind, label string
}

func (n freshNode) Meta() nodeapi.NodeMeta {
	m := n.NodeType.Meta()
	m.Type = n.kind
	m.Label = n.label
	m.Pure = false
	m.Retryable = false
	m.Description = "Reads the current desktop when this step executes"
	m.Inputs = []nodeapi.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}}
	m.Outputs = append(m.Outputs, flow.ErrorPort())
	return m
}
