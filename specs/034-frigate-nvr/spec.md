# Feature specification: Frigate NVR integration

**Feature**: `specs/034-frigate-nvr`
**Created**: 2026-10-31
**Status**: Implemented
**Input**: Support open-source NVRs without becoming one, starting with
Frigate. Karakuri does not record, detect, or store video: Frigate does.
Connect to a Frigate server, show its cameras on dashboards live or as
snapshots, start flows on what Frigate detects (a person in the driveway, a car
in a zone, an alert ending), hand a flow the event's snapshot and clip, list
recent events on a dashboard, switch a camera's detection, recording, snapshots,
and motion on and off as devices, and show a camera or the whole server as
unavailable when it is down. The camera widget, the event trigger, and the
event list are shaped so other open-source NVRs can be added later.

## User scenarios and testing

### User story 1: Connect Frigate (Priority: P1)

An author adds the household's Frigate server as a connection, with the
username and password Frigate asks for, and tests it. The test reports
Frigate's version and its cameras.

**Why this priority**: Every other story reads Frigate through this
connection.
**Independent test**: Save a Frigate connection, test it, and list its
cameras.

**Acceptance scenarios**:

1. **Given** a Frigate URL and credentials, **When** the connection is tested,
   **Then** it reports Frigate's version and the names of its cameras.
2. **Given** a Frigate server reachable without login, such as its internal
   port, **When** no credentials are set, **Then** it connects the same way.
3. **Given** wrong credentials or an unreachable server, **When** it is tested,
   **Then** it fails with the reason.
4. **Given** a saved connection, **When** anyone reads it, **Then** its password
   is masked.
5. **Given** a connected server, **When** Frigate restarts or a new camera is
   added, **Then** the connection follows without being saved again.

### User story 2: Watch Frigate cameras (Priority: P1)

The kitchen panel shows the driveway camera live and the backyard as a
snapshot every ten seconds, with Frigate's detection boxes drawn on it.

**Why this priority**: Seeing a camera is the first thing people put on a
wall panel.
**Independent test**: Bind the camera widget to two Frigate cameras, one live
and one by snapshot, open the dashboard as a viewer, and see both.

**Acceptance scenarios**:

1. **Given** a camera widget bound to a Frigate camera, **When** a viewer opens
   the dashboard, **Then** the server relays the camera's live view or its
   latest snapshot at the chosen interval, without the viewer reaching
   Frigate.
2. **Given** the bounding boxes option, **When** snapshots are shown, **Then**
   Frigate's detection boxes are drawn on them.
3. **Given** a viewer of a dashboard that binds one camera, **When** they ask
   for another camera's image, **Then** the request is refused, as for Home
   Assistant cameras.
4. **Given** a camera Frigate reports as not receiving frames, or a Frigate
   server that is down, **When** the widget is shown, **Then** it says the
   camera is unavailable rather than freezing on an old frame.

### User story 3: React to detections (Priority: P1)

When Frigate raises an alert for a person in the driveway zone, a flow turns
on the porch light and sends a notification with the snapshot; when the alert
ends, the flow turns the light off.

**Why this priority**: Detections are why people run Frigate beside home
automation.
**Independent test**: With a detection trigger filtered to a camera, label,
and zone, walk through the zone and check one run at the start and one at the
end, carrying the snapshot.

**Acceptance scenarios**:

1. **Given** a detection trigger filtered by camera, object label, zone, and
   minimum score, **When** Frigate reports a matching object, **Then** one run
   starts with the camera, label, zones, score, start time, and links to the
   snapshot and clip served through karakuri.
2. **Given** the trigger set to start, update, end, or any, **When** the event
   moves through those stages, **Then** it fires at the chosen stages only.
3. **Given** a review trigger set to alerts or detections, **When** Frigate
   opens or closes a review item, **Then** one run starts per stage with the
   item's severity, objects, zones, and camera.
4. **Given** a flow that needs the image, **When** it reads an event's
   snapshot or a camera's latest frame, **Then** it gets the image as binary,
   ready for a notification, a file, or an AI vision node.
5. **Given** Frigate is unreachable, **When** a detection would have been
   reported, **Then** nothing fires, the connection shows the error, and the
   connection status trigger fires; events missed while it was down are not
   replayed.

### User story 4: Recent events on a dashboard (Priority: P2)

The hallway panel lists the last ten events from the front door camera with
thumbnails, labels, and times; tapping one plays its clip.

**Why this priority**: People check what happened without opening Frigate.
**Independent test**: Place the event list widget filtered to a camera and
label, trigger an event, and play its clip.

**Acceptance scenarios**:

1. **Given** an event list widget filtered by camera, label, and zone, **When**
   it is shown, **Then** it lists matching events, newest first, with
   thumbnails, labels, zones, and times, updating as new events arrive.
2. **Given** an event with a clip, **When** a viewer taps it, **Then** the clip
   plays through karakuri.
3. **Given** a viewer of a dashboard, **When** they ask for events or clips of
   cameras the widget does not bind, **Then** the request is refused.

### User story 5: Switch camera features (Priority: P2)

An author turns off detection on the backyard camera while the kids play,
from a dashboard toggle and from a flow on a schedule.

**Why this priority**: Pausing detection or recording is the most common
Frigate control, and devices already do toggles everywhere.
**Independent test**: Add a camera's detection switch as a device, toggle it
from a dashboard, and check Frigate's state.

**Acceptance scenarios**:

1. **Given** a connected Frigate server, **When** an author adds a camera's
   switches, **Then** detection, recording, snapshots, and motion become toggle
   devices that show Frigate's current state.
2. **Given** a switch device, **When** it is toggled, **Then** Frigate changes
   the setting, and the device shows the state Frigate reports back.
3. **Given** a camera or server that is down, **When** its switches are drawn,
   **Then** they are unavailable, as feature 032 defines for devices.

### Edge cases

- Frigate 0.14 and later require login on the external port; the internal
  port does not. Both are supported.
- Self-signed certificates are common on home servers; a connection can trust
  a given certificate or skip verification, and says which it does.
- A camera renamed in Frigate's configuration is a new camera; devices and
  widgets bound to the old name show it as missing.
- Events and reviews from Frigate versions without review items fall back to
  events.
- Clips are relayed as Frigate serves them; karakuri does not transcode or
  keep them.
- Large snapshot and clip requests are bounded so a dashboard cannot hold the
  server's memory.
- Many viewers of one live camera share one upstream stream.

## Requirements

### Functional requirements

- **FR-001**: The system MUST offer a Frigate connection kind with URL,
  optional username and password, and certificate settings, with the password
  encrypted and masked.
- **FR-002**: A connection test MUST report Frigate's version and cameras, or
  the reason it failed.
- **FR-003**: The connection MUST receive Frigate's events and review updates
  in real time, follow Frigate restarts and configuration changes, and report
  its status.
- **FR-004**: The camera widget MUST bind Frigate cameras for live view or
  snapshots at an interval, optionally with detection boxes, relayed by the
  server under the same viewer restrictions as Home Assistant cameras.
- **FR-005**: Flows MUST be able to start on Frigate object events filtered by
  camera, label, sub-label, zone, minimum score, and stage, and on review items
  filtered by camera and severity, each carrying links to the snapshot, thumbnail,
  and clip served through karakuri.
- **FR-006**: Flows MUST be able to read an event's snapshot, an event's clip
  link, and a camera's latest frame as binary images.
- **FR-007**: A dashboard event list MUST show recent matching events with
  thumbnails and play clips, refreshing as events arrive, under the dashboard's
  viewer restrictions.
- **FR-008**: A camera's detection, recording, snapshot, and motion switches
  MUST be addable as devices that read Frigate's state and change it.
- **FR-009**: A camera that is not receiving frames and every camera of an
  unreachable server MUST be shown as unavailable in widgets and devices.
- **FR-010**: Karakuri MUST NOT record, detect, transcode, or store video;
  everything it shows or hands a flow comes from Frigate on request.
- **FR-011**: The new connection kind, nodes, widget options, and routes MUST be
  exposed through REST, MCP, and the editor with the same rules.

### Key entities

- **Frigate connection**: one Frigate server: URL, credentials, certificate
  settings, version, and cameras.
- **Camera**: a camera Frigate knows, with its zones, objects it tracks, and
  whether it is receiving frames.
- **Event**: one tracked object: camera, label, sub-label, zones, score, start
  and end, and whether it has a snapshot and a clip.
- **Review item**: Frigate's grouping of activity into alerts and detections,
  with severity, objects, zones, start, and end.

## Success criteria

- **SC-001**: A Frigate server is connected and its cameras listed with one
  test and no configuration on the Frigate side.
- **SC-002**: A person entering a watched zone starts the flow within two
  seconds of Frigate reporting it, and the alert ending starts the end run.
- **SC-003**: A dashboard viewer sees a live Frigate camera and recent events
  with playable clips without any access to Frigate itself.
- **SC-004**: Toggling a camera's detection device changes Frigate's setting
  and the device shows Frigate's state within the same time a device update
  takes.
- **SC-005**: Relevant Makefile builds and tests pass, the connection form,
  camera widget, event list, and new nodes render without console errors, and
  the development service stays active.

## Assumptions and scope

- Frigate 0.14 or later is the target; its HTTP API and websocket provide
  everything here, so no MQTT broker is needed for Frigate.
- Other NVRs (Shinobi, ZoneMinder, motionEye, Scrypted) are out of scope for
  this feature; the widget source, the event trigger's payload, and the event
  list are named for NVRs in general so they can be added later.
- Live view uses a stream Frigate already serves; WebRTC and two-way audio are
  out of scope.
- PTZ control, timeline scrubbing, exports, face and plate management, and
  Frigate configuration editing stay in Frigate's own interface; sub-labels
  such as recognised faces and plates are passed through.
- The lab Frigate (0.18.2, five cameras including indoor ones) is used for
  read-only live checks; switching a real camera's detection or recording off
  needs the user's say-so, since it pauses the house's security. Toggles and
  event lifecycles are otherwise tested against a simulated Frigate API.

## References

- [Home media and list widgets](../024-home-media-widgets/spec.md)
- [Configured notification services](../018-notification-services/spec.md)
- [Zigbee2MQTT discovery and device availability](../032-zigbee2mqtt-discovery/spec.md)
- [ESPHome native API](../033-esphome-native-api/spec.md)
