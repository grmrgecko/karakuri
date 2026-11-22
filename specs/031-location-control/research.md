# Research: Location control kind

## A composite kind with `state` primary

- **Decision**: `location` is a composite kind like `climate`: value type
  object, attributes `state` (primary), `latitude`, `longitude`, `accuracy`,
  `source`, and read-only `since`.
- **Rationale**: the existing attribute machinery already gives partial
  writes, the editor's attribute picker, per-attribute port typing in Get and
  Set Control, per-attribute history samples, and MQTT device keys. A bare set
  of `"home"` lands on `state` through the primary attribute.
- **Alternatives**: a `select` control plus separate number controls for
  coordinates, which loses the link between a reading and its position; a
  plain text kind, which cannot hold coordinates.

## Who sets `since`

- **Decision**: the writer sets `since`: the control store in `apply` and the
  MQTT relay in `receive`. Home Assistant rows take `last_changed`.
- **Rationale**: `since` must change only when `state` changes and must not be
  writable. Both writers already see the previous and next value in one
  place. `coerceObject` passes read-only attributes through by design (devices
  report `current` that way), so the store overwrites `since` after merging
  instead of relying on coercion.
- **Alternatives**: compute `since` from history, which needs a forwarding
  destination; use the control's `updated_at`, which moves on coordinate
  updates.

## Zones

- **Decision**: a `zones` choices option with defaults `home` labelled Home
  and `not_home` labelled Away, a `restrict_zones` flag (default off), a
  `manual` flag for the dashboard picker (default off), and
  `show_coordinates` (default off).
- **Rationale**: Home Assistant reports zone names as free text, so an
  unrestricted control accepts whatever a source says, while a household that
  wants only home, work, and school can refuse the rest. The picker is off by
  default because a location set by hand overrides the sources until the next
  report, which an author should choose.
- **Alternatives**: import Home Assistant zones, out of scope in the spec.

## Initial value

- **Decision**: a new location control starts as `{}` and draws "Not
  reported".
- **Rationale**: every other state would be a claim. `home` would arm flows
  that act on arrival; `not_home` would arm those that act on departure.
  `Store.Update` re-checks the value against new options, and an empty object
  passes, so tightening zones never resets a control that has no state.

## Control Changed attribute filter

- **Decision**: with an attribute set on a composite control, fire when that
  attribute's value differs between the old and new value, instead of when the
  write named that attribute.
- **Rationale**: SC-002 requires one firing per state change. A flow or MCP
  call that writes `{state, latitude, longitude}` names no attribute, so the
  current filter skips it and a `state` watcher never fires. Comparing values
  also fixes the same gap for a light whose brightness changes in a
  whole-value write. A write naming the attribute with an unchanged value is
  already swallowed by the store, so no firing is lost.
- **Alternatives**: a location-specific "state changed" trigger, which the
  spec rules out.

## Dashboard conditions

- **Decision**: a control condition with no attribute named reads the kind's
  primary attribute when the value is an object.
- **Rationale**: comparing `{"state":"home",...}` as JSON to `home` never
  matches, so today such a condition is always false. The primary attribute is
  what a bare set writes and what the spec says conditions compare.
- **Alternatives**: require authors to type `state` in the attribute field,
  which leaves the silent always-false case in place.

## History forwarding

- **Decision**: `location.state` is an enumeration whose options are the
  control's zones; coordinates and accuracy are numbers; `source` is text;
  `since` is not recorded.
- **Rationale**: numeric destinations get a state-set series for presence, as
  they do for a select. `since` repeats the time of the state sample.

## Home Assistant mapping

- **Decision**: `person` and `device_tracker` map to `location`, read-only.
  Value: `state` from the entity state, `latitude`, `longitude`, `accuracy`
  from `gps_accuracy`, `source` from `source` (person) or `source_type`
  (tracker), `since` from `last_changed`.
- **Rationale**: these are the attributes Home Assistant reports for both
  domains. Setting a tracker through `device_tracker.see` is out of scope.

## OwnTracks

- **Decision**: the documented script handles `_type: "transition"` (enter
  sets `state` to the region's `desc`, or `home` when it matches a configured
  home region name; leave sets `not_home`) and `_type: "location"` (sets
  `latitude`, `longitude`, `accuracy` from `lat`, `lon`, `acc`, and `source`
  to `gps`). Other message types return nothing.
- **Rationale**: the spec keeps the zone from transitions and coordinates from
  location updates, so a GPS fix inside a region boundary does not flip the
  zone.

## Fusion with existing nodes

- **Decision**: one Control Changed watches every source, with the first in
  `control` and the rest in a new `also_watch` list. A Script keeps each
  source's latest reading in a flow variable and routes to `home` or `away`.
  `home` sets the person to `home` and sends to a Trigger's Reset port; `away`
  feeds the Trigger (Send now Nothing, Then wait 10m, "A message while waiting
  is ignored"), whose Expired output sets `not_home`.
- **Rationale**: implementation found that a flow cannot merge several trigger
  paths into one node, because an input takes one edge, and that a Script
  cannot pull Control Value nodes, because value edges bind config fields and
  script ports are message ports. Without one decision node the sources need
  separate Triggers, which cannot cancel each other's away timer. Watching a
  list of controls from one trigger is the smallest change to an existing node
  that closes the gap, and it is what the spec allows. Flow variables and the
  Trigger's wait are both stored, so readings and the pending away survive a
  restart. "Is ignored" keeps the first away reading's deadline.
- **Alternatives**: a presence node, which the spec rules out; letting inputs
  take several message edges, which changes the engine's single-source rule
  for every node; polling on a schedule, which delays home by up to the
  interval.
