package nvrnodes

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/engine"
	"github.com/grmrgecko/karakuri/pkg/flow"
	"github.com/grmrgecko/karakuri/pkg/integrations/frigate"
	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
	"go.uber.org/zap"
)

// connectRetryDelay is how long an armed trigger waits before looking for
// its connection again when it is not running yet.
const connectRetryDelay = 5 * time.Second

// resolveNVR resolves a configured connection ID to a recorder.
func resolveNVR(ec engine.ExecContext, id string) (nvr.NVR, error) {
	raw, err := ec.Connection(id)
	if err != nil {
		return nil, err
	}
	r, ok := raw.(nvr.NVR)
	if !ok {
		return nil, fmt.Errorf("connection %s is not an NVR connection", id)
	}
	return r, nil
}

// waitNVR resolves the connection, retrying until it runs or ctx ends.
func waitNVR(ctx context.Context, ec engine.ExecContext, id string) (nvr.NVR, bool) {
	for {
		r, err := resolveNVR(ec, id)
		if err == nil {
			return r, true
		}
		ec.Logger().Debug("waiting for NVR connection", zap.Error(err))
		select {
		case <-ec.Clock().After(connectRetryDelay):
		case <-ctx.Done():
			return nil, false
		}
	}
}

// connectionField is the Frigate connection picker.
func connectionField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "connection", Label: "NVR", Type: flow.FieldConnection, Required: true,
		ConnectionKind: frigate.Kind, Help: "Frigate connection.",
	}
}

// stagesField picks which stages fire.
func stagesField() engine.FieldSpec {
	return engine.FieldSpec{
		ID: "stages", Label: "Stages", Type: flow.FieldString, Control: "tokens", Default: "start",
		Options: []engine.Option{
			{Value: nvr.StageStart, Label: "Start"}, {Value: nvr.StageUpdate, Label: "Update"},
			{Value: nvr.StageEnd, Label: "End"},
		},
		Help: "Start fires once when Frigate begins tracking, end once when it stops. Update fires on every change, often several times a second.",
	}
}

// tokens reads a comma-separated tokens field.
func tokens(raw string) []string {
	var out []string
	for _, t := range strings.Split(raw, ",") {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// set builds a lookup from a tokens field; nil means no filter.
func set(raw string) map[string]bool {
	list := tokens(raw)
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]bool, len(list))
	for _, t := range list {
		out[t] = true
	}
	return out
}

// anyIn reports whether any item is in the set.
func anyIn(items []string, s map[string]bool) bool {
	for _, i := range items {
		if s[i] {
			return true
		}
	}
	return false
}

// EventTrigger starts a run on a recorder's object events.
type EventTrigger struct{}

func (EventTrigger) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "nvr-event",
		Label:             "NVR Event",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderNVR,
		Description:       "Starts a run when the NVR detects an object, filtered by camera, label, zone, and score",
		Icon:              "las la-video",
		TestWithoutParams: true,
		Outputs:           []engine.PortSpec{{ID: "out", Label: "Event", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{ID: "cameras", Label: "Cameras", Type: flow.FieldString, Control: "tokens", Placeholder: "doorbell",
				Help: "Only these cameras. Empty fires for every camera."},
			{ID: "labels", Label: "Labels", Type: flow.FieldString, Control: "tokens", Placeholder: "person",
				Help: "Only these objects, such as person, car, or dog. Empty fires for every label."},
			{ID: "sub_label", Label: "Sub label contains", Type: flow.FieldString, Placeholder: "Alex",
				Help: "Only events whose sub label, such as a recognised face or plate, contains this text."},
			{ID: "zones", Label: "Zones", Type: flow.FieldString, Control: "tokens", Placeholder: "driveway",
				Help: "Only objects in any of these zones. Start and update fire when the object enters one; end fires when it had been in one."},
			{ID: "min_score", Label: "Minimum score", Type: flow.FieldNumber, Default: 0,
				Help: "Only events Frigate scores at least this, from 0 to 1."},
			stagesField(),
			{ID: "test_params", Label: "Test parameters", Type: flow.FieldJSON, Help: "Payload a test run starts with."},
		},
	}
}

// eventConfig is the decoded nvr-event configuration.
type eventConfig struct {
	Connection string  `json:"connection"`
	Cameras    string  `json:"cameras"`
	Labels     string  `json:"labels"`
	SubLabel   string  `json:"sub_label"`
	Zones      string  `json:"zones"`
	MinScore   float64 `json:"min_score"`
	Stages     string  `json:"stages"`
}

// matches reports whether an event passes the trigger's filters.
func (c eventConfig) matches(e nvr.Event) bool {
	stages := set(c.Stages)
	if stages == nil {
		stages = map[string]bool{nvr.StageStart: true}
	}
	if !stages[e.Stage] {
		return false
	}
	if cams := set(c.Cameras); cams != nil && !cams[e.Camera] {
		return false
	}
	if labels := set(c.Labels); labels != nil && !labels[e.Label] {
		return false
	}
	if want := strings.TrimSpace(c.SubLabel); want != "" {
		if e.SubLabel == nil || !strings.Contains(strings.ToLower(*e.SubLabel), strings.ToLower(want)) {
			return false
		}
	}
	if e.Score < c.MinScore {
		return false
	}
	if zones := set(c.Zones); zones != nil {
		if e.Stage == nvr.StageEnd {
			return anyIn(e.Zones, zones)
		}
		return anyIn(e.EnteredZones, zones)
	}
	return true
}

// Execute passes the seeded message through, or a sample on a test run.
func (EventTrigger) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	var cfg eventConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	sample := nvr.Event{ID: "1793910167.825535-sample", Camera: "doorbell", Label: "person", Zones: []string{},
		EnteredZones: []string{}, Score: 0.86, Stage: nvr.StageStart, StartTime: time.Now().UTC()}
	if cams := tokens(cfg.Cameras); len(cams) > 0 {
		sample.Camera = cams[0]
	}
	if labels := tokens(cfg.Labels); len(labels) > 0 {
		sample.Label = labels[0]
	}
	return engine.One("out", eventMsg(sample, cfg.Connection)), nil
}

// Start watches the recorder's events until the context ends.
func (EventTrigger) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg eventConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	r, ok := waitNVR(ctx, ec, cfg.Connection)
	if !ok {
		return nil
	}
	unwatch := r.WatchEvents(func(e nvr.Event) {
		if cfg.matches(e) {
			emit(eventMsg(e, cfg.Connection))
		}
	})
	defer unwatch()
	<-ctx.Done()
	return nil
}

// toMap renders a value as the JSON object a payload holds.
func toMap(v any) map[string]any {
	raw, _ := json.Marshal(v)
	out := map[string]any{}
	_ = json.Unmarshal(raw, &out)
	return out
}

// eventMsg renders an event as a message.
func eventMsg(e nvr.Event, connID string) *engine.Msg {
	payload := toMap(e)
	payload["connection_id"] = connID
	return &engine.Msg{
		Payload: payload,
		Topic:   e.Camera + "/" + e.Label,
		Meta:    map[string]any{"nvr_camera": e.Camera, "nvr_event": e.ID, "nvr_stage": e.Stage},
	}
}

// ReviewTrigger starts a run on a recorder's review items: alerts and
// detections, Frigate's grouping of activity worth looking at.
type ReviewTrigger struct{}

func (ReviewTrigger) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:              "nvr-review",
		Label:             "NVR Review",
		Category:          flow.CategoryTrigger,
		Folder:            flow.FolderNVR,
		Description:       "Starts a run when the NVR raises an alert or detection for review",
		Icon:              "las la-exclamation-triangle",
		TestWithoutParams: true,
		Outputs:           []engine.PortSpec{{ID: "out", Label: "Review", Type: flow.PortObject}},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{ID: "cameras", Label: "Cameras", Type: flow.FieldString, Control: "tokens", Placeholder: "doorbell",
				Help: "Only these cameras. Empty fires for every camera."},
			{ID: "severity", Label: "Severity", Type: flow.FieldSelect, Default: "alert",
				Options: []engine.Option{{Value: "alert", Label: "Alert"}, {Value: "detection", Label: "Detection"}, {Value: "any", Label: "Any"}},
				Help:    "Alerts are what Frigate's config marks as needing attention; detections are the rest."},
			stagesField(),
			{ID: "test_params", Label: "Test parameters", Type: flow.FieldJSON, Help: "Payload a test run starts with."},
		},
	}
}

// reviewConfig is the decoded nvr-review configuration.
type reviewConfig struct {
	Connection string `json:"connection"`
	Cameras    string `json:"cameras"`
	Severity   string `json:"severity"`
	Stages     string `json:"stages"`
}

// matches reports whether a review item passes the trigger's filters.
func (c reviewConfig) matches(r nvr.Review) bool {
	stages := set(c.Stages)
	if stages == nil {
		stages = map[string]bool{nvr.StageStart: true}
	}
	if !stages[r.Stage] {
		return false
	}
	if cams := set(c.Cameras); cams != nil && !cams[r.Camera] {
		return false
	}
	severity := c.Severity
	if severity == "" {
		severity = "alert"
	}
	return severity == "any" || severity == r.Severity
}

// Execute passes the seeded message through, or a sample on a test run.
func (ReviewTrigger) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	if msg := inputs[engine.TriggerInput]; msg != nil && msg.Payload != nil {
		return engine.One("out", msg), nil
	}
	var cfg reviewConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	sample := nvr.Review{ID: "1793911471.77406-sample", Camera: "doorbell", Severity: "alert",
		Objects: []string{"person"}, Zones: []string{}, Stage: nvr.StageStart, StartTime: time.Now().UTC()}
	if cams := tokens(cfg.Cameras); len(cams) > 0 {
		sample.Camera = cams[0]
	}
	return engine.One("out", reviewMsg(sample, cfg.Connection)), nil
}

// Start watches the recorder's review items until the context ends.
func (ReviewTrigger) Start(ctx context.Context, ec engine.ExecContext, emit func(*engine.Msg)) error {
	var cfg reviewConfig
	if err := ec.Config(&cfg); err != nil {
		return err
	}
	r, ok := waitNVR(ctx, ec, cfg.Connection)
	if !ok {
		return nil
	}
	unwatch := r.WatchReviews(func(item nvr.Review) {
		if cfg.matches(item) {
			emit(reviewMsg(item, cfg.Connection))
		}
	})
	defer unwatch()
	<-ctx.Done()
	return nil
}

// reviewMsg renders a review item as a message.
func reviewMsg(r nvr.Review, connID string) *engine.Msg {
	payload := toMap(r)
	payload["connection_id"] = connID
	return &engine.Msg{
		Payload: payload,
		Topic:   r.Camera + "/" + r.Severity,
		Meta:    map[string]any{"nvr_camera": r.Camera, "nvr_review": r.ID, "nvr_stage": r.Stage},
	}
}

// ImageNode reads a camera's latest frame or an event's snapshot as an
// image, for a notification attachment or an AI agent's vision input.
type ImageNode struct{}

func (ImageNode) Meta() engine.NodeMeta {
	return engine.NodeMeta{
		Type:        "nvr-image",
		Label:       "NVR Image",
		Folder:      flow.FolderNVR,
		Description: "Reads a camera's latest frame or an event's snapshot from the NVR",
		Icon:        "las la-camera",
		Inputs:      []engine.PortSpec{{ID: "in", Label: "In", Type: flow.PortAny}},
		Outputs: []engine.PortSpec{
			{ID: "out", Label: "Bytes", Type: flow.PortBinary},
			{ID: "image", Label: "Image", Type: flow.PortImage},
		},
		ConfigSpec: []engine.FieldSpec{
			connectionField(),
			{ID: "source", Label: "Source", Type: flow.FieldSelect, Default: "camera",
				Options: []engine.Option{{Value: "camera", Label: "Camera's latest frame"}, {Value: "event", Label: "Event snapshot"}}},
			{ID: "camera", Label: "Camera", Type: flow.FieldString, Placeholder: "doorbell",
				Help:   "Camera name, or an expression such as {{ payload.camera }}.",
				ShowIf: &flow.ShowIf{Field: "source", Equals: "camera"}},
			{ID: "event", Label: "Event", Type: flow.FieldString, Placeholder: "{{ payload.id }}",
				Help:   "Event ID, usually {{ payload.id }} from an NVR Event trigger.",
				ShowIf: &flow.ShowIf{Field: "source", Equals: "event"}},
			{ID: "height", Label: "Height", Type: flow.FieldNumber, Default: 0,
				Help:   "Scale the frame to this many pixels high. 0 keeps the camera's size.",
				ShowIf: &flow.ShowIf{Field: "source", Equals: "camera"}},
			{ID: "bbox", Label: "Bounding boxes", Type: flow.FieldBoolean, Default: false,
				Help: "Draw Frigate's boxes around tracked objects.", ShowIf: &flow.ShowIf{Field: "source", Equals: "camera"}},
		},
		Retryable: true,
	}
}

// imageConfig is the decoded nvr-image configuration.
type imageConfig struct {
	Connection string `json:"connection"`
	Source     string `json:"source"`
	Camera     string `json:"camera"`
	Event      string `json:"event"`
	Height     int    `json:"height"`
	BBox       bool   `json:"bbox"`
}

// Execute fetches the image and emits it as bytes and as an image value.
func (ImageNode) Execute(ctx context.Context, ec engine.ExecContext, inputs map[string]*engine.Msg) (map[string][]*engine.Msg, error) {
	var cfg imageConfig
	if err := ec.Config(&cfg); err != nil {
		return nil, err
	}
	msg := inputs["in"]
	if msg == nil {
		msg = &engine.Msg{}
	}
	r, err := resolveNVR(ec, cfg.Connection)
	if err != nil {
		return nil, err
	}
	var data []byte
	var contentType, label string
	switch cfg.Source {
	case "event":
		id, err := ec.Interpolate(cfg.Event, msg)
		if err != nil {
			return nil, err
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("no event ID; set Event, usually {{ payload.id }}")
		}
		resp, err := r.EventMedia(ctx, id, nvr.MediaSnapshot, "")
		if err != nil {
			return nil, err
		}
		data, contentType, err = readImage(resp.Body, resp.Header.Get("Content-Type"))
		if err != nil {
			return nil, err
		}
		label = id
	default:
		name, err := ec.Interpolate(cfg.Camera, msg)
		if err != nil {
			return nil, err
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("no camera; set Camera")
		}
		resp, err := r.Snapshot(ctx, name, nvr.ImageOptions{Height: cfg.Height, BBox: cfg.BBox})
		if err != nil {
			return nil, err
		}
		data, contentType, err = readImage(resp.Body, resp.Header.Get("Content-Type"))
		if err != nil {
			return nil, err
		}
		label = name
	}
	meta := map[string]any{"content_type": contentType, "mime": contentType, "nvr_source": label}
	out := &engine.Msg{Payload: data, Topic: label, Meta: meta, Group: msg.Group}
	image := &engine.Msg{Payload: flow.ImageValue{MIME: contentType, Data: base64.StdEncoding.EncodeToString(data)},
		Topic: label, Meta: meta, Group: msg.Group}
	return map[string][]*engine.Msg{"out": {out}, "image": {image}}, nil
}

// readImage reads a body that must be an image.
func readImage(body io.ReadCloser, contentType string) ([]byte, string, error) {
	defer body.Close()
	data, err := io.ReadAll(body)
	if err != nil {
		return nil, "", fmt.Errorf("reading the image: %w", err)
	}
	if !strings.HasPrefix(contentType, "image/") {
		contentType = "image/jpeg"
	}
	return data, contentType, nil
}
