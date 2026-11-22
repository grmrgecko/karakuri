# Feature specification: Location control kind

**Feature**: `specs/031-location-control`
**Created**: 2026-10-31
**Status**: Implemented
**Input**: A `location` control kind: where a person or a tracked thing is, as
a zone or home/away state with optional coordinates, accuracy, source, and
when the state last changed. Home Assistant `person` and `device_tracker`
entities are drawn as it, MQTT trackers such as OwnTracks can report into it,
and a flow can combine several sources into one person's location using
existing nodes. Presence stays a flow pattern, not a separate feature.

## User scenarios and testing

### User story 1: See where everyone is (Priority: P1)

A kitchen dashboard shows James and Mikinzi with "Home", "Away", or the zone
they are in ("Work", "School"), and how long they have been there. The rows
come straight from the Home Assistant `person` entities, without the author
setting anything up.

**Why this priority**: Presence on a dashboard is the most common use of
location, and Home Assistant already tracks it.
**Independent test**: Place an Entity Control and an Entities Card row for a
`person` entity and a `device_tracker` entity; change their state in Home
Assistant and watch the rows update.

**Acceptance scenarios**:

1. **Given** a `person` or `device_tracker` entity, **When** it is placed on a
   dashboard, **Then** it is drawn as a location: its zone name, "Home" for
   `home`, "Away" for `not_home`, and the time since it changed.
2. **Given** an entity whose state is `unavailable` or `unknown`, **When** it
   is drawn, **Then** it shows as unavailable, like other entities.
3. **Given** an entity reporting latitude, longitude, and GPS accuracy,
   **When** the author turns on coordinates for the row, **Then** they are
   shown with the accuracy.
4. **Given** a location row, **When** a viewer taps it, **Then** nothing is
   commanded: Home Assistant locations are readings.

### User story 2: Keep a person's location in a control (Priority: P1)

An author makes a "James" control of kind location. A flow sets it from
whatever sources the author trusts, and dashboards, other flows, and
dashboard conditions read it like any control.

**Why this priority**: People combine several signals (phone GPS, Wi-Fi,
a BLE tag) in their own way; a control is where the result lives.
**Independent test**: Create a location control, set its state and
coordinates from a flow and over MCP, and read it back in a flow and on a
dashboard.

**Acceptance scenarios**:

1. **Given** a location control, **When** a flow sets its state to `home`
   (or any configured zone), **Then** the value changes, `since` is set to the
   time of the change, and Control Changed triggers fire.
2. **Given** a write that only updates coordinates or accuracy with the same
   state, **When** it is applied, **Then** `since` does not change.
3. **Given** a write with a state the control's zones do not list, **When**
   zones are restricted, **Then** it is refused with the allowed zones; when
   they are not restricted, any zone name is accepted.
4. **Given** a location control on a dashboard, **When** an author allows it,
   **Then** a viewer can pick a zone by hand, for a manual override.
5. **Given** a dashboard condition on a location control's value, **When** the
   state is `home`, **Then** the condition compares the state, as it does for a
   select control.

### User story 3: Report a phone's location over MQTT (Priority: P2)

An author points OwnTracks at the broker and adds an MQTT device of kind
location whose state script reads OwnTracks messages: region enter and leave
events set the zone, and location updates set the coordinates.

**Why this priority**: Some households do not run Home Assistant; MQTT is how
phones and BLE trackers report on their own.
**Independent test**: Publish recorded OwnTracks `location` and `transition`
messages to a device using the documented state script and check its state.

**Acceptance scenarios**:

1. **Given** the documented OwnTracks state script, **When** a `transition`
   enter message for region "home" arrives, **Then** the device state is
   `home`; a leave message sets `not_home`.
2. **Given** a `location` message, **When** it arrives, **Then** latitude,
   longitude, and accuracy update without changing the zone.

### User story 4: Combine sources into one person (Priority: P2)

The flow guide shows how to fuse a phone, a Wi-Fi presence check, and a BLE
tag into one location control: home as soon as any source says home, away
only after every source has said away for ten minutes.

**Why this priority**: Fusion rules differ per household; a documented,
working pattern built from existing nodes serves them without a new node.
**Independent test**: Build the documented flow, drive its sources, and check
the control changes at the documented moments.

**Acceptance scenarios**:

1. **Given** the documented flow and all sources away, **When** one source
   reports home, **Then** the control becomes `home` at once.
2. **Given** the control at home, **When** every source reports away, **Then**
   the control becomes `not_home` ten minutes later, unless a source reports
   home again first.
3. **Given** a server restart during the ten minutes, **When** the server comes
   back, **Then** the pending change still happens on time (feature 029).

### Edge cases

- Zone names are compared as Home Assistant reports them; display labels are
  per control or row and change only what is shown.
- A location without coordinates shows only its state.
- Coordinates outside latitude −90 to 90 or longitude −180 to 180 are refused.
- Accuracy is in metres and must be zero or more.
- `since` is set by the system on state change and cannot be written.
- A location stored before this feature does not exist; nothing migrates.
- Values reach history forwarding (feature 027) as the state string, so a
  time-series destination records zone changes.

## Requirements

### Functional requirements

- **FR-001**: The control catalog MUST offer a `location` kind with
  attributes `state` (primary, string), `latitude`, `longitude`, `accuracy`
  (numbers), `source` (string), and `since` (read-only time of the last state
  change).
- **FR-002**: Location options MUST include zone labels (value-to-label pairs
  with defaults `home` → Home and `not_home` → Away), whether zones are
  restricted to the listed ones, and whether coordinates are shown.
- **FR-003**: Writes MUST validate coordinates and accuracy, enforce restricted
  zones, and set `since` only when `state` changes; partial writes MUST merge
  like other composite kinds.
- **FR-004**: Dashboards MUST draw Home Assistant `person` and
  `device_tracker` entities as read-only locations, taking coordinates,
  accuracy, and source from their attributes and `since` from the state's last
  change.
- **FR-005**: The location face MUST show the state's label, the time since it
  changed, and, when enabled and present, coordinates and accuracy; for
  writable controls it MUST offer the zones as a picker.
- **FR-006**: MQTT devices MUST accept `location` as their control kind; the
  state script guide MUST include a working OwnTracks script.
- **FR-007**: Flows MUST read and write location controls through the existing
  control nodes, with the primary attribute `state` used where a single value
  is expected, and dashboard conditions MUST compare the state.
- **FR-008**: The flow guide MUST document combining several sources into one
  location control with existing nodes, using a durable timer for the away
  delay. Control Changed MUST be able to watch several controls from one
  trigger, since a decision fed by several sources needs one input.
- **FR-009**: Catalog metadata, REST, MCP, and editor fields MUST agree, as for
  the existing kinds.

### Key entities

- **Location value**: state, optional latitude, longitude, accuracy, source,
  and the system-set `since`.
- **Zone label**: a state value and the text shown for it.

## Success criteria

- **SC-001**: A Home Assistant `person` row shows its zone label and updates
  within the same time as other entity rows when the person moves.
- **SC-002**: A location control set by a flow shows the new state on an open
  dashboard and fires Control Changed once per state change, not once per
  coordinate update.
- **SC-003**: The documented OwnTracks script turns recorded enter, leave, and
  location messages into the expected device states.
- **SC-004**: The documented fusion flow changes the control at once for home
  and ten minutes after the last away, including across a restart.
- **SC-005**: Relevant Makefile builds and tests pass, the new face and
  options render without console errors, and the development service stays
  active.

## Assumptions and scope

- A map widget, distance from home, and geofence editing are out of scope;
  coordinates are shown as text.
- Home Assistant zones are not imported; zone names are whatever the source
  reports.
- Setting a Home Assistant person or device tracker from karakuri is out of
  scope; those rows are read-only.
- Fusion uses existing nodes; whether they suffice is confirmed in planning,
  and a gap found there becomes a planned change to an existing node rather
  than a new presence node.

## References

- [Connections, MQTT devices, and controls](../004-connections-devices/spec.md)
- [Dashboard display control](../023-dashboard-display-control/spec.md)
- [Durable waits](../029-durable-waits/spec.md)
- [Flow guide](../../docs/flow-authoring.md)
- [MQTT device state scripts](../../docs/mqtt-device-state-script.md)
