// Package nvr is what routes, nodes, and widgets know about a network video
// recorder: its cameras, their images and streams, and the events it
// detects. Frigate is the first kind that satisfies it.
package nvr

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// MaxEvents bounds one event listing.
const MaxEvents = 100

// NVR is a running connection to a network video recorder.
type NVR interface {
	// Cameras returns the recorder's cameras in a stable order.
	Cameras() []Camera
	// Receiving reports whether a camera is receiving frames. An unknown
	// camera is not.
	Receiving(camera string) bool
	// Snapshot returns a camera's latest frame. The caller closes the body.
	Snapshot(ctx context.Context, camera string, opts ImageOptions) (*http.Response, error)
	// Stream subscribes to a camera's shared live stream.
	Stream(ctx context.Context, camera string, opts ImageOptions) (*Subscription, error)
	// Events lists recent events, newest first.
	Events(ctx context.Context, q EventQuery) ([]Event, error)
	// EventMedia fetches one event's snapshot, thumbnail, or clip, with the
	// event so the caller can check its camera first.
	EventMedia(ctx context.Context, id, kind, rangeHeader string) (*http.Response, error)
	// Event reads one event.
	Event(ctx context.Context, id string) (Event, error)
	// WatchEvents calls fn for every event message until the returned
	// function is called.
	WatchEvents(fn func(Event)) func()
	// WatchReviews calls fn for every review item message.
	WatchReviews(fn func(Review)) func()
}

// Event media kinds.
const (
	MediaSnapshot  = "snapshot"
	MediaThumbnail = "thumbnail"
	MediaClip      = "clip"
)

// Event and review stages.
const (
	StageStart  = "start"
	StageUpdate = "update"
	StageEnd    = "end"
)

// Camera is one camera the recorder knows.
type Camera struct {
	Name      string          `json:"name" description:"Camera name in the recorder's config."`
	Zones     []string        `json:"zones" description:"Zone names defined on the camera."`
	Objects   []string        `json:"objects" description:"Object labels the camera tracks."`
	Receiving bool            `json:"receiving" description:"Whether the camera received frames at the last check."`
	Switches  map[string]bool `json:"switches,omitempty" description:"Camera features and whether each is on: detect, record, snapshots, motion."`
}

// ImageOptions shapes a snapshot or stream.
type ImageOptions struct {
	Height int
	FPS    int
	BBox   bool
}

// EventQuery filters an event listing.
type EventQuery struct {
	Cameras []string
	Labels  []string
	Zones   []string
	Limit   int
}

// Event is one tracked object, as relayed and as a trigger's payload.
type Event struct {
	ID           string     `json:"id"`
	Camera       string     `json:"camera"`
	Label        string     `json:"label"`
	SubLabel     *string    `json:"sub_label"`
	Zones        []string   `json:"zones"`
	EnteredZones []string   `json:"entered_zones"`
	Score        float64    `json:"score"`
	Stage        string     `json:"stage,omitempty"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      *time.Time `json:"end_time"`
	HasClip      bool       `json:"has_clip"`
	HasSnapshot  bool       `json:"has_snapshot"`
	Snapshot     string     `json:"snapshot"`
	Thumbnail    string     `json:"thumbnail"`
	Clip         string     `json:"clip"`
	// PreviousZones are the zones the object was in before this message,
	// which tells a trigger when it first entered one.
	PreviousZones []string `json:"-"`
}

// Review is one review item: activity grouped into an alert or a detection.
type Review struct {
	ID        string     `json:"id"`
	Camera    string     `json:"camera"`
	Severity  string     `json:"severity"`
	Objects   []string   `json:"objects"`
	Zones     []string   `json:"zones"`
	Stage     string     `json:"stage"`
	StartTime time.Time  `json:"start_time"`
	EndTime   *time.Time `json:"end_time"`
}

// Subscription is one viewer of a shared stream. Frames carries whole JPEG
// images; it closes when the upstream ends or Close is called.
type Subscription struct {
	Frames <-chan []byte
	Close  func()
}

// NotReceivingError is a request for a camera that is receiving no frames.
type NotReceivingError struct{ Camera string }

func (e *NotReceivingError) Error() string {
	return fmt.Sprintf("camera %q is not receiving frames", e.Camera)
}

// UnknownCameraError is a request for a camera the recorder does not have.
type UnknownCameraError struct{ Camera string }

func (e *UnknownCameraError) Error() string {
	return fmt.Sprintf("the recorder has no camera %q", e.Camera)
}

// MediaPath is the relay path of one event's media on a connection.
func MediaPath(connectionID, eventID, kind string) string {
	return "/api/connections/" + connectionID + "/nvr/events/" + eventID + "/" + kind
}

// EventTopic is the broker topic carrying one camera's events to dashboard
// viewers. Camera AllCameras carries every camera's.
func EventTopic(connectionID, camera string) string {
	return "nvr:" + connectionID + ":events:" + camera
}

// AllCameras is the camera name of the topic carrying every camera's events.
const AllCameras = "*"

// RelayEvents publishes each event's start and end to its camera's topic and
// to the all-cameras topic, so an event list refreshes as events arrive.
// Updates are left out: Frigate sends several a second for each object. It
// returns the function that stops relaying.
func RelayEvents(connectionID string, r NVR, publish func(topic, kind string, data any)) func() {
	return r.WatchEvents(func(e Event) {
		if e.Stage == StageUpdate {
			return
		}
		data := map[string]any{"id": e.ID, "camera": e.Camera, "label": e.Label, "stage": e.Stage}
		publish(EventTopic(connectionID, e.Camera), "nvr_event", data)
		publish(EventTopic(connectionID, AllCameras), "nvr_event", data)
	})
}
