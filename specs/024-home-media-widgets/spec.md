# Feature specification: Home media and list widgets

**Feature**: `specs/024-home-media-widgets`
**Created**: 2026-10-24
**Status**: Implemented
**Input**: Add the smart-mirror widget types Karakuri lacks: camera streams, now
playing, to-do lists, history graphs, countdowns, and a Wi-Fi QR code, plus
spoken alerts, a table form of Flow Text, and air quality in the weather widget.

## User scenarios and testing

### User story 1: Watch a camera on a dashboard (Priority: P1)

An author places a Camera widget bound to a Home Assistant `camera` entity. A
doorbell flow jumps the hallway panel to the page holding it.

**Why this priority**: Cameras are the most-used smart-mirror widget
Karakuri has no answer for, and feature 023's doorbell page has nothing to show.
**Independent test**: Bind a camera, open the dashboard as an anonymous viewer
of a public dashboard, and see the live image; request another camera through
the same routes and be refused.

**Acceptance scenarios**:

1. **Given** a Camera widget in live mode, **When** a viewer opens the dashboard,
   **Then** the server relays Home Assistant's MJPEG stream for that camera.
2. **Given** snapshot mode with a 5-second interval, **When** viewed, **Then**
   the image refreshes every 5 seconds.
3. **Given** a viewer of a dashboard that binds one camera, **When** they request
   another camera's snapshot or stream, **Then** the request is refused.
4. **Given** Home Assistant is unreachable, **When** the widget loads, **Then**
   it shows an error and retries.

### User story 2: See and control what is playing (Priority: P1)

A Now Playing widget bound to a `media_player` entity shows cover art, title,
artist, and progress, with play, pause, skip, and volume when the dashboard
permits control.

**Why this priority**: Media is the most-requested Home Assistant domain the
dashboard draws poorly.
**Independent test**: Bind a media player, start playback in Home Assistant,
and watch the widget follow track changes; press pause.

**Acceptance scenarios**:

1. **Given** a playing media player, **When** the track changes, **Then** title,
   artist, art, and progress update without a reload.
2. **Given** art that Home Assistant serves from its own proxy, **When** the
   widget loads it, **Then** the server relays it with the connection's token,
   which never reaches the browser.
3. **Given** a read-only dashboard or a display-only binding, **When** viewed,
   **Then** no controls are drawn and service calls are refused.

### User story 3: Tick off a shared list (Priority: P1)

A To-do List widget bound to a Home Assistant `todo` entity (shopping list,
Todoist, CalDAV, and anything else Home Assistant syncs) shows open items and
lets a household member tick them off or add one.

**Why this priority**: To-do and shopping lists are a common smart-mirror use;
one widget over Home Assistant covers every service it supports.
**Independent test**: Bind the shopping list, add an item from the widget, tick
it off, and see Home Assistant's list change.

**Acceptance scenarios**:

1. **Given** a todo entity, **When** the widget loads, **Then** it lists the items
   from Home Assistant, open first.
2. **Given** control is permitted, **When** an item is ticked, **Then**
   `todo.update_item` marks it completed and the list refreshes.
3. **Given** the list changes in Home Assistant, **When** the entity's state
   changes, **Then** the widget refetches.

### User story 4: Graph a reading over time (Priority: P2)

A History Graph widget draws one or more sensors over the last hours.

**Why this priority**: It replaces the many single-number modules (system stats,
glucose, electricity price) with one widget.
**Independent test**: Bind two temperature sensors over 24 hours and compare the
curve with Home Assistant's history view.

**Acceptance scenarios**:

1. **Given** Home Assistant series, **When** the widget loads, **Then** it reads
   their recorded history for the configured hours and extends the lines live.
2. **Given** a control or MQTT device series, **When** viewed, **Then** the line
   starts at page load and grows with live updates, since those sources keep no
   history.
3. **Given** a request for history of an entity the dashboard does not bind,
   **When** made, **Then** it is refused.

### User story 5: Hear an alert (Priority: P2)

An alert command or dashboard notification endpoint with `speak` set is read
aloud by each viewer's browser.

**Independent test**: Send an alert with `speak: true` to an open viewer and hear
the title and message.

**Acceptance scenarios**:

1. **Given** a viewer with speech available, **When** a speaking alert arrives,
   **Then** it reads the title and message once.
2. **Given** a viewer opened after the alert was raised, **When** it loads,
   **Then** it does not read the existing alert.
3. **Given** a render, **When** it photographs the dashboard, **Then** nothing
   is spoken.

### User story 6: Small display widgets (Priority: P3)

Authors add a Countdown to dated events, a Wi-Fi QR code for guests, flow data
as a table, and air quality and pollen to the weather widget.

**Acceptance scenarios**:

1. **Given** a countdown to an annual date, **When** the date passes, **Then** it
   counts to the next year.
2. **Given** a Wi-Fi QR widget, **When** a phone scans it, **Then** it offers to
   join the configured network.
3. **Given** Flow Text receiving an array of objects, **When** shown, **Then**
   it draws a table with the objects' keys as columns, or the configured
   `columns` in order; an array of strings shows as lines.
4. **Given** the weather widget's air quality layout, **When** shown, **Then** it
   draws AQI, particulates, and pollen from Open-Meteo where available.

### Edge cases

- Camera and art routes check the entity's domain; a `camera` route will not
  relay a `media_player`, and the reverse.
- Art whose `entity_picture` is an external URL is loaded directly by the browser;
  only Home Assistant's own `/api/` paths are relayed.
- History requests are limited to 1 to 168 hours.
- A Wi-Fi password in a widget's config is readable by every viewer of the
  dashboard, as the QR code itself is.
- Pollen data exists only where Open-Meteo publishes it (Europe); elsewhere the
  pollen row is omitted.

## Requirements

### Functional requirements

- **FR-001**: The server MUST relay a bound Home Assistant camera's snapshot and
  MJPEG stream, and a bound media player's Home Assistant-hosted art, using the
  connection's stored token, to callers who hold connection read or whose
  dashboard binds that entity.
- **FR-002**: The server MUST return a bound entity's recorded history for 1 to
  168 hours and a bound `todo` entity's items under the same rule.
- **FR-003**: To-do and media writes MUST go through the existing service call
  route and its dashboard control policy.
- **FR-004**: The widget bridge MUST offer an authenticated `fetch` that carries
  the dashboard claim, for widgets that read binary or streaming responses.
- **FR-005**: Alerts MUST accept `speak`; viewers MUST read a newly raised
  speaking alert once, and renders MUST NOT.
- **FR-006**: The core plugin MUST ship Camera, Now Playing, To-do List, History
  Graph, Countdown, and Wi-Fi QR widgets; Flow Text MUST render arrays of objects
  as tables; the weather widget MUST offer an air quality layout.

### Key entities

- No persisted entities. Widget configurations hold host bindings and options.

## Success criteria

- **SC-001**: A dashboard viewer can read the camera, art, history, and to-do data
  of the entities the dashboard binds and of no other entity.
- **SC-002**: The Home Assistant token appears in no response to a browser.

## Assumptions and scope

- Camera streams come from Home Assistant, or from Frigate under
  [feature 034](../034-frigate-nvr/spec.md). Home Assistant's MJPEG relay works
  for every camera it supports, at the frame rate it chooses.
- Control and MQTT devices keep no history; their graphs start at page load.

## References

- [Design plan](plan.md)
- [Dashboard display control](../023-dashboard-display-control/spec.md)
- [Dashboard access and rendering](../007-dashboard-access-rendering/spec.md)
