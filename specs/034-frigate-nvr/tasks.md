# Tasks: Frigate NVR integration

**Feature**: `specs/034-frigate-nvr`
**Input**: [spec.md](spec.md), [plan.md](plan.md), [research.md](research.md),
[data-model.md](data-model.md), [contracts/interfaces.md](contracts/interfaces.md),
[quickstart.md](quickstart.md).

The lab Frigate at `http://10.0.10.5:5000` watches a real house. Live checks
there are read only: connect, list, snapshots, streams, events, clips, and
triggers on events that happen anyway. Switch toggles run against the
simulated server in `frigatetest` unless the user names a camera that may be
toggled and restored.

## Phase 1: Shared preparation

- [X] T001 Simulated Frigate in `pkg/integrations/frigate/frigatetest/server.go`: an `httptest` server answering `/api/version`, `POST /api/login` (sets cookie `frigate_token`, 401 on a wrong password, optional expiry to force a re-login), `/api/config` (cameras with zones and tracked objects), `/api/stats` (`cameras.<name>.camera_fps`), `/api/<camera>/latest.jpg?height=&bbox=`, the MJPEG stream `/api/<camera>?fps=&height=` (`multipart/x-mixed-replace;boundary=frame`, counting open upstreams), `/api/events?cameras=&labels=&zones=&limit=`, `/api/events/<id>/{snapshot.jpg,thumbnail.webp,clip.mp4}` with `Range` support, and `/ws` (answers `onConnect` with `camera_activity`, records `<camera>/<feature>/set` messages and echoes the new state); hooks to push `events` and `reviews` messages, set a camera's fps, change the config, and drop the websocket

## Phase 2: Foundation, connection and NVR interface

Every user story phase depends on this phase.

- [X] T002 `pkg/integrations/nvr/nvr.go`: the `NVR` interface from the contract (`Cameras`, `Receiving`, `Snapshot`, `Stream`, `Events`, `EventMedia`, `WatchEvents`, `WatchReviews`), `Camera {Name, Zones, Objects, Receiving}`, `Event` and `Review` with the fields in data-model.md, `ImageOptions {Height, FPS, BBox}`, `EventQuery {Cameras, Labels, Zones, Limit}` with limit at most 100, `Subscription` (frames channel and `Close`), and `ErrNotReceiving` carrying the camera name for the 503 text `camera "<name>" is not receiving frames`
- [X] T003 `pkg/integrations/frigate/kind.go`: kind `frigate` with `url` (required), `username`, `password` (secret), `tls_skip_verify`, `ca_cert`; URL validation (http or https, no path beyond `/`); probe returning `Frigate <version>, <n> cameras: <names>` or the failure reason (`login refused`, unreachable, not a Frigate server)
- [X] T004 `pkg/integrations/frigate/client.go`: HTTP client with the TLS options, login by `POST /api/login` when credentials are set, the `frigate_token` cookie kept in memory, one re-login and retry on a 401; snapshot capped at 5 MB and 10 s, thumbnail at 1 MB; the password and token never logged
- [X] T005 `pkg/integrations/frigate/driver.go`: on start `GET /api/version`, login, `GET /api/config` for cameras; websocket `/ws` (with `github.com/coder/websocket`, the token cookie when logged in), send `{"topic":"onConnect"}`; dispatch `camera_activity` into per-camera switch state, `events` and `reviews` (`{type, before, after}`) to watchers with `stage` mapped `new` to `start`, `entered_zones` from `after.current_zones` minus `before.current_zones`, relay paths under `/api/connections/<id>/nvr/events/<event>/`; refresh cameras on config topics; poll `/api/stats` every 15 s for `camera_fps > 0`; ping every 30 s; on error set the connection status with the reason and reconnect with backoff 1 s doubling to 60 s; the driver satisfies `nvr.NVR`
- [X] T006 `pkg/integrations/frigate/stream.go`: one upstream MJPEG per camera and height, started by the first subscriber, parsed into whole frames by boundary, fanned out to subscribers (a slow subscriber drops frames rather than blocking others), closed 10 s after the last subscriber leaves
- [X] T007 Plugin `pkg/nodes/nvrnodes/plugin.go` registering the kind; wire it into the built-in plugin list in `cmd/karakuri/serve.go`; connection resolution helper `nvrFor(connectionID) (nvr.NVR, error)` used by routes and nodes
- [X] T008 Tests in `pkg/integrations/frigate`: probe with and without login, wrong password, re-login after an expired token, websocket dispatch of events and reviews into watchers with stage and zone entry, config change adding a camera, stats poll marking a camera not receiving, reconnect after the websocket drops, the shared stream with two subscribers (one upstream, whole frames to each) and the 10 s close

## Phase 3: User story 1, connect Frigate (P1)

**Goal**: an author connects Frigate and sees its cameras.
**Independent check**: quickstart scenario 1.

- [X] T009 [US1] `GET /api/connections/{id}/nvr/cameras` (`api:dashboards:write`) returning cameras with zones, objects, and receiving state, and MCP `list_nvr_cameras {connection_id}`, in `pkg/api/nvr_handler.go` and `pkg/mcpserver/tools_nvr.go`
- [X] T010 [US1] Masking of the password through REST and MCP connection reads, and the kind's form fields in the connection editor (no custom editor needed)

## Phase 4: User story 2, watch cameras (P1)

**Goal**: dashboards show Frigate cameras live or as snapshots, scoped to viewers.
**Independent check**: quickstart scenario 2.

- [X] T011 [US2] `pkg/dashscope`: binding source `nvr` (`{source: "nvr", connectionId, camera}`) and capability `CapNVR {Owner: connectionId, Ref: camera}`; a camera of `*` grants every camera of that connection; derive the capability from widget bindings in `derive.go`
- [X] T012 [US2] Relay routes in `pkg/api/nvr_handler.go`: `GET /api/connections/{id}/nvr/cameras/{camera}/snapshot?height=&bbox=` (JPEG, 503 when not receiving, 502 with the reason when Frigate fails) and `GET /api/connections/{id}/nvr/cameras/{camera}/stream?height=&fps=&bbox=` (relayed from the shared upstream until the viewer disconnects); viewers pass with `CapNVR` for the camera, authors with `api:connections:read`; Frigate credentials and the token never reach the response
- [X] T013 [US2] Camera widget in `plugins/core/widgets/camera/src`: `source: "nvr"` beside `ha` in `config.ts`, `CameraConfig.svelte` with a Frigate connection picker, a camera picker from the cameras route, and a bounding boxes option; `relay.ts` and `mjpeg.ts` build the NVR relay URLs; the widget's binding is `nvr`; an unavailable state when the relay answers 503; existing `ha` configs unchanged
- [X] T014 [US2] Tests: relay route capability refusal (viewer without the camera gets 403, `*` passes for any camera of that connection only), 503 for a camera with no frames, the stream route delivering whole frames; dashscope binding and derive tests

## Phase 5: User story 3, react to detections (P1)

**Goal**: flows start on events and review items and read images.
**Independent check**: quickstart scenario 3.

- [X] T015 [US3] `nvr-event` trigger in `pkg/nodes/nvrnodes/nodes.go`: fields `connection`, `cameras`, `labels`, `sub_label` (contains, case-insensitive), `zones` (any of), `min_score`, `stages` (`start`, `update`, `end`), `test_params`; with zones set, a `start` or `update` matches only when `entered_zones` meets the filter, an `end` when the object was in a filtered zone; payload per the contract
- [X] T016 [US3] `nvr-review` trigger: `connection`, `cameras`, `severity` (`alert`, `detection`, any), `stages`; payload `{id, camera, severity, objects, zones, stage, start_time, end_time}`
- [X] T017 [US3] `nvr-image` node: `connection`, `source` (`camera` or `event`), `camera` or `event` (expressions allowed), `height`, `bbox`; output binary with `meta.content_type`; error output on failure; retryable
- [X] T018 [US3] Event media relay `GET /api/connections/{id}/nvr/events/{event}/{snapshot|thumbnail|clip}`: the event's camera checked against `CapNVR`; clip forwards `Range` and returns 206 as Frigate does; MCP `get_nvr_snapshot {connection_id, camera}` returning an image content block
- [X] T019 [US3] Node tests against the simulated server: each filter and stage, zone entry, the review trigger by severity, the image node for a camera and an event, the error output when the camera is not receiving

## Phase 6: User story 4, recent events on a dashboard (P2)

**Goal**: an event list widget with thumbnails and clips.
**Independent check**: quickstart scenario 4.

- [X] T020 [US4] `GET /api/connections/{id}/nvr/events?cameras=&labels=&zones=&limit=` (limit at most 100) returning events limited to cameras the caller may read, and MCP `list_nvr_events`; live updates to subscribed viewers when events arrive, through the existing dashboard live channel
- [X] T021 [US4] `plugins/core/widgets/nvr-events` (new widget package, registered like the others): config `{source: "nvr", connectionId, camera, labels, zones, limit, showThumbnails}` with `camera` allowed to be `*`; a list of label, camera, zones, score, and time with thumbnails; tapping an item plays its clip in a dialog; refreshes as events arrive; empty state text "No events yet"
- [X] T022 [US4] Tests: events route filters and capability narrowing; widget config defaults

## Phase 7: User story 5, switch camera features (P2)

**Goal**: detect, record, snapshots, and motion become devices.
**Independent check**: quickstart scenario 5.

- [X] T023 [US5] `mqttrelay.EntityAvailability` (`WatchEntityAvailability(objectID string, fn func(available bool)) (func(), error)`): when a device's transport implements it, apply availability with source `entity` (offline when false, the connection rule otherwise); transport `frigate` validated like `esphome`
- [X] T024 [US5] Generalise `pkg/esphomedevices` into `pkg/entitydevices` (list a connection's entities against devices, add with `<connection-entity name>` and numeric suffixes, mark missing and clear on return) for any `EntityTransport`; ESPHome behaviour unchanged; update `pkg/api/esphome_handler.go`, `pkg/mcpserver/tools_devices.go`, and `cmd/karakuri/serve.go`
- [X] T025 [US5] `pkg/integrations/frigate/devices.go`: the driver as `EntityTransport` with object IDs `<camera>/<feature>` (features detect, record, snapshots, motion), entity type `switch`, kind `boolean`, values `{on}` from `camera_activity`, commands as `{"topic":"<camera>/<feature>/set","payload":"ON"|"OFF"}`; `EntityAvailability` from the camera's receiving state; device names `<camera> <feature>`
- [X] T026 [US5] REST `GET /api/mqtt_devices/entities?connection_id=` and `POST /api/mqtt_devices/entities`, with the ESPHome paths kept as aliases; MCP `discover_entities` and `add_entities` beside the ESPHome tool names
- [X] T027 [US5] Devices settings: the ESPHome discovery dialog generalised to an entity dialog by connection kind (`frontend/src/routes/settings/ESPHomeDiscovery.svelte` renamed to `EntityDiscovery.svelte`), with "Add from Frigate" beside "Add from ESPHome"
- [X] T028 [US5] Tests through the simulated server: listing switches, add, toggle detect and the reported state, the device unavailable when the camera's fps drops to zero and back online when it recovers, unknown when the connection is down; ESPHome device tests pass unchanged after the move

## Final verification and documentation

- [X] T029 Write `docs/frigate.md` (connecting, login, cameras on dashboards, scope, triggers, the image node, the event list, switches as devices, offline behaviour, limits, what karakuri does not do), serve it as an MCP guide and through the guide route (`docs/docs.go`, `pkg/api/guide_handler.go`, `pkg/mcpserver/resources.go`); update `docs/dashboard-authoring.md`, `docs/flow-authoring.md`, `docs/mcp.md`, and `docs/mqtt-device-state-script.md`; set the spec status to Implemented
- [X] T030 Run a full `make dev` (widgets included), `make test TARGET=server`, and `make test TARGET=frontend`; restart `karakuri-dev.service`; browser check of the camera widget config, a Frigate camera live and as snapshots, the event list with a clip, and the entity dialog, with settled screenshots and a console check
- [X] T031 Live quickstart against the lab Frigate, read only: scenarios 1 to 4 and the switch listing of scenario 5 without adding or toggling; remove the test dashboard, flow, and connection; confirm the service is active and http://localhost:8080 answers; report what was created and removed

Live checks on 2026-11-05 against the lab Frigate 0.18.2, read only: connection
test and camera list; snapshot (36 KB in 13 ms at height 360 with boxes); the
shared stream (17 frames in 4 s, closed 10 s after the viewer left); events,
thumbnails, event snapshots, and a 19 s clip played in the browser; MCP
snapshot, events, and switch listing; a test flow whose NVR Event trigger fired
on a real kitchen event's end and whose NVR Image node fetched its snapshot; a
test dashboard with both camera modes and the event list refreshing as events
arrived. No switch was added or toggled on the lab server; toggles and the
switch devices' availability are covered against the simulated server.

## Dependencies and execution order

- T001 precedes the tests of every phase.
- Phase 2 blocks the stories: T002 precedes T003 to T007; T004 precedes T005;
  T005 precedes T006.
- US1 and US2 can proceed in parallel after Phase 2. US3's media relay (T018)
  reuses US2's capability (T011). US4 needs T011 and T018 for thumbnails and
  clips. US5 needs only Phase 2.
- T023 and T024 precede T025; T024 precedes T026 and T027.
- Commit each verified phase on its own; run a full `make dev` before each
  commit that touches shared widget code.

## Parallel opportunities

- T003 and T002 touch different packages; T009 and T011 can run side by side.
- T015, T016, and T017 share `nodes.go` and run in sequence; T018 is a route
  and can run beside them.
- T021 (widget) and T020 (route) can run in parallel once the contract is fixed.
- US5 (T023 to T028) is independent of US2 to US4.

## Implementation strategy

The connection and driver (Phase 2) with cameras on dashboards (US2) are the
first deliverable: Frigate cameras on a wall panel through karakuri. Detection
triggers (US3) follow, then the event list (US4) and the switches (US5).
