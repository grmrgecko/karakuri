# Contract: NVR interfaces

## Go

```go
package nvr

// NVR is a connection to a network video recorder. Frigate is the first.
type NVR interface {
	Cameras() []Camera
	Receiving(camera string) bool
	Snapshot(ctx context.Context, camera string, opts ImageOptions) (*http.Response, error)
	Stream(ctx context.Context, camera string, opts ImageOptions) (*Subscription, error)
	Events(ctx context.Context, q EventQuery) ([]Event, error)
	EventMedia(ctx context.Context, id, kind, rangeHeader string) (*http.Response, error)
	Event(ctx context.Context, id string) (Event, error)
	WatchEvents(fn func(Event)) func()
	WatchReviews(fn func(Review)) func()
}
```

`mqttrelay` gains:

```go
// EntityAvailability is an entity transport that also says when one entity
// cannot answer although the connection is up.
type EntityAvailability interface {
	WatchEntityAvailability(objectID string, fn func(available bool)) (func(), error)
}
```

## REST

Viewer routes check `CapNVR` for the camera (or `*`); authors with
`api:connections:read` pass.

- `GET /api/connections/{id}/nvr/cameras` (`api:dashboards:write`): cameras
  with zones, objects, and receiving state, for pickers.
- `GET /api/connections/{id}/nvr/cameras/{camera}/snapshot?height=&bbox=`:
  JPEG, 503 `camera "kitchen" is not receiving frames` when stopped.
- `GET /api/connections/{id}/nvr/cameras/{camera}/stream?height=&fps=&bbox=`:
  `multipart/x-mixed-replace` relayed from the shared upstream.
- `GET /api/connections/{id}/nvr/events?cameras=&labels=&zones=&limit=`:
  events, limited to cameras the caller may read.
- `GET /api/connections/{id}/nvr/events/{event}/{snapshot|thumbnail|clip}`:
  the event's media; a viewer's grant is checked against the event's camera
  first. A clip forwards `Range`; Frigate builds clips on request and answers
  with the whole clip.
- `GET /api/mqtt_devices/entities?connection_id=` and
  `POST /api/mqtt_devices/entities` replace the ESPHome-only routes for any
  entity transport; the ESPHome paths stay as aliases.

## MCP

- `list_nvr_cameras {connection_id}`
- `list_nvr_events {connection_id, cameras?, labels?, zones?, limit?}`
- `get_nvr_snapshot {connection_id, camera}` returns the image as an MCP image
  content block.
- `discover_entities` and `add_entities` generalise the ESPHome tools; the
  ESPHome names stay.

## Nodes

`nvr-event` (trigger): `connection`, `cameras`, `labels`, `sub_label`,
`zones`, `min_score`, `stages` (`start`, `update`, `end`), `test_params`.
Payload:

```json
{ "id": "1793028855.753667-vb87ip", "camera": "doorbell", "label": "person",
  "sub_label": null, "zones": ["driveway"], "entered_zones": ["driveway"],
  "score": 0.86, "stage": "start", "start_time": "2026-10-31T15:20:55Z",
  "end_time": null, "has_clip": true, "has_snapshot": true,
  "snapshot": "/api/connections/<id>/nvr/events/<event>/snapshot",
  "thumbnail": "/api/connections/<id>/nvr/events/<event>/thumbnail",
  "clip": "/api/connections/<id>/nvr/events/<event>/clip" }
```

`nvr-review` (trigger): `connection`, `cameras`, `severity` (`alert`,
`detection`, or any), `stages`; payload `{id, camera, severity, objects, zones,
stage, start_time, end_time}`.

`nvr-image`: `connection`, `source` (`camera` or `event`), `camera` or
`event` (expressions allowed), `height`, `bbox`; output `out` is binary with
`meta.content_type` and output `image` a typed image value; failures go
through the node's error handling and are retryable.

## Broker topics

`nvr:<connection>:events:<camera>` and `nvr:<connection>:events:*` carry
`{id, camera, label, stage}` for each event's start and end, so event lists
refresh. A viewer may subscribe to a camera's topic with that camera's grant
and to `*` only with a grant of every camera.

## Widgets

Camera widget config adds `source: "nvr"` with `camera` and `bbox`; `source:
"ha"` is unchanged. The `nvr-events` widget config: `{source: "nvr",
connectionId, camera, labels, zones, limit, showThumbnails}` where `camera` may
be `*`.
