# Research: Frigate NVR integration

Read-only probes of the lab Frigate (0.18.2, five cameras, 10.0.10.5) on
2026-10-31 settled the API details below.

## Transport: HTTP and websocket, not MQTT

- **Decision**: the driver uses Frigate's HTTP API and its `/ws` websocket.
- **Rationale**: the websocket upgrades on the internal port (101) and carries
  `{topic, payload}` messages mirroring Frigate's MQTT topics. After sending
  `{"topic":"onConnect"}` it delivered `camera_activity` (per camera: motion,
  objects, and `config` with `detect`, `record`, `snapshots`, `enabled`,
  `alerts`, `detections`, ...), `<camera>/motion`, and status topics. Events
  and reviews arrive on `events` and `reviews`. No broker setup is needed,
  which the spec asks for.
- **Alternatives**: Frigate's MQTT output, which needs a broker shared with
  Frigate and duplicates what the websocket already carries.

## Authentication

- **Decision**: with credentials, `POST /api/login` and keep the
  `frigate_token` cookie; on a 401, log in once more and retry. Without
  credentials, call the internal port directly.
- **Rationale**: the lab answers 401 on port 8971 and 200 on 5000, as Frigate
  0.14 and later do.

## Snapshots and streams

- **Decision**: snapshots from `/api/<camera>/latest.jpg?height=<h>&bbox=1`;
  live view from `/api/<camera>?fps=<n>&height=<h>`, an MJPEG multipart stream
  (`multipart/x-mixed-replace;boundary=frame`); event media from
  `/api/events/<id>/snapshot.jpg`, `thumbnail.webp`, and `clip.mp4`.
- **Rationale**: measured on the lab: `latest.jpg` 52 KB in 0.35 s,
  `latest.jpg?height=180` 3 KB in 7 ms, the stream answers 200 with the
  multipart type, an event snapshot 300 KB in 0.4 s. `latest.jpg?h=180` hung
  for 15 s, so the parameter must be `height`.
- **Alternatives**: go2rtc WebRTC or MSE, out of scope in the spec.

## Shared upstream streams

- **Decision**: one upstream stream per camera and height, fanned out to every
  viewer by whole frames, closed ten seconds after the last viewer.
- **Rationale**: each MJPEG stream makes Frigate encode frames; a wall of
  panels showing the same doorbell should cost one encode.

## Receiving frames

- **Decision**: poll `/api/stats` every 15 s and treat `camera_fps` of zero as
  not receiving frames.
- **Rationale**: the lab's stats report `camera_fps` per camera (about 5). The
  websocket's `<camera>/status/detect` topics report feature status, not
  frames: the kitchen showed `offline` there while its stats showed 5.1 fps.

## Events and reviews

- **Decision**: events from the `events` topic, `{type, before, after}`;
  reviews from `reviews` in the same shape; the event list reads
  `/api/events?cameras=&labels=&zones=&limit=`.
- **Rationale**: the lab's `/api/events` returns objects with `id`, `camera`,
  `label`, `sub_label`, `zones`, `data.score`, `start_time`, `end_time`,
  `has_clip`, and `has_snapshot`. `/api/review` answered an empty list, so
  review items are tested against the simulated server and confirmed live
  only if one occurs.

## Binding and scope

- **Decision**: a new binding source `nvr` with `connectionId` and `camera`,
  and capability `CapNVR`. A camera of `*` grants the whole connection, for an
  event list over every camera.
- **Rationale**: the existing `ha` binding names an entity of a Home Assistant
  connection; an NVR camera is not one, and reusing `ha` would make the scope
  check guess at the connection's kind.

## Switches as devices

- **Decision**: extend feature 033's entity transport with an optional
  availability watch, and generalise `esphomedevices` to `entitydevices`.
- **Rationale**: Frigate switches are entities of a connection exactly as
  ESPHome entities are; a second copy of listing, adding, and missing tracking
  would drift. Per-entity availability is new because a camera can stop while
  its server is fine.

## Testing without touching the house

- **Decision**: `frigatetest` simulates `/api/version`, `/api/login`,
  `/api/config`, `/api/stats`, snapshots, the MJPEG stream, events and media,
  and `/ws` with scripted messages and recorded `set` commands. The lab is read
  only.
- **Rationale**: switching a real camera's detection or recording pauses the
  house's security; the user has not allowed it.
