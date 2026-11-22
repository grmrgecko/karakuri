# Design plan: Frigate NVR integration

**Feature**: `specs/034-frigate-nvr` | **Date**: 2026-10-31 | **Spec**: [spec.md](spec.md)

## Summary

A `frigate` connection kind holds one Frigate server. Its driver talks to
Frigate's HTTP API (logging in when credentials are set), keeps Frigate's
websocket open for events, review items, and camera feature states, polls
Frigate's stats for which cameras are receiving frames, and reconnects on its
own. Everything a viewer or flow sees is fetched from Frigate on request and
relayed: snapshots, a shared MJPEG stream per camera, events, thumbnails, and
clips with range requests. Karakuri stores no video.

Dashboards gain an `nvr` binding source, `{source: "nvr", connectionId,
camera}`, granting read of one camera's images, stream, and events, so the
camera widget and a new event list widget are scoped exactly as Home Assistant
cameras are. Flows gain an NVR event trigger, an NVR review trigger, and an
NVR image node that returns a snapshot as binary. A camera's detect, record,
snapshots, and motion switches become devices with transport `frigate`,
reusing the entity transport of feature 033, which gains per-entity
availability so a camera that stops receiving frames makes its switches
unavailable.

The names (`nvr` source, `nvr-event`, `nvr-review`, `nvr-image`, the event
list) are general so another NVR can register a kind that satisfies the same
interface later. [research.md](research.md) records the decisions and what the
lab Frigate showed, [data-model.md](data-model.md) the shapes,
[contracts/interfaces.md](contracts/interfaces.md) the routes, nodes, and
widgets, and [quickstart.md](quickstart.md) the checks.

## Technical context

- Language and dependencies: Go 1.26.5, Svelte 5. No new dependencies:
  `github.com/coder/websocket` (already used by the Home Assistant driver)
  for Frigate's websocket, `net/http` for its API.
- Components:
  - `pkg/integrations/nvr` (new): the `NVR` interface the routes, nodes, and
    widgets use (cameras, camera status, snapshot, stream, events, event media,
    event and review watchers), event and review types.
  - `pkg/integrations/frigate` (new): `kind.go` (schema, secrets, TLS options,
    probe), `client.go` (HTTP with login and re-login on 401, TLS), `driver.go`
    (websocket loop, camera list from `/api/config`, stats poll, watchers,
    reconnect), `stream.go` (one upstream MJPEG per camera shared by viewers),
    `devices.go` (switch entities for the entity transport), and
    `frigatetest` (a simulated Frigate API and websocket).
  - `pkg/mqttrelay`: optional `EntityAvailability` on a transport, applied as
    availability source `entity`; transport `frigate` validated like `esphome`.
  - `pkg/esphomedevices` generalised into `pkg/entitydevices`, listing and
    adding any transport's entities and tracking missing ones; ESPHome and
    Frigate both use it.
  - `pkg/dashscope`: source `nvr` and capability `CapNVR {Owner: connection,
    Ref: camera}`; a camera of `*` grants every camera of that connection, for
    an event list bound to all of them.
  - `pkg/api`: NVR relay and listing routes; Frigate entity discovery and add.
  - `pkg/nodes/nvrnodes` (new): `nvr-event`, `nvr-review`, `nvr-image`, and
    the plugin registering the Frigate kind.
  - `plugins/core/widgets/camera`: source `nvr` beside `ha`, bounding boxes
    option; `plugins/core/widgets/nvr-events` (new): the event list.
  - Frontend: Add from Frigate in Devices (the ESPHome dialog generalised to an
    entity dialog by transport).
  - Docs: `docs/frigate.md` as a guide, `docs/dashboard-authoring.md`,
    `docs/flow-authoring.md`, `docs/mcp.md`.
- Storage: no new tables; switch devices use `mqtt_devices` with transport
  `frigate`.
- Limits: snapshot 5 MB and 10 s; thumbnail 1 MB; events list at most 100;
  clip relayed with range requests and no size cap but the viewer's request
  lifetime; stats poll every 15 s; websocket ping 30 s; reconnect backoff 1 to
  60 s; one upstream stream per camera and height, closed 10 s after its last
  viewer leaves.

## Constitution check

- **Failures**: an unreachable Frigate sets the connection to error and fires
  the connection status trigger; relays answer 502 with the reason and 503 for
  a camera receiving no frames; triggers fire nothing while disconnected and
  missed events are not replayed, as the spec states; the image node fails
  through its error output and is retryable.
- **Drafts and deployment**: node fields come from metadata; the camera
  widget's config gains fields with defaults so existing Home Assistant
  cameras are unchanged.
- **Permissions**: the Frigate connection follows connection permissions.
  Viewer routes check `CapNVR` for the camera, with events and media checked
  against the event's camera. Author listing routes need dashboard write.
  Switch devices command under device rules. Frigate credentials and its
  session token never reach a browser.
- **Existing state**: camera widget configs without `source: nvr` keep their
  meaning; no migration; no key change.
- **Verification**: `make dev`, `make test`, the simulated Frigate in tests,
  read-only checks against the lab Frigate (cameras, snapshots, stream,
  events, clips, triggers on real events), switch toggles only on the
  simulated server unless the user allows otherwise, and a browser check of
  the widgets and dialogs.

## Design and contracts

### Driver

```text
start: GET /api/version, login if credentials (POST /api/login, cookie token)
       GET /api/config -> cameras, zones, tracked objects
       open /ws, send {"topic":"onConnect"}
loop:  camera_activity -> switch states per camera (detect, record, snapshots, motion)
       events   -> {type: new|update|end, before, after} -> event watchers
       reviews  -> {type: new|update|end, before, after} -> review watchers
       config/camera topics -> refresh cameras on change
every 15s: GET /api/stats -> camera_fps per camera -> receiving frames or not
on error: status error, close, backoff 1s..60s, start again
```

A 401 on any request logs in again once and retries. Commands to switches are
websocket messages `{"topic":"<camera>/<feature>/set","payload":"ON"}`; the
state comes back in `camera_activity`.

### Relays

The relay routes resolve the connection to an `nvr.NVR`, check the viewer's
capability for the camera, and copy Frigate's response. Snapshot uses
`latest.jpg?height=&bbox=`; Frigate's own parameter is `height` (the lab showed
that `h` hangs). The stream subscribes the viewer to the camera's shared
upstream `/api/<camera>?fps=&height=&bbox=`, which starts on the first viewer
and closes ten seconds after the last; each viewer receives whole frames from
the next boundary. Clips forward `Range` and return `206` as Frigate does.

### Triggers and the image node

`nvr-event` filters by cameras, labels, sub-label text, zones (any of),
minimum score, and stages; it fires on Frigate's event messages, using
`after` for the current object and `before` to tell when an object first
enters a filtered zone. `nvr-review` filters by cameras, severity, and stages.
Payloads carry relay paths for snapshot, thumbnail, and clip, valid for
viewers and flows of the server. `nvr-image` returns a camera's latest frame
or an event's snapshot as binary with `meta.content_type`.

### Switches as devices

The Frigate driver satisfies `mqttrelay.EntityTransport` with object IDs
`<camera>/<feature>` and boolean values `{on}`, and `EntityAvailability` with a
camera's receiving-frames state. `pkg/entitydevices` lists them as entities of
kind Toggle; adding names devices `<camera> <feature>`.

## Verification

| Scenario | Test |
| --- | --- |
| US1 | Probe and driver tests against the simulated Frigate with and without login, a wrong password, a re-login after 401, and a camera added by a config change. Live: probe of the lab Frigate on port 5000. |
| US2 | Relay tests for snapshot, the shared stream with two viewers and frame boundaries, 503 for a camera with no frames, and capability refusal. Live: browser check of the camera widget on a lab camera. |
| US3 | Trigger tests over simulated event and review messages for each filter and stage, zone entry, and the image node. Live: a trigger on a real lab event, observed without changing anything. |
| US4 | Event list route tests with filters and capability; browser check of the widget with a real event's clip. |
| US5 | Device tests through the simulated server: listing, add, toggle and reported state, unavailable when a camera stops. |

## Risks and open decisions

- Frigate's websocket and API are versioned with Frigate; the simulated server
  follows 0.18 as seen on the lab server, and older versions without review
  items fall back to events.
- MJPEG costs Frigate an encode per stream; sharing one upstream per camera and
  height keeps that to one per camera.
- `pkg/esphomedevices` becomes `pkg/entitydevices`; ESPHome behaviour stays
  as it is.
- Live switch toggles need the user's permission; the simulated server is
  otherwise the evidence for them.
