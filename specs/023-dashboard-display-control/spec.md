# Feature specification: Dashboard display control

**Feature**: `specs/023-dashboard-display-control`
**Created**: 2026-10-24
**Status**: Implemented
**Input**: Let flows act on a dashboard that is already on screen, and add the
always-on display behavior wall panels and mirrors need: pages, visibility locks
and conditions, alerts, dimming, a region layout, animations, and three display
widgets.

## User scenarios and testing

### User story 1: Command a live dashboard from a flow (Priority: P1)

An author wires a presence sensor to a `dashboard-command` node. When the
hallway empties, the node hides the camera widget and dims the panel. When
someone walks in, a second command undims it.

**Why this priority**: Every other runtime behavior in this feature is driven
through the same command path.
**Independent test**: Open a dashboard in two browsers, send each action through
MCP `command_dashboard`, and watch both viewers change without reloading.

**Acceptance scenarios**:

1. **Given** two open viewers of one dashboard, **When** a flow sends `hide` for
   a placement, **Then** both hide it within one broker delivery and a viewer
   opened afterwards also starts with it hidden.
2. **Given** a command naming a dashboard that does not exist, **When** the node
   runs, **Then** it fails through its error port with a nonretryable error and
   no state changes.
3. **Given** the server restarts, **When** a viewer reconnects, **Then** the
   dashboard shows its authored start state: runtime locks, alerts, dim
   overrides, commanded page, and pushed content are gone.
4. **Given** a user with view but not edit access, **When** they call the REST or
   MCP command endpoint, **Then** the call is refused.

### User story 2: Show and hide widgets without the sources fighting (Priority: P1)

A presence flow, a night schedule, and a manual toggle each hide the same
widget for their own reason. It only reappears once all of them have let go.

**Why this priority**: A single visible flag lets the last writer win, so one
automation silently undoes another.
**Independent test**: Hide a placement with locks `presence` and `night`, show
it with `presence`, and confirm it stays hidden until `night` is released.

**Acceptance scenarios**:

1. **Given** a placement hidden with locks `a` and `b`, **When** `show` releases
   `a`, **Then** it stays hidden; **When** `show` releases `b`, **Then** it
   appears.
2. **Given** a placement marked hidden on start, **When** a viewer opens the
   dashboard, **Then** it is hidden under the lock `start` until a `show`
   releases `start` or forces it.
3. **Given** a `show` with `force`, **When** it runs, **Then** every lock on the
   placement is cleared.
4. **Given** a placement with a state condition on `binary_sensor.garage_door`
   equal to `on`, **When** the entity changes state, **Then** the placement
   appears or disappears live, and a viewer with no Home Assistant permission
   can still evaluate it.
5. **Given** a placement with a time condition from 22:00 to 06:00, **When** the
   viewer's local clock enters or leaves the window, **Then** the placement
   appears or disappears without a reload.

### User story 3: Alert the whole panel (Priority: P1)

A flow, or a notification endpoint pointing at a dashboard, puts a toast or a
full-screen message over everything on it.

**Why this priority**: Flows that already notify phones should be able to flash
the wall panel with the same endpoint selection.
**Independent test**: Configure a `dashboard` notification service with one
endpoint, send through the notification node, and see the alert on an open
viewer; clear it through a command.

**Acceptance scenarios**:

1. **Given** an alert with a 10-second duration, **When** it is sent, **Then**
   every viewer shows it and the server removes it after 10 seconds.
2. **Given** a full-screen alert, **When** it is shown, **Then** it covers the
   widgets until it expires or is cleared.
3. **Given** six alerts sent while none expire, **When** the sixth arrives,
   **Then** the oldest is dropped and five remain.
4. **Given** a notification endpoint whose target dashboard was deleted, **When**
   the node sends, **Then** delivery fails with an error naming the missing
   dashboard and does not retry.

### User story 4: Rotate pages (Priority: P2)

An author splits a kitchen panel into "Today", "Weather", and "Cameras" pages,
keeps the clock on all of them, and rotates every 20 seconds. A doorbell flow
jumps to "Cameras".

**Why this priority**: A small screen holds more when its content takes turns.
**Independent test**: Create three pages, place widgets on each and one on all,
set a rotation interval, deploy, and watch the viewer cycle.

**Acceptance scenarios**:

1. **Given** pages and a 20-second interval, **When** the viewer is open,
   **Then** it advances one page every 20 seconds and wraps.
2. **Given** a widget with no page, **When** any page is current, **Then** it is
   shown.
3. **Given** a `page` command naming the "Cameras" title, **When** it arrives,
   **Then** viewers jump to it and their rotation timer restarts.
4. **Given** a page is deleted, **When** it held placements, **Then** those
   placements move to every page rather than disappearing.

### User story 5: Keep an always-on screen healthy (Priority: P2)

A bedroom panel dims to 80% between 23:00 and 07:00, goes dark after five
minutes without a touch, wakes on touch, and shifts its content a few pixels
every few minutes to spread burn-in.

**Why this priority**: These panels run all day and are seen at night.
**Independent test**: Set a night window that includes the current time and an
idle timeout, open the viewer, and check the overlay opacity before and after
input.

**Acceptance scenarios**:

1. **Given** a night window covering now at level 80, **When** the viewer loads,
   **Then** an overlay darkens the panel to 80%.
2. **Given** a `dim` command at level 100, **When** it arrives, **Then** it
   replaces the scheduled level until `undim`.
3. **Given** a dimmed panel, **When** someone touches it, presses a key, or
   scrolls, **Then** it brightens locally for the idle timeout, or 30 seconds
   without one, and dims again. The first tap only wakes it.
4. **Given** pixel shift of 4, **When** the viewer is open, **Then** the content
   moves by at most 4 pixels in each axis every two minutes.
5. **Given** a headless render, **When** it photographs the dashboard, **Then**
   dimming, idle, pixel shift, rotation timers, and reload commands are ignored.

### User story 6: Lay out a mirror by region (Priority: P2)

An author building a mirror places widgets into named regions instead of grid
cells. The regions stack and size to the widgets in them.

**Why this priority**: Content whose height changes reads better stacked in a
corner than clipped in a fixed cell.
**Independent test**: Switch a dashboard to the region layout, place widgets in
`top_left`, `top_right`, `lower_third`, and `fullscreen_below`, and render it.

**Acceptance scenarios**:

1. **Given** two widgets in `top_left`, **When** viewed, **Then** they stack in
   order at the top left, each `height` rows of `row_height` pixels tall.
2. **Given** a widget in `fullscreen_below`, **When** viewed, **Then** it fills
   the viewport behind every other region.
3. **Given** a placement whose region is not one of the defined names, **When**
   the dashboard is validated, **Then** validation reports it.
4. **Given** the editor in region layout, **When** the author picks a region and
   moves a widget up or down, **Then** the draft saves its region and order.

### User story 7: Show text from flows and the news (Priority: P3)

An author adds a rotating message whose lines change by time of day, date, and
weather; a flow-text widget showing an AI node's morning summary; and a news
feed of headlines from two RSS sources.

**Why this priority**: These are the content widgets a mirror is built from.
**Independent test**: Place each widget, push content with a `content` command,
and point the news feed at a local RSS file.

**Acceptance scenarios**:

1. **Given** a rotating message with morning and `....-12-25` lines, **When** it
   is Christmas morning, **Then** it picks from both lists; with special days
   set to replace, only the date's lines.
2. **Given** a `content` command with a list of strings for a rotating message,
   **When** it arrives, **Then** those lines replace the configured ones until
   cleared.
3. **Given** a flow-text widget, **When** content is pushed as Markdown, **Then**
   it renders as sanitized HTML without loading remote images.
4. **Given** a news feed configured with an RSS URL, **When** a viewer opens the
   dashboard, **Then** the server fetches only URLs stored in that placement's
   served configuration; the route takes a placement ID and no URL.

### User story 8: Animate visibility changes (Priority: P3)

Widgets fade or slide in and out when they are shown, hidden, or change page.

**Why this priority**: Cosmetic.
**Independent test**: Set a slide-in and fade-out animation, hide and show the
widget, and observe the transitions.

**Acceptance scenarios**:

1. **Given** an animation of `slide-up` in and `fade` out at 600 ms, **When** the
   placement is hidden and shown, **Then** it animates for 600 ms each way and
   the widget is not reloaded.

### Edge cases

- Commands for a placement that is not in the served layout are refused, so a
  typo cannot grow the state map.
- A lock name is 1 to 64 characters of letters, digits, `_`, `-`, or `.`.
  A placement holds at most 16 locks.
- Alert title is at most 200 bytes and message at most 4 KiB. Duration is 0 to
  86400 seconds; 0 keeps the alert until cleared.
- Content is at most 64 KiB per placement after JSON encoding.
- A condition whose source cannot be read (deleted entity, revoked binding) is
  treated as not met, so the placement stays hidden rather than flashing.
- A time window whose start equals its end covers the whole day.
- The page interval is ignored with fewer than two pages.
- Region layout ignores `x` and sections. Switching to fit flattens sections as
  before; switching to regions keeps placements and their grid positions so a
  switch back restores them.
- A viewer that misses broker messages while disconnected fetches the full
  state on reconnect.

## Requirements

### Functional requirements

- **FR-001**: The system MUST keep a runtime display state per dashboard holding
  placement locks, the commanded page, active alerts, a dim override, pushed
  placement content, and variable values. It MUST NOT persist this state; a restart MUST return every
  dashboard to its authored start state.
- **FR-002**: The system MUST accept the actions `show`, `hide`, `page`,
  `next_page`, `previous_page`, `alert`, `clear_alert`, `dim`, `undim`, `reload`,
  `content`, and `variable`, validate them against the served layout, and refuse invalid
  commands without changing state.
- **FR-003**: The system MUST publish every state change and transient event on
  the dashboard's live topic and serve the current state over HTTP, both
  available to anyone who may view the dashboard, including render jobs for it.
- **FR-004**: The system MUST offer commands through a `dashboard-command` flow
  node, REST `POST /api/dashboards/{id}/commands`, and MCP `command_dashboard`.
  REST and MCP MUST require edit access. A flow MUST be able to command any
  dashboard, matching the server authority flows already have over controls.
- **FR-005**: A placement MUST be visible only when it holds no locks, its
  conditions pass, and it belongs to the current page or to every page.
- **FR-006**: `hide` MUST add a named lock (default `command`); `show` MUST
  remove the named lock, or every lock with `force`. A placement authored hidden
  on start MUST begin with the lock `start`.
- **FR-007**: Placements MUST support `state` conditions bound to a control,
  Home Assistant entity, or MQTT device value with the operators `equals`,
  `not_equals`, `above`, `below`, and `contains`, and `time` conditions with a
  local start, end, and optional weekdays. Feature 025 adds `variable` conditions.
  Conditions combine with all or any.
- **FR-008**: Each condition binding MUST grant read access to its source
  through dashboard scope the way a display-only widget binding does, and MUST
  grant no write.
- **FR-009**: Dashboards MUST support ordered pages with titles. A placement MUST
  belong to one page or to every page. Deleting a page MUST move its placements
  to every page.
- **FR-010**: A dashboard MUST support an optional page rotation interval of 5
  to 3600 seconds. `page` MUST accept a page ID, title, or 1-based index.
- **FR-011**: Alerts MUST support a title, message, level (`info`, `success`,
  `warning`, `error`), style (`toast`, `fullscreen`), and duration. At most five
  alerts MUST be active per dashboard, dropping the oldest. The server MUST
  remove expired alerts and publish the change.
- **FR-012**: The notification catalog MUST include a `dashboard` service whose
  endpoint target is a dashboard ID or slug and whose settings choose alert
  style, level, and duration. It MUST deliver through the same command path.
- **FR-013**: Dashboards MUST support a night dim window with a level, an idle
  dim after a number of seconds without input with its own level, and pixel
  shift of 0 to 16 pixels. A `dim` override MUST take precedence over the
  schedule; a touch, key, or wheel MUST lift dimming locally for the idle
  timeout or 30 seconds; renders MUST ignore all of these.
- **FR-014**: The system MUST offer a `regions` layout mode with the regions
  `top_bar`, `top_left`, `top_center`, `top_right`, `upper_third`,
  `middle_center`, `lower_third`, `bottom_left`, `bottom_center`, `bottom_right`,
  `bottom_bar`, `fullscreen_below`, and `fullscreen_above`. Placements in a
  region MUST stack by `y`, take `height` rows of `row_height` pixels, and take
  `width` of `grid_cols` of the region's width.
- **FR-015**: Placements MUST support enter and exit animations (`none`, `fade`,
  `slide-up`, `slide-down`, `slide-left`, `slide-right`, `zoom`) with a duration
  of 0 to 5000 ms, applied without remounting the widget.
- **FR-016**: Pages, display settings, and placement page, region, and display
  settings MUST be part of the draft, deployment snapshot, restore, diff, export,
  and import. A dashboard with none of them set MUST snapshot identically to one
  deployed before this feature.
- **FR-017**: Explicit validation MUST report unknown regions, page references
  to other dashboards, malformed conditions, unknown animations, and out-of-range
  display settings.
- **FR-018**: The core plugin MUST ship `rotating-message`, `flow-text`, and
  `news-feed` widgets. The news feed MUST fetch through a host route that reads
  feed URLs only from the requesting placement's served configuration, with
  variable values substituted only when they are options, as feature 025 states.
- **FR-019**: A `mirror` theme MUST provide a black background, transparent
  cards without shadows, and light type.
- **FR-020**: REST, MCP, the browser editor, and editor chat MUST share the
  service-layer rules for pages and display settings.

### Key entities

- **Dashboard page**: ID, dashboard, title, sort order. Versioned with the layout.
- **Dashboard display settings**: page interval, night dim, idle dim, pixel
  shift. Versioned.
- **Placement display settings**: name, hidden on start, conditions and match
  mode, enter and exit animation, and the style and status cues of feature
  044. Versioned with the placement, alongside its page and region.
- **Live display state**: per-dashboard, in memory: locks per placement,
  commanded page, alerts, dim override, content per placement, variable values,
  sequence number.

## Success criteria

- **SC-001**: Each command action changes every connected viewer and a newly
  opened viewer the same way, checked in two browsers.
- **SC-002**: A placement held by two locks stays hidden until both are released.
- **SC-003**: A condition binding lets an anonymous viewer of a public dashboard
  read that one source and nothing else on its connection, and press nothing.
- **SC-004**: A dashboard deployed before this feature reports no draft changes
  after the upgrade.
- **SC-005**: The news feed route accepts no URL and fetches only the feeds in
  the placement's served configuration.
- **SC-006**: A render of a dashboard with an active dim override and night
  window is not darkened.

## Assumptions and scope

- Commands act on display only; they never write the draft or a deployment.
- Live state belongs to the server process; several viewers see the same locks,
  alerts, dim override, and content. Rotation timers and local wake after input
  belong to each viewer, so two panels on a rotating dashboard need not show
  the same page at the same moment.
- Time conditions, night dim, and idle dim use the viewer's clock and time zone.
- Powering a display off is out of scope; the agent owns host power actions.
- Widgets render their own content, so the host cannot fade a widget's content
  when it re-renders. Widgets that rotate content fade it themselves.
- An alert's text is visible to everyone who may view the dashboard, including
  anonymous viewers of a public one.
- Draft writes accept a state condition whose source is not chosen yet; it
  never matches and grants nothing. Explicit validation reports it.
- In the grid layouts a hidden placement keeps its cell; in the region layout
  it gives up its space so the stack closes up.

## References

- [Design plan](plan.md)
- [Dashboard authoring](../006-dashboard-authoring/spec.md)
- [Dashboard access and rendering](../007-dashboard-access-rendering/spec.md)
- [Notification services](../018-notification-services/spec.md)
- [Dashboard variables](../025-dashboard-variables/spec.md)
- [docs/dashboard-authoring.md](../../docs/dashboard-authoring.md)
- [docs/dashboard-security.md](../../docs/dashboard-security.md)
