package frigate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/grmrgecko/karakuri/pkg/integrations/nvr"
)

// Media limits. A clip has no size cap: it is relayed in ranges for as long
// as the viewer's request lasts.
const (
	maxSnapshotBytes  = 5 << 20
	maxThumbnailBytes = 1 << 20
	snapshotTimeout   = 10 * time.Second
	maxEventsBytes    = 4 << 20
)

// CameraNameRe matches a Frigate camera name, checked before a name is put
// in a Frigate URL path.
var CameraNameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

// EventIDRe matches a Frigate event or review ID: start time and a suffix.
var EventIDRe = regexp.MustCompile(`^[0-9]{1,12}\.[0-9]{1,9}-[a-z0-9]{1,16}$`)

// trackedObject is an event as Frigate's websocket and API send it. The
// websocket sends current_zones and entered_zones; the API sends zones and
// the score under data.
type trackedObject struct {
	ID           string          `json:"id"`
	Camera       string          `json:"camera"`
	Label        string          `json:"label"`
	SubLabel     json.RawMessage `json:"sub_label"`
	TopScore     *float64        `json:"top_score"`
	Score        *float64        `json:"score"`
	CurrentZones []string        `json:"current_zones"`
	EnteredZones []string        `json:"entered_zones"`
	Zones        []string        `json:"zones"`
	StartTime    float64         `json:"start_time"`
	EndTime      *float64        `json:"end_time"`
	HasClip      bool            `json:"has_clip"`
	HasSnapshot  bool            `json:"has_snapshot"`
	Data         struct {
		Score    *float64 `json:"score"`
		TopScore *float64 `json:"top_score"`
	} `json:"data"`
}

// changeMessage is an events message: the object before and after a change.
type changeMessage struct {
	Type   string         `json:"type"`
	Before *trackedObject `json:"before"`
	After  *trackedObject `json:"after"`
}

// reviewItem is a review item as Frigate sends it.
type reviewItem struct {
	ID        string   `json:"id"`
	Camera    string   `json:"camera"`
	Severity  string   `json:"severity"`
	StartTime float64  `json:"start_time"`
	EndTime   *float64 `json:"end_time"`
	Data      struct {
		Objects []string `json:"objects"`
		Zones   []string `json:"zones"`
	} `json:"data"`
}

// reviewMessage is a reviews message.
type reviewMessage struct {
	Type   string      `json:"type"`
	Before *reviewItem `json:"before"`
	After  *reviewItem `json:"after"`
}

// stageOf maps Frigate's message type to a stage.
func stageOf(t string) string {
	switch t {
	case "new":
		return nvr.StageStart
	case "end":
		return nvr.StageEnd
	}
	return nvr.StageUpdate
}

// unixTime converts Frigate's fractional Unix seconds.
func unixTime(s float64) time.Time {
	sec, frac := math.Modf(s)
	return time.Unix(int64(sec), int64(frac*1e9)).UTC()
}

// subLabel reads a sub label, which Frigate sends as a string or as a
// [name, score] pair.
func subLabel(raw json.RawMessage) *string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return &s
	}
	var pair []any
	if json.Unmarshal(raw, &pair) == nil && len(pair) > 0 {
		if s, ok := pair[0].(string); ok {
			return &s
		}
	}
	return nil
}

// firstScore returns the first score that is set.
func firstScore(scores ...*float64) float64 {
	for _, s := range scores {
		if s != nil && *s > 0 {
			return *s
		}
	}
	return 0
}

// eventFrom builds an event from a tracked object. Zones are where the object
// is now; at the end, the zones it entered over its life, since an ended
// object is in none.
func (d *Driver) eventFrom(o trackedObject, before *trackedObject, msgType string) nvr.Event {
	e := nvr.Event{
		ID: o.ID, Camera: o.Camera, Label: o.Label, SubLabel: subLabel(o.SubLabel),
		Score:     firstScore(o.TopScore, o.Data.TopScore, o.Score, o.Data.Score),
		StartTime: unixTime(o.StartTime), HasClip: o.HasClip, HasSnapshot: o.HasSnapshot,
	}
	if msgType != "" {
		e.Stage = stageOf(msgType)
	}
	if o.EndTime != nil {
		t := unixTime(*o.EndTime)
		e.EndTime = &t
	}
	switch {
	case o.Zones != nil:
		e.Zones = o.Zones
	case e.Stage == nvr.StageEnd:
		e.Zones = o.EnteredZones
	default:
		e.Zones = o.CurrentZones
	}
	if e.Zones == nil {
		e.Zones = []string{}
	}
	if before != nil {
		e.PreviousZones = before.CurrentZones
	}
	e.EnteredZones = []string{}
	if e.Stage != nvr.StageEnd {
		prev := map[string]bool{}
		for _, z := range e.PreviousZones {
			prev[z] = true
		}
		if e.Stage == nvr.StageStart {
			prev = map[string]bool{}
		}
		for _, z := range e.Zones {
			if !prev[z] {
				e.EnteredZones = append(e.EnteredZones, z)
			}
		}
	}
	id := d.id.String()
	e.Snapshot = nvr.MediaPath(id, o.ID, nvr.MediaSnapshot)
	e.Thumbnail = nvr.MediaPath(id, o.ID, nvr.MediaThumbnail)
	e.Clip = nvr.MediaPath(id, o.ID, nvr.MediaClip)
	return e
}

// reviewFrom builds a review from Frigate's review item.
func reviewFrom(r reviewItem, msgType string) nvr.Review {
	out := nvr.Review{ID: r.ID, Camera: r.Camera, Severity: r.Severity, Objects: r.Data.Objects,
		Zones: r.Data.Zones, Stage: stageOf(msgType), StartTime: unixTime(r.StartTime)}
	if r.EndTime != nil {
		t := unixTime(*r.EndTime)
		out.EndTime = &t
	}
	if out.Objects == nil {
		out.Objects = []string{}
	}
	if out.Zones == nil {
		out.Zones = []string{}
	}
	return out
}

// Events lists recent events, newest first.
func (d *Driver) Events(ctx context.Context, q nvr.EventQuery) ([]nvr.Event, error) {
	limit := q.Limit
	if limit <= 0 || limit > nvr.MaxEvents {
		limit = nvr.MaxEvents
	}
	v := url.Values{}
	v.Set("limit", strconv.Itoa(limit))
	for key, list := range map[string][]string{"cameras": q.Cameras, "labels": q.Labels, "zones": q.Zones} {
		if len(list) > 0 {
			v.Set(key, strings.Join(list, ","))
		}
	}
	var objects []trackedObject
	if err := d.client.getJSON(ctx, "/api/events?"+v.Encode(), maxEventsBytes, &objects); err != nil {
		return nil, err
	}
	out := make([]nvr.Event, 0, len(objects))
	for _, o := range objects {
		out = append(out, d.eventFrom(o, nil, ""))
	}
	return out, nil
}

// Event reads one event.
func (d *Driver) Event(ctx context.Context, id string) (nvr.Event, error) {
	if !EventIDRe.MatchString(id) {
		return nvr.Event{}, fmt.Errorf("invalid event id %q", id)
	}
	var o trackedObject
	if err := d.client.getJSON(ctx, "/api/events/"+id, 1<<20, &o); err != nil {
		return nvr.Event{}, err
	}
	return d.eventFrom(o, nil, ""), nil
}

// cancelBody closes a response body and cancels its request's context.
type cancelBody struct {
	io.Reader
	closer io.Closer
	cancel context.CancelFunc
}

func (b cancelBody) Close() error {
	err := b.closer.Close()
	b.cancel()
	return err
}

// fetch gets a media path, bounding its time and size when they are set. A
// status other than 200 or 206 is an error carrying Frigate's status.
func (d *Driver) fetch(ctx context.Context, path string, header http.Header, timeout time.Duration, limit int64) (*http.Response, error) {
	cancel := context.CancelFunc(func() {})
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, timeout)
	}
	resp, err := d.client.get(ctx, path, header)
	if err != nil {
		cancel()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		resp.Body.Close()
		cancel()
		if resp.StatusCode == http.StatusNotFound {
			return nil, &MediaNotFoundError{Path: path}
		}
		return nil, fmt.Errorf("frigate answered %s for %s", resp.Status, path)
	}
	var r io.Reader = resp.Body
	if limit > 0 {
		r = io.LimitReader(resp.Body, limit)
	}
	resp.Body = cancelBody{Reader: r, closer: resp.Body, cancel: cancel}
	return resp, nil
}

// MediaNotFoundError is media Frigate does not have, such as the clip of an
// event that recorded none.
type MediaNotFoundError struct{ Path string }

func (e *MediaNotFoundError) Error() string { return "frigate has no " + e.Path }

// Snapshot returns a camera's latest frame.
func (d *Driver) Snapshot(ctx context.Context, cameraName string, opts nvr.ImageOptions) (*http.Response, error) {
	if !CameraNameRe.MatchString(cameraName) {
		return nil, fmt.Errorf("invalid camera name %q", cameraName)
	}
	if err := d.ready(cameraName); err != nil {
		return nil, err
	}
	v := url.Values{}
	if opts.Height > 0 {
		// Frigate's parameter is height; h is not read and the request
		// hangs until it times out.
		v.Set("height", strconv.Itoa(opts.Height))
	}
	if opts.BBox {
		v.Set("bbox", "1")
	}
	path := "/api/" + cameraName + "/latest.jpg"
	if len(v) > 0 {
		path += "?" + v.Encode()
	}
	return d.fetch(ctx, path, nil, snapshotTimeout, maxSnapshotBytes)
}

// EventMedia fetches one event's snapshot, thumbnail, or clip. A clip
// forwards the viewer's Range header.
func (d *Driver) EventMedia(ctx context.Context, id, kind, rangeHeader string) (*http.Response, error) {
	if !EventIDRe.MatchString(id) {
		return nil, fmt.Errorf("invalid event id %q", id)
	}
	switch kind {
	case nvr.MediaSnapshot:
		return d.fetch(ctx, "/api/events/"+id+"/snapshot.jpg", nil, snapshotTimeout, maxSnapshotBytes)
	case nvr.MediaThumbnail:
		return d.fetch(ctx, "/api/events/"+id+"/thumbnail.webp", nil, snapshotTimeout, maxThumbnailBytes)
	case nvr.MediaClip:
		header := http.Header{}
		if rangeHeader != "" {
			header.Set("Range", rangeHeader)
		}
		return d.fetch(ctx, "/api/events/"+id+"/clip.mp4", header, 0, 0)
	}
	return nil, fmt.Errorf("unknown event media %q", kind)
}
