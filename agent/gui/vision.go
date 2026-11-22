package gui

import (
	"context"
	"fmt"
	"image"

	"github.com/grmrgecko/karakuri/agent/agentnode"
	"github.com/grmrgecko/karakuri/automation"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/nodeapi"
)

type imageMatchNode struct {
	sess    *automation.Session
	initErr error
}

func (imageMatchNode) Meta() nodeapi.NodeMeta {
	m := nodeapi.NodeMeta{Type: "image-match", Label: "Image match", Folder: folder, Icon: "las la-search", Retryable: true, Description: "Finds same-scale reference images using a minimum RGB match percentage", Inputs: []nodeapi.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}}, Outputs: []nodeapi.PortSpec{{ID: "out", Label: "Matches", Type: flow.PortObject}, flow.ErrorPort()}}
	m.ConfigSpec = []nodeapi.FieldSpec{
		{ID: "image", Label: "Image", Type: flow.FieldJSON, Port: flow.PortBinary, Required: true, ShowIf: &nodeapi.ShowIf{Field: "capture", Equals: "input"}},
		{ID: "reference_source", Label: "Reference image", Type: flow.FieldSelect, Default: "file", Options: []nodeapi.Option{{Value: "file", Label: "File on agent host"}, {Value: "input", Label: "Binary reference input"}}},
		{ID: "reference", Label: "Reference", Type: flow.FieldJSON, Port: flow.PortBinary, Required: true, ShowIf: &nodeapi.ShowIf{Field: "reference_source", Equals: "input"}},
		{ID: "reference_path", Label: "Reference path", Type: flow.FieldString, Required: true, ShowIf: &nodeapi.ShowIf{Field: "reference_source", Equals: "file"}, Help: "PNG/JPEG on this agent host at the same scale as the capture. Supports {{ }} expressions."},
		{ID: "minimum_match", Label: "Minimum match (%)", Type: flow.FieldNumber, Default: imageMatchDefaultMinimum, Help: "Greater than zero through 100. Measures mean RGB agreement, not recognition probability. 100 requires identical RGB pixels."},
		{ID: "max_results", Label: "Maximum results", Type: flow.FieldNumber, Default: 10, Help: "1 through 100. Results merge overlapping detections; an early result cap reports truncation."},
	}
	// Capture fields retain the screenshot node's existing targeting contract.
	for _, field := range (screenshotNode{}).Meta().ConfigSpec {
		if field.ID == "format" || field.ID == "quality" {
			continue
		}
		if field.ID == "capture" {
			field.Label = "Search image"
			field.Options = append(field.Options, nodeapi.Option{Value: "input", Label: "Binary image input"})
		}
		m.ConfigSpec = append(m.ConfigSpec, field)
	}
	return m
}

// imageMatchDefaultMinimum is the match percentage a configuration without one
// searches at.
const imageMatchDefaultMinimum = 98

// imageMatchConfig is the decoded match configuration. MinimumMatch is a
// pointer so an absent field keeps its default while an explicit zero is
// still rejected.
type imageMatchConfig struct {
	screenshotConfig
	Image           []byte   `json:"image"`
	ReferenceSource string   `json:"reference_source"`
	Reference       []byte   `json:"reference"`
	ReferencePath   string   `json:"reference_path"`
	MinimumMatch    *float64 `json:"minimum_match"`
	MaxResults      int      `json:"max_results"`
}

func (n *imageMatchNode) Execute(ctx context.Context, ec nodeapi.ExecContext, inputs map[string]*nodeapi.Msg) (map[string][]*nodeapi.Msg, error) {
	var cfg imageMatchConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := agentnode.PrimaryInput(inputs)
	var reference image.Image
	var err error
	switch cfg.ReferenceSource {
	case "", "file":
		path, e := agentnode.Interpolate(ec, cfg.ReferencePath, msg)
		if e != nil {
			return nil, e
		}
		reference, err = automation.ReadImage(path)
	case "input":
		reference, err = automation.DecodeImage(cfg.Reference)
	default:
		return nil, fmt.Errorf("unknown reference source %q", cfg.ReferenceSource)
	}
	if err != nil {
		return nil, fmt.Errorf("reading reference image: %w", err)
	}
	var img image.Image
	var window *automation.SnapshotWindowInfo
	switch cfg.Capture {
	case "input":
		img, err = automation.DecodeImage(cfg.Image)
	case "", captureScreenMode, captureRegionMode, captureWindowMode, captureSelectedMode:
		if n.initErr != nil {
			return nil, n.initErr
		}
		req := automation.CaptureRequest{Display: cfg.Display, Region: cfg.Capture == captureRegionMode, DesktopCoordinates: cfg.DesktopCoordinates, X: cfg.X, Y: cfg.Y, Width: cfg.Width, Height: cfg.Height}
		if cfg.Capture == captureWindowMode || cfg.Capture == captureSelectedMode {
			target, e := cfg.windowFilters.target(ec, msg, cfg.WindowID, cfg.WindowMatch, false, false)
			if e != nil {
				return nil, e
			}
			if cfg.Capture == captureWindowMode {
				target = automation.WindowTarget{Active: true}
			}
			w, e := automation.SelectWindow(ctx, n.sess, target)
			if e != nil {
				return nil, e
			}
			value := automation.NewWindowSnapshot(n.sess, w).Window
			window = &value
			img, err = n.sess.Desktop.CaptureWindow(ctx, w)
		} else {
			img, err = n.sess.Screen.Capture(ctx, req)
		}
	default:
		return nil, fmt.Errorf("unknown search image source %q", cfg.Capture)
	}
	if err != nil {
		return nil, err
	}
	minimum := float64(imageMatchDefaultMinimum)
	if cfg.MinimumMatch != nil {
		minimum = *cfg.MinimumMatch
	}
	result, err := automation.MatchImage(ctx, img, reference, automation.ImageMatchOptions{MinimumMatch: minimum, MaxResults: cfg.MaxResults})
	if err != nil {
		return nil, err
	}
	geometry := automation.MapImageMatches(img, &result)
	payload := struct {
		automation.ImageMatchResult
		Capture automation.CaptureGeometry     `json:"capture"`
		Window  *automation.SnapshotWindowInfo `json:"window,omitempty"`
	}{result, geometry, window}
	out := agentnode.PassThrough(msg, nil)
	out.Payload = payload
	return nodeapi.One("out", out), nil
}
